// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	_ "embed"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const provTestHost = "mail.example.com"

// provZmprovRuns records what each zmprov process of a test was given and how
// many LDAP calls had been made before it started.
type provZmprovRuns struct {
	stdin       []string
	ldapBefore  []int
	exitCode    int
	failMessage string
}

func provRecordZmprov(fakes *systest.Fakes) *provZmprovRuns {
	runs := &provZmprovRuns{}

	fakes.Exec.OnPath("runuser", func(c *sys.Cmd) (sys.Result, error) {
		var b bytes.Buffer

		_, _ = b.ReadFrom(c.Stdin)
		runs.stdin = append(runs.stdin, b.String())
		runs.ldapBefore = append(runs.ldapBefore, len(provTestLDAP().calls))

		if runs.failMessage != "" {
			_, _ = c.Stderr.Write([]byte(runs.failMessage))
		}

		return sys.Result{ExitCode: runs.exitCode}, nil
	})

	return runs
}

func provNorm(m map[string][]string) map[string][]string {
	if m == nil {
		return map[string][]string{}
	}

	return m
}

func TestRunProv_ModifyOnlyBatchRunsNoProcessAndKeepsTheOrder(t *testing.T) {
	e, fakes, _ := testEnv(t)

	var p Prov

	p.Add("mcf", "zimbraSkinLogoURL", "/logo.png")
	p.Add("mcf", "+zimbraReverseProxyUpstreamEwsServers", "a.example.com")
	p.Add("mcf", "+zimbraReverseProxyUpstreamEwsServers", "b.example.com")
	p.Add("ms", provTestHost, "zimbraServiceEnabled", "stats", "zimbraServiceEnabled", "mailbox")
	p.Add("ms", provTestHost, "-zimbraServiceEnabled", "stats")
	p.Add("mc", "default", "zimbraPrefTimeZoneId", "Europe/Rome")

	require.NoError(t, e.RunProv(&p))

	assert.Empty(t, fakes.Exec.Calls(), "no zmprov process")
	assert.Equal(t, 1, provTestLDAP().opened)
	assert.Equal(t, 1, provTestLDAP().closed)

	var dns []string
	for _, c := range provTestLDAP().calls {
		dns = append(dns, c.DN)
	}

	assert.Equal(t, []string{
		provGlobalDN, provGlobalDN, provGlobalDN,
		provServerDN(provTestHost), provServerDN(provTestHost), "cn=default,cn=cos,cn=zimbra",
	}, dns)
	assert.Equal(t, []string{"/logo.png"}, provTestLDAP().values(provGlobalDN, "zimbraSkinLogoURL"))
	assert.Equal(t, []string{"a.example.com", "b.example.com"},
		provTestLDAP().values(provGlobalDN, "zimbraReverseProxyUpstreamEwsServers"))
	assert.Equal(t, []string{"mailbox"}, provTestLDAP().values(provServerDN(provTestHost), "zimbraServiceEnabled"))
	assert.Equal(t, []string{"Europe/Rome"}, provTestLDAP().values("cn=default,cn=cos,cn=zimbra", "zimbraPrefTimeZoneId"))
}

func TestRunProv_AddOfAPresentValueAndRemoveOfAnAbsentOneSucceed(t *testing.T) {
	e, _, _ := testEnv(t)
	provTestLDAP().set(provGlobalDN, "zimbraReverseProxyUpstreamEwsServers", "a.example.com")

	var p Prov

	p.Add("mcf", "+zimbraReverseProxyUpstreamEwsServers", "a.example.com")
	p.Add("mcf", "-zimbraReverseProxyUpstreamEwsServers", "gone.example.com")
	p.Add("mcf", "-zimbraReverseProxyAvailableLookupTargets", "")

	require.NoError(t, e.RunProv(&p))
	assert.Equal(t, []string{"a.example.com"}, provTestLDAP().values(provGlobalDN, "zimbraReverseProxyUpstreamEwsServers"))
}

func TestProvParse_MapsArgumentsToModifications(t *testing.T) {
	e, _, _ := testEnv(t)

	for name, tc := range map[string]struct {
		args              []string
		add, del, replace map[string][]string
		wantDN            string
		wantTarget        provTarget
		wantName          string
	}{
		"replace": {
			args:    []string{"mcf", "zimbraSkinLogoURL", "x"},
			replace: map[string][]string{"zimbraSkinLogoURL": {"x"}},
			wantDN:  provGlobalDN,
		},
		"values of one attribute replace it in order": {
			args:       []string{"ms", provTestHost, "zimbraServiceEnabled", "service", "zimbraServiceEnabled", "mailbox"},
			replace:    map[string][]string{"zimbraServiceEnabled": {"service", "mailbox"}},
			wantDN:     provServerDN(provTestHost),
			wantTarget: provServer, wantName: provTestHost,
		},
		"plus adds, minus removes": {
			args:   []string{"ms", provTestHost, "+zimbraServiceEnabled", "a", "+zimbraServiceEnabled", "b", "-zimbraServiceInstalled", "c"},
			add:    map[string][]string{"zimbraServiceEnabled": {"a", "b"}},
			del:    map[string][]string{"zimbraServiceInstalled": {"c"}},
			wantDN: provServerDN(provTestHost), wantTarget: provServer, wantName: provTestHost,
		},
		"an empty value unsets the attribute": {
			args:   []string{"mcf", "zimbraSkinLogoURL", ""},
			del:    map[string][]string{"zimbraSkinLogoURL": nil},
			wantDN: provGlobalDN,
		},
		"removing an empty value does nothing": {
			args:   []string{"mcf", "-zimbraSkinLogoURL", ""},
			wantDN: provGlobalDN,
		},
		"attribute names are case-insensitive": {
			args:    []string{"mcf", "ZIMBRASKINLOGOURL", "x"},
			replace: map[string][]string{"ZIMBRASKINLOGOURL": {"x"}},
			wantDN:  provGlobalDN,
		},
		"class of service": {
			args:    []string{"mc", "my cos", "zimbraPrefUseKeyboardShortcuts", "TRUE"},
			replace: map[string][]string{"zimbraPrefUseKeyboardShortcuts": {"TRUE"}},
			wantDN:  "cn=my cos,cn=cos,cn=zimbra", wantTarget: provCOS, wantName: "my cos",
		},
		"special characters of the name are escaped": {
			args:    []string{"ms", "a,b", "zimbraIPMode", "ipv4"},
			replace: map[string][]string{"zimbraIPMode": {"ipv4"}},
			wantDN:  `cn=a\,b,cn=servers,cn=zimbra`, wantTarget: provServer, wantName: "a,b",
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, ok := e.provParse(tc.args)
			require.True(t, ok)
			assert.Equal(t, tc.wantTarget, c.target)
			assert.Equal(t, tc.wantName, c.name)
			assert.Equal(t, provNorm(tc.add), provNorm(c.add))
			assert.Equal(t, provNorm(tc.del), provNorm(c.del))
			assert.Equal(t, provNorm(tc.replace), provNorm(c.replace))
			assert.Equal(t, tc.wantDN, c.dn("cn=zimbra"))
		})
	}
}

func TestRunProv_LeavesToZmprovWhatItCannotDoLikeZmprov(t *testing.T) {
	longURL := strings.Repeat("x", 257)
	id := "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0"

	for name, args := range map[string][]string{
		"callback: mail mode sets zimbraMailLocalBind":    {"ms", provTestHost, "zimbraMailMode", "https"},
		"callback: mail port writes localconfig":          {"ms", provTestHost, "zimbraMailPort", "8080"},
		"callback: port conflict check":                   {"ms", provTestHost, "zimbraImapBindPort", "7143"},
		"callback: proxy port conflict check":             {"mcf", "zimbraMailProxyPort", "0"},
		"callback: host pool check":                       {"mc", "default", "zimbraMailHostPool", id},
		"unknown attribute":                               {"mcf", "zimbraNotInTheList", "x"},
		"one listed and one unlisted attribute":           {"ms", provTestHost, "zimbraServiceEnabled", "a", "zimbraMailMode", "http"},
		"enum value out of range":                         {"ms", provTestHost, "zimbraIPMode", "dual"},
		"boolean that is not TRUE or FALSE":               {"mcf", "zimbraReverseProxyHttpEnabled", "yes"},
		"negative integer":                                {"mcf", "zimbraReverseProxyIPLoginLimit", "-1"},
		"integer out of range":                            {"mcf", "zimbraReverseProxyIPLoginLimit", "99999999999"},
		"duration without digits":                         {"mcf", "zimbraReverseProxyAuthWaitInterval", "s"},
		"string longer than the maximum":                  {"mcf", "zimbraSkinLogoURL", longURL},
		"plus and plain form of one attribute":            {"mcf", "+zimbraServiceEnabled", "a", "zimbraServiceEnabled", "b"},
		"empty value among several":                       {"ms", provTestHost, "zimbraServiceEnabled", "a", "zimbraServiceEnabled", ""},
		"server given by id":                              {"ms", id, "zimbraIPMode", "ipv4"},
		"class of service given by id":                    {"mc", id, "zimbraPrefTimeZoneId", "UTC"},
		"missing value":                                   {"mcf", "zimbraSkinLogoURL"},
		"no attribute":                                    {"mcf"},
		"no server":                                       {"ms"},
		"empty attribute name":                            {"mcf", "", "x"},
		"binary attribute whose file cannot be read":      {"mcf", "zimbraSSLDHParam", "/missing.pem"},
		"creation of a server":                            {"cs", provTestHost},
		"creation of an account, password on stdin":       {"ca", "u@example.com", "Secret1", "displayName", "U"},
		"creation of a domain":                            {"cd", "example.com"},
		"alias":                                           {"aaa", "u@example.com", "a@example.com"},
		"another command naming an allowlisted attribute": {"gcf", "zimbraSkinLogoURL"},
	} {
		t.Run(name, func(t *testing.T) {
			e, fakes, _ := testEnv(t)
			runs := provRecordZmprov(fakes)

			require.NoError(t, e.Prov(args...))

			assert.Equal(t, []string{provLine(args) + "\nexit\n"}, runs.stdin)
			assert.Zero(t, provTestLDAP().opened, "no LDAP connection")
			assert.Empty(t, provTestLDAP().calls)
		})
	}
}

func TestRunProv_BinaryAttributeStoresTheContentOfTheFile(t *testing.T) {
	e, fakes, _ := testEnv(t)
	fakes.FS.Put("/opt/zextras/conf/dhparam.pem.crb", []byte("-----BEGIN DH PARAMETERS-----\nAAAA\n"), 0o640)

	require.NoError(t, e.Prov("mcf", "zimbraSSLDHParam", "/opt/zextras/conf/dhparam.pem.crb"))

	assert.Equal(t, []string{"-----BEGIN DH PARAMETERS-----\nAAAA\n"},
		provTestLDAP().values(provGlobalDN, "zimbraSSLDHParam"))
	assert.Empty(t, fakes.Exec.Calls())
}

func TestRunProv_MixedBatchRunsInOrderAndOnlyCreationsGoToZmprov(t *testing.T) {
	e, fakes, _ := testEnv(t)
	runs := provRecordZmprov(fakes)

	var file bytes.Buffer

	e.Log = NewLog(&bytes.Buffer{}, &file)

	var p Prov

	p.Add("ms", "h1.example.com", "zimbraIPMode", "ipv4")
	p.Add("cs", "h2.example.com")
	p.Add("mcf", "zimbraSkinLogoURL", "/logo.png")
	p.Add("ca", "admin@example.com", "S3cretPass", "zimbraIsAdminAccount", "TRUE")
	p.Add("aaa", "admin@example.com", "root@example.com")
	p.Add("ms", "h2.example.com", "zimbraIPMode", "both")

	require.NoError(t, e.RunProv(&p))

	assert.Equal(t, []string{
		"\"cs\" \"h2.example.com\"\nexit\n",
		"\"ca\" \"admin@example.com\" \"S3cretPass\" \"zimbraIsAdminAccount\" \"TRUE\"\n" +
			"\"aaa\" \"admin@example.com\" \"root@example.com\"\nexit\n",
	}, runs.stdin, "one process for each run of creations, in order")
	assert.Equal(t, []int{1, 2}, runs.ldapBefore, "LDAP changes run between them")
	assert.Len(t, provTestLDAP().calls, 3)
	assert.Equal(t, []string{"both"}, provTestLDAP().values(provServerDN("h2.example.com"), "zimbraIPMode"))

	log := file.String()
	assert.Contains(t, log, "zmprov batch: ldap: ms h1.example.com | zmprov: cs h2.example.com | "+
		"ldap: mcf zimbraSkinLogoURL | zmprov: ca admin@example.com; aaa admin@example.com | ldap: ms h2.example.com")
	assert.NotContains(t, log, "S3cretPass")

	for _, c := range fakes.Exec.Calls() {
		assert.NotContains(t, c.String(), "S3cretPass")
	}
}

func TestRunProv_ErrorsAreReportedAfterEveryCommandRan(t *testing.T) {
	t.Run("an LDAP failure does not stop the batch", func(t *testing.T) {
		e, _, _ := testEnv(t)
		provTestLDAP().failDN[provGlobalDN] = errors.New("LDAP Result Code 50 Insufficient Access Rights")

		var p Prov

		p.Add("mcf", "zimbraSkinLogoURL", "/logo.png")
		p.Add("ms", provTestHost, "zimbraIPMode", "ipv6")

		err := e.RunProv(&p)
		require.ErrorIs(t, err, ErrProv)
		assert.Contains(t, err.Error(), "ldap: mcf zimbraSkinLogoURL")
		assert.Contains(t, err.Error(), "Insufficient Access Rights")
		assert.Equal(t, []string{"ipv6"}, provTestLDAP().values(provServerDN(provTestHost), "zimbraIPMode"))
		assert.Equal(t, 1, provTestLDAP().closed)
	})

	t.Run("a zmprov failure is reported with the LDAP changes applied", func(t *testing.T) {
		e, fakes, _ := testEnv(t)
		runs := provRecordZmprov(fakes)
		runs.exitCode = 2
		runs.failMessage = "ERROR: account.NO_SUCH_DOMAIN (no such domain: x)\n"

		var p Prov

		p.Add("ca", "a@x", "pw")
		p.Add("mcf", "zimbraSkinLogoURL", "/logo.png")

		err := e.RunProv(&p)
		require.ErrorIs(t, err, ErrProv)
		assert.Contains(t, err.Error(), "NO_SUCH_DOMAIN")
		assert.Equal(t, []string{"/logo.png"}, provTestLDAP().values(provGlobalDN, "zimbraSkinLogoURL"))
	})

	t.Run("zmprov exits with an error and says nothing", func(t *testing.T) {
		e, fakes, _ := testEnv(t)
		provRecordZmprov(fakes).exitCode = 2

		err := e.Prov("cd", "example.com")
		require.ErrorIs(t, err, ErrProv)
		assert.Contains(t, err.Error(), "exit status 2")
	})

	t.Run("zmprov cannot start", func(t *testing.T) {
		e, fakes, _ := testEnv(t)
		fakes.Exec.OnPath("runuser", func(*sys.Cmd) (sys.Result, error) { return sys.Result{}, errors.New("no such file") })

		err := e.Prov("cd", "example.com")
		require.ErrorIs(t, err, ErrProv)
		assert.Contains(t, err.Error(), "no such file")
	})

	t.Run("no LDAP connection", func(t *testing.T) {
		e, fakes, _ := testEnv(t)
		runs := provRecordZmprov(fakes)
		provTestLDAP().openErr = errors.New("connection refused")

		var p Prov

		p.Add("mcf", "zimbraSkinLogoURL", "/logo.png")
		p.Add("cd", "example.com")
		p.Add("mcf", "zimbraMtaMyOrigin", "example.com")

		err := e.RunProv(&p)
		require.ErrorIs(t, err, ErrProv)
		assert.Contains(t, err.Error(), "opening LDAP: connection refused")
		assert.Len(t, runs.stdin, 1, "the creation still runs")
	})
}

func TestRunProv_EmptyBatchDoesNothing(t *testing.T) {
	e, fakes, _ := testEnv(t)

	require.NoError(t, e.RunProv(&Prov{}))
	assert.Empty(t, fakes.Exec.Calls())
	assert.Zero(t, provTestLDAP().opened)
}

func TestProvAttr_ValidMirrorsZmprovCheckValue(t *testing.T) {
	for name, tc := range map[string]struct {
		attr  provAttr
		value string
		want  bool
	}{
		"empty unsets":          {provAttr{kind: provBool}, "", true},
		"bool TRUE":             {provAttr{kind: provBool}, "TRUE", true},
		"bool FALSE":            {provAttr{kind: provBool}, "FALSE", true},
		"bool lower case":       {provAttr{kind: provBool}, "true", false},
		"int":                   {provAttr{kind: provInt}, "3600", true},
		"int zero":              {provAttr{kind: provInt}, "0", true},
		"int negative":          {provAttr{kind: provInt}, "-1", false},
		"int text":              {provAttr{kind: provInt}, "ten", false},
		"enum member":           {provAttr{kind: provEnum, enum: []string{"a", "b"}}, "b", true},
		"enum other":            {provAttr{kind: provEnum, enum: []string{"a", "b"}}, "c", false},
		"duration seconds":      {provAttr{kind: provDuration}, "10s", true},
		"duration milliseconds": {provAttr{kind: provDuration}, "10ms", true},
		"duration bare":         {provAttr{kind: provDuration}, "10", true},
		"duration bad unit":     {provAttr{kind: provDuration}, "10w", false},
		"string at the limit":   {provAttr{kind: provString, max: 3}, "abc", true},
		"string over the limit": {provAttr{kind: provString, max: 3}, "abcd", false},
		"string unlimited":      {provAttr{kind: provString}, strings.Repeat("x", 5000), true},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.attr.valid(tc.value))
		})
	}
}

//go:embed testdata/attrs-allowlist.tsv
var provAttrSnapshot string

// provReviewedCallbacks are the attrs.xml callbacks that were read and found to
// do nothing bootstrap depends on, with why; an allowlisted attribute may carry
// one of them. None is needed today. A callback goes in here only after
// reading its class in carbonio-mailbox (store/.../account/callback), and an
// attribute with a callback that validates or writes something is routed to
// zmprov by leaving it out of provAttrList.
var provReviewedCallbacks = map[string]string{}

// provSnapshotRow is a line of testdata/attrs-allowlist.tsv, which
// scripts/check-attrs-drift.sh compares with attrs.xml.
type provSnapshotRow struct {
	typ, callback, min, max, value, immutable, deprecated string
}

func provSnapshotRows(t *testing.T) map[string]provSnapshotRow {
	t.Helper()

	rows := map[string]provSnapshotRow{}
	lines := strings.Split(strings.TrimRight(provAttrSnapshot, "\n"), "\n")
	require.Equal(t, "name\ttype\tcallback\tmin\tmax\tvalue\timmutable\tdeprecated", lines[0])

	for _, line := range lines[1:] {
		f := strings.Split(line, "\t")
		require.Len(t, f, 8, line)

		rows[strings.ToLower(f[0])] = provSnapshotRow{f[1], f[2], f[3], f[4], f[5], f[6], f[7]}
	}

	return rows
}

func TestProvAttrList_MatchesTheSnapshotOfAttrsXML(t *testing.T) {
	rows := provSnapshotRows(t)

	kindTypes := map[provKind][]string{
		provString:   {"string", "astring", "email"},
		provBool:     {"boolean"},
		provInt:      {"integer"},
		provEnum:     {"enum"},
		provDuration: {"duration"},
		provBinary:   {"binary"},
	}

	for name, a := range provAttrList {
		t.Run(name, func(t *testing.T) {
			row, ok := rows[strings.ToLower(name)]
			require.True(t, ok, "%s is allowlisted but not in the snapshot: add its name to testdata/attrs-allowlist.tsv "+
				"and run scripts/check-attrs-drift.sh --update", name)

			if row.callback != "" {
				_, reviewed := provReviewedCallbacks[row.callback]
				assert.True(t, reviewed, "%s has the callback %s, which nobody reviewed: read it, then either "+
					"record it in provReviewedCallbacks or take the attribute off provAttrList (zmprov runs it)",
					name, row.callback)
			}

			assert.Contains(t, kindTypes[a.kind], row.typ, "the type of %s in attrs.xml", name)
			assert.Empty(t, row.immutable, "an immutable attribute is refused by zmprov")
			assert.Empty(t, row.deprecated, "zmprov warns about, and may refuse, a deprecated attribute")

			switch a.kind {
			case provEnum:
				assert.Equal(t, row.value, strings.Join(a.enum, ","))
			case provInt:
				assert.Contains(t, []string{"", "0"}, row.min, "provAttr.valid accepts 0 and more")
				assert.Empty(t, row.max)
			case provString:
				if a.max == 0 {
					assert.Empty(t, row.max)
				} else {
					assert.Equal(t, strconv.Itoa(a.max), row.max)
				}
			default:
			}
		})
	}

	for name := range rows {
		_, ok := provAttrIndex()[name]
		assert.True(t, ok, "%s is in the snapshot but not allowlisted", name)
	}
}
