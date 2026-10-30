// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/localconfig"
)

// runLC executes cmd the way Run does, but captures the output and returns the
// fatal error instead of exiting. args is the process command line.
func runLC(cmd *LocalconfigCmd, args ...string) (stdout, stderr string, err error) {
	var out, errOut bytes.Buffer

	opts := cmd.options(args)
	opts.stdout = &out
	opts.stderr = &errOut

	err = opts.execute()

	return out.String(), errOut.String(), err
}

func TestEdit_MultiKeyIsAllOrNothing(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		force   bool
		wantErr string
	}{
		{
			name:    "protected key after a plain key",
			args:    []string{"first=1", "zimbra_home=/x"},
			wantErr: "can not edit key zimbra_home",
		},
		{
			name:    "malformed argument after a plain key",
			args:    []string{"first=1", "no_equals"},
			wantErr: "argument 'no_equals' not in key=value form",
		},
		{
			name:    "argument starting with equals",
			args:    []string{"first=1", "=value"},
			wantErr: "argument '=value' not in key=value form",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := newTestLocalconfig(t, map[string]string{"existing": "old"})
			before, err := localconfig.LoadLocalConfigFromFile(path)
			require.NoError(t, err)

			_, _, err = runLC(&LocalconfigCmd{ConfigPath: path, Edit: true, Force: tt.force, KeyArgs: tt.args})
			require.Error(t, err)
			assert.Equal(t, tt.wantErr, err.Error())

			after, err := localconfig.LoadLocalConfigFromFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after, "a failed -e must not write anything")
		})
	}
}

func TestEdit_MultiKeySavesEverything(t *testing.T) {
	path := newTestLocalconfig(t, map[string]string{"existing": "old"})

	_, _, err := runLC(&LocalconfigCmd{ConfigPath: path, Edit: true, KeyArgs: []string{"existing=new", "added=1", "added=2"}})
	require.NoError(t, err)

	cfg, err := localconfig.LoadLocalConfigFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new", cfg["existing"])
	assert.Equal(t, "2", cfg["added"], "the last assignment of a key wins")
}

func TestEdit_InsufficientArguments(t *testing.T) {
	path := newTestLocalconfig(t, nil)

	_, _, err := runLC(&LocalconfigCmd{ConfigPath: path, Edit: true})
	require.Error(t, err)
	assert.Equal(t, "insufficient arguments", err.Error())
}

func TestUnset_MultiKeyIsAllOrNothing(t *testing.T) {
	path := newTestLocalconfig(t, map[string]string{"doomed": "v", "keeper": "k"})

	_, _, err := runLC(&LocalconfigCmd{ConfigPath: path, Unset: true, KeyArgs: []string{"doomed", "never_set"}})
	require.Error(t, err)
	assert.Equal(t, "key never_set is not set", err.Error())

	cfg, err := localconfig.LoadLocalConfigFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, "v", cfg["doomed"], "a failed -u must not remove anything")
}

func TestFailure_ReportRespectsQuiet(t *testing.T) {
	for _, quiet := range []bool{false, true} {
		var errOut bytes.Buffer

		opts := &localconfigOpts{quiet: quiet, stderr: &errOut}
		opts.reportFailure(failf("can not edit key %s", "zimbra_home"))

		if quiet {
			assert.Empty(t, errOut.String(), "legacy Logging.error is silent with -q")
		} else {
			assert.Equal(t, "Error: can not edit key zimbra_home\n", errOut.String())
		}
	}
}

func TestPrint_UnknownModeFailsBeforeLoadingConfig(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.xml")

	stdout, _, err := runLC(&LocalconfigCmd{ConfigPath: missing, Mode: "bogus"})
	require.Error(t, err)
	assert.Equal(t, "failed to create writer bogus", err.Error())
	assert.Empty(t, stdout)
}

func TestPrint_UnknownModeWinsOverIncompatibleOption(t *testing.T) {
	_, _, err := runLC(&LocalconfigCmd{Mode: "bogus", ShowChanged: true, ShowDefaults: true})
	require.Error(t, err)
	assert.Equal(t, "failed to create writer bogus", err.Error())
}

func TestCheckMode(t *testing.T) {
	for _, mode := range []string{"", "plain", "shell", "export", "nokey", "xml"} {
		assert.NoError(t, checkMode(mode), mode)
	}

	for _, mode := range []string{"PLAIN", "json", "xml "} {
		require.Error(t, checkMode(mode), mode)
	}
}

// TestCompatibleOptions pins the LocalConfigCLI.checkCompatibleOptions table:
// -i/--all accept only -q, -e -qfrc, -u -qfc, -p -qc, and the print branches
// (-n, -d, none) -qscmx. -l is never checked.
func TestCompatibleOptions(t *testing.T) {
	tests := []struct {
		name    string
		cmd     LocalconfigCmd
		wantErr string
	}{
		{"edit with quiet force random config", LocalconfigCmd{Edit: true, Quiet: true, Force: true, Random: true, ConfigPath: "x"}, ""},
		{"edit with path", LocalconfigCmd{Edit: true, ShowPath: true}, "option 'p' can not be used with option 'e'"},
		{"edit with mode", LocalconfigCmd{Edit: true, Mode: "xml"}, "option 'm' can not be used with option 'e'"},
		{"edit with show passwords", LocalconfigCmd{Edit: true, ShowPasswords: true}, "option 's' can not be used with option 'e'"},
		{"edit with expand", LocalconfigCmd{Edit: true, Expand: true}, "option 'x' can not be used with option 'e'"},
		{"edit with default", LocalconfigCmd{Edit: true, ShowDefaults: true}, "option 'd' can not be used with option 'e'"},
		{"edit with unset", LocalconfigCmd{Edit: true, Unset: true}, "option 'u' can not be used with option 'e'"},
		{"edit with info", LocalconfigCmd{Edit: true, Info: true}, "option 'e' can not be used with option 'i'"},
		{"unset with quiet force config", LocalconfigCmd{Unset: true, Quiet: true, Force: true, ConfigPath: "x"}, ""},
		{"unset with random", LocalconfigCmd{Unset: true, Random: true}, "option 'r' can not be used with option 'u'"},
		{"unset with mode", LocalconfigCmd{Unset: true, Mode: "xml"}, "option 'm' can not be used with option 'u'"},
		{"path with quiet config", LocalconfigCmd{ShowPath: true, Quiet: true, ConfigPath: "x"}, ""},
		{"path with force", LocalconfigCmd{ShowPath: true, Force: true}, "option 'f' can not be used with option 'p'"},
		{"path with mode", LocalconfigCmd{ShowPath: true, Mode: "plain"}, "option 'm' can not be used with option 'p'"},
		{"changed with default", LocalconfigCmd{ShowChanged: true, ShowDefaults: true}, "option 'd' can not be used with option 'n'"},
		{"changed with output options", LocalconfigCmd{ShowChanged: true, ShowPasswords: true, Mode: "shell", Expand: true, Quiet: true, ConfigPath: "x"}, ""},
		{"changed with force", LocalconfigCmd{ShowChanged: true, Force: true}, "option 'f' can not be used with option 'n'"},
		{"default with output options", LocalconfigCmd{ShowDefaults: true, ShowPasswords: true, Mode: "export", Expand: true, Quiet: true}, ""},
		{"default with random", LocalconfigCmd{ShowDefaults: true, Random: true}, "option 'r' can not be used with option 'd'"},
		{"plain with random", LocalconfigCmd{Random: true}, "invalid option 'r'"},
		{"plain with force", LocalconfigCmd{Force: true}, "invalid option 'f'"},
		{"info with quiet", LocalconfigCmd{Info: true, Quiet: true}, ""},
		{"info with config", LocalconfigCmd{Info: true, ConfigPath: "x"}, "option 'c' can not be used with option 'i'"},
		{"info with mode", LocalconfigCmd{Info: true, Mode: "xml"}, "option 'm' can not be used with option 'i'"},
		{"all with quiet", LocalconfigCmd{All: true, Quiet: true}, ""},
		{"all with show passwords", LocalconfigCmd{All: true, ShowPasswords: true}, "option 's' can not be used with option 'all'"},
		{"info with all", LocalconfigCmd{Info: true, All: true}, "option 'all' can not be used with option 'i'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// ConfigPath only marks "-c given" here; the real file is set below.
			opts := tt.cmd.options(nil)
			opts.configPath = newTestLocalconfig(t, map[string]string{"k": "v"})
			opts.given = tt.cmd.givenOptions()
			opts.stdout = &bytes.Buffer{}
			opts.stderr = &bytes.Buffer{}
			opts.keys = []string{"k=v"}

			if tt.cmd.Random || tt.cmd.Unset {
				opts.keys = []string{"k"}
			}

			err := opts.execute()

			if tt.wantErr == "" {
				assert.NoError(t, err)

				return
			}

			require.Error(t, err)
			assert.Equal(t, tt.wantErr, err.Error())
		})
	}
}

func TestCompatibleOptions_ReportsFirstOffenderInCommandLineOrder(t *testing.T) {
	cmd := &LocalconfigCmd{Edit: true, ShowPath: true, ShowDefaults: true, ConfigPath: "x"}

	tests := []struct {
		args    []string
		wantErr string
	}{
		{[]string{"configd", "localconfig", "-e", "-p", "-d"}, "option 'p' can not be used with option 'e'"},
		{[]string{"configd", "localconfig", "-d", "-e", "-p"}, "option 'd' can not be used with option 'e'"},
		{[]string{"configd", "localconfig", "-ed", "-c", "x", "-p"}, "option 'd' can not be used with option 'e'"},
		{[]string{"configd", "localconfig", "--show-defaults", "--edit", "--show-path"}, "option 'd' can not be used with option 'e'"},
	}

	for _, tt := range tests {
		_, _, err := runLC(cmd, tt.args...)
		require.Error(t, err)
		assert.Equal(t, tt.wantErr, err.Error(), strings.Join(tt.args, " "))
	}
}

func TestOptionSequence(t *testing.T) {
	tests := []struct {
		args []string
		want []string
	}{
		{[]string{"configd", "-test.v"}, nil},
		{[]string{"configd", "localconfig", "-q", "-s", "-m", "export"}, []string{"q", "s", "m"}},
		{[]string{"configd", "localconfig", "-fe", "a=1"}, []string{"f", "e"}},
		{[]string{"configd", "localconfig", "-c", "-p", "-e"}, []string{"c", "e"}},
		{[]string{"configd", "localconfig", "-mxml", "--quiet", "--mode=shell"}, []string{"m", "q", "m"}},
		{[]string{"configd", "localconfig", "-e", "--", "-p=1"}, []string{"e"}},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, optionSequence(tt.args), strings.Join(tt.args, " "))
	}
}

func TestInfo_PrintsSupportedKeyDoc(t *testing.T) {
	stdout, stderr, err := runLC(&LocalconfigCmd{Info: true, KeyArgs: []string{"zimbra_java_home"}})
	require.NoError(t, err)
	assert.Equal(t, "zimbra_java_home:\nPath to a JDK/J2SDK.\n* Changes are in effect after server restart.\n", stdout)
	assert.Empty(t, stderr)
}

func TestInfo_ReloadableKeyHasNoRestartNote(t *testing.T) {
	stdout, _, err := runLC(&LocalconfigCmd{Info: true, KeyArgs: []string{"imap_max_consecutive_error"}})
	require.NoError(t, err)
	assert.Equal(t, "imap_max_consecutive_error:\n"+
		"If greater than 0, drop the imap connection if imap_max_consecutive_error value\n"+
		"is reached.\n", stdout)
}

func TestInfo_UnsupportedKeyNeedsAll(t *testing.T) {
	stdout, _, err := runLC(&LocalconfigCmd{Info: true, KeyArgs: []string{"av_notify_domain"}})
	require.NoError(t, err)
	assert.Empty(t, stdout, "-i hides keys without @Supported")

	stdout, _, err = runLC(&LocalconfigCmd{All: true, KeyArgs: []string{"av_notify_domain"}})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(stdout, "av_notify_domain:\n"), stdout)
}

func TestInfo_UnknownKeyWarnsAndSeparatorCountsSkippedKeys(t *testing.T) {
	// Legacy prints a blank line before every printed key whose index is > 0,
	// including after skipped keys.
	stdout, stderr, err := runLC(&LocalconfigCmd{Info: true,
		KeyArgs: []string{"no_such_key", "zimbra_java_home"}})
	require.NoError(t, err)
	assert.Equal(t, "Warning: 'no_such_key' is not a known key\n", stderr)
	assert.Equal(t, "\nzimbra_java_home:\nPath to a JDK/J2SDK.\n* Changes are in effect after server restart.\n", stdout)
}

func TestInfo_QuietSilencesWarning(t *testing.T) {
	_, stderr, err := runLC(&LocalconfigCmd{Info: true, Quiet: true, KeyArgs: []string{"no_such_key"}})
	require.NoError(t, err)
	assert.Empty(t, stderr)
}

func TestInfo_NoArgumentsListsAllSupportedKeysSorted(t *testing.T) {
	stdout, _, err := runLC(&LocalconfigCmd{Info: true})
	require.NoError(t, err)

	var headers []string

	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasSuffix(line, ":") && !strings.Contains(line, " ") {
			headers = append(headers, strings.TrimSuffix(line, ":"))
		}
	}

	require.NotEmpty(t, headers)
	assert.IsIncreasing(t, headers)
	assert.Contains(t, headers, "zimbra_home")
	assert.NotContains(t, headers, "av_notify_domain")
}

func TestWrapDoc(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		limit int
		want  string
	}{
		{"fits on one line", "one two three", 80, "one two three\n"},
		{"wraps greedily", "aaa bbb ccc ddd", 8, "aaa bbb\nccc ddd\n"},
		{"double space keeps an empty token", "one  two", 80, "one  two\n"},
		{"token as long as the limit gets its own line", "ab cdefgh ij", 6, "ab\ncdefgh\nij\n"},
		{"trailing whitespace is dropped", "one two \n", 80, "one two\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf strings.Builder

			wrapDoc(&buf, tt.text, tt.limit)
			assert.Equal(t, tt.want, buf.String())
		})
	}
}

func TestReloadLocalConfig_Success(t *testing.T) {
	var gotReload bool

	mux := http.NewServeMux()
	mux.HandleFunc("/service/admin/soap", func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)

		if strings.Contains(buf.String(), "ReloadLocalConfigRequest") {
			gotReload = true

			_, _ = w.Write([]byte(`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>` +
				`<ReloadLocalConfigResponse xmlns="urn:zimbraAdmin"/></soap:Body></soap:Envelope>`))

			return
		}

		_, _ = w.Write([]byte(`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>` +
			`<AuthResponse xmlns="urn:zimbraAdmin" lifetime="1000"><authToken>tok</authToken></AuthResponse>` +
			`</soap:Body></soap:Envelope>`))
	})

	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	host, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "https://"))
	require.NoError(t, err)

	cfg := map[string]string{lcKeySoapServer: host, lcKeyAdminPort: port, "zimbra_ldap_password": "pw"}
	require.NoError(t, reloadLocalConfig(context.Background(), cfg))
	assert.True(t, gotReload, "the server must receive a ReloadLocalConfigRequest")
}

func TestReloadLocalConfig_ServiceNotRunning(t *testing.T) {
	srv := httptest.NewTLSServer(http.NewServeMux())
	host, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "https://"))
	require.NoError(t, err)
	srv.Close()

	cfg := map[string]string{lcKeySoapServer: host, lcKeyAdminPort: port, "zimbra_ldap_password": "pw"}

	err = reloadLocalConfig(context.Background(), cfg)
	require.Error(t, err)
	assert.Equal(t, "'mailbox' service is not running", err.Error())
}

// --- -u: legacy LocalConfig.isSet / remove ---

// A known key that is not in the file is "set" for legacy isSet(); unsetting
// it succeeds and never writes an empty value.
func TestUnset_KnownButUnsetKeyIsNoOp(t *testing.T) {
	path := newTestLocalconfig(t, map[string]string{"existing": "old"})

	_, stderr, err := runLC(&LocalconfigCmd{ConfigPath: path, Unset: true, KeyArgs: []string{"zimbra_java_home"}})
	require.NoError(t, err)
	assert.Empty(t, stderr)

	after, err := localconfig.LoadLocalConfigFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"existing": "old"}, after)
}

// Unsetting a configured known key drops it from the file (the default applies
// again); legacy never stores "".
func TestUnset_ConfiguredKnownKeyIsDropped(t *testing.T) {
	path := newTestLocalconfig(t, map[string]string{"zimbra_java_home": "/custom"})

	_, _, err := runLC(&LocalconfigCmd{ConfigPath: path, Unset: true, KeyArgs: []string{"zimbra_java_home"}})
	require.NoError(t, err)

	after, err := localconfig.LoadLocalConfigFromFile(path)
	require.NoError(t, err)
	assert.NotContains(t, after, "zimbra_java_home")
}

func TestUnset_UnknownKeyIsNotSet(t *testing.T) {
	path := newTestLocalconfig(t, nil)

	_, _, err := runLC(&LocalconfigCmd{ConfigPath: path, Unset: true, KeyArgs: []string{"no_such_key"}})
	require.Error(t, err)
	assert.Equal(t, "key no_such_key is not set", err.Error())
}

// --- -n: legacy LocalConfig.printChanged ---

func TestChanged_NoArgsListsConfiguredKeysOnly(t *testing.T) {
	path := newTestLocalconfig(t, map[string]string{
		"custom_key":           "custom",
		"zimbra_home":          "/opt/zextras",          // equals its raw default: unchanged
		"zimbra_java_home":     "/other/java",           // differs: changed
		"zimbra_tmp_directory": "/opt/zextras/data/tmp", // expanded equals, raw ${...} default does not
	})

	stdout, stderr, err := runLC(&LocalconfigCmd{ConfigPath: path, ShowChanged: true})
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, "custom_key = custom\n"+
		"zimbra_java_home = /other/java\n"+
		"zimbra_tmp_directory = /opt/zextras/data/tmp\n", stdout)
}

func TestChanged_ListedKeys(t *testing.T) {
	path := newTestLocalconfig(t, map[string]string{"zimbra_java_home": "/other/java"})

	stdout, stderr, err := runLC(&LocalconfigCmd{
		ConfigPath: path, ShowChanged: true,
		// zimbra_home: unconfigured, expands to its raw default -> dropped;
		// zimbra_log_directory: unconfigured but its raw default holds ${...} -> listed;
		// no_such_key: unknown and unset -> warning.
		KeyArgs: []string{"zimbra_home", "zimbra_log_directory", "no_such_key"},
	})
	require.NoError(t, err)
	assert.Equal(t, "zimbra_log_directory = /opt/zextras/log\n", stdout)
	assert.Equal(t, "Warning: null valued key 'no_such_key'\n", stderr)
}

// --- -d / print warnings ---

func TestDefaults_UnknownKeyWarns(t *testing.T) {
	path := newTestLocalconfig(t, nil)

	stdout, stderr, err := runLC(&LocalconfigCmd{ConfigPath: path, ShowDefaults: true, KeyArgs: []string{"zimbra_home", "bogus"}})
	require.NoError(t, err)
	assert.Equal(t, "zimbra_home = /opt/zextras\n", stdout)
	assert.Equal(t, "Warning: not a known key 'bogus'\n", stderr)
}

// --- missing / malformed config file ---

func TestMissingConfigFile_WarnsAndStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localconfig.xml")

	stdout, stderr, err := runLC(&LocalconfigCmd{ConfigPath: path, KeyArgs: []string{"zimbra_home"}})
	require.NoError(t, err)
	assert.Equal(t, "zimbra_home = /opt/zextras\n", stdout)
	assert.Equal(t, "Warning: local config file `"+path+"' is not readable\n", stderr)

	stdout, stderr, err = runLC(&LocalconfigCmd{ConfigPath: path, ShowPath: true})
	require.NoError(t, err)
	assert.Equal(t, path+"\n", stdout)
	assert.Equal(t, "Warning: local config file `"+path+"' is not readable\n", stderr)

	_, stderr, err = runLC(&LocalconfigCmd{ConfigPath: path, Quiet: true, ShowPath: true})
	require.NoError(t, err)
	assert.Empty(t, stderr, "-q silences the warning")
}

func TestMissingConfigFile_EditCreatesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localconfig.xml")

	stdout, stderr, err := runLC(&LocalconfigCmd{ConfigPath: path, Edit: true, KeyArgs: []string{"a=1"}})
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Equal(t, "Warning: local config file `"+path+"' is not readable\n", stderr)

	created, err := localconfig.LoadLocalConfigFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"a": "1"}, created)
}

func TestMalformedConfigFile_IsFatal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localconfig.xml")
	require.NoError(t, os.WriteFile(path, []byte("<localconfig><key"), 0o600))

	var out, errOut bytes.Buffer

	opts := (&LocalconfigCmd{ConfigPath: path, ShowPath: true}).options(nil)
	opts.stdout, opts.stderr = &out, &errOut

	err := opts.execute()
	require.Error(t, err)
	assert.Equal(t, "failed when reading config file", err.Error())
	assert.Empty(t, out.String(), "nothing may run on a malformed config")

	opts.reportFailure(err)

	lines := strings.Split(strings.TrimSpace(errOut.String()), "\n")
	require.Len(t, lines, 3, errOut.String())
	assert.Equal(t, "Warning: Problem parsing local config file '"+path+"'", lines[0])
	assert.Equal(t, "Error: failed when reading config file", lines[1])
	assert.Contains(t, lines[2], "failed to parse localconfig XML")
}

func TestEdit_SaveFailureReportsPathAndCause(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-dir", "localconfig.xml")

	var errOut bytes.Buffer

	opts := (&LocalconfigCmd{ConfigPath: path, Edit: true, KeyArgs: []string{"a=1"}}).options(nil)
	opts.stdout, opts.stderr = &bytes.Buffer{}, &errOut

	err := opts.execute()
	require.Error(t, err)
	assert.Equal(t, "save to "+path+" failed", err.Error())

	errOut.Reset()
	opts.reportFailure(err)
	assert.True(t, strings.HasPrefix(errOut.String(), "Error: save to "+path+" failed\n"), errOut.String())
	assert.Greater(t, strings.Count(errOut.String(), "\n"), 1, "the cause follows the message")
}

// --- legacy long option names ---

func TestLegacyLongOptionNames(t *testing.T) {
	parse := func(t *testing.T, argv ...string) *LocalconfigCmd {
		t.Helper()

		cli := &CLI{}
		parser, err := kong.New(cli, kong.Vars{"version": "test"})
		require.NoError(t, err)

		_, err = parser.Parse(append([]string{"localconfig"}, argv...))
		require.NoError(t, err, argv)

		return &cli.Localconfig
	}

	legacy := parse(t, "--config", "/x.xml", "--format", "xml", "--show", "--changed", "--quiet", "--expand", "k")
	assert.Equal(t, "/x.xml", legacy.ConfigPath)
	assert.Equal(t, "xml", legacy.Mode)
	assert.True(t, legacy.ShowPasswords)
	assert.True(t, legacy.ShowChanged)
	assert.True(t, legacy.Quiet)
	assert.True(t, legacy.Expand)
	assert.Equal(t, []string{"k"}, legacy.KeyArgs)

	assert.True(t, parse(t, "--path").ShowPath)
	assert.True(t, parse(t, "--default").ShowDefaults)
	assert.True(t, parse(t, "--edit", "a=1").Edit)
	assert.True(t, parse(t, "--unset", "a").Unset)
	assert.True(t, parse(t, "--random", "--edit", "a").Random)
	assert.True(t, parse(t, "--force", "--edit", "a=1").Force)
	assert.True(t, parse(t, "--info").Info)
	assert.True(t, parse(t, "--all").All)
	assert.True(t, parse(t, "--reload").Reload)
	assert.Equal(t, "xml", parse(t, "--format=xml").Mode)
	assert.Equal(t, "/y.xml", parse(t, "--config=/y.xml").ConfigPath)

	// The names Kong derived before the aliases keep working.
	current := parse(t, "--config-path", "/z.xml", "--mode", "shell", "--show-passwords", "--show-changed",
		"--show-path", "--show-defaults")
	assert.Equal(t, "/z.xml", current.ConfigPath)
	assert.Equal(t, "shell", current.Mode)
	assert.True(t, current.ShowPasswords)
	assert.True(t, current.ShowChanged)
	assert.True(t, current.ShowPath)
	assert.True(t, current.ShowDefaults)
}

func TestOptionSequence_LegacyLongNames(t *testing.T) {
	args := []string{"configd", "localconfig", "--changed", "--show", "--format", "xml", "--config=/x", "--default", "--path"}
	assert.Equal(t, []string{"n", "s", "m", "c", "d", "p"}, optionSequence(args))

	// First offender follows the typed order, whichever spelling is used.
	_, _, err := runLC(&LocalconfigCmd{ShowChanged: true, ShowDefaults: true, ConfigPath: "x"}, "configd", "localconfig", "--default", "--changed")
	require.Error(t, err)
	assert.Equal(t, "option 'd' can not be used with option 'n'", err.Error())
}
