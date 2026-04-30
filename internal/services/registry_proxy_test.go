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
