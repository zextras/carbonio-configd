// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/rununit"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const unitSpecDir = rununit.DefaultDir

// newUnitController returns a Controller whose service processes go through a
// Runner on the same fakes, on a booted host and as an unprivileged user.
func newUnitController(t *testing.T) (*Controller, *systest.Fakes) {
	t.Helper()

	ctl, f := newFakeController(t, WithRunDir(t.TempDir()))
	f.FS.PutDir("/run/systemd/system", 0o755)
	ctl.units = rununit.New(ctl.env)

	return ctl, f
}

// directExecs are the commands that ran as children of the Controller, i.e.
// everything but systemctl.
func directExecs(f *systest.Fakes) []string {
	var out []string

	for _, l := range f.Exec.Lines() {
		if !strings.HasPrefix(l, "/usr/bin/systemctl") {
			out = append(out, l)
		}
	}

	return out
}

// unitSpec decodes the spec the Runner left for kind/service, read while the
// fake systemctl "starts" the unit.
type unitSpec struct {
	Path   string   `json:"path"`
	Args   []string `json:"args"`
	Env    []string `json:"env"`
	Stdout string   `json:"stdout"`
}

func captureSpec(t *testing.T, f *systest.Fakes, kind, service string, into *unitSpec) {
	t.Helper()

	unit := rununit.Unit(rununit.Kind(kind), service)

	f.Exec.OnCmdline("/usr/bin/systemctl --no-ask-password start "+unit, func(*sys.Cmd) (sys.Result, error) {
		data, ok := f.FS.Data(unitSpecDir + "/" + kind + "-" + service + ".json")
		require.True(t, ok, "spec of %s", unit)
		require.NoError(t, json.Unmarshal(data, into))

		if kind == "forking" {
			f.FS.Put(unitSpecDir+"/forking-"+service+".rc", []byte(`{"code":0}`), 0o600)
		}

		return sys.Result{}, nil
	})
}

func TestStartDirect_LauncherRunsInAForkingUnit(t *testing.T) {
	t.Parallel()

	ctl, f := newUnitController(t)

	var got unitSpec

	captureSpec(t, f, "forking", "memcached", &got)

	def := &ServiceDef{
		Name: "memcached", BinaryPath: "/opt/zextras/common/bin/memcached", BinaryArgs: []string{"-d", "-p", "11211"},
	}
	f.FS.Put(def.BinaryPath, nil, 0o755)

	require.NoError(t, ctl.startDirect(context.Background(), "memcached", def))

	assert.Equal(t, "/opt/zextras/common/bin/memcached", got.Path)
	assert.Equal(t, []string{"-d", "-p", "11211"}, got.Args)
	assert.Empty(t, directExecs(f), "memcached is not a child of the caller")
}

func TestStartDirect_SDNotifyDaemonRunsInAnExecUnit(t *testing.T) {
	t.Parallel()

	ctl, f := newUnitController(t)

	var got unitSpec

	f.Exec.OnCmdline("/usr/bin/systemctl --no-ask-password start carbonio-run-exec@opendkim.service",
		func(*sys.Cmd) (sys.Result, error) {
			data, _ := f.FS.Data(unitSpecDir + "/exec-opendkim.json")
			require.NoError(t, json.Unmarshal(data, &got))

			// What the daemon does once it is up.
			conn, err := net.Dial("unixgram", ctl.p.sdNotifySocket("opendkim"))
			require.NoError(t, err)

			_, err = conn.Write([]byte("READY=1"))
			require.NoError(t, err)
			require.NoError(t, conn.Close())

			return sys.Result{}, nil
		})
	f.Exec.OnCmdline("/usr/bin/systemctl show --property=MainPID", func(*sys.Cmd) (sys.Result, error) {
		return sys.Result{Stdout: []byte("777\n")}, nil
	})

	def := &ServiceDef{
		Name: "opendkim", BinaryPath: "/opt/zextras/common/sbin/opendkim", BinaryArgs: []string{"-f"}, UseSDNotify: true,
		UnitArgs: []string{"-g", "daemon off;"},
	}
	f.FS.Put(def.BinaryPath, nil, 0o755)

	require.NoError(t, ctl.startDirect(context.Background(), "opendkim", def))

	assert.Equal(t, []string{"-f", "-g", "daemon off;"}, got.Args, "a daemon that forks away is kept in the foreground in its unit")
	assert.Equal(t, []string{"NOTIFY_SOCKET=" + ctl.p.sdNotifySocket("opendkim")}, got.Env,
		"the daemon reports readiness to configd, not to systemd")
	assert.Empty(t, directExecs(f))
}

func TestStartDetached_DaemonLogsAndRunsInAnExecUnit(t *testing.T) {
	t.Parallel()

	ctl, f := newUnitController(t)

	var got unitSpec

	captureSpec(t, f, "exec", "clamd", &got)

	def := &ServiceDef{Name: "clamd", BinaryPath: "/opt/zextras/common/sbin/clamd", Detached: true, LogFile: "/opt/zextras/log/clamd.out"}
	f.FS.Put(def.BinaryPath, nil, 0o755)

	require.NoError(t, ctl.startDirect(context.Background(), "clamd", def))

	assert.Equal(t, "/opt/zextras/log/clamd.out", got.Stdout)
	assert.Empty(t, directExecs(f))
}

func TestStartDetached_ConfigdItselfStaysAChild(t *testing.T) {
	t.Parallel()

	ctl, f := newUnitController(t)

	def := &ServiceDef{
		Name: "configd", BinaryPath: "/opt/zextras/bin/configd", Detached: true, Ready: ReadyConfigd,
		LogFile: "/opt/zextras/log/configd.out",
	}
	f.FS.Put(def.BinaryPath, nil, 0o755)

	require.NoError(t, ctl.startDirect(context.Background(), "configd", def))

	assert.Equal(t, []string{"/opt/zextras/bin/configd"}, directExecs(f), "configd is not a service of itself")
	assert.Empty(t, callsWithPrefix(f, "/usr/bin/systemctl"))
}

func TestStartDirect_StartsAChildWhenPolkitDenies(t *testing.T) {
	t.Parallel()

	ctl, f := newUnitController(t)

	f.Exec.OnCmdline("/usr/bin/systemctl --no-ask-password start carbonio-run-forking@memcached.service",
		func(*sys.Cmd) (sys.Result, error) {
			return sys.Result{Stderr: []byte("Failed to start carbonio-run-forking@memcached.service: Access denied\n")},
				&sys.ExitError{Code: 1}
		})

	def := &ServiceDef{Name: "memcached", BinaryPath: "/opt/zextras/common/bin/memcached", BinaryArgs: []string{"-d"}}
	f.FS.Put(def.BinaryPath, nil, 0o755)

	require.NoError(t, ctl.startDirect(context.Background(), "memcached", def))

	assert.Equal(t, []string{"/opt/zextras/common/bin/memcached -d"}, directExecs(f),
		"without the units the service is started as a child, as before")
}
