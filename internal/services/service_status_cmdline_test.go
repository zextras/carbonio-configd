// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"os/exec"
	"testing"
)

// startFakeProcess runs a process whose /proc/<pid>/cmdline contains argv
// (`sh -c "sleep 30; :" argv...`; the trailing ":" keeps the shell from
// exec'ing sleep and losing argv) and kills it when the test ends.
func startFakeProcess(t *testing.T, argv ...string) {
	t.Helper()

	cmd := exec.CommandContext(context.Background(), "sh", append([]string{"-c", "sleep 30; :"}, argv...)...)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fake process: %v", err)
	}

	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
}

// A process that merely mentions the service name (Consul Connect sidecar
// "-sidecar-for carbonio-<svc>", a tail on its log, ...) must not make the
// service look running in legacy mode; the service binary itself must.
// Before CO-4569 the bare-name ProcessName made memcached look "already
// running" at boot, so zmcontrol skipped starting it.
func TestServiceStatus_IgnoresProcessesMentioningTheName(t *testing.T) {
	withMode(t, false)

	for _, tc := range []struct {
		service   string
		unrelated []string
	}{
		{svcMemcached, []string{
			"consul", "connect", "envoy", "-token-file", "/etc/carbonio/memcached/service-discover/token",
			"-sidecar-for", "carbonio-memcached",
		}},
		{svcCbpolicyd, []string{"tail", "-f", "/opt/zextras/log/cbpolicyd.log"}},
		{svcOpendkim, []string{"vi", "/opt/zextras/conf/opendkim.conf"}},
		{svcFreshclam, []string{"tail", "-f", "/opt/zextras/log/freshclam.log"}},
		{svcSaslauthd, []string{"consul", "connect", "envoy", "-sidecar-for", "carbonio-saslauthd"}},
		{svcAntivirus, []string{"tail", "-f", "/opt/zextras/log/clamd.log"}},
		{svcLdap, []string{"less", "/opt/zextras/log/slapd.log"}},
	} {
		t.Run(tc.service, func(t *testing.T) {
			def := LookupService(tc.service)
			if running, _ := isRunningByPidFile(def.PidFile); running {
				t.Skipf("%s pid file reports a live process on this host", tc.service)
			}

			if pidFromProcessName(context.Background(), def.ProcessName) > 0 {
				t.Skipf("%s is really running on this host", tc.service)
			}

			startFakeProcess(t, tc.unrelated...)

			if running, err := ServiceStatus(context.Background(), tc.service); err != nil || running {
				t.Errorf("unrelated process %v: ServiceStatus = %v, %v; want false, nil", tc.unrelated, running, err)
			}

			startFakeProcess(t, def.ProcessName, "-d")

			if running, err := ServiceStatus(context.Background(), tc.service); err != nil || !running {
				t.Errorf("process %q: ServiceStatus = %v, %v; want true, nil", def.ProcessName, running, err)
			}
		})
	}
}
