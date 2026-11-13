// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/zxadmin"
)

// gsFakeAdmin is the admin API of zmgsautil: it records what it is asked and
// answers with the configured results.
type gsFakeAdmin struct {
	created  []zxadmin.GalSyncAccountRequest
	added    []zxadmin.GalSyncAccountRequest
	deleted  []string
	deleteBy []bool
	synced   []zxadmin.GalSyncRequest

	accounts []zxadmin.GalSyncAccount
	err      error
}

func (g *gsFakeAdmin) CreateGalSyncAccount(
	_ context.Context, r *zxadmin.GalSyncAccountRequest,
) (zxadmin.GalSyncAccount, error) {
	g.created = append(g.created, *r)

	return g.accounts[0], g.err
}

func (g *gsFakeAdmin) AddGalSyncDataSource(
	_ context.Context, r *zxadmin.GalSyncAccountRequest,
) ([]zxadmin.GalSyncAccount, error) {
	g.added = append(g.added, *r)

	return g.accounts, g.err
}

func (g *gsFakeAdmin) DeleteGalSyncAccount(_ context.Context, account string, byID bool) error {
	g.deleted = append(g.deleted, account)
	g.deleteBy = append(g.deleteBy, byID)

	return g.err
}

func (g *gsFakeAdmin) SyncGalAccount(_ context.Context, r *zxadmin.GalSyncRequest) error {
	g.synced = append(g.synced, *r)

	return g.err
}

const (
	gsAccountID   = "0b1c2d3e-0000-4000-8000-0000000000aa"
	gsAccountName = "galsync.abc@example.com"
)

// gsEnv is an Env whose directory holds the GAL sync account and whose admin
// API is the returned fake.
func gsEnv(t *testing.T) (*Env, *gsFakeAdmin) {
	t.Helper()

	e, _, _ := testEnv(t)
	f := provTestLDAP()
	f.set("uid=galsync.abc,ou=people,dc=example,dc=com", "objectClass", "zimbraAccount")
	f.set("uid=galsync.abc,ou=people,dc=example,dc=com", "zimbraId", gsAccountID)
	f.set("uid=galsync.abc,ou=people,dc=example,dc=com", "zimbraMailDeliveryAddress", gsAccountName)

	admin := &gsFakeAdmin{accounts: []zxadmin.GalSyncAccount{{Name: gsAccountName, ID: gsAccountID}}}
	old := svcOpenGAL
	svcOpenGAL = func(*Env) (svcGALAdmin, error) { return admin, nil }

	t.Cleanup(func() { svcOpenGAL = old })

	return e, admin
}

func gsRun(e *Env, args ...string) (status int, stdout, stderr string) {
	var out, errOut bytes.Buffer

	status = RunGalSyncUtil(e, args, &out, &errOut)

	return status, out.String(), errOut.String()
}

func TestRunGalSyncUtil_UsageForEveryIncompleteCommandLine(t *testing.T) {
	e, admin := gsEnv(t)

	for name, args := range map[string][]string{
		"no arguments":               nil,
		"unknown command":            {"frobnicate"},
		"help":                       {"trickleSync", "-h", "x"},
		"option before command":      {"-a", gsAccountName, "trickleSync", "-n", "InternalGAL"},
		"sync without a data source": {"trickleSync", "-a", gsAccountName},
		"sync without an account":    {"fullSync", "-n", "InternalGAL"},
		"create without server":      {"createAccount", "-a", "g@d", "-n", "N", "--domain", "d", "-t", "zimbra"},
		"create bad type":            {"createAccount", "-a", "g@d", "-n", "N", "-x", "d", "-t", "ZIMBRA", "-s", "h"},
		"add without type":           {"addDataSource", "-a", "g@d", "-n", "N", "-x", "d"},
		"add without domain":         {"addDataSource", "-a", "g@d", "-n", "N", "-t", "ldap"},
		"delete without account":     {"deleteAccount"},
	} {
		status, out, errOut := gsRun(e, args...)

		assert.Equal(t, 1, status, name)
		assert.Equal(t, gsautilUsage, out, name)
		assert.Empty(t, errOut, name)
	}

	assert.Empty(t, admin.synced)
	assert.Empty(t, admin.created)
	assert.Empty(t, admin.added)
	assert.Empty(t, admin.deleted)
}

func TestRunGalSyncUtil_OptionErrorsArePrintedBeforeTheUsage(t *testing.T) {
	e, admin := gsEnv(t)

	status, out, _ := gsRun(e, "trickleSync", "-z")
	assert.Equal(t, 1, status)
	assert.Equal(t, "error: Unrecognized option: -z\n"+gsautilUsage, out)

	status, out, _ = gsRun(e, "trickleSync", "-a")
	assert.Equal(t, 1, status)
	assert.Equal(t, "error: Missing argument for option: a\n"+gsautilUsage, out)

	status, out, _ = gsRun(e, "trickleSync", "-a", "-n", "InternalGAL")
	assert.Equal(t, 1, status)
	assert.Equal(t, "error: Missing argument for option: a\n"+gsautilUsage, out, "an option is not a value")

	status, out, _ = gsRun(e, "trickleSync", "-h")
	assert.Equal(t, 1, status)
	assert.Equal(t, "error: Missing argument for option: h\n"+gsautilUsage, out)

	assert.Empty(t, admin.synced)
}

func TestParseGsautilArgs_AcceptsTheCommonsCLIForms(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want gsautilOpts
	}{
		"short":                        {[]string{"c", "-a", "x", "-n", "y"}, gsautilOpts{"a": "x", "n": "y"}},
		"long":                         {[]string{"c", "--account", "x", "--did", "d1"}, gsautilOpts{"a": "x", "d": "d1"}},
		"long with equals":             {[]string{"c", "--account=x", "--name=y"}, gsautilOpts{"a": "x", "n": "y"}},
		"short with equals":            {[]string{"c", "-a=x"}, gsautilOpts{"a": "x"}},
		"short glued":                  {[]string{"c", "-ax", "-ny"}, gsautilOpts{"a": "x", "n": "y"}},
		"first one wins":               {[]string{"c", "-a", "x", "-a", "y"}, gsautilOpts{"a": "x"}},
		"quotes stripped":              {[]string{"c", "-a", `"x y"`}, gsautilOpts{"a": "x y"}},
		"inner quote kept":             {[]string{"c", "-a", `"x"y"`}, gsautilOpts{"a": `"x"y"`}},
		"value like a negative number": {[]string{"c", "-p", "-5"}, gsautilOpts{"p": "-5"}},
		"after --":                     {[]string{"c", "-a", "x", "--", "-z"}, gsautilOpts{"a": "x"}},
		"empty value":                  {[]string{"c", "-f", ""}, gsautilOpts{"f": ""}},
	} {
		got, err := parseGsautilArgs(tc.args)
		require.NoError(t, err, name)
		assert.Equal(t, tc.want, got, name)
	}
}

func TestRunGalSyncUtil_SyncByAccountNameLooksTheIDUp(t *testing.T) {
	e, admin := gsEnv(t)

	status, out, errOut := gsRun(e, "trickleSync", "-a", gsAccountName, "-n", "InternalGAL")

	assert.Equal(t, 0, status, errOut)
	assert.Empty(t, out)
	assert.Equal(t, []zxadmin.GalSyncRequest{{AccountID: gsAccountID, DataSourceName: "InternalGAL"}}, admin.synced)
}

func TestRunGalSyncUtil_SyncCommandsAskForFullAndReset(t *testing.T) {
	e, admin := gsEnv(t)

	for _, args := range [][]string{
		{"TRICKLESYNC", "-i", gsAccountID, "-d", "ds1"},
		{"fullSync", "-i", gsAccountID, "-d", "ds1"},
		{"forceSync", "-i", gsAccountID, "-d", "ds1", "-n", "InternalGAL"},
	} {
		status, out, errOut := gsRun(e, args...)
		assert.Equal(t, 0, status, errOut)
		assert.Empty(t, out)
	}

	assert.Equal(t, []zxadmin.GalSyncRequest{
		{AccountID: gsAccountID, DataSourceID: "ds1"},
		{AccountID: gsAccountID, DataSourceID: "ds1", Full: true},
		{AccountID: gsAccountID, DataSourceID: "ds1", DataSourceName: "InternalGAL", Full: true, Reset: true},
	}, admin.synced)
}

func TestRunGalSyncUtil_UnknownAccountIsAServiceErrorWithStatusZero(t *testing.T) {
	e, admin := gsEnv(t)

	status, out, _ := gsRun(e, "trickleSync", "-a", "nobody@example.com", "-n", "InternalGAL")

	assert.Equal(t, 0, status)
	assert.Equal(t, "Error: no such account: nobody@example.com\n", out)
	assert.Empty(t, admin.synced)
}

func TestRunGalSyncUtil_ServerFaultIsPrintedWithStatusZero(t *testing.T) {
	e, admin := gsEnv(t)
	admin.err = &zxadmin.Fault{Op: "SyncGalAccount", Code: "account.NO_SUCH_ACCOUNT", Reason: "no such account: x"}

	status, out, errOut := gsRun(e, "forceSync", "-i", "x", "-d", "d")

	assert.Equal(t, 0, status)
	assert.Equal(t, "Error: no such account: x\n", out)
	assert.Empty(t, errOut)
}

func TestRunGalSyncUtil_UnreachableServerIsAnErrorOnStderrWithStatusOne(t *testing.T) {
	e, admin := gsEnv(t)
	admin.err = errors.New("connection refused")

	status, out, errOut := gsRun(e, "deleteAccount", "-a", gsAccountName)

	assert.Equal(t, 1, status)
	assert.Empty(t, out)
	assert.Contains(t, errOut, "connection refused")
}

func TestRunGalSyncUtil_CreateAccountSendsTheRequestAndPrintsTheAccount(t *testing.T) {
	e, admin := gsEnv(t)

	status, out, errOut := gsRun(e, "createAccount", "-a", gsAccountName, "-n", "InternalGAL", "--domain", "example.com",
		"-t", "zimbra", "-s", "mbox.example.com", "-f", "_InternalGAL", "-p", "1d")

	assert.Equal(t, 0, status, errOut)
	assert.Equal(t, gsAccountName+"\t"+gsAccountID+"\n", out)
	assert.Equal(t, []zxadmin.GalSyncAccountRequest{{
		Account: gsAccountName, Name: "InternalGAL", Domain: "example.com", Type: "zimbra",
		Folder: "_InternalGAL", Server: "mbox.example.com", PollingInterval: "1d",
	}}, admin.created)
}

func TestRunGalSyncUtil_AddDataSourcePrintsEveryAccountAndSendsNoServer(t *testing.T) {
	e, admin := gsEnv(t)
	admin.accounts = []zxadmin.GalSyncAccount{{Name: "a@d", ID: "1"}, {Name: "b@d", ID: "2"}}

	status, out, errOut := gsRun(e, "addDataSource", "-a", "a@d", "-n", "Ext", "-x", "d", "-t", "ldap")

	assert.Equal(t, 0, status, errOut)
	assert.Equal(t, "a@d\t1\nb@d\t2\n", out)
	assert.Equal(t, []zxadmin.GalSyncAccountRequest{{Account: "a@d", Name: "Ext", Domain: "d", Type: "ldap"}}, admin.added)
}

func TestRunGalSyncUtil_DeleteAccountByNameOrByID(t *testing.T) {
	e, admin := gsEnv(t)

	for _, args := range [][]string{
		{"deleteAccount", "-a", gsAccountName},
		{"deleteAccount", "-i", gsAccountID},
		{"deleteAccount", "-a", gsAccountName, "-i", gsAccountID},
	} {
		status, out, errOut := gsRun(e, args...)
		assert.Equal(t, 0, status, errOut)
		assert.Empty(t, out)
	}

	assert.Equal(t, []string{gsAccountName, gsAccountID, gsAccountName}, admin.deleted)
	assert.Equal(t, []bool{false, true, false}, admin.deleteBy, "by name unless only the id is given")
}

func TestRunGalSyncUtil_NoAdminAPIIsAnErrorWithStatusOne(t *testing.T) {
	e, _ := gsEnv(t)

	svcOpenGAL = func(*Env) (svcGALAdmin, error) { return nil, errors.New("no localconfig") }

	status, out, errOut := gsRun(e, "deleteAccount", "-i", gsAccountID)

	assert.Equal(t, 1, status)
	assert.Empty(t, out)
	assert.Contains(t, errOut, "no localconfig")
}

func TestRunGalSyncUtil_DirectoryDownIsAServiceErrorWithStatusZero(t *testing.T) {
	e, _ := gsEnv(t)
	provTestLDAP().openErr = errors.New("ldap unreachable")

	status, out, _ := gsRun(e, "trickleSync", "-a", gsAccountName, "-n", "InternalGAL")

	assert.Equal(t, 0, status)
	assert.Contains(t, out, "Error: opening LDAP: ldap unreachable")
}
