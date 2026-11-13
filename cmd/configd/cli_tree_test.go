// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// TestCommandTree_Parse is the parity table of the command line: for each
// argument vector the command that runs, the state it is handed, or the parse
// error. The expectations are those of the command line before the move to
// cobra.
func TestCommandTree_Parse(t *testing.T) {
	tests := []struct {
		name    string
		argv    []string
		want    string
		wantErr string
		check   func(t *testing.T, cli *CLI)
	}{
		{name: "no arguments run the daemon", want: "daemon"},
		{name: "daemon flag without a command", argv: []string{"--disable-restarts"}, want: "daemon",
			check: func(t *testing.T, cli *CLI) { assert.True(t, cli.DisableRestarts) }},
		{name: "explicit daemon", argv: []string{"daemon", "--disable-restarts"}, want: "daemon",
			check: func(t *testing.T, cli *CLI) { assert.True(t, cli.DisableRestarts) }},
		{name: "global flag before a command", argv: []string{"--disable-restarts", "release"}, want: "release",
			check: func(t *testing.T, cli *CLI) { assert.True(t, cli.DisableRestarts) }},
		{name: "global flag after a command", argv: []string{"release", "--disable-restarts"}, want: "release",
			check: func(t *testing.T, cli *CLI) { assert.True(t, cli.DisableRestarts) }},
		{name: "release -V", argv: []string{"release", "-V"}, want: "release",
			check: func(t *testing.T, cli *CLI) { assert.True(t, cli.Release.Packages) }},
		{name: "unknown command", argv: []string{"bogus"}, wantErr: "unexpected argument bogus"},
		{name: "unknown long flag", argv: []string{"--bogus"}, wantErr: "unknown flag --bogus"},
		{name: "unknown short flag", argv: []string{"-x"}, wantErr: "unknown flag -x"},
		{name: "stray argument", argv: []string{"release", "x"}, wantErr: "unexpected argument x"},
		{name: "disable-restarts takes no separate value", argv: []string{"--disable-restarts", "false", "release"},
			wantErr: "unexpected argument false"},

		{name: "localconfig flags", argv: []string{"localconfig", "-m", "xml", "-c", "/x", "-qpsdn", "k1", "k2=v"},
			want: "localconfig", check: func(t *testing.T, cli *CLI) {
				c := cli.Localconfig
				assert.Equal(t, "xml", c.Mode)
				assert.Equal(t, "/x", c.ConfigPath)
				assert.True(t, c.Quiet && c.ShowPath && c.ShowPasswords && c.ShowDefaults && c.ShowChanged)
				assert.Equal(t, []string{"k1", "k2=v"}, c.KeyArgs)
			}},
		{name: "localconfig attached short value", argv: []string{"localconfig", "-mxml"}, want: "localconfig",
			check: func(t *testing.T, cli *CLI) { assert.Equal(t, "xml", cli.Localconfig.Mode) }},
		{name: "localconfig key is repeatable and not split", argv: []string{"localconfig", "-k", "a", "--key=b,c", "d"},
			want: "localconfig", check: func(t *testing.T, cli *CLI) {
				assert.Equal(t, []string{"a", "b,c"}, cli.Localconfig.Key)
				assert.Equal(t, []string{"d"}, cli.Localconfig.KeyArgs)
			}},
		{name: "localconfig after -- is positional", argv: []string{"localconfig", "--", "-x"}, want: "localconfig",
			check: func(t *testing.T, cli *CLI) {
				assert.Equal(t, []string{"-x"}, cli.Localconfig.KeyArgs)
				assert.False(t, cli.Localconfig.Expand)
			}},
		{name: "localconfig missing flag value", argv: []string{"localconfig", "-k"}, wantErr: "flag needs an argument"},

		{name: "service without a verb", argv: []string{"service"}, want: "service"},
		{name: "service with an unknown verb keeps its arguments", argv: []string{"service", "bogus", "a", "-x"},
			want: "service", check: func(t *testing.T, cli *CLI) {
				assert.Contains(t, cli.Service.Usage.Args, "bogus")
			}},
		{name: "service verb needs a name", argv: []string{"service", "start"}, wantErr: `expected "<name>"`},
		{name: "service list takes nothing", argv: []string{"service", "list", "x"}, wantErr: "unexpected argument x"},
		{name: "service name may be empty", argv: []string{"service", "stop", ""}, want: "service stop",
			check: func(t *testing.T, cli *CLI) { assert.Empty(t, cli.Service.Stop.Name) }},
		{name: "start --no-rewrite after the name", argv: []string{"service", "start", "mta", "--no-rewrite"},
			want: "service start", check: func(t *testing.T, cli *CLI) {
				assert.Equal(t, "mta", cli.Service.Start.Name)
				assert.True(t, cli.Service.Start.NoRewrite)
				assert.Empty(t, cli.Service.Start.Extra)
			}},
		{name: "start --no-rewrite before the name", argv: []string{"service", "start", "--no-rewrite", "mta"},
			want: "service start", check: func(t *testing.T, cli *CLI) {
				assert.Equal(t, "mta", cli.Service.Start.Name)
				assert.True(t, cli.Service.Start.NoRewrite)
			}},
		{name: "extras are kept as given, flags included", argv: []string{"service", "start", "mta", "x", "--no-rewrite"},
			want: "service start", check: func(t *testing.T, cli *CLI) {
				assert.Equal(t, []string{"x", "--no-rewrite"}, cli.Service.Start.Extra)
				assert.False(t, cli.Service.Start.NoRewrite)
			}},
		{name: "legacy norewrite word is an extra", argv: []string{"service", "restart", "mta", "norewrite", "-x", "--whatever", "foo"},
			want: "service restart", check: func(t *testing.T, cli *CLI) {
				assert.Equal(t, []string{"norewrite", "-x", "--whatever", "foo"}, cli.Service.Restart.Extra)
			}},
		{name: "stop has no --no-rewrite", argv: []string{"service", "stop", "mta", "--no-rewrite"}, want: "service stop",
			check: func(t *testing.T, cli *CLI) { assert.Equal(t, []string{"--no-rewrite"}, cli.Service.Stop.Extra) }},
		{name: "global flag after the name is a flag", argv: []string{"service", "stop", "--disable-restarts", "mailbox"},
			want: "service stop", check: func(t *testing.T, cli *CLI) {
				assert.True(t, cli.DisableRestarts)
				assert.Equal(t, "mailbox", cli.Service.Stop.Name)
			}},
		{name: "global flag among the extras stays an extra", argv: []string{"service", "stop", "mta", "-y", "--disable-restarts"},
			want: "service stop", check: func(t *testing.T, cli *CLI) {
				assert.False(t, cli.DisableRestarts)
				assert.Equal(t, []string{"-y", "--disable-restarts"}, cli.Service.Stop.Extra)
			}},
		{name: "hidden systemd verb", argv: []string{"service", "restart-systemd", "mta"}, want: "service restart-systemd"},

		{name: "status without a name", argv: []string{"status"}, want: "status"},
		{name: "status with a name", argv: []string{"status", "mta"}, want: "status",
			check: func(t *testing.T, cli *CLI) { assert.Equal(t, "mta", cli.Status.Name) }},
		{name: "status takes one name", argv: []string{"status", "mta", "extra"}, wantErr: "unexpected argument extra"},

		{name: "control needs a verb", argv: []string{"control"}, wantErr: `expected one of "start", "startup", "stop", "shutdown", "restart", ...`},
		{name: "control shutdown", argv: []string{"control", "shutdown"}, want: "control shutdown"},
		{name: "control verbs take nothing", argv: []string{"control", "start", "x"}, wantErr: "unexpected argument x"},

		{name: "init needs a component", argv: []string{"init"}, wantErr: `expected "<component>"`},
		{name: "init --force", argv: []string{"init", "mta", "-f"}, want: "init", check: func(t *testing.T, cli *CLI) {
			assert.Equal(t, "mta", cli.Init.Component)
			assert.True(t, cli.Init.Force)
		}},
		{name: "init takes one component", argv: []string{"init", "a", "b"}, wantErr: "unexpected argument b"},

		{name: "proxy rewrites by default", argv: []string{"proxy"}, want: "proxy"},
		{name: "proxy takes config names", argv: []string{"proxy", "mta", "x"}, want: "proxy",
			check: func(t *testing.T, cli *CLI) { assert.Equal(t, []string{"mta", "x"}, cli.Proxy.Rewrite.ExtraConfigs) }},
		{name: "proxy conf short flags", argv: []string{"proxy", "conf", "/etc/nginx.conf", "-m", "-n", "-e"},
			want: "proxy conf", check: func(t *testing.T, cli *CLI) {
				c := cli.Proxy.Conf
				assert.True(t, c.Markers && c.NoComments && c.NoEmpty)
				assert.Equal(t, "/etc/nginx.conf", c.ConfigFile)
			}},
		{name: "proxy conf legacy long names", argv: []string{"proxy", "conf", "--nocomments", "--noempty"},
			want: "proxy conf", check: func(t *testing.T, cli *CLI) {
				assert.True(t, cli.Proxy.Conf.NoComments)
				assert.True(t, cli.Proxy.Conf.NoEmpty)
			}},
		{name: "proxy gen -v is --verbose", argv: []string{"proxy", "gen", "-vn", "-s", "mbox", "-c", "a=b,c=d", "stray"},
			want: "proxy gen", check: func(t *testing.T, cli *CLI) {
				g := cli.Proxy.Gen
				assert.True(t, g.Verbose && g.DryRun)
				assert.False(t, cli.Version)
				assert.Equal(t, "mbox", g.Server)
				assert.Equal(t, []string{"a=b,c=d"}, g.Config)
				assert.Equal(t, []string{"stray"}, g.Args)
			}},
		{name: "proxy gen long flags", want: "proxy gen", argv: []string{
			"proxy", "gen", "--workdir=/w", "--templatedir", "/t", "--prefix", "p", "--template-prefix", "q",
			"--include-dir", "i", "--force-dns-resolution", "--definitions", "--defaults", "--config", "a=b", "--config", "c=d",
		}, check: func(t *testing.T, cli *CLI) {
			g := cli.Proxy.Gen
			assert.Equal(t, "/w", g.WorkDir)
			assert.Equal(t, "/t", g.TemplateDir)
			assert.Equal(t, "p", g.Prefix)
			assert.Equal(t, "q", g.TemplatePrefix)
			assert.Equal(t, "i", g.IncludeDir)
			assert.True(t, g.ForceDNS && g.Definitions && g.Defaults)
			assert.Equal(t, []string{"a=b", "c=d"}, g.Config)
		}},
		{name: "proxy enable", argv: []string{"proxy", "enable", "http"}, want: "proxy enable",
			check: func(t *testing.T, cli *CLI) { assert.Equal(t, "http", cli.Proxy.Enable.Protocol) }},
		{name: "proxy disable needs a protocol", argv: []string{"proxy", "disable"}, wantErr: `expected "<protocol>"`},
		{name: "proxy status", argv: []string{"proxy", "status"}, want: "proxy status"},
		{name: "proxy template needs a verb", argv: []string{"proxy", "template"}, wantErr: `expected one of "fmt", "lint", "render"`},
		{name: "template fmt paths", argv: []string{"proxy", "template", "fmt", "--check", "a", "b"}, want: "proxy template fmt",
			check: func(t *testing.T, cli *CLI) {
				assert.True(t, cli.Proxy.Template.Fmt.Check)
				assert.Equal(t, []string{"a", "b"}, cli.Proxy.Template.Fmt.Paths)
			}},
		{name: "template fmt needs a path", argv: []string{"proxy", "template", "fmt"}, wantErr: `expected "<path> ..."`},
		{name: "template fmt --check and -w exclude each other", argv: []string{"proxy", "template", "fmt", "--check", "-w", "a"},
			wantErr: "--check and --write can't be used together"},
		{name: "template lint needs a path", argv: []string{"proxy", "template", "lint"}, wantErr: `expected "<path> ..."`},
		{name: "template render needs its flags", argv: []string{"proxy", "template", "render", "--values", "v.yaml"},
			wantErr: `required flag(s) "templates", "out" not set`},
		{name: "template render makes paths absolute", argv: []string{"proxy", "template", "render", "--values=/v.yaml", "--templates=/t", "--out=/o"},
			want: "proxy template render", check: func(t *testing.T, cli *CLI) {
				r := cli.Proxy.Template.Render
				assert.Equal(t, []string{"/v.yaml", "/t", "/o"}, []string{r.Values, r.Templates, r.Out})
			}},

		{name: "tls", argv: []string{"tls", "--force", "--host", "h", "mixed"}, want: "tls",
			check: func(t *testing.T, cli *CLI) {
				assert.Equal(t, "mixed", cli.TLS.Mode)
				assert.True(t, cli.TLS.Force)
				assert.Equal(t, "h", cli.TLS.Host)
			}},
		{name: "tls takes one mode", argv: []string{"tls", "a", "b"}, wantErr: "unexpected argument b"},

		{name: "ldap needs a verb", argv: []string{"ldap"}, wantErr: `expected one of "gacf", "gcf", "gs", "mcf", "search", ...`},
		{name: "ldap connection flags before the verb", want: "ldap gacf", argv: []string{
			"ldap", "--url", "ldap://a", "--url", "ldap://b", "--bind-dn", "uid=x", "--password-file", "/pw", "--json", "gacf",
		}, check: func(t *testing.T, cli *CLI) {
			assert.Equal(t, []string{"ldap://a", "ldap://b"}, cli.LDAP.URL)
			assert.Equal(t, "uid=x", cli.LDAP.BindDN)
			assert.Equal(t, "/pw", cli.LDAP.PasswordFile)
			assert.True(t, cli.LDAP.JSON)
		}},
		{name: "ldap connection flags after the verb", argv: []string{"ldap", "gacf", "--json"}, want: "ldap gacf",
			check: func(t *testing.T, cli *CLI) { assert.True(t, cli.LDAP.JSON) }},
		{name: "ldap url is not split", argv: []string{"ldap", "--url", "a,b", "gacf"}, want: "ldap gacf",
			check: func(t *testing.T, cli *CLI) { assert.Equal(t, []string{"a,b"}, cli.LDAP.URL) }},
		{name: "ldap gcf needs attributes", argv: []string{"ldap", "gcf"}, wantErr: `expected "<attrs> ..."`},
		{name: "ldap gs server", argv: []string{"ldap", "gs", "h"}, want: "ldap gs",
			check: func(t *testing.T, cli *CLI) { assert.Equal(t, "h", cli.LDAP.Gs.Server) }},
		{name: "ldap mcf takes its arguments verbatim", argv: []string{"ldap", "mcf", "-attr", "v", "--json"}, want: "ldap mcf",
			check: func(t *testing.T, cli *CLI) {
				assert.Equal(t, []string{"-attr", "v", "--json"}, cli.LDAP.Mcf.Changes)
				assert.False(t, cli.LDAP.JSON)
			}},
		{name: "ldap mcf after connection flags", argv: []string{"ldap", "--json", "mcf", "+a", "b"}, want: "ldap mcf",
			check: func(t *testing.T, cli *CLI) {
				assert.True(t, cli.LDAP.JSON)
				assert.Equal(t, []string{"+a", "b"}, cli.LDAP.Mcf.Changes)
			}},
		{name: "ldap mcf needs changes", argv: []string{"ldap", "mcf"}, wantErr: `expected "<changes> ..."`},
		{name: "ldap search defaults", argv: []string{"ldap", "search"}, want: "ldap search",
			check: func(t *testing.T, cli *CLI) { assert.Equal(t, "sub", cli.LDAP.Search.Scope) }},
		{name: "ldap search flags", want: "ldap search", argv: []string{
			"ldap", "search", "--base", "b", "--filter", "(a=b)", "--scope=one", "--attr", "x", "--attr", "y,z",
		}, check: func(t *testing.T, cli *CLI) {
			s := cli.LDAP.Search
			assert.Equal(t, "b", s.Base)
			assert.Equal(t, "(a=b)", s.Filter)
			assert.Equal(t, "one", s.Scope)
			assert.Equal(t, []string{"x", "y,z"}, s.Attr)
		}},
		{name: "ldap search scope is an enum", argv: []string{"ldap", "search", "--scope", "bogus"},
			wantErr: `must be one of "base","one","sub" but got "bogus"`},
		{name: "ldap wait default timeout", argv: []string{"ldap", "wait"}, want: "ldap wait",
			check: func(t *testing.T, cli *CLI) { assert.Equal(t, 120*time.Second, cli.LDAP.Wait.Timeout) }},
		{name: "ldap wait timeout", argv: []string{"ldap", "wait", "--timeout=2m"}, want: "ldap wait",
			check: func(t *testing.T, cli *CLI) { assert.Equal(t, 2*time.Minute, cli.LDAP.Wait.Timeout) }},
		{name: "ldap wait bad timeout", argv: []string{"ldap", "wait", "--timeout", "bogus"}, wantErr: "--timeout"},

		{name: "rewrite needs a config name", argv: []string{"rewrite"}, wantErr: `expected "<config-names> ..."`},
		{name: "rewrite names", argv: []string{"rewrite", "mta", "proxy"}, want: "rewrite",
			check: func(t *testing.T, cli *CLI) { assert.Equal(t, []string{"mta", "proxy"}, cli.Rewrite.ConfigNames) }},
		{name: "plan sections and --json", argv: []string{"plan", "mta", "proxy", "--json"}, want: "plan",
			check: func(t *testing.T, cli *CLI) {
				assert.Equal(t, []string{"mta", "proxy"}, cli.Plan.Sections)
				assert.True(t, cli.Plan.JSON)
			}},
		{name: "plan rejects unknown flags", argv: []string{"plan", "--bogus"}, wantErr: "unknown flag --bogus"},
		{name: "shadow", argv: []string{"shadow", "--shadow-dir", "/tmp/s", "--single-cycle"}, want: "shadow",
			check: func(t *testing.T, cli *CLI) {
				assert.Equal(t, "/tmp/s", cli.Shadow.Dir)
				assert.True(t, cli.Shadow.Once)
			}},
		{name: "stat keeps the options of the collector", argv: []string{"stat", "io", "-x", "--log", "/tmp/io.csv"},
			want: "stat", check: func(t *testing.T, cli *CLI) {
				assert.Equal(t, []string{"io", "-x", "--log", "/tmp/io.csv"}, cli.Stat.Args)
			}},
		{name: "stat run takes one command line per collector", argv: []string{"stat", "run", "io -x", "cpu"},
			want: "stat", check: func(t *testing.T, cli *CLI) {
				assert.Equal(t, []string{"run", "io -x", "cpu"}, cli.Stat.Args)
			}},
		{name: "shadow takes no argument", argv: []string{"shadow", "x"}, wantErr: "unexpected argument x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cli, got, err := parseArgv(t, tt.argv...)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)

			if tt.check != nil {
				tt.check(t, cli)
			}
		})
	}
}

// buildTagFlags are the global flags that only the profiling and tracing
// builds have.
var buildTagFlags = []string{
	"cpuprofile", "memprofile", "trace", "profile-duration", "once", "enable-tracing", "tracing-output",
}

// TestCommandTree_Flags pins the flag inventory: every command's own flags,
// with their short letters, and the global ones every command inherits.
func TestCommandTree_Flags(t *testing.T) {
	flags := map[string][]string{
		"":                      {"disable-restarts", "help:h", "version:v"},
		"localconfig":           {"mode:m", "config-path:c", "quiet:q", "show-path:p", "show-passwords:s", "show-defaults:d", "show-changed:n", "edit:e", "unset:u", "random:r", "force:f", "expand:x", "info:i", "all", "reload:l", "key:k"},
		"service start":         {"no-rewrite"},
		"service restart":       {"no-rewrite"},
		"init":                  {"force:f"},
		"tls":                   {"force", "host"},
		"release":               {"packages:V"},
		"plan":                  {"json"},
		"shadow":                {"shadow-dir", "single-cycle"},
		"proxy conf":            {"markers:m", "no-comments:n", "no-empty:e"},
		"proxy gen":             {"verbose", "workdir:w", "templatedir:t", "dry-run:n", "defaults:d", "definitions:D", "prefix:p", "template-prefix:P", "include-dir:i", "server:s", "force-dns-resolution:f", "config:c"},
		"proxy template fmt":    {"check", "write:w"},
		"proxy template render": {"values", "templates", "out"},
		"ldap":                  {"url", "bind-dn", "password-file", "json"},
		"ldap search":           {"base", "filter", "scope", "attr"},
		"ldap wait":             {"timeout"},
		"ldap gacf":             {},
		"service stop":          {},
		"control start":         {},
		"proxy template lint":   {},
		"status":                {},
		"rewrite":               {},
		"daemon":                {},
		"proxy enable":          {},
		"proxy status":          {},
		"service start-systemd": {},
	}

	root, _ := newRootCmd("configd", &CLI{}, systest.New().System(), &stdio{Out: io.Discard, Err: io.Discard})

	for path, want := range flags {
		t.Run("flags of "+path, func(t *testing.T) {
			cmd, _, err := root.Find(strings.Fields(path))
			require.NoError(t, err)

			if path == "" {
				cmd = root
			}

			own := cmd.LocalNonPersistentFlags()
			if path == "" || path == "ldap" {
				own = cmd.PersistentFlags()
			}

			var got []string

			own.VisitAll(func(f *pflag.Flag) {
				entry := f.Name
				if f.Shorthand != "" {
					entry += ":" + f.Shorthand
				}

				got = append(got, entry)
			})

			// The profiling and tracing flags exist only in the builds that
			// enable them.
			got = slices.DeleteFunc(got, func(f string) bool { return slices.Contains(buildTagFlags, f) })

			assert.ElementsMatch(t, want, got)
		})
	}
}

// TestCommandTree_Visible pins the commands the help lists and the ones it
// hides.
func TestCommandTree_Visible(t *testing.T) {
	root, _ := newRootCmd("configd", &CLI{}, systest.New().System(), &stdio{Out: io.Discard, Err: io.Discard})

	root.InitDefaultCompletionCmd()

	names := func(cmd *cobra.Command) []string {
		var out []string

		for _, sub := range cmd.Commands() {
			if sub.IsAvailableCommand() {
				out = append(out, sub.Name())
			}
		}

		return out
	}

	assert.ElementsMatch(t, []string{
		"completion", "control", "init", "ldap", "localconfig", "plan", "proxy", "release",
		"rewrite", "service", "shadow", "stat", "status", "tls",
	}, names(root))

	find := func(path ...string) *cobra.Command {
		cmd, _, err := root.Find(path)
		require.NoError(t, err)

		return cmd
	}

	assert.ElementsMatch(t, []string{"list", "start", "stop", "kill", "restart", "reload", "status", "refresh", "rotate"}, names(find("service")))
	assert.ElementsMatch(t, []string{"start", "startup", "stop", "shutdown", "restart", "status"}, names(find("control")))
	assert.ElementsMatch(t, []string{"conf", "gen", "enable", "disable", "status", "template"}, names(find("proxy")))
	assert.ElementsMatch(t, []string{"fmt", "lint", "render"}, names(find("proxy", "template")))
	assert.ElementsMatch(t, []string{"gacf", "gcf", "gs", "mcf", "search", "wait"}, names(find("ldap")))

	for _, hidden := range [][]string{{"daemon"}, {"service", "start-systemd"}, {"service", "stop-systemd"}, {"service", "reload-systemd"}, {"service", "restart-systemd"}} {
		assert.True(t, find(hidden...).Hidden, hidden)
	}
}

// TestCommandTree_Completion is the smoke test of shell completion: the
// commands, their flags and, where cheap, their values.
func TestCommandTree_Completion(t *testing.T) {
	t.Setenv("CARBONIO_BASE_DIR", "/nonexistent-base")

	complete := func(args ...string) string {
		var out, errb bytes.Buffer

		status := run(append([]string{"configd", cobra.ShellCompRequestCmd}, args...), systest.New().System(), nil, &out, &errb)
		require.Zero(t, status, errb.String())
		assert.NotContains(t, errb.String(), "Warning:", "completion must not run the perl probe")

		return out.String()
	}

	tests := []struct {
		name string
		args []string
		want []string
		not  []string
	}{
		{"commands", []string{""}, []string{"service", "localconfig", "completion"}, []string{"daemon", "start-systemd"}},
		{"service names after a verb", []string{"service", "start", ""}, []string{"mta", "proxy", "mailbox"}, nil},
		{"service names are filtered by prefix", []string{"service", "restart", "pro"}, []string{"proxy"}, []string{"mailbox"}},
		{"no second service name", []string{"service", "start", "mta", ""}, nil, []string{"mailbox"}},
		{"status takes a service name", []string{"status", ""}, []string{"mta", "mailbox"}, nil},
		{"service verbs", []string{"service", ""}, []string{"start", "stop", "list"}, []string{"start-systemd"}},
		{"verb flags", []string{"service", "start", "--"}, []string{"--no-rewrite"}, nil},
		{"localconfig flags", []string{"localconfig", "--"}, []string{"--mode", "--config-path", "--key", "--edit"}, []string{"--all", "--format"}},
		{"localconfig modes", []string{"localconfig", "--mode", ""}, []string{"plain", "shell", "export", "nokey", "xml"}, nil},
		{"localconfig keys", []string{"localconfig", "zimbra_ser"}, []string{"zimbra_server_hostname"}, nil},
		{"ldap scopes", []string{"ldap", "search", "--scope", ""}, []string{"base", "one", "sub"}, nil},
		{"ldap verb flags", []string{"ldap", "search", "--"}, []string{"--base", "--filter", "--url", "--json"}, nil},
		{"proxy protocols", []string{"proxy", "enable", ""}, []string{"http", "imaps", "pop3s"}, nil},
		{"init components", []string{"init", ""}, []string{"mta", "proxy"}, nil},
		{"tls modes", []string{"tls", ""}, []string{"both", "http", "https", "mixed", "redirect"}, nil},
		{"global flags", []string{"release", "--"}, []string{"--packages", "--version", "--disable-restarts"}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := complete(tt.args...)

			lines := map[string]bool{}

			for line := range strings.SplitSeq(got, "\n") {
				lines[strings.SplitN(line, "\t", 2)[0]] = true
			}

			for _, w := range tt.want {
				assert.True(t, lines[w], "missing %q in:\n%s", w, got)
			}

			for _, n := range tt.not {
				assert.False(t, lines[n], "unexpected %q in:\n%s", n, got)
			}
		})
	}
}

// TestCommandTree_CompletionScripts checks that the completion command emits a
// script for every shell the packages install.
func TestCommandTree_CompletionScripts(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			status, stdout, stderr := runConfigd(t, "completion", shell)

			assert.Zero(t, status, stderr)
			assert.Contains(t, stdout, "configd")
			assert.Empty(t, stderr)
		})
	}
}

// TestCommandTree_HelpAndVersion pins what --help and --version print where.
func TestCommandTree_HelpAndVersion(t *testing.T) {
	for _, argv := range [][]string{{"--version"}, {"-v"}, {"release", "-v"}, {"service", "--version"}} {
		status, stdout, stderr := runConfigd(t, argv...)

		assert.Zero(t, status, argv)
		assert.Equal(t, formatVersion()+"\n", stdout, argv)
		assert.Empty(t, stderr, argv)
	}

	for _, argv := range [][]string{{"service", "start", "--help"}, {"ldap", "--help"}, {"-h"}, {"help", "proxy"}} {
		status, stdout, stderr := runConfigd(t, argv...)

		assert.Zero(t, status, argv)
		assert.Contains(t, stdout, "Usage: configd ", argv)
		assert.Empty(t, stderr, argv)
	}

	_, stdout, _ := runConfigd(t, "service", "start", "--help")
	assert.Contains(t, stdout, "Usage: configd service start <name> [<extra> ...] [flags]")
	assert.Contains(t, stdout, "--no-rewrite")
	assert.Contains(t, stdout, "Global flags:")
	assert.NotContains(t, stdout, "[=true|false]")
}

// TestApp_ExitStatus pins how the error Execute returns becomes the process
// status and the text on stderr.
func TestApp_ExitStatus(t *testing.T) {
	tests := []struct {
		name       string
		started    bool
		err        error
		want       int
		wantStderr string
	}{
		{"success", false, nil, 0, ""},
		{"a usage error is reported as a parse error, status 1", false, errors.New("unknown flag --x"), 1, "configd: error: unknown flag --x\n"},
		{"a failing command is reported as an error, status 1", true, errors.New("boom"), 1, "Error: boom\n"},
		{"a command's own status passes through silently", true, exitStatus(3), 3, ""},
		{"version and help end with status 0 before the run begins", false, exitStatus(0), 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer

			a := &app{started: tt.started}

			assert.Equal(t, tt.want, a.exitStatus(tt.err, "configd", &stderr))
			assert.Equal(t, tt.wantStderr, stderr.String())
		})
	}
}
