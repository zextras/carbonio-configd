// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const (
	ipMyCnf     = "/opt/zextras/conf/my.cnf"
	ipAntispam  = "/opt/zextras/conf/antispam-my.cnf"
	ipBothFlags = "-Xmx1g -Djava.net.preferIPv4Stack=true -Djava.net.preferIPv6Addresses=true"
)

func ipLookupOK(context.Context, string, string) ([]net.IP, error) {
	return []net.IP{net.ParseIP("192.0.2.1")}, nil
}

// ipServerSearch answers the searches of iptool.pl: the server entry (with
// its zimbraIPMode, when mode is set) and the services it runs.
func ipServerSearch(mode string, services ...string) func(b, f string) ([]ldapEntry, error) {
	return func(_, f string) ([]ldapEntry, error) {
		if !strings.Contains(f, "(cn="+ldapTestHost+")") {
			return nil, nil
		}

		if strings.Contains(f, "zimbraServiceEnabled=") {
			for _, s := range services {
				if strings.Contains(f, "zimbraServiceEnabled="+s+")") {
					return []ldapEntry{{DN: "cn=" + ldapTestHost}}, nil
				}
			}

			return nil, nil
		}

		attrs := map[string][]string{}
		if mode != "" {
			attrs["zimbraIPMode"] = []string{mode}
		}

		return []ldapEntry{{DN: "cn=" + ldapTestHost, Attrs: attrs}}, nil
	}
}

func ipToolTestEnv(t *testing.T) (*Env, *systest.Fakes, *ldapFake) {
	t.Helper()

	e, fakes, f := ldapToolEnv(t)
	require.NoError(t, e.SetLC(map[string]string{
		"zimbra_zmjava_options": ipBothFlags, "mailboxd_java_options": "-server " + ipV4Stack,
		"mysql_bind_address": "localhost", "mysql_mycnf": ipMyCnf,
		"antispam_mysql_host": "127.0.0.1", "antispam_mysql_mycnf": ipAntispam,
	}))

	fakes.FS.Put(ipMyCnf, []byte("[mysqld]\nbind-address = 127.0.0.1\nport=3306\n"), 0o644)
	fakes.FS.Put(ipAntispam, []byte("[mysqld]\nbind-address = 127.0.0.1\n"), 0o644)

	return e, fakes, f
}

func ipLC(t *testing.T, e *Env, key string) string {
	t.Helper()

	lc, err := e.LC()
	require.NoError(t, err)

	return lc[key]
}

// TestIPTool_Modes follows libexec/iptool.pl for a node with the mailbox and
// the mta: the Java options (lines 160-200 of the script), the MySQL address in
// the local config and in my.cnf, removed and set again with zminiutil's
// backups ".pre" and ".pre-bind", and the same for the antispam MySQL.
func TestIPTool_Modes(t *testing.T) {
	cases := map[string]struct{ zmjava, mailboxd, addr string }{
		"ipv4": {"-Xmx1g -Djava.net.preferIPv4Stack=true", "-server -Djava.net.preferIPv4Stack=true", "127.0.0.1"},
		"both": {"-Xmx1g", "-server", "::1"},
		"ipv6": {"-Xmx1g -Djava.net.preferIPv6Addresses=true", "-server -Djava.net.preferIPv6Addresses=true", "::1"},
	}

	for mode, c := range cases {
		e, fakes, f := ipToolTestEnv(t)
		f.searchFn = ipServerSearch(mode, "mailbox", "mta")

		var out, errb bytes.Buffer

		require.NoError(t, ipTool(e, f.kit(), ipToolOptions{}, ipLookupOK, &out, &errb), mode)

		assert.Equal(t, "Setting local config zimbra_zmjava_options="+c.zmjava+"\n"+
			"Setting local config mailboxd_java_options="+c.mailboxd+"\n"+
			"Setting local config mysql_bind_address="+c.addr+"\n"+
			"Setting local config antispam_mysql_host="+c.addr+"\n", out.String(), mode)
		assert.Empty(t, errb.String(), mode)

		for key, want := range map[string]string{
			"zimbra_zmjava_options": c.zmjava, "mailboxd_java_options": c.mailboxd,
			"mysql_bind_address": c.addr, "antispam_mysql_host": c.addr,
		} {
			assert.Equal(t, want, ipLC(t, e, key), mode+" "+key)
		}

		for _, file := range []string{ipMyCnf, ipAntispam} {
			got, _ := fakes.FS.Data(file)
			assert.Contains(t, string(got), "[mysqld]\n\nbind-address = "+c.addr+"\n", mode+" "+file)
			assert.NotContains(t, string(got), "127.0.0.1\nbind", mode)

			pre, _ := fakes.FS.Data(file + ".pre")
			assert.Contains(t, string(pre), "bind-address = 127.0.0.1\n", mode+": the original")

			preBind, _ := fakes.FS.Data(file + ".pre-bind")
			assert.NotContains(t, string(preBind), "bind-address", mode+": after the removal")
		}
	}
}

func TestIPTool_DryRunWritesNothing(t *testing.T) {
	e, fakes, f := ipToolTestEnv(t)
	f.searchFn = ipServerSearch("", "mailbox", "mta")

	var out, errb bytes.Buffer

	require.NoError(t, ipTool(e, f.kit(), ipToolOptions{DryRun: true, Mode: "both"}, ipLookupOK, &out, &errb))

	assert.Equal(t, "Would set zimbra_zmjava_options to -Xmx1g\nWould set mailboxd_java_options to -server\n"+
		"Would set mysql_bind_address to ::1\nWould set antispam_mysql_host to ::1\n", out.String())
	assert.Equal(t, ipBothFlags, ipLC(t, e, "zimbra_zmjava_options"))
	assert.False(t, fakes.FS.Exists(ipMyCnf+".pre"))

	got, _ := fakes.FS.Data(ipMyCnf)
	assert.Equal(t, "[mysqld]\nbind-address = 127.0.0.1\nport=3306\n", string(got))
}

func TestIPTool_OnlyWhatTheNodeRuns(t *testing.T) {
	cases := map[string]struct {
		services []string
		mysql    string
		lines    []string
	}{
		"no services": {nil, "localhost", []string{"zimbra_zmjava_options"}},
		"mailbox": {
			[]string{"mailbox"}, "localhost",
			[]string{"zimbra_zmjava_options", "mailboxd_java_options", "mysql_bind_address"},
		},
		"mailbox with a MySQL on a real address": {
			[]string{"mailbox"}, "10.0.0.5", []string{"zimbra_zmjava_options", "mailboxd_java_options"},
		},
		"mta": {[]string{"mta"}, "localhost", []string{"zimbra_zmjava_options", "antispam_mysql_host"}},
	}

	for name, c := range cases {
		e, _, f := ipToolTestEnv(t)
		require.NoError(t, e.SetLC(map[string]string{"mysql_bind_address": c.mysql}))
		f.searchFn = ipServerSearch("both", c.services...)

		var out bytes.Buffer

		require.NoError(t, ipTool(e, f.kit(), ipToolOptions{}, ipLookupOK, &out, &out), name)

		var got []string

		for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
			got = append(got, strings.SplitN(strings.TrimPrefix(l, "Setting local config "), "=", 2)[0])
		}

		assert.Equal(t, c.lines, got, name)
	}
}

func TestIPTool_AntispamFileIsOnlyEditedWhenItExists(t *testing.T) {
	e, fakes, f := ipToolTestEnv(t)
	require.NoError(t, fakes.FS.Remove(ipAntispam))
	f.searchFn = ipServerSearch("both", "mta")

	require.NoError(t, ipTool(e, f.kit(), ipToolOptions{}, ipLookupOK, &bytes.Buffer{}, &bytes.Buffer{}))

	assert.Equal(t, "::1", ipLC(t, e, "antispam_mysql_host"))
	assert.False(t, fakes.FS.Exists(ipAntispam), "not created")
	assert.False(t, fakes.FS.Exists(ipAntispam+".pre"))
}

func TestIPTool_MissingMyCnfIsReportedAndTheRunGoesOn(t *testing.T) {
	e, fakes, f := ipToolTestEnv(t)
	require.NoError(t, fakes.FS.Remove(ipMyCnf))
	f.searchFn = ipServerSearch("both", "mailbox")

	var out, errb bytes.Buffer

	require.NoError(t, ipTool(e, f.kit(), ipToolOptions{}, ipLookupOK, &out, &errb))
	assert.Contains(t, errb.String(), "can't open file "+ipMyCnf)
	assert.Equal(t, "::1", ipLC(t, e, "mysql_bind_address"))
}

// iptool.pl 100-130: a name that does not resolve is reported, not fatal.
func TestIPTool_DNSRecords(t *testing.T) {
	cases := map[string]struct {
		mode   string
		lookup ipLookup
		want   string
	}{
		"ipv4 resolves": {"ipv4", ipLookupOK, ""},
		"ipv4 fails": {
			"ipv4", func(context.Context, string, string) ([]net.IP, error) { return nil, errors.New("x") },
			"Error: Unable to resolve " + ldapTestHost + " A record\n",
		},
		"both: only AAAA is missing": {
			"both", func(_ context.Context, network, _ string) ([]net.IP, error) {
				if network == "ip6" {
					return nil, nil
				}

				return []net.IP{net.ParseIP("192.0.2.1")}, nil
			},
			"Error: Unable to resolve " + ldapTestHost + " AAAA record\n",
		},
		"ipv6 asks for AAAA only": {
			"ipv6", func(_ context.Context, network, _ string) ([]net.IP, error) {
				if network == "ip4" {
					return nil, errors.New("must not be asked")
				}

				return []net.IP{net.ParseIP("2001:db8::1")}, nil
			},
			"",
		},
	}

	for name, c := range cases {
		e, _, f := ipToolTestEnv(t)
		f.searchFn = ipServerSearch(c.mode)

		var out bytes.Buffer

		require.NoError(t, ipTool(e, f.kit(), ipToolOptions{DryRun: true}, c.lookup, &out, &out), name)
		assert.True(t, strings.HasPrefix(out.String(), c.want), "%s: %q", name, out.String())
	}
}

func TestIPTool_Failures(t *testing.T) {
	cases := map[string]struct {
		setup func(e *Env, f *ldapFake)
		out   string
		errw  string
		code  int
	}{
		"server not found": {
			func(_ *Env, f *ldapFake) { f.searchFn = func(string, string) ([]ldapEntry, error) { return nil, nil } },
			"Error: Server " + ldapTestHost + " not found\n", "", 1,
		},
		"unknown mode": {
			func(_ *Env, f *ldapFake) { f.searchFn = ipServerSearch("dual") },
			"Error: Unknown IP mode dual\n", "", 1,
		},
		"no mode set": {
			func(_ *Env, f *ldapFake) { f.searchFn = ipServerSearch("") },
			"Error: Unknown IP mode \n", "", 1,
		},
		"cannot connect": {
			func(e *Env, _ *ldapFake) {
				e.kit.dial = func(*Env, ldapDial) (ldapConn, error) { return nil, errors.New("refused") }
			},
			"", "Error connecting to LDAP server: " + ldapToolLMR + "\n", 255,
		},
		"bind refused": {
			func(_ *Env, f *ldapFake) { f.bindOK = func(string, string) bool { return false } },
			"", "Bind: invalid credentials\n", 255,
		},
		"search fails": {
			func(_ *Env, f *ldapFake) {
				f.searchFn = func(string, string) ([]ldapEntry, error) { return nil, ldapDiag(1, "busy") }
			},
			"", "busy\n", 255,
		},
	}

	for name, c := range cases {
		e, _, f := ipToolTestEnv(t)
		c.setup(e, f)

		var out, errb bytes.Buffer

		err := ipTool(e, e.kit, ipToolOptions{}, ipLookupOK, &out, &errb)

		assert.Equal(t, c.code, ldapToolStatus(err, &errb), name)
		assert.Equal(t, c.out, out.String(), name)
		assert.Equal(t, c.errw, errb.String(), name)
	}
}

func TestIPTool_StartTLSWhenSupported(t *testing.T) {
	e, _, f := ipToolTestEnv(t)
	require.NoError(t, e.SetLC(map[string]string{"ldap_starttls_supported": "1"}))
	f.searchFn = ipServerSearch("both")

	require.NoError(t, ipTool(e, f.kit(), ipToolOptions{DryRun: true}, ipLookupOK, &bytes.Buffer{}, &bytes.Buffer{}))
	assert.Equal(t, []ldapDial{{URLs: []string{ldapToolLMR}, TLS: ldapTLSInsecure}}, f.dials)
}

// iptool.pl 28-62: -m needs -d and a known mode.
func TestRunIPTool_RefusedOptions(t *testing.T) {
	for _, args := range [][]string{
		{"-h"}, {"--help"}, {"-x"}, {"--mode=ipv4"}, {"-d", "-m", "dual"}, {"-d", "-m"},
	} {
		e, _, f := ipToolTestEnv(t)

		var out, errb bytes.Buffer

		assert.Equal(t, 1, RunIPTool(e, args, &out, &errb), args)
		assert.Contains(t, errb.String(), "zmiptool [-d [-m ipmode]]", args)
		assert.Empty(t, f.events, "no connection: %v", args)
	}
}

func TestIPJavaOptions(t *testing.T) {
	cases := map[string]struct {
		in, want string
		mode     string
	}{
		"ensure appends":        {"-a", "-a " + ipV4Stack, "ipv4"},
		"ensure keeps":          {"-a " + ipV4Stack, "-a " + ipV4Stack, "ipv4"},
		"remove cuts the blank": {"-a " + ipV6Addresses + " -b", "-a -b", "both"},
		"ipv6 swaps":            {"-a " + ipV4Stack, "-a " + ipV6Addresses, "ipv6"},
		"both removes both":     {"-a " + ipV4Stack + " " + ipV6Addresses, "-a", "both"},
	}

	for name, c := range cases {
		assert.Equal(t, c.want, ipJavaOptions(c.in, ipModes[c.mode]), name)
	}
}
