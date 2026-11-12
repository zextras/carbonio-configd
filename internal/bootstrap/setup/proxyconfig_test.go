// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The expectations below are read off libexec/zmproxyconfig (the Perl script
// this code replaces); the comments cite its line numbers.

const pcTestHost = "mail.example.com"

type pcWrite struct {
	dn                string
	add, del, replace map[string][]string
}

// pcFakeWriter records the zmprov commands of a run, split into the
// modifications they make.
type pcFakeWriter struct {
	writes []pcWrite
	err    error
}

// run is pcRunProv: "ms <host> attr value ..." (dn "ms <host>") or
// "mcf attr value ..." (dn "mcf"), +attr and -attr adding and deleting a value.
func (w *pcFakeWriter) run(_ *Env, p *Prov) error {
	for _, c := range p.cmds {
		wr := pcWrite{dn: c[0], add: map[string][]string{}, del: map[string][]string{}, replace: map[string][]string{}}
		rest := c[1:]

		if c[0] == "ms" {
			wr.dn, rest = "ms "+c[1], c[2:]
		}

		for i := 0; i+1 < len(rest); i += 2 {
			attr, val := rest[i], rest[i+1]

			switch {
			case strings.HasPrefix(attr, "+"):
				wr.add[attr[1:]] = append(wr.add[attr[1:]], val)
			case strings.HasPrefix(attr, "-"):
				wr.del[attr[1:]] = append(wr.del[attr[1:]], val)
			default:
				wr.replace[attr] = append(wr.replace[attr], val)
			}
		}

		w.writes = append(w.writes, wr)
	}

	return w.err
}

func pcEnt(kv ...string) srvEntry {
	ent := srvEntry{}

	for i := 0; i+1 < len(kv); i += 2 {
		ent[strings.ToLower(kv[i])] = append(ent[strings.ToLower(kv[i])], kv[i+1])
	}

	return ent
}

// pcScenario is the node a run starts from.
type pcScenario struct {
	server, global srvEntry // a nil server is not in LDAP
	installed      []string
	lcHost         string
	vips           []srvEntry
}

type pcResult struct {
	code     int
	out, err string
	writes   []pcWrite
	e        *Env
}

func (r pcResult) replaced(t *testing.T) map[string]string {
	t.Helper()

	require.Len(t, r.writes, 1, "one LDAP modify per entry: %s", r.err)

	out := map[string]string{}
	for k, v := range r.writes[0].replace {
		require.Len(t, v, 1)

		out[k] = v[0]
	}

	return out
}

func pcSetup(t *testing.T, sc pcScenario) (*Env, *pcFakeWriter, pcIO, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()

	dir := &srvFakeDir{global: sc.global, servers: map[string]srvEntry{}, vips: sc.vips}
	if sc.server != nil {
		dir.servers[pcTestHost] = sc.server
	}

	e, _, _ := srvTestEnv(t, dir)
	w := pcUseFake(t, e, sc.lcHost)

	var out, errw bytes.Buffer

	pio := pcIO{out: &out, err: &errw, installed: func(pkg string) (bool, error) {
		for _, p := range sc.installed {
			if p == pkg {
				return true, nil
			}
		}

		return false, nil
	}}

	return e, w, pio, &out, &errw
}

// pcUseFake makes the run write to a recorder and gives the node its host name
// (zimbra_server_hostname, default pcTestHost).
func pcUseFake(t *testing.T, e *Env, lcHost string) *pcFakeWriter {
	t.Helper()

	if lcHost == "" {
		lcHost = pcTestHost
	}

	require.NoError(t, os.WriteFile(e.Paths.LocalConfig, []byte(
		`<?xml version="1.0"?><localconfig><key name="zimbra_server_hostname"><value>`+lcHost+
			`</value></key></localconfig>`), 0o600))

	w := &pcFakeWriter{}
	old := pcRunProv
	pcRunProv = w.run

	t.Cleanup(func() { pcRunProv = old })

	return w
}

// pcRun runs one zmproxyconfig command line (words split on spaces).
func pcRun(t *testing.T, sc pcScenario, args string) pcResult {
	t.Helper()

	e, w, pio, out, errw := pcSetup(t, sc)

	o, ok := ParseProxyConfigArgs(strings.Fields(args), errw)
	require.True(t, ok)

	code := proxyConfig(e, pio, o)

	return pcResult{code: code, out: out.String(), err: errw.String(), writes: w.writes, e: e}
}

var (
	pcBoth      = []string{PkgAppserver, srvPkgProxy}
	pcStoreOnly = []string{PkgAppserver}
	pcProxyOnly = []string{srvPkgProxy}
)

func pcMailPorts(imap, pop string) map[string]string {
	i, p := strings.Split(imap, ":"), strings.Split(pop, ":")

	return map[string]string{
		"zimbraImapBindPort": i[0], "zimbraImapProxyBindPort": i[1],
		"zimbraImapSSLBindPort": i[2], "zimbraImapSSLProxyBindPort": i[3],
		"zimbraPop3BindPort": p[0], "zimbraPop3ProxyBindPort": p[1],
		"zimbraPop3SSLBindPort": p[2], "zimbraPop3SSLProxyBindPort": p[3],
	}
}

func pcWebPorts(web string) map[string]string {
	w := strings.Split(web, ":")

	return map[string]string{
		"zimbraMailPort": w[0], "zimbraMailProxyPort": w[1], "zimbraMailSSLPort": w[2], "zimbraMailSSLProxyPort": w[3],
	}
}

func pcMerge(maps ...map[string]string) map[string]string {
	out := map[string]string{}

	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}

	return out
}

func pcAdmin(enabled, store, proxy string) map[string]string {
	return map[string]string{
		"zimbraReverseProxyAdminEnabled": enabled, "zimbraAdminPort": store, "zimbraAdminProxyPort": proxy,
	}
}

func TestProxyConfig_AttributesPerMode(t *testing.T) {
	storeOnServer := pcEnt("zimbraServiceEnabled", "mailbox")
	bothOnServer := pcEnt("zimbraServiceEnabled", "mailbox", "zimbraServiceEnabled", "proxy")

	tests := []struct {
		name      string
		sc        pcScenario
		args      string
		want      map[string]string
		wantAdded bool // +zimbraServiceEnabled proxy
	}{
		{
			// 239-251: store and proxy on one host, mail; 282-284 the upstream; 286-288 the service.
			name: "enable mail, store and proxy, no upstream SSL",
			sc:   pcScenario{server: storeOnServer, installed: pcBoth},
			args: "-u -m -e -o -i 7143:143:7993:993 -p 7110:110:7995:995 -H " + pcTestHost,
			want: pcMerge(pcMailPorts("7143:143:7993:993", "7110:110:7995:995"), map[string]string{
				"zimbraImapCleartextLoginEnabled": "TRUE", "zimbraPop3CleartextLoginEnabled": "TRUE",
				"zimbraReverseProxyLookupTarget": "TRUE", "zimbraReverseProxyMailEnabled": "TRUE",
				"zimbraReverseProxySSLToUpstreamEnabled": "FALSE",
			}),
			wantAdded: true,
		},
		{
			// 252-281: web with SSL to the upstream (-U): getMailMode 781-812 makes the
			// store "both" (it was http) and keeps the proxy mode -x.
			name: "enable web, store and proxy, upstream SSL, https proxy",
			sc:   pcScenario{server: pcEnt("zimbraServiceEnabled", "mailbox", "zimbraMailMode", "http"), installed: pcBoth},
			args: "-U -w -e -o -x https -a 8080:80:8443:443 -H " + pcTestHost,
			want: pcMerge(pcWebPorts("8080:80:8443:443"), map[string]string{
				"zimbraMailReferMode": "reverse-proxied", "zimbraMailMode": "both",
				"zimbraReverseProxyMailMode": "https", "zimbraReverseProxyLookupTarget": "TRUE",
				"zimbraReverseProxyHttpEnabled": "TRUE", "zimbraReverseProxySSLToUpstreamEnabled": "TRUE",
			}),
			wantAdded: true,
		},
		{
			// 781-812: SSL to the upstream and an https store stay https; an unset -x gives an https proxy.
			name: "enable web, upstream SSL, https store, default proxy mode",
			sc: pcScenario{
				server:    pcEnt("zimbraServiceEnabled", "mailbox", "zimbraServiceEnabled", "proxy", "zimbraMailMode", "https"),
				installed: pcBoth,
			},
			args: "-U -w -e -o -H " + pcTestHost,
			want: pcMerge(pcWebPorts("8080:80:8443:443"), map[string]string{
				// zimbraMailMode https is what the server stores already.
				"zimbraMailReferMode":        "reverse-proxied",
				"zimbraReverseProxyMailMode": "https", "zimbraReverseProxyLookupTarget": "TRUE",
				"zimbraReverseProxyHttpEnabled": "TRUE", "zimbraReverseProxySSLToUpstreamEnabled": "TRUE",
			}),
		},
		{
			// 799-808: without SSL to the upstream the store is http and the proxy takes -x.
			name: "enable web, no upstream SSL, redirect proxy",
			sc:   pcScenario{server: bothOnServer, installed: pcBoth},
			args: "-u -w -e -o -x redirect -H " + pcTestHost,
			want: pcMerge(pcWebPorts("8080:80:8443:443"), map[string]string{
				"zimbraMailReferMode": "reverse-proxied", "zimbraMailMode": "http",
				"zimbraReverseProxyMailMode": "redirect", "zimbraReverseProxyLookupTarget": "TRUE",
				"zimbraReverseProxyHttpEnabled": "TRUE", "zimbraReverseProxySSLToUpstreamEnabled": "FALSE",
			}),
		},
		{
			// 146-153: the upstream value comes from LDAP (the global config here) when -u/-U
			// is not given; 266-269: -C turns the admin console proxy on with -n.
			name: "enable web, upstream SSL from the global config, admin console proxy",
			sc: pcScenario{
				server: bothOnServer, global: pcEnt("zimbraReverseProxySSLToUpstreamEnabled", "TRUE", "zimbraMailMode", "both"),
				installed: pcBoth,
			},
			args: "-w -e -C -n 7072:9072 -x https -H " + pcTestHost,
			want: pcMerge(pcWebPorts("8080:80:8443:443"), pcAdmin("TRUE", "7072", "9072"), map[string]string{
				"zimbraMailReferMode": "reverse-proxied", "zimbraMailMode": "both",
				"zimbraReverseProxyMailMode": "https", "zimbraReverseProxyLookupTarget": "TRUE",
				"zimbraReverseProxyHttpEnabled": "TRUE", "zimbraReverseProxySSLToUpstreamEnabled": "TRUE",
			}),
		},
		{
			// 334-346: a proxy node: ports and the switch, no store attributes; 375-379 the service.
			name: "enable mail, proxy only",
			sc:   pcScenario{server: pcEnt(), installed: pcProxyOnly},
			args: "-U -m -e -o -H " + pcTestHost,
			want: pcMerge(pcMailPorts("7143:143:7993:993", "7110:110:7995:995"), map[string]string{
				"zimbraReverseProxyMailEnabled": "TRUE", "zimbraReverseProxySSLToUpstreamEnabled": "TRUE",
			}),
			wantAdded: true,
		},
		{
			// 347-360: web on a proxy node: the proxy mail mode, no store mode.
			name: "enable web, proxy only",
			sc:   pcScenario{server: pcEnt("zimbraServiceEnabled", "proxy"), installed: pcProxyOnly},
			args: "-u -w -e -o -x redirect -a 9080:81:9443:444 -c -n 7071:9071 -H " + pcTestHost,
			want: pcMerge(pcWebPorts("9080:81:9443:444"), pcAdmin("FALSE", "7071", "9071"), map[string]string{
				"zimbraReverseProxyMailMode": "redirect", "zimbraReverseProxyHttpEnabled": "TRUE",
				"zimbraReverseProxySSLToUpstreamEnabled": "FALSE",
			}),
		},
		{
			// 291-305: a store without a proxy on the host; -o makes an installed but not enabled one count.
			name: "enable mail, store only, -o",
			sc:   pcScenario{server: pcEnt(), installed: pcStoreOnly},
			args: "-u -m -e -o -H " + pcTestHost,
			want: pcMerge(pcMailPorts("7143:143:7993:993", "7110:110:7995:995"), map[string]string{
				"zimbraImapCleartextLoginEnabled": "TRUE", "zimbraPop3CleartextLoginEnabled": "TRUE",
				"zimbraReverseProxyLookupTarget": "TRUE", "zimbraReverseProxySSLToUpstreamEnabled": "FALSE",
			}),
		},
		{
			// 306-318: the store web branch; 322-328: with -c the Perl script sent n2 as zimbraAdminPort.
			name: "enable web, store only, admin console proxy off",
			sc:   pcScenario{server: pcEnt("zimbraServiceEnabled", "mailbox"), installed: pcStoreOnly},
			args: "-u -w -e -c -H " + pcTestHost,
			want: pcMerge(pcWebPorts("8080:80:8443:443"), pcAdmin("FALSE", "9071", "9071"), map[string]string{
				"zimbraMailReferMode": "reverse-proxied", "zimbraReverseProxyLookupTarget": "TRUE",
				"zimbraMailMode": "http", "zimbraReverseProxySSLToUpstreamEnabled": "FALSE",
			}),
		},
		{
			// 403-441: both packages; -s 414-418; the read-back that would remove the proxy service
			// (442-451) never ran, see disableBoth.
			name: "disable mail and web, store and proxy, secure",
			sc:   pcScenario{server: bothOnServer, installed: pcBoth},
			args: "-m -w -d -s -o -x https -H " + pcTestHost,
			want: pcMerge(pcMailPorts("143:7143:993:7993", "110:7110:995:7995"), pcWebPorts("80:0:443:0"),
				pcAdmin("FALSE", "7071", "9071"), map[string]string{
					"zimbraReverseProxyMailEnabled": "FALSE", "zimbraImapCleartextLoginEnabled": "FALSE",
					"zimbraPop3CleartextLoginEnabled": "FALSE", "zimbraMailReferMode": "wronghost",
					"zimbraMailMode": "https", "zimbraReverseProxyMailMode": "https",
					"zimbraReverseProxyHttpEnabled": "FALSE",
				}),
		},
		{
			// 403-415: the call of the bootstrap, mail only: the ports it gives.
			name: "disable mail with the bootstrap's ports",
			sc:   pcScenario{server: bothOnServer, installed: pcBoth},
			args: "-m -d -o -i 7143:143:7993:993 -p 7110:110:7995:995 -H " + pcTestHost,
			want: pcMerge(pcMailPorts("7143:143:7993:993", "7110:110:7995:995"),
				map[string]string{"zimbraReverseProxyMailEnabled": "FALSE"}),
		},
		{
			// 453-492: a store alone; -t 489-492; the default store mode is http.
			name: "disable mail and web, store only, lookup target off",
			sc:   pcScenario{server: storeOnServer, installed: pcStoreOnly},
			args: "-m -w -d -t -s -H " + pcTestHost,
			want: pcMerge(pcMailPorts("143:7143:993:7993", "110:7110:995:7995"), pcWebPorts("80:0:443:0"),
				pcAdmin("FALSE", "7071", "9071"), map[string]string{
					"zimbraImapCleartextLoginEnabled": "FALSE", "zimbraPop3CleartextLoginEnabled": "FALSE",
					"zimbraMailReferMode": "wronghost", "zimbraMailMode": "http",
					"zimbraReverseProxyLookupTarget": "FALSE",
				}),
		},
		{
			// 494-525: a proxy alone: the proxy mail mode comes from -x, admin -n is used.
			name: "disable web, proxy only, admin ports",
			sc:   pcScenario{server: pcEnt("zimbraServiceEnabled", "proxy"), installed: pcProxyOnly},
			args: "-w -d -c -x https -n 7075:9075 -a 80:0:443:0 -H " + pcTestHost,
			want: pcMerge(pcWebPorts("80:0:443:0"), pcAdmin("FALSE", "7075", "9075"), map[string]string{
				"zimbraReverseProxyMailMode": "https", "zimbraReverseProxyHttpEnabled": "FALSE",
			}),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := pcRun(t, tc.sc, tc.args)

			require.Zero(t, r.code, r.err)
			assert.Equal(t, tc.want, r.replaced(t))
			assert.Equal(t, "ms mail.example.com", r.writes[0].dn)
			assert.Empty(t, r.writes[0].del)

			if tc.wantAdded {
				assert.Equal(t, map[string][]string{"zimbraServiceEnabled": {"proxy"}}, r.writes[0].add)
			} else {
				assert.Empty(t, r.writes[0].add)
			}
		})
	}
}

func TestProxyConfig_FullResetWritesTheGlobalConfigOnce(t *testing.T) {
	// 192-214: -f alone, no -m/-w needed.
	r := pcRun(t, pcScenario{server: pcEnt(), global: pcEnt(), installed: pcBoth}, "-f -H "+pcTestHost)

	require.Zero(t, r.code, r.err)
	assert.Equal(t, "mcf", r.writes[0].dn)
	assert.Equal(t, map[string]string{
		"zimbraReverseProxyDomainNameQuery":      "(&(zimbraVirtualIPAddress=${IPADDR})(objectClass=zimbraDomain))",
		"zimbraReverseProxyMailHostQuery":        "(|(zimbraMailDeliveryAddress=${USER})(zimbraMailAlias=${USER})(zimbraId=${USER}))",
		"zimbraReverseProxyPortQuery":            "(&(zimbraServiceHostname=${MAILHOST})(objectClass=zimbraServer))",
		"zimbraReverseProxyMailHostAttribute":    "zimbraMailHost",
		"zimbraReverseProxyPop3PortAttribute":    "zimbraPop3BindPort",
		"zimbraReverseProxyPop3SSLPortAttribute": "zimbraPop3SSLBindPort",
		"zimbraReverseProxyImapPortAttribute":    "zimbraImapBindPort",
		"zimbraReverseProxyImapSSLPortAttribute": "zimbraImapSSLBindPort",
		"zimbraReverseProxyDomainNameAttribute":  "zimbraDomainName",
		"zimbraReverseProxyAuthWaitInterval":     "10s",
		"zimbraReverseProxyIPLoginLimit":         "0",
		"zimbraReverseProxyIPLoginLimitTime":     "3600",
		"zimbraReverseProxyUserLoginLimit":       "0",
		"zimbraReverseProxyUserLoginLimitTime":   "3600",
	}, pcFlatten(r.writes[0].replace))
	assert.Len(t, r.writes, 1, "the server entry is not touched")
}

func pcFlatten(m map[string][]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = strings.Join(v, ",")
	}

	return out
}

func TestProxyConfig_NothingToChangeWritesNothing(t *testing.T) {
	r1 := pcRun(t, pcScenario{server: pcEnt("zimbraServiceEnabled", "proxy"), installed: pcProxyOnly},
		"-m -d -H "+pcTestHost)
	require.Zero(t, r1.code, r1.err)

	stored := pcEnt("zimbraServiceEnabled", "proxy")
	for k, v := range r1.replaced(t) {
		stored[strings.ToLower(k)] = []string{v}
	}

	r2 := pcRun(t, pcScenario{server: stored, installed: pcProxyOnly}, "-m -d -H "+pcTestHost)

	assert.Zero(t, r2.code, r2.err)
	assert.Empty(t, r2.writes, "a second run changes nothing")
}

func TestProxyConfig_TheBootstrapCallsAreOneModify(t *testing.T) {
	sc := pcScenario{server: pcEnt("zimbraServiceEnabled", "mailbox"), installed: pcBoth}
	e, w, pio, _, errw := pcSetup(t, sc)

	mail := &ProxyConfigOptions{
		Enable: true, Mail: true, Override: true, Host: pcTestHost, NoUpstreamSSL: true,
		ImapPorts: "7143:143:7993:993", PopPorts: "7110:110:7995:995",
	}
	web := &ProxyConfigOptions{
		Enable: true, Web: true, Override: true, Host: pcTestHost, NoUpstreamSSL: true,
		Mode: "https", WebPorts: "8080:80:8443:443",
	}

	require.Zero(t, proxyConfig(e, pio, mail, web), errw.String())
	require.Len(t, w.writes, 1)

	got := pcFlatten(w.writes[0].replace)
	assert.Equal(t, "TRUE", got["zimbraReverseProxyMailEnabled"])
	assert.Equal(t, "TRUE", got["zimbraReverseProxyHttpEnabled"])
	assert.Equal(t, map[string][]string{"zimbraServiceEnabled": {"proxy"}}, w.writes[0].add,
		"the proxy service is added once for the two runs")
}

// The bootstrap's call on a store with the proxy: the web ports and the mode go
// to zmprov in one "ms", so CheckPortConflict, MailPort and LocalBind see them
// together (they write zimbra_mail_service_port and zimbraMailLocalBind).
func TestProxyConfig_StoreChangeGoesToZmprovAsOneCommand(t *testing.T) {
	e, fakes, _ := srvTestEnv(t, &srvFakeDir{servers: map[string]srvEntry{
		pcTestHost: pcEnt("zimbraServiceEnabled", "mailbox"),
	}})
	pcUseFake(t, e, "")
	pcRunProv = func(e *Env, p *Prov) error { return e.RunProv(p) }

	stdin := provStdin(fakes)

	var out, errw bytes.Buffer

	pio := pcIO{out: &out, err: &errw, installed: func(pkg string) (bool, error) { return pkg == PkgAppserver, nil }}
	o, ok := ParseProxyConfigArgs(strings.Fields("-u -w -e -o -a 8080:80:8443:443 -H "+pcTestHost), &errw)
	require.True(t, ok)

	require.Zero(t, proxyConfig(e, pio, o), errw.String())

	lines := strings.Split(strings.TrimSpace(*stdin), "\n")
	require.Len(t, lines, 2, "one ms and the exit")
	assert.True(t, strings.HasPrefix(lines[0], `"ms" "mail.example.com" `), lines[0])
	assert.Contains(t, lines[0], `"zimbraMailPort" "8080" "zimbraMailProxyPort" "80"`)
	assert.Contains(t, lines[0], `"zimbraMailMode" "http"`)
	assert.Equal(t, "exit", lines[1])
}

func TestProxyConfig_InvalidValuesFailTheirCommandOnly(t *testing.T) {
	// A missing port misaligns the Perl script's zmprov arguments and zmprov rejects the
	// command; the other commands of the run still apply (the script only reported zmprov's
	// exit status, 2).
	r := pcRun(t, pcScenario{server: pcEnt("zimbraServiceEnabled", "mailbox"), installed: pcStoreOnly},
		"-u -m -w -e -o -i 7143:143:7993 -H "+pcTestHost)

	assert.Equal(t, 2, r.code)
	assert.Contains(t, r.err, "ERROR: service.INVALID_REQUEST (invalid request: invalid value for zimbraImapSSLProxyBindPort")
	assert.NotContains(t, r.replaced(t), "zimbraImapBindPort")
	assert.Equal(t, "8080", r.replaced(t)["zimbraMailPort"])

	r = pcRun(t, pcScenario{server: pcEnt("zimbraServiceEnabled", "proxy"), installed: pcProxyOnly},
		"-u -w -e -o -x bogus -H "+pcTestHost)
	assert.Equal(t, 2, r.code, "-x is checked as zimbraReverseProxyMailMode")
	assert.Contains(t, r.err,
		"ERROR: account.INVALID_ATTR_VALUE (zimbraReverseProxyMailMode must be one of: https,redirect,both)")
}

func TestProxyConfig_RefusalsAndExitStatus(t *testing.T) {
	both := pcScenario{server: pcEnt("zimbraServiceEnabled", "mailbox"), installed: pcBoth}

	tests := []struct {
		name    string
		sc      pcScenario
		args    string
		code    int
		out     string // in stdout
		errText string // in stderr
	}{
		// 163-171
		{"upstream SSL refuses an http proxy", both, "-U -w -e -x http -H " + pcTestHost, 1,
			"Error: proxy mail mode http is invalid when SSL to upstream is enabled\nUsage: ", ""},
		// 163-171, the value read from LDAP
		{"the stored upstream SSL refuses a mixed proxy",
			pcScenario{server: pcEnt("zimbraReverseProxySSLToUpstreamEnabled", "TRUE"), installed: pcBoth},
			"-w -e -x mixed -H " + pcTestHost, 1, "invalid when SSL to upstream is enabled", ""},
		// 173-177
		{"arguments after the options", both, "-m -e -H " + pcTestHost + " extra", 1, "Usage: ", ""},
		// 179-182
		{"another host without -r", pcScenario{server: pcEnt(), installed: pcBoth, lcHost: "other"},
			"-m -e -H " + pcTestHost, 1, "Must use -r option to modify a remote host.\n", ""},
		// 184-187
		{"no package", pcScenario{server: pcEnt()}, "-m -e -H " + pcTestHost, 1, "",
			"Error: The store or proxy package must be installed.\n"},
		// 705-730 checkPackage: the server is not in LDAP (the store branch asks isEnabled)
		{"no server entry", pcScenario{installed: pcStoreOnly}, "-m -e -H " + pcTestHost, 1,
			"Error: No services found in zimbraServiceEnabled on " + pcTestHost + "\n", ""},
		// 55-58
		{"help", both, "-h", 1, "Usage: /opt/zextras/libexec/zmproxyconfig [-h] [-o] [-m] [-w]", ""},
		// 114-118
		{"admin console without -w", both, "-m -e -C -H " + pcTestHost, 1,
			"Error: web proxy configure should be specified (-w) before admin console proxy is configured.\nUsage: ", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := pcRun(t, tc.sc, tc.args)

			assert.Equal(t, tc.code, r.code)
			assert.Contains(t, r.out, tc.out)
			assert.Contains(t, r.err, tc.errText)
			assert.Empty(t, r.writes)
		})
	}
}

func TestProxyConfig_RemoteHostUsesTheServicesLDAPLists(t *testing.T) {
	// 737-740: with -r isInstalled reads zimbraServiceInstalled; 186 needs one of the two.
	remote := pcScenario{
		server:    pcEnt("zimbraServiceInstalled", "mailbox", "zimbraServiceInstalled", "proxy", "zimbraServiceEnabled", "mailbox"),
		installed: nil, lcHost: "other.example.com",
	}

	r := pcRun(t, remote, "-u -m -e -r -H "+pcTestHost)
	require.Zero(t, r.code, r.err)
	assert.Equal(t, "TRUE", r.replaced(t)["zimbraReverseProxyMailEnabled"], "both installed: the first branch")

	remote.server = pcEnt("zimbraServiceInstalled", "imapproxy")
	r = pcRun(t, remote, "-m -e -r -H "+pcTestHost)
	assert.Equal(t, 1, r.code, "imapproxy is not in the script's package map")
	assert.Contains(t, r.err, "must be installed")
}

func TestProxyConfig_WriteFailureIsExitTwo(t *testing.T) {
	e, w, pio, _, errw := pcSetup(t, pcScenario{server: pcEnt("zimbraServiceEnabled", "proxy"), installed: pcProxyOnly})
	w.err = errors.New("insufficient access")

	o, ok := ParseProxyConfigArgs(strings.Fields("-m -d -H "+pcTestHost), errw)
	require.True(t, ok)

	assert.Equal(t, 2, proxyConfig(e, pio, o))
	assert.Contains(t, errw.String(), "insufficient access")
}

func TestProxyConfig_VirtualIPWarningsAfterEnableAndFull(t *testing.T) {
	sc := pcScenario{
		server: pcEnt("zimbraServiceEnabled", "proxy"), installed: pcProxyOnly,
		global: pcEnt("zimbraReverseProxySNIEnabled", "FALSE"),
		vips:   []srvEntry{pcEnt("zimbraDomainName", "d.example.com", "zimbraVirtualIPAddress", "192.0.2.1")},
	}

	for _, args := range []string{"-m -e -o -H " + pcTestHost, "-f -H " + pcTestHost} {
		e, _, pio, _, errw := pcSetup(t, sc)
		pio.warnVIPs = true

		o, ok := ParseProxyConfigArgs(strings.Fields(args), errw)
		require.True(t, ok)
		require.Zero(t, proxyConfig(e, pio, o), errw.String())

		assert.Contains(t, errw.String(), "Warning: domain d.example.com has zimbraVirtualIPAddress set (192.0.2.1)")
		assert.Contains(t, errw.String(), "Warning: zimbraReverseProxySNIEnabled is FALSE")
	}

	e, _, pio, _, errw := pcSetup(t, sc)
	pio.warnVIPs = true

	o, _ := ParseProxyConfigArgs(strings.Fields("-m -d -o -H "+pcTestHost), errw)
	require.Zero(t, proxyConfig(e, pio, o))
	assert.Empty(t, errw.String(), "-d does not warn (675)")
}

func TestRunProxyConfig_ReportsOptionErrorsLikePerl(t *testing.T) {
	e, _, _ := srvTestEnv(t, &srvFakeDir{})

	var out, errw bytes.Buffer

	// 41: getopts fails, die "Unable to set options" (exit 255).
	assert.Equal(t, 255, RunProxyConfig(e, []string{"-z"}, &out, &errw))
	assert.Equal(t, "Unknown option: z\nUnable to set options\n", errw.String())
	assert.Empty(t, out.String())

	errw.Reset()
	assert.Equal(t, 255, RunProxyConfig(e, []string{"-H"}, &out, &errw))
	assert.Equal(t, "Unable to set options\n", errw.String(), "a value is missing")
}

func TestProxyConfigOptions_Parse(t *testing.T) {
	tests := []struct {
		args string
		want ProxyConfigOptions
		ok   bool
	}{
		{"-ume -H h", ProxyConfigOptions{NoUpstreamSSL: true, Mail: true, Enable: true, Host: "h"}, true},
		{"-meo -i 7143:143:7993:993 -Hh", ProxyConfigOptions{
			Mail: true, Enable: true, Override: true, ImapPorts: "7143:143:7993:993", Host: "h",
		}, true},
		{"-wd -x https -a 80:0:443:0 -H h", ProxyConfigOptions{
			Web: true, Disable: true, Mode: "https", WebPorts: "80:0:443:0", Host: "h",
		}, true},
		{"-mdstrf -p110:7110:995:7995 -n 7071:9071 -H h", ProxyConfigOptions{
			Mail: true, Disable: true, ClearTextOff: true, LookupTargetOff: true, Remote: true, Full: true,
			PopPorts: "110:7110:995:7995", AdminPorts: "7071:9071", Host: "h",
		}, true},
		{"-cCU -h", ProxyConfigOptions{AdminOff: true, AdminOn: true, UpstreamSSL: true, Help: true}, true},
		{"-H 0 -x ''", ProxyConfigOptions{Mode: "''"}, true},
		{"-e -- -m", ProxyConfigOptions{Enable: true, Extra: []string{"-m"}}, true},
		{"-e extra -m", ProxyConfigOptions{Enable: true, Extra: []string{"extra", "-m"}}, true},
		{"-e -", ProxyConfigOptions{Enable: true, Extra: []string{"-"}}, true},
		{"-e -q", ProxyConfigOptions{Enable: true}, false},
		{"-e -H", ProxyConfigOptions{Enable: true}, false},
	}

	for _, tc := range tests {
		t.Run(tc.args, func(t *testing.T) {
			var errw bytes.Buffer

			got, ok := ParseProxyConfigArgs(strings.Fields(tc.args), &errw)

			assert.Equal(t, tc.ok, ok)

			if tc.ok {
				assert.Equal(t, tc.want, *got)
				assert.Empty(t, errw.String())
			} else {
				assert.True(t, got.Enable)
			}
		})
	}
}

func TestProxyConfigOptions_Check(t *testing.T) {
	// Each row is one check of the script, in its order; the comment is its line.
	tests := []struct {
		args string
		msg  string
		bad  bool
	}{
		{"-m -e -H h", "", false},
		{"-f -H h", "", false},                            // 81: -f alone
		{"-h -m -e -H h", "", true},                       // 55
		{"-m -e", "", true},                               // 60
		{"-m -e -d -H h", "", true},                       // 65
		{"-m -H h", "", true},                             // 71
		{"-e -H h", "", true},                             // 76
		{"-f -e -H h", "", true},                          // 81
		{"-m -i 1:2:3:4 -H h", "", true},                  // 87
		{"-w -e -i 1:2:3:4 -H h", "", true},               // 92
		{"-m -e -a 1:2:3:4 -H h", "", true},               // 96
		{"-m -d -x http -H h", "", true},                  // 100
		{"-m -e -x http -H h", "", true},                  // 104
		{"-m -e -t -H h", "", true},                       // 109
		{"-m -e -c -H h", pcMsgAdminNeedsWeb, true},       // 114
		{"-w -e -n 1:2 -H h", pcMsgNeedsAdmin, true},      // 120
		{"-w -d -C -H h", pcMsgDisableWebAdmin, true},     // 126
		{"-m -d -u -H h", pcMsgUpstreamNeedsEnable, true}, // 132
		{"-m -e -u -U -H h", pcMsgUpstreamBoth, true},     // 138
		{"-w -e -x https -a 1:2:3:4 -C -n 1:2 -u -H h", "", false},
		{"-w -d -c -n 1:2 -x https -H h", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.args, func(t *testing.T) {
			o, ok := ParseProxyConfigArgs(strings.Fields(tc.args), &bytes.Buffer{})
			require.True(t, ok)

			err := o.Check()

			if !tc.bad {
				assert.NoError(t, err)

				return
			}

			var u *pcUsageError

			require.ErrorAs(t, err, &u)
			assert.Equal(t, tc.msg, u.msg)
		})
	}
}

func TestProxyConfigUsage_NamesTheProgramAndEndsWithTwoBlankLines(t *testing.T) {
	u := ProxyConfigUsage("/x/zmproxyconfig")

	assert.True(t, strings.HasPrefix(u, "Usage: /x/zmproxyconfig [-h] [-o] [-m] [-w] [-d "))
	assert.Contains(t, u, "\t-U: enable SSL connection from proxy to mail store.\n")
	assert.Contains(t, u, "-a default on disable: 80:0:443:0\n")
	assert.True(t, strings.HasSuffix(u, "is enabled: https\n\n\n"))
}

// zimbraReverseProxyMailMode is https, redirect or both. zmprov refuses the
// whole "ms" that carries another value (http and mixed included), so the
// script exited 2 with the ports of that command unchanged; the other commands
// of the run (the upstream SSL switch here) still applied.
func TestProxyConfig_ProxyMailModeOutsideTheEnumFailsItsCommand(t *testing.T) {
	for _, mode := range []string{"http", "mixed", "bogus"} {
		sc := pcScenario{server: pcEnt("zimbraServiceEnabled", "mailbox", "zimbraServiceEnabled", "proxy"), installed: pcBoth}

		r := pcRun(t, sc, "-u -w -e -o -x "+mode+" -a 8080:80:8443:443 -H "+pcTestHost)

		assert.Equal(t, 2, r.code, mode)
		assert.Equal(t, "ERROR: account.INVALID_ATTR_VALUE (zimbraReverseProxyMailMode must be one of: https,redirect,both)\n",
			r.err, mode)
		assert.Equal(t, map[string]string{"zimbraReverseProxySSLToUpstreamEnabled": "FALSE"}, r.replaced(t),
			"no port of the refused command is written: %s", mode)
	}

	// -x both is accepted (it is in the enum); with -U the script refuses it before any zmprov call.
	r := pcRun(t, pcScenario{server: pcEnt("zimbraServiceEnabled", "proxy"), installed: pcProxyOnly},
		"-u -w -e -o -x both -a 8080:80:8443:443 -H "+pcTestHost)
	assert.Zero(t, r.code, r.err)
	assert.Equal(t, "both", r.replaced(t)["zimbraReverseProxyMailMode"])
}

// Perl line 36: the proxy mail mode starts as http, so -w -d on a proxy
// without -x sends http, which zmprov refuses: the ports stay, the admin
// console command applies.
func TestProxyConfig_DisableWebOnProxyWithoutModeIsRefusedByTheEnum(t *testing.T) {
	r := pcRun(t, pcScenario{server: pcEnt("zimbraServiceEnabled", "proxy"), installed: pcProxyOnly},
		"-w -d -o -H "+pcTestHost)

	assert.Equal(t, 2, r.code)
	assert.Contains(t, r.err, "zimbraReverseProxyMailMode must be one of")
	assert.Equal(t, pcAdmin("FALSE", "7071", "9071"), r.replaced(t))

	// With -x https it applies; the plain mail disable never had a mode.
	r = pcRun(t, pcScenario{server: pcEnt("zimbraServiceEnabled", "proxy"), installed: pcProxyOnly},
		"-m -d -o -H "+pcTestHost)
	assert.Zero(t, r.code, r.err)
	assert.Equal(t, pcMerge(pcMailPorts("143:7143:993:7993", "110:7110:995:7995"),
		map[string]string{"zimbraReverseProxyMailEnabled": "FALSE"}), r.replaced(t))
}
