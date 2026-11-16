// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

// registerThrottlingVariables registers throttling and rate limit configuration variables
func (g *Generator) registerThrottlingVariables() {
	g.registerVar("mail.limit.iplogin", 0,
		withAttribute("zimbraReverseProxyIPLoginLimit"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("Maximum login attempts per IP (0 = unlimited)"),
	)
	g.registerVar("mail.limit.iplogintime", "1h",
		withAttribute("zimbraReverseProxyIPLoginLimitTime"),
		withValueType(ValueTypeTime),
		withOverrideType(OverrideConfig),
		withDescription("Time window for IP-based login limits"),
	)
	g.registerMessageVar("mail.limit.ipthrottlemsg", "zimbraReverseProxyIpThrottleMsg",
		"Login rejected due to IP address throttle", "Error message shown when IP is throttled")
	g.registerVar("mail.limit.userlogin", 0,
		withAttribute("zimbraReverseProxyUserLoginLimit"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("Maximum login attempts per user (0 = unlimited)"),
	)
	g.registerVar("mail.limit.userlogintime", "1h",
		withAttribute("zimbraReverseProxyUserLoginLimitTime"),
		withValueType(ValueTypeTime),
		withOverrideType(OverrideConfig),
		withDescription("Time window for user-based login limits"),
	)
	g.registerMessageVar("mail.limit.userthrottlemsg", "zimbraReverseProxyUserThrottleMsg",
		"Login rejected due to user throttle", "Error message shown when user is throttled")
}
