// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"strings"
	"testing"
)

func TestProxyProcessNameDoesNotCollideWithPrometheusExporter(t *testing.T) {
	def := LookupService("proxy")
	if def == nil {
		t.Fatal("proxy service not found in registry")
	}

	if def.ProcessName == "" {
		t.Fatal("proxy.ProcessName is empty")
	}

	const nginxMaster = "/opt/zextras/common/sbin/nginx -c /opt/zextras/conf/nginx.conf"
	const prometheusExporter = "/usr/bin/carbonio-prometheus-nginx-exporter " +
		"--nginx.scrape-uri=https://localhost/nginx_status --no-nginx.ssl-verify"

	if !strings.Contains(nginxMaster, def.ProcessName) {
		t.Errorf("proxy.ProcessName %q does not match nginx master cmdline %q",
			def.ProcessName, nginxMaster)
	}

	if strings.Contains(prometheusExporter, def.ProcessName) {
		t.Errorf("proxy.ProcessName %q falsely matches prometheus exporter cmdline %q "+
			"(this would make zmcontrol status report the exporter PID as nginx)",
			def.ProcessName, prometheusExporter)
	}
}

// TestAntivirusProcessNameDoesNotCollideWithConfigReferences verifies the
// antivirus (clamd) ProcessName matches the real clamd master cmdline but NOT a
// process that merely references clamd.conf in its argv (e.g. a transient
// config-rewrite helper). A bare "clamd" substring would false-match the latter,
// making ServiceStatus report antivirus running while clamd is down and making
// ServiceStart skip clamd via the already-running short-circuit.
func TestAntivirusProcessNameDoesNotCollideWithConfigReferences(t *testing.T) {
	def := LookupService("antivirus")
	if def == nil {
		t.Fatal("antivirus service not found in registry")
	}

	if def.ProcessName == "" {
		t.Fatal("antivirus.ProcessName is empty")
	}

	const clamdMaster = "/opt/zextras/common/sbin/clamd --config-file=/opt/zextras/conf/clamd.conf"
	const configReference = "/bin/sh -c postconf -e something; cat /opt/zextras/conf/clamd.conf"

	if !strings.Contains(clamdMaster, def.ProcessName) {
		t.Errorf("antivirus.ProcessName %q does not match clamd master cmdline %q",
			def.ProcessName, clamdMaster)
	}

	if strings.Contains(configReference, def.ProcessName) {
		t.Errorf("antivirus.ProcessName %q falsely matches a process that only "+
			"references clamd.conf %q", def.ProcessName, configReference)
	}
}
