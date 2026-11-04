// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package rununit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const (
	testDir     = "/run/test-units"
	systemctl   = systemctlBin + " --no-ask-password "
	nginxExec   = "carbonio-run-exec@nginx.service"
	mtaForking  = "carbonio-run-forking@mta.service"
	denied      = "Failed to start carbonio-run-exec@nginx.service: Access denied\n"
	activeState = "active\n"
)

// newRunner returns a Runner over fakes of a booted host and an unprivileged
// caller, with a pid wait that returns at once.
func newRunner(t *testing.T) (*Runner, *systest.Fakes) {
	t.Helper()

	f := systest.New()
	f.FS.PutDir("/run/systemd/system", 0o755)

	r := New(f.System())
	r.dir = testDir
	r.waitExit = func(int) error { return nil }

	return r, f
}

func startedLines(f *systest.Fakes) []string {
	var out []string

	for _, l := range f.Exec.Lines() {
		if strings.HasPrefix(l, systemctlBin) {
			out = append(out, l)
		}
	}

	return out
}

// onSystemctl answers one systemctl verb.
func onSystemctl(f *systest.Fakes, verbAndUnit string, h systest.Handler) {
	f.Exec.OnCmdline(systemctl+verbAndUnit, h)
}

func TestRun_ForkingUnit(t *testing.T) {
	t.Parallel()

	r, f := newRunner(t)

	var spec1 spec

	onSystemctl(f, "start "+mtaForking, func(*sys.Cmd) (sys.Result, error) {
		data, ok := f.FS.Data(testDir + "/forking-mta.json")
		require.True(t, ok, "the spec is written before the unit starts")
		require.NoError(t, json.Unmarshal(data, &spec1))

		f.FS.Put(testDir+"/forking-mta.rc", []byte(`{"code":0}`), 0o600)
		f.FS.Put(testDir+"/forking-mta.out", []byte("postfix/postfix-script: starting the Postfix mail system\n"), 0o600)

		return sys.Result{}, nil
	})

	res, err := r.Run(context.Background(), "mta", &sys.Cmd{
		Path: "/usr/bin/sudo", Args: []string{"-n", "/opt/zextras/common/sbin/postfix", "start"}, Combined: true,
	})

	require.NoError(t, err)
	assert.Contains(t, string(res.Stdout), "starting the Postfix mail system")
	assert.Equal(t, "/usr/bin/sudo", spec1.Path)
	assert.Equal(t, []string{"-n", "/opt/zextras/common/sbin/postfix", "start"}, spec1.Args)
	assert.Equal(t, testDir+"/forking-mta.rc", spec1.Result)
	assert.Equal(t, testDir+"/forking-mta.out", spec1.Stdout, "the output is captured to be returned")

	assert.Equal(t, []string{
		systemctl + "is-active " + mtaForking,
		systemctl + "start " + mtaForking,
	}, startedLines(f))

	mode, _ := f.FS.Mode(testDir)
	assert.Equal(t, os.FileMode(0o700), mode.Perm(), "the spec directory is closed to others")

	for _, name := range []string{"json", "rc", "out"} {
		assert.False(t, f.FS.Exists(testDir+"/forking-mta."+name), "%s is removed", name)
	}
}

func TestRun_ReportsTheLaunchersExitStatus(t *testing.T) {
	t.Parallel()

	r, f := newRunner(t)

	onSystemctl(f, "start "+mtaForking, func(*sys.Cmd) (sys.Result, error) {
		f.FS.Put(testDir+"/forking-mta.rc", []byte(`{"code":3}`), 0o600)
		f.FS.Put(testDir+"/forking-mta.out", []byte("fatal: no master.cf\n"), 0o600)

		return sys.Result{}, nil
	})

	res, err := r.Run(context.Background(), "mta", &sys.Cmd{Path: "/opt/zextras/bin/zmmtactl", Args: []string{"start"}})

	var exit *sys.ExitError

	require.ErrorAs(t, err, &exit)
	assert.Equal(t, 3, exit.Code)
	assert.Equal(t, 3, res.ExitCode)
	assert.Equal(t, "fatal: no master.cf\n", string(res.Stdout))
	assert.True(t, r.Enabled(), "a failing launcher is not a reason to stop using units")
}

func TestRun_LauncherThatCouldNotBeStarted(t *testing.T) {
	t.Parallel()

	r, f := newRunner(t)

	onSystemctl(f, "start "+mtaForking, func(*sys.Cmd) (sys.Result, error) {
		f.FS.Put(testDir+"/forking-mta.rc", []byte(`{"code":-1,"error":"fork/exec /x: no such file or directory"}`), 0o600)

		return sys.Result{}, nil
	})

	_, err := r.Run(context.Background(), "mta", &sys.Cmd{Path: "/x"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such file or directory")
}

func TestStart_ExecUnit(t *testing.T) {
	t.Parallel()

	r, f := newRunner(t)

	log, err := f.FS.OpenFile("/opt/zextras/log/nginx.out", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	require.NoError(t, err)

	var got spec

	onSystemctl(f, "start "+nginxExec, func(*sys.Cmd) (sys.Result, error) {
		data, ok := f.FS.Data(testDir + "/exec-nginx.json")
		require.True(t, ok)
		require.NoError(t, json.Unmarshal(data, &got))

		return sys.Result{}, nil
	})
	f.Exec.OnCmdline(systemctlBin+" show --property=MainPID", func(*sys.Cmd) (sys.Result, error) {
		return sys.Result{Stdout: []byte("4242\n")}, nil
	})

	proc, err := r.Start(context.Background(), "nginx", &sys.Cmd{
		Path: "/opt/zextras/common/sbin/nginx", Args: []string{"-c", "/opt/zextras/conf/nginx.conf"},
		Env: []string{"NOTIFY_SOCKET=/run/carbonio/notify-nginx.sock"}, Stdout: log, Stderr: log, Setsid: true,
	})

	require.NoError(t, err)
	assert.Equal(t, 4242, proc.Pid())
	assert.Equal(t, "/opt/zextras/common/sbin/nginx", got.Path)
	assert.Equal(t, []string{"NOTIFY_SOCKET=/run/carbonio/notify-nginx.sock"}, got.Env)
	assert.Equal(t, "/opt/zextras/log/nginx.out", got.Stdout)
	assert.Equal(t, "/opt/zextras/log/nginx.out", got.Stderr)
	assert.Empty(t, got.Result, "an exec unit has no wrapper to leave a result")
	assert.True(t, f.FS.Exists(testDir+"/exec-nginx.json"), "the unit's helper removes the spec once it has read it")
	assert.NoError(t, proc.Release())
	assert.Empty(t, f.Exec.Calls()[len(f.Exec.Calls())-1].Env, "systemctl runs with the caller's environment")
}

func TestStart_UnitArgsApplyOnlyInAUnit(t *testing.T) {
	t.Parallel()

	r, f := newRunner(t)

	var got spec

	onSystemctl(f, "start "+nginxExec, func(*sys.Cmd) (sys.Result, error) {
		data, _ := f.FS.Data(testDir + "/exec-nginx.json")
		require.NoError(t, json.Unmarshal(data, &got))

		return sys.Result{}, nil
	})

	cmd := &sys.Cmd{Path: "/opt/zextras/common/sbin/nginx", Args: []string{"-c", "x"}}

	_, err := r.Start(context.Background(), "nginx", cmd, "-g", "daemon off;")
	require.NoError(t, err)
	assert.Equal(t, []string{"-c", "x", "-g", "daemon off;"}, got.Args)
	assert.Equal(t, []string{"-c", "x"}, cmd.Args, "the caller's command is not modified")

	r.disable(context.Background(), nginxExec, errors.New("refused"))

	_, err = r.Start(context.Background(), "nginx", cmd, "-g", "daemon off;")
	require.NoError(t, err)
	execs := f.Rec.Of("exec")
	assert.Equal(t, "/opt/zextras/common/sbin/nginx -c x", execs[len(execs)-1],
		"as a plain child the daemon keeps its usual arguments")
}

func TestStart_WaitReportsHowTheUnitEnded(t *testing.T) {
	t.Parallel()

	r, f := newRunner(t)

	onSystemctl(f, "start "+nginxExec, nil)
	f.Exec.OnCmdline(systemctlBin+" show --property=MainPID", func(*sys.Cmd) (sys.Result, error) {
		return sys.Result{Stdout: []byte("4242\n")}, nil
	})
	f.Exec.OnCmdline(systemctlBin+" show --property=ActiveState", func(*sys.Cmd) (sys.Result, error) {
		return sys.Result{Stdout: []byte("ActiveState=failed\nResult=exit-code\nExecMainStatus=2\n")}, nil
	})

	proc, err := r.Start(context.Background(), "nginx", &sys.Cmd{Path: "/opt/zextras/common/sbin/nginx"})
	require.NoError(t, err)

	var exit *sys.ExitError

	require.ErrorAs(t, proc.Wait(), &exit)
	assert.Equal(t, 2, exit.Code)
}

func TestStart_WaitSucceedsForACleanExit(t *testing.T) {
	t.Parallel()

	r, f := newRunner(t)

	f.Exec.OnCmdline(systemctlBin+" show --property=MainPID", func(*sys.Cmd) (sys.Result, error) {
		return sys.Result{Stdout: []byte("4242\n")}, nil
	})
	f.Exec.OnCmdline(systemctlBin+" show --property=ActiveState", func(*sys.Cmd) (sys.Result, error) {
		return sys.Result{Stdout: []byte("ActiveState=inactive\nResult=success\nExecMainStatus=0\n")}, nil
	})

	proc, err := r.Start(context.Background(), "nginx", &sys.Cmd{Path: "/opt/zextras/common/sbin/nginx"})
	require.NoError(t, err)
	assert.NoError(t, proc.Wait())
}

func TestStaleUnitIsClearedFirst(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ state, verb string }{
		{"deactivating\n", "stop"},
		{"failed\n", "reset-failed"},
	} {
		t.Run(strings.TrimSpace(tc.state), func(t *testing.T) {
			t.Parallel()

			r, f := newRunner(t)

			f.Exec.OnCmdline(systemctl+"is-active "+nginxExec, func(*sys.Cmd) (sys.Result, error) {
				return sys.Result{Stdout: []byte(tc.state)}, &sys.ExitError{Code: 3}
			})

			_, err := r.Start(context.Background(), "nginx", &sys.Cmd{Path: "/opt/zextras/common/sbin/nginx"})
			require.NoError(t, err)

			lines := startedLines(f)
			require.GreaterOrEqual(t, len(lines), 3)
			assert.Equal(t, systemctl+"is-active "+nginxExec, lines[0])
			assert.Equal(t, systemctl+tc.verb+" "+nginxExec, lines[1], "the name is freed before it is reused")
			assert.Equal(t, systemctl+"start "+nginxExec, lines[2])
		})
	}
}

// A start that races with another one (the daemon's watchdog and zmcontrol)
// must not replace the service the first one is starting.
func TestRunningUnitIsJoinedNotRestarted(t *testing.T) {
	t.Parallel()

	for _, state := range []string{"active\n", "activating\n"} {
		t.Run(strings.TrimSpace(state), func(t *testing.T) {
			t.Parallel()

			r, f := newRunner(t)

			f.Exec.OnCmdline(systemctl+"is-active "+nginxExec, func(*sys.Cmd) (sys.Result, error) {
				return sys.Result{Stdout: []byte(state)}, nil
			})
			f.Exec.OnCmdline(systemctlBin+" show --property=MainPID", func(*sys.Cmd) (sys.Result, error) {
				return sys.Result{Stdout: []byte("4242\n")}, nil
			})
			// The first start's spec, not yet read by its unit.
			f.FS.Put(testDir+"/exec-nginx.json", []byte(`{"path":"/first"}`), 0o600)

			proc, err := r.Start(context.Background(), "nginx", &sys.Cmd{Path: "/opt/zextras/common/sbin/nginx"})

			require.NoError(t, err)
			assert.Equal(t, 4242, proc.Pid())

			for _, l := range startedLines(f) {
				assert.NotContains(t, l, " stop ", "the running service is not stopped")
			}

			data, _ := f.FS.Data(testDir + "/exec-nginx.json")
			assert.JSONEq(t, `{"path":"/first"}`, string(data), "the first start's spec is not touched")
		})
	}
}

func TestRunningLauncherUnitIsJoined(t *testing.T) {
	t.Parallel()

	r, f := newRunner(t)

	f.Exec.OnCmdline(systemctl+"is-active "+mtaForking, func(*sys.Cmd) (sys.Result, error) {
		return sys.Result{Stdout: []byte("active\n")}, nil
	})
	f.FS.Put(testDir+"/forking-mta.rc", []byte(`{"code":0}`), 0o600)

	res, err := r.Run(context.Background(), "mta", &sys.Cmd{Path: "/opt/zextras/bin/zmmtactl", Args: []string{"start"}})

	require.NoError(t, err)
	assert.Empty(t, res.Stdout)
	assert.True(t, f.FS.Exists(testDir+"/forking-mta.rc"), "the first launch's result is left for it")
	assert.False(t, f.FS.Exists(testDir+"/forking-mta.json"), "no second spec is written")
}

func TestFallsBackToAChildWhenSystemdSaysNo(t *testing.T) {
	cases := map[string]sys.Result{
		"polkit denied":       {Stderr: []byte(denied)},
		"authentication":      {Stderr: []byte("Failed to start x: Interactive authentication required.\n")},
		"template missing":    {Stderr: []byte("Failed to start x: Unit carbonio-run-exec@nginx.service not found.\n")},
		"bus unreachable":     {Stderr: []byte("Failed to connect to bus: No such file or directory\n")},
		"not booted":          {Stderr: []byte("System has not been booted with systemd as init system\n")},
		"systemctl not found": {},
	}

	for name, failure := range cases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer

			logger.InitStructuredLogging(&logger.Config{Output: &buf, Level: logger.LogLevelInfo})

			r, f := newRunner(t)

			var err error = &sys.ExitError{Code: 1}
			if name == "systemctl not found" {
				err = errors.New("fork/exec /usr/bin/systemctl: no such file or directory")
			}

			f.Exec.OnCmdline(systemctl+"start "+nginxExec, func(*sys.Cmd) (sys.Result, error) { return failure, err })

			cmd := &sys.Cmd{Path: "/opt/zextras/common/sbin/nginx", Args: []string{"-c", "x"}}

			proc, startErr := r.Start(context.Background(), "nginx", cmd)
			require.NoError(t, startErr)
			assert.NotZero(t, proc.Pid())
			assert.False(t, r.Enabled(), "units are given up on after the first refusal")

			f.Rec.Reset()

			_, startErr = r.Start(context.Background(), "nginx", cmd)
			require.NoError(t, startErr)
			assert.Equal(t, []string{"/opt/zextras/common/sbin/nginx -c x"}, f.Rec.Of("exec"),
				"no systemctl any more: the process is started directly")

			_, runErr := r.Run(context.Background(), "mta", &sys.Cmd{Path: "/opt/zextras/bin/zmmtactl", Args: []string{"start"}})
			require.NoError(t, runErr)
			assert.Equal(t, 1, strings.Count(buf.String(), "Cannot start services as systemd units"), "one warning")
			assert.Contains(t, buf.String(), "level=WARN")
		})
	}
}

func TestFailedJobIsNotAFallback(t *testing.T) {
	t.Parallel()

	r, f := newRunner(t)

	onSystemctl(f, "start "+nginxExec, func(*sys.Cmd) (sys.Result, error) {
		return sys.Result{Stderr: []byte("Job for carbonio-run-exec@nginx.service failed because the control process exited with error code.\n")},
			&sys.ExitError{Code: 1}
	})

	_, err := r.Start(context.Background(), "nginx", &sys.Cmd{Path: "/opt/zextras/common/sbin/nginx"})

	require.Error(t, err)
	assert.True(t, r.Enabled())
	assert.Empty(t, f.Rec.Of("exec.start"), "the service is not started a second time as a child")
}

func TestRunsAsAChildWhenUnitsCannotCarryTheCommand(t *testing.T) {
	t.Parallel()

	var notAFile bytes.Buffer

	for name, tc := range map[string]struct {
		prep    func(*systest.Fakes)
		service string
		cmd     *sys.Cmd
	}{
		"systemd is not init": {func(f *systest.Fakes) { _ = f.FS.RemoveAll("/run/systemd/system") }, "nginx", &sys.Cmd{Path: "/bin/true"}},
		"caller is root":      {func(f *systest.Fakes) { f.Proc.UserID = 0 }, "nginx", &sys.Cmd{Path: "/bin/true"}},
		"relative program":    {nil, "nginx", &sys.Cmd{Path: "true"}},
		"stdin":               {nil, "nginx", &sys.Cmd{Path: "/bin/true", Stdin: strings.NewReader("x")}},
		"other environment":   {nil, "nginx", &sys.Cmd{Path: "/bin/true", Env: []string{"LD_PRELOAD=/x"}}},
		"stdout not a file":   {nil, "nginx", &sys.Cmd{Path: "/bin/true", Stdout: &notAFile}},
		"odd unit name":       {nil, "../nginx", &sys.Cmd{Path: "/bin/true"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r, f := newRunner(t)
			if tc.prep != nil {
				tc.prep(f)
			}

			_, err := r.Start(context.Background(), tc.service, tc.cmd)
			require.NoError(t, err)
			_, err = r.Run(context.Background(), tc.service, tc.cmd)
			require.NoError(t, err)

			assert.Equal(t, []string{tc.cmd.String(), tc.cmd.String()}, f.Rec.Of("exec"),
				"started directly, systemctl never asked")
		})
	}
}
