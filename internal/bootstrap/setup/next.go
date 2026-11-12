// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
)

// adminProxyPort is the port the proxy serves the admin console on
// (web.carbonio.admin.port).
const adminProxyPort = 6071

// httpsPort is the default HTTPS port, left out of a URL.
const httpsPort = 443

// Next is what the operator does after a run.
type Next struct {
	// StartCommand starts the services when the run did not (start_services
	// is off); empty when they were started.
	StartCommand string
	// Admin and Webmail are the console URLs of the node ("" when the node
	// does not serve them).
	Admin, Webmail string
}

// roleTargets are the systemd targets of the roles, in start order.
var roleTargets = []struct{ role, target string }{
	{bootstrap.RoleDirectoryServer, "carbonio-directory-server.target"},
	{bootstrap.RoleAppserver, "carbonio-appserver.target"},
	{bootstrap.RoleProxy, "carbonio-proxy.target"},
	{bootstrap.RoleMTA, "carbonio-mta.target"},
}

// NextSteps works out what to tell the operator once the node is configured.
func NextSteps(e *Env) Next {
	var n Next

	if !e.Cfg.StartServices {
		n.StartCommand = startCommand(e)
	}

	if !e.Cfg.HasRole(bootstrap.RoleProxy) || !e.Cfg.Proxy.WebEnabled {
		return n
	}

	host := e.Cfg.Host.PublicHostname
	if host == "" {
		host = e.Cfg.Host.Hostname
	}

	n.Admin = "https://" + host + ":" + strconv.Itoa(adminProxyPort)
	n.Webmail = "https://" + host

	if p := e.Cfg.Proxy.Ports.HTTPS; p != 0 && p != httpsPort {
		n.Webmail += ":" + strconv.Itoa(p)
	}

	return n
}

// startCommand is the command that starts the node's services: systemctl
// for the role targets on a systemd node, zmcontrol otherwise.
func startCommand(e *Env) string {
	if !e.Facts.Systemd {
		return "su - zextras -c 'zmcontrol start'"
	}

	var targets []string

	for _, rt := range roleTargets {
		if e.Cfg.HasRole(rt.role) {
			targets = append(targets, rt.target)
		}
	}

	if len(targets) == 0 {
		targets = svcSystemdTargets
	}

	return "systemctl start " + strings.Join(targets, " ")
}
