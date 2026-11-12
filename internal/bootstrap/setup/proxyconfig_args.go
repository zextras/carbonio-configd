// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"fmt"
	"io"
	"strings"
)

// ProxyConfigOptions are the options of libexec/zmproxyconfig, the Perl
// script this package replaces (getopts 'a:defhH:i:mop:rstwx:cCn:uU'). An
// option that takes a value and was given "" or "0" is unset, as Perl's
// truthiness made it.
type ProxyConfigOptions struct {
	Enable          bool     // -e
	Disable         bool     // -d
	Mail            bool     // -m: the mail proxy portions
	Web             bool     // -w: the web proxy portions
	Override        bool     // -o: act on an installed but not enabled store
	Remote          bool     // -r: the host is not this one; use LDAP for the package checks
	ClearTextOff    bool     // -s: IMAP and POP3 cleartext login FALSE on disable
	LookupTargetOff bool     // -t: zimbraReverseProxyLookupTarget FALSE on disable
	Full            bool     // -f: reset the reverse proxy queries, port attributes and throttling
	Help            bool     // -h
	AdminOff        bool     // -c: the admin console proxy off
	AdminOn         bool     // -C: the admin console proxy on
	NoUpstreamSSL   bool     // -u: SSL to the upstream off
	UpstreamSSL     bool     // -U: SSL to the upstream on
	Host            string   // -H
	WebPorts        string   // -a HTTP-STORE:HTTP-PROXY:HTTPS-STORE:HTTPS-PROXY
	ImapPorts       string   // -i IMAP-STORE:IMAP-PROXY:IMAPS-STORE:IMAPS-PROXY
	PopPorts        string   // -p POP-STORE:POP-PROXY:POPS-STORE:POPS-PROXY
	AdminPorts      string   // -n ADMIN-STORE:ADMIN-PROXY
	Mode            string   // -x the proxy mail mode (enable) or the store mail mode (disable)
	Extra           []string // arguments left after the options
}

// pcOptionFlags are the options without a value, by letter.
var pcOptionFlags = map[byte]func(*ProxyConfigOptions){
	'd': func(o *ProxyConfigOptions) { o.Disable = true },
	'e': func(o *ProxyConfigOptions) { o.Enable = true },
	'f': func(o *ProxyConfigOptions) { o.Full = true },
	'h': func(o *ProxyConfigOptions) { o.Help = true },
	'm': func(o *ProxyConfigOptions) { o.Mail = true },
	'o': func(o *ProxyConfigOptions) { o.Override = true },
	'r': func(o *ProxyConfigOptions) { o.Remote = true },
	's': func(o *ProxyConfigOptions) { o.ClearTextOff = true },
	't': func(o *ProxyConfigOptions) { o.LookupTargetOff = true },
	'w': func(o *ProxyConfigOptions) { o.Web = true },
	'c': func(o *ProxyConfigOptions) { o.AdminOff = true },
	'C': func(o *ProxyConfigOptions) { o.AdminOn = true },
	'u': func(o *ProxyConfigOptions) { o.NoUpstreamSSL = true },
	'U': func(o *ProxyConfigOptions) { o.UpstreamSSL = true },
}

// pcOptionValues are the options that take a value, by letter.
var pcOptionValues = map[byte]func(*ProxyConfigOptions, string){
	'a': func(o *ProxyConfigOptions, v string) { o.WebPorts = v },
	'H': func(o *ProxyConfigOptions, v string) { o.Host = v },
	'i': func(o *ProxyConfigOptions, v string) { o.ImapPorts = v },
	'p': func(o *ProxyConfigOptions, v string) { o.PopPorts = v },
	'x': func(o *ProxyConfigOptions, v string) { o.Mode = v },
	'n': func(o *ProxyConfigOptions, v string) { o.AdminPorts = v },
}

// ParseProxyConfigArgs parses args as Getopt::Std did: options may be
// bundled (-ume), a value follows its letter directly or as the next
// argument, "--" ends the options and so does the first argument that is not
// one. Unknown options are reported on stderr like Getopt::Std; ok is false
// when there was one or a value is missing.
func ParseProxyConfigArgs(args []string, stderr io.Writer) (o *ProxyConfigOptions, ok bool) {
	o = &ProxyConfigOptions{}
	ok = true

	i := 0
	for i < len(args) && len(args[i]) > 1 && args[i][0] == '-' {
		cluster := args[i][1:]
		i++

		if cluster == "-" {
			break
		}

		var good bool

		i, good = o.parseCluster(cluster, args, i, stderr)
		ok = ok && good
	}

	if i < len(args) {
		o.Extra = args[i:]
	}

	o.normalize()

	return o, ok
}

// parseCluster applies the options of one "-xyz" argument; it returns the
// index of the next argument, after any value it consumed.
func (o *ProxyConfigOptions) parseCluster(cluster string, args []string, next int, stderr io.Writer) (int, bool) {
	ok := true

	for pos := range len(cluster) {
		letter := cluster[pos]

		if set, isFlag := pcOptionFlags[letter]; isFlag {
			set(o)

			continue
		}

		set, takesValue := pcOptionValues[letter]
		if !takesValue {
			_, _ = fmt.Fprintf(stderr, "Unknown option: %c\n", letter)
			ok = false

			continue
		}

		value := cluster[pos+1:]

		if value == "" {
			if next >= len(args) {
				return next, false
			}

			value = args[next]
			next++
		}

		set(o, value)

		break
	}

	return next, ok
}

// normalize turns the values Perl reads as false ("" and "0") into unset.
func (o *ProxyConfigOptions) normalize() {
	for _, v := range []*string{&o.Host, &o.WebPorts, &o.ImapPorts, &o.PopPorts, &o.AdminPorts, &o.Mode} {
		if *v == "0" {
			*v = ""
		}
	}
}

// pcUsageError is a refusal of the options: msg is printed first (when set),
// then the usage text, and the exit status is 1.
type pcUsageError struct{ msg string }

func (u *pcUsageError) Error() string { return u.msg }

const (
	pcMsgAdminNeedsWeb = "Error: web proxy configure should be specified (-w) before admin console proxy is configured.\n"
	pcMsgNeedsAdmin    = "Error: admin console proxy ports should be configured when admin console proxy is " +
		"configured (-C/-c).\n"
	pcMsgDisableWebAdmin = "Error: when disable web proxy, you can't enable admin console proxy. " +
		"Instead use \"-c -n n1:n2\" to specify the admin console ports after proxy is disabled.\n"
	pcMsgUpstreamNeedsEnable = "Error: it's meaningful to specify \"SSL to upstream\" configure only when " +
		"enable proxy (-e).\n"
	pcMsgUpstreamBoth = "Error: -u and -U can't be set together.\n"
)

// pcRule is one option check of zmproxyconfig: bad is true when the options
// are refused, with msg ahead of the usage text.
type pcRule struct {
	bad func(o *ProxyConfigOptions) bool
	msg string
}

func (o *ProxyConfigOptions) has() bool { return o.Disable || o.Enable }

// pcRules are the checks of zmproxyconfig 55-142, in the script's order. The
// first that fails decides the message.
var pcRules = []pcRule{
	{func(o *ProxyConfigOptions) bool { return o.Help }, ""},                                 // 55
	{func(o *ProxyConfigOptions) bool { return o.Host == "" }, ""},                           // 60
	{func(o *ProxyConfigOptions) bool { return o.Disable && o.Enable }, ""},                  // 65
	{func(o *ProxyConfigOptions) bool { return !o.Full && !o.has() }, ""},                    // 71
	{func(o *ProxyConfigOptions) bool { return !o.Full && !o.Mail && !o.Web }, ""},           // 76
	{func(o *ProxyConfigOptions) bool { return o.Full && o.has() && !o.Web && !o.Mail }, ""}, // 81
	{func(o *ProxyConfigOptions) bool {
		return !o.has() && (o.ImapPorts != "" || o.PopPorts != "" || o.WebPorts != "")
	}, ""}, // 87
	{func(o *ProxyConfigOptions) bool {
		return o.has() && (o.ImapPorts != "" || o.PopPorts != "") && !o.Mail
	}, ""}, // 92
	{func(o *ProxyConfigOptions) bool { return o.has() && o.WebPorts != "" && !o.Web }, ""},               // 96
	{func(o *ProxyConfigOptions) bool { return o.Disable && o.Mode != "" && !o.Web }, ""},                 // 100
	{func(o *ProxyConfigOptions) bool { return o.Enable && o.Mode != "" && !o.Web }, ""},                  // 104
	{func(o *ProxyConfigOptions) bool { return o.LookupTargetOff && !o.Disable }, ""},                     // 109
	{func(o *ProxyConfigOptions) bool { return (o.AdminOff || o.AdminOn) && !o.Web }, pcMsgAdminNeedsWeb}, // 114
	{func(o *ProxyConfigOptions) bool {
		return o.AdminPorts != "" && !o.AdminOff && !o.AdminOn
	}, pcMsgNeedsAdmin}, // 120
	{func(o *ProxyConfigOptions) bool { return o.Disable && o.Web && o.AdminOn }, pcMsgDisableWebAdmin}, // 126
	{func(o *ProxyConfigOptions) bool {
		return (o.NoUpstreamSSL || o.UpstreamSSL) && !o.Enable
	}, pcMsgUpstreamNeedsEnable}, // 132
	{func(o *ProxyConfigOptions) bool { return o.NoUpstreamSSL && o.UpstreamSSL }, pcMsgUpstreamBoth}, // 138
}

// Check applies the option checks of zmproxyconfig; the error is a
// *pcUsageError.
func (o *ProxyConfigOptions) Check() error {
	for _, r := range pcRules {
		if r.bad(o) {
			return &pcUsageError{msg: r.msg}
		}
	}

	return nil
}

// ProxyConfigUsage is the text zmproxyconfig printed for -h and for refused
// options; program is its path.
func ProxyConfigUsage(program string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Usage: %s [-h] [-o] [-m] [-w] [-d [-r] [-s] [-a w1:w2:w3:w4] [-c [-n n1:n2]] "+
		"[-i p1:p2:p3:p4] [-p p1:p2:p3:p4] [-x mailmode]] [-e [-a w1:w2:w3:w4] [[-c|-C] [-n n1:n2]] "+
		"[-i p1:p2:p3:p4] [-p p1:p2:p3:p4] [-u|-U] [-x mailmode]] [-f] -H hostname\n", program)
	b.WriteString(pcUsageBody)
	b.WriteString("\n\n")

	return b.String()
}

const pcUsageBody = "\t-h: display this help message\n" +
	"\t-H: Hostname of server on which enable/disable proxy functionality.\n" +
	"\t-a: Colon separated list of Web ports to use. Format: HTTP-STORE:HTTP-PROXY:HTTPS-STORE:HTTPS-PROXY " +
	"(Ex: 8080:80:8443:443)\n" +
	"\t-d: disable proxy\n" +
	"\t-e: enable proxy\n" +
	"\t-f: Full reset on reverse proxy queries, port attributes, and POP/IMAP throttling.\n" +
	"\t-i: Colon separated list of IMAP ports to use. Format: IMAP-STORE:IMAP-PROXY:IMAPS-STORE:IMAPS-PROXY " +
	"(Ex: 7143:143:7993:993)\n" +
	"\t-m: Toggle mail proxy portions\n" +
	"\t-o: Override enabled checks\n" +
	"\t-p: Colon separated list of POP ports to use. Format: POP-STORE:POP-PROXY:POPS-STORE:POPS-PROXY " +
	"(Ex: 7110:110:7995:995)\n" +
	"\t-r: Run against a remote host.  Note that this requires the server to be properly configured in the " +
	"LDAP master.\n" +
	"\t-s: Set cleartext to FALSE (secure mode) on disable\n" +
	"\t-t: Disable reverse proxy lookup target for store server.  Only valid with -d.  Be sure that you " +
	"intend for all proxy function for the server to be disabled\n" +
	"\t-w: Toggle Web proxy portions\n" +
	"\t-c: Disable Admin Console proxy portions.\n" +
	"\t-C: Enable Admin Console proxy portions.\n" +
	"\t-n: Colon separated list of Admin Console ports to use. Format: ADMIN-CONSOLE-STORE:ADMIN-CONSOLE-PROXY " +
	"(Ex: 7071:9071)\n" +
	"\t-x: the proxy mail mode when enable proxy, or the store mail mode when disable proxy " +
	"(Both default: http).\n" +
	"\t-u: disable SSL connection from proxy to mail store.\n" +
	"\t-U: enable SSL connection from proxy to mail store.\n" +
	"hostname is the value of the zimbra_server_hostname LC key for the server being modified.\n" +
	"Required options are -f by itself, or -f with -d or -e.\n" +
	"Note that -d or -e require one or both of -m and -w.\n" +
	"Note that -i or -p require -m.\n" +
	"Note that -a requires -w.\n" +
	"Note that -c/-C requires -w, and -n requires -c/-C. When disabling web proxy, admin console proxy will " +
	"be automatically disabled.\n" +
	"Note that -u or -U are only available when proxy is enabled by -e.\n" +
	"Note that -x requires -w and -d for store.\n" +
	"Note that -x requires -w for proxy.\n" +
	"Note that no matter what mail mode is set by -x and no matter proxy is enabled or disabled, " +
	"admin console's mode is always https.\n" +
	"The following are the defaults for -a, -i, -p, and -x if they are not supplied as options.\n" +
	"-a default on enable: 8080:80:8443:443\n" +
	"-a default on disable: 80:0:443:0\n" +
	"-i default on enable: 7143:143:7993:993\n" +
	"-i default on disable: 143:7143:993:7993\n" +
	"-p default on enable: 7110:110:7995:995\n" +
	"-p default on disable: 110:7110:995:7995\n" +
	"-n default on enable: 7071:9071\n" +
	"-n default on disable: 7071:9071\n" +
	"-x default on store disable: http\n" +
	"-x default on proxy enable/disable: http, but -x default on proxy enable when upstream ssl connection " +
	"is enabled: https\n"
