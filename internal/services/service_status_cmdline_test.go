// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"testing"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// A process that merely mentions the service name (Consul Connect sidecar
// "-sidecar-for carbonio-<svc>", a tail on its log, ...) must not make the
// service look running in legacy mode; the service binary itself must.
// Before CO-4569 the bare-name ProcessName made memcached look "already
// running" at boot, so zmcontrol skipped starting it.
//
// The process table is the systest fake, so the verdict depends neither on the
// host's /proc (a really running slapd/memcached, a concurrent copy of this
// test) nor on real child processes.
func TestServiceStatus_IgnoresProcessesMentioningTheName(t *testing.T) {
	t.Parallel()

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
		{svcClamd, []string{"tail", "-f", "/opt/zextras/log/clamd.log"}},
		{svcLdap, []string{"less", "/opt/zextras/log/slapd.log"}},
	} {
		t.Run(tc.service, func(t *testing.T) {
			t.Parallel()

			ctl, f := newFakeController(t)
			def := ctl.LookupService(tc.service)

			f.Proc.Add(sys.ProcEntry{PID: 4001, Argv: tc.unrelated})

			if running, err := ctl.ServiceStatus(context.Background(), tc.service); err != nil || running {
				t.Errorf("unrelated process %v: ServiceStatus = %v, %v; want false, nil", tc.unrelated, running, err)
			}

			f.Proc.Add(sys.ProcEntry{PID: 4002, Argv: []string{def.ProcessName, "-d"}})

			if running, err := ctl.ServiceStatus(context.Background(), tc.service); err != nil || !running {
				t.Errorf("process %q: ServiceStatus = %v, %v; want true, nil", def.ProcessName, running, err)
			}
		})
	}
}
