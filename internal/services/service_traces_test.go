// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// updateTraces rewrites testdata/service_traces.golden instead of comparing
// with it: go test ./internal/services -run TestServiceTraces -update-traces.
var updateTraces = flag.Bool("update-traces", false, "rewrite the service lifecycle trace golden file")

const traceGoldenPath = "testdata/service_traces.golden"

// traceLocalConfig is the localconfig every trace scenario runs with: just the
// keys the launchers consume, with fixed values so the traces are stable.
func traceLocalConfig() map[string]string {
	return map[string]string{
		"zimbra_server_hostname":    "mbx.example.com",
		"ldap_url":                  "ldap://ldap.example.com:389",
		"ldap_port":                 "389",
		"ldap_postfix_password":     "secret",
		"ldap_starttls_supported":   "1",
		"postfix_mail_owner":        "postfix",
		"postfix_setgid_group":      "postdrop",
		"mysql_mycnf":               "/opt/zextras/conf/my.cnf",
		"mysql_errlogfile":          "/opt/zextras/log/mysql_error.log",
		"antispam_mysql_enabled":    "TRUE",
		"antispam_mysql_host":       "localhost",
		"antispam_mysql_mycnf":      "/opt/zextras/conf/antispam-my.cnf",
		"antispam_mysql_errlogfile": "/opt/zextras/log/antispam-mysqld.log",
		"mailboxd_java_home":        "/fake/java",
		"mailboxd_java_heap_size":   "1024",
		"zmstat_mysql_enabled":      "TRUE",
		"zmstat_nginx_enabled":      "TRUE",
		"zmstat_mtaqueue_enabled":   "FALSE",
	}
}

// socketFS is the in-memory filesystem with real unlinking of notify sockets:
// sd_notify sockets are kernel objects, so removing their path must reach the
// real filesystem for a start after a stop to bind it again.
type socketFS struct {
	sys.FS
}

func (s socketFS) Remove(name string) error {
	if strings.Contains(name, "/notify-") {
		_ = os.Remove(name)
	}

	return s.FS.Remove(name)
}

// traceHost is one fake host a lifecycle scenario runs on.
type traceHost struct {
	ctl    *Controller
	f      *systest.Fakes
	runDir string
}

// newTraceHost builds a fake host in the given orchestration mode with every
// binary a launcher looks for installed and nothing running.
func newTraceHost(t *testing.T, strict bool) *traceHost {
	t.Helper()

	// sd_notify sockets are real unix sockets: give the host a short, private
	// runtime directory (unix socket paths are limited to ~100 bytes).
	runDir, err := os.MkdirTemp("", "cfgd")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(runDir) })

	f := systest.New()
	env := f.System()
	env.FS = socketFS{FS: env.FS}
	lc := traceLocalConfig()
	ctl := NewController(withMode(env, strict),
		WithRunDir(runDir),
		WithConfigLoader(func() (map[string]string, error) { return lc, nil }),
		WithLDAPProbe(func(context.Context, []string) error { return nil }),
	)
	stubReadiness(ctl)
	ctl.withBlockingStart()

	h := &traceHost{ctl: ctl, f: f, runDir: runDir}
	h.install()

	if strict {
		f.FS.PutDir("/run/systemd/system", 0o755)
	}

	return h
}

// install puts every binary and data file the launchers stat on the host.
func (h *traceHost) install() {
	ctl, f := h.ctl, h.f

	for _, def := range ctl.registry {
		if def.BinaryPath != "" {
			f.FS.Put(def.BinaryPath, []byte("x"), 0o755)
		}
	}

	f.FS.Put("/fake/java/bin/java", []byte("x"), 0o755)
	f.FS.Put(ctl.p.common+"/sbin/amavisd", []byte("x"), 0o755)
	f.FS.Put(ctl.p.aliases, []byte("root: admin\n"), 0o644)
	f.FS.Put(ctl.p.cacheFile, []byte("antispam\nmailbox\n"), 0o644)
	f.FS.Put(ctl.p.milterOptions, []byte("zimbraMilterServerEnabled=TRUE\n"), 0o644)
	f.FS.Put(ctl.p.cbpolicydDB, []byte("db"), 0o644)
	f.FS.Put(ctl.p.configrewrite, []byte("#!/bin/sh"), 0o755)
	f.FS.Put(ctl.p.altermime, []byte("#!/bin/sh"), 0o755)

	for _, collector := range []string{
		zmstatProc, zmstatCPU, zmstatVM, zmstatIO, zmstatDF, zmstatFD, zmstatAllprocs,
		"zmstat-mysql", "zmstat-nginx", "zmstat-mtaqueue",
	} {
		f.FS.Put(ctl.p.libexec+"/"+collector, []byte("x"), 0o755)
	}

	f.Proc.SetGID("postfix", 1001)

	// Daemons built with sd_notify report READY=1 as soon as they are started.
	for _, def := range ctl.registry {
		if def.UseSDNotify && def.BinaryPath != "" {
			f.Exec.OnPath(def.BinaryPath, func(c *sys.Cmd) (sys.Result, error) {
				notifyReady(c)

				return sys.Result{}, nil
			})
		}
	}

	// Daemons that fork away and are expected to be up right after the launch
	// show up when started, as the real ones do.
	f.Exec.OnPath(ctl.p.common+"/sbin/amavisd", func(*sys.Cmd) (sys.Result, error) {
		f.Proc.Add(amavisdEntry(amavisdPID))

		return sys.Result{}, nil
	})
	f.Exec.OnCmdline(sudoBin+" "+ctl.p.amavisMC, func(*sys.Cmd) (sys.Result, error) {
		f.Proc.Add(amavisMCEntry(ctl, amavisMCPID))

		return sys.Result{}, nil
	})
}

// notifyReady sends READY=1 to the NOTIFY_SOCKET of cmd, like a daemon that
// finished initializing.
func notifyReady(cmd *sys.Cmd) {
	for _, e := range cmd.Env {
		socket, ok := strings.CutPrefix(e, "NOTIFY_SOCKET=")
		if !ok {
			continue
		}

		conn, err := net.Dial("unixgram", socket)
		if err != nil {
			return
		}

		_, _ = conn.Write([]byte("READY=1\n"))
		_ = conn.Close()
	}
}

// postfixDown scripts `postfix status`/`postfix stop` the way a stopped
// postfix answers them: exit 1 with its "is not running" warning.
func (h *traceHost) postfixDown() {
	const notRunning = "postfix/postfix-script: warning: the Postfix mail system is not running"

	h.f.Exec.OnCmdline(sudoBin+" "+h.ctl.p.postfixBin+" status", systest.Fail(1, notRunning))
	h.f.Exec.OnCmdline(sudoBin+" "+h.ctl.p.postfixBin+" stop", systest.Fail(1, notRunning))
	h.f.Exec.OnCmdline(sudoBin+" -n "+h.ctl.p.postfixBin+" status", systest.Fail(1, notRunning))
	h.f.Exec.OnCmdline(sudoBin+" -n "+h.ctl.p.postfixBin+" stop", systest.Fail(1, notRunning))
}

// run adds the processes of service name (and, for a composite, of its
// applicable members) to the process table, with the pidfile each declares.
func (h *traceHost) run(name string, pid *int) {
	def := h.ctl.registry[name]

	if len(def.Members) > 0 {
		for _, m := range h.ctl.applicableMembers(context.Background(), def) {
			h.run(m, pid)
		}

		return
	}

	if def.ProcessName == "" {
		return
	}

	if name == svcStats {
		h.f.FS.PutDir(h.ctl.p.statsPidDir, 0o755)

		for _, collector := range []string{zmstatCPU, zmstatVM} {
			*pid++
			script := h.ctl.p.libexec + "/" + collector
			h.f.Proc.Add(sys.ProcEntry{PID: *pid, Argv: []string{"/usr/bin/perl", "-w", script}})
			h.f.FS.Put(h.ctl.p.statsPidDir+"/"+collector+".pid", []byte(strconv.Itoa(*pid)), 0o644)
		}

		return
	}

	*pid++

	h.f.Proc.Add(processEntryFor(def, *pid))

	if def.PidFile != "" {
		h.f.FS.Put(def.PidFile, []byte(strconv.Itoa(*pid)), 0o644)
	}

	for range def.Companions {
		*pid++

		h.f.Proc.Add(sys.ProcEntry{PID: *pid, Argv: []string{"/usr/bin/perl", h.ctl.p.amavisMC}})
	}
}

// processEntryFor is a process-table entry carrying def's ProcessName identity.
func processEntryFor(def *ServiceDef, pid int) sys.ProcEntry {
	fields := strings.Fields(def.ProcessName)
	prog := strings.NewReplacer("m*d*", "mariadbd").Replace(fields[0])

	argv := append([]string{prog}, fields[1:]...)
	e := sys.ProcEntry{PID: pid, Argv: argv}

	if path.IsAbs(prog) {
		e.Exe = prog
	}

	if prog == "amavisd" {
		e.Argv = []string{"amavisd", "(master)"}
	}

	return e
}

// traceScenario is one verb applied to one service in one starting state.
type traceScenario struct {
	name   string
	strict bool
	// running puts the service's processes in the table before the verb.
	running bool
	// down scripts postfix as stopped.
	down bool
	do   func(ctx context.Context, ctl *Controller, svc string) string
}

func result(err error) string {
	if err != nil {
		return "err: " + err.Error()
	}

	return "ok"
}

func traceScenarios() []traceScenario {
	start := func(ctx context.Context, ctl *Controller, svc string) string {
		return result(ctl.ServiceStart(ctx, svc))
	}
	stop := func(ctx context.Context, ctl *Controller, svc string) string {
		return result(ctl.ServiceStop(ctx, svc))
	}
	status := func(ctx context.Context, ctl *Controller, svc string) string {
		running, err := ctl.ServiceStatus(ctx, svc)

		return fmt.Sprintf("running=%t %s", running, result(err))
	}
	restart := func(ctx context.Context, ctl *Controller, svc string) string {
		return result(ctl.ServiceRestart(ctx, svc))
	}
	reload := func(ctx context.Context, ctl *Controller, svc string) string {
		return result(ctl.ServiceReload(ctx, svc))
	}
	startSystemd := func(ctx context.Context, ctl *Controller, svc string) string {
		return result(ctl.ServiceStartSystemd(ctx, svc))
	}
	stopSystemd := func(ctx context.Context, ctl *Controller, svc string) string {
		return result(ctl.ServiceStopSystemd(ctx, svc))
	}
	refresh := func(ctx context.Context, ctl *Controller, svc string) string {
		rep, err := ctl.ServiceRefresh(ctx, svc)

		return fmt.Sprintf("signaled=%v gone=%v %s", rep.Signaled, rep.NotRunning, result(err))
	}
	rotate := func(ctx context.Context, ctl *Controller, svc string) string {
		rep, err := ctl.ServiceRotate(ctx, svc)

		return fmt.Sprintf("signaled=%v gone=%v %s", rep.Signaled, rep.NotRunning, result(err))
	}

	return []traceScenario{
		{name: "legacy start cold", down: true, do: start},
		{name: "legacy start running", running: true, do: start},
		{name: "legacy stop running", running: true, do: stop},
		{name: "legacy stop stopped", down: true, do: stop},
		{name: "legacy status running", running: true, do: status},
		{name: "legacy status stopped", down: true, do: status},
		{name: "legacy restart running", running: true, do: restart},
		{name: "legacy reload running", running: true, do: reload},
		{name: "legacy reload stopped", down: true, do: reload},
		{name: "legacy start-systemd cold", down: true, do: startSystemd},
		{name: "legacy stop-systemd running", running: true, do: stopSystemd},
		{name: "legacy refresh running", running: true, do: refresh},
		{name: "legacy refresh stopped", down: true, do: refresh},
		{name: "legacy rotate running", running: true, do: rotate},
		{name: "systemd start", strict: true, do: start},
		{name: "systemd stop", strict: true, do: stop},
		{name: "systemd restart", strict: true, do: restart},
		{name: "systemd reload", strict: true, do: reload},
		{name: "systemd status active", strict: true, do: status},
		{name: "systemd start-systemd", strict: true, do: startSystemd},
		{name: "systemd stop-systemd", strict: true, do: stopSystemd},
	}
}

// keepTraceOp drops what a launcher is free to vary without any consumer
// seeing it: reads of files, and the release of started child handles.
func keepTraceOp(op systest.Op) bool {
	switch {
	case op.Kind == "fs.read", op.Kind == "exec.release":
		return false
	case strings.Contains(op.Detail, "/notify-"):
		// The stop-side sd_notify observer is an asynchronous goroutine.
		return false
	default:
		return true
	}
}

// TestServiceTraces pins, for every registered service, the exact sequence of
// commands, signals and file mutations of every legacy CLI verb in both
// orchestration modes (the golden file). It is the safety net of the
// registry refactor: the launchers may be restructured freely as long as this
// trace does not change.
func TestServiceTraces(t *testing.T) {
	var out strings.Builder

	names := newTraceHost(t, false).ctl.AllServiceNames()

	for _, svc := range names {
		for _, sc := range traceScenarios() {
			fmt.Fprintf(&out, "## %s | %s\n", svc, sc.name)

			h := newTraceHost(t, sc.strict)
			if sc.down {
				h.postfixDown()
			}

			pid := 1000
			if sc.running {
				h.run(svc, &pid)
			}

			h.f.Rec.Reset()

			stop := pumpClock(t, h.f.Clock, time.Second)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			res := sc.do(ctx, h.ctl, svc)

			cancel()
			stop()

			for _, op := range h.f.Rec.Ops() {
				if keepTraceOp(op) {
					fmt.Fprintf(&out, "%s\n", strings.ReplaceAll(op.String(), h.runDir, defaultRunDir))
				}
			}

			fmt.Fprintf(&out, "=> %s\n\n", strings.ReplaceAll(res, h.runDir, defaultRunDir))
		}
	}

	if *updateTraces {
		require.NoError(t, os.WriteFile(traceGoldenPath, []byte(out.String()), 0o644))

		return
	}

	want, err := os.ReadFile(traceGoldenPath)
	require.NoError(t, err, "run with -update-traces to create the golden file")

	if string(want) != out.String() {
		t.Fatalf("service lifecycle traces changed:\n%s", firstDifference(string(want), out.String()))
	}
}

// firstDifference renders the first diverging lines of two traces with their
// scenario header.
func firstDifference(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	header := ""

	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}

		if i < len(gl) {
			g = gl[i]
		}

		if strings.HasPrefix(g, "## ") {
			header = g
		}

		if w != g {
			return fmt.Sprintf("%s (line %d)\n  want: %s\n  got:  %s", header, i+1, w, g)
		}
	}

	return "no difference"
}
