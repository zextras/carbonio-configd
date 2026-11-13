// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package provision_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/provision"
)

// output runs a command and returns what it printed.
func (r *rig) output(t *testing.T, args ...string) string {
	t.Helper()

	var out bytes.Buffer

	require.NoError(t, r.Run(args, &out), "%v", args)

	return out.String()
}

func TestRun_GetServerDumpsSortedAttributesAndInheritsTheGlobalConfig(t *testing.T) {
	r := newRig(t)
	r.withServer(t)
	r.dir.Set(globalDN, "zimbraMailPort", "8080")
	r.dir.Set(globalDN, "zimbraImapBindPort", "7143")
	r.run(t, "ms", host, "zimbraMailPort", "8081")

	got := r.output(t, "gs", host, "zimbraMailPort", "zimbraImapBindPort", "zimbraServiceEnabled")

	assert.Equal(t, "# name mail.example.com\n"+
		"zimbraImapBindPort: 7143\n"+
		"zimbraMailPort: 8081\n"+
		"zimbraServiceEnabled: service\n\n", got)
}

func TestRun_GetServerWithoutDefaultsShowsOnlyTheEntry(t *testing.T) {
	r := newRig(t)
	r.withServer(t)
	r.dir.Set(globalDN, "zimbraImapBindPort", "7143")

	got := r.output(t, "gs", "-e", host, "zimbraImapBindPort", "zimbraServiceEnabled")

	assert.Equal(t, "# name mail.example.com\nzimbraServiceEnabled: service\n\n", got)
}

func TestRun_GetServerByIDKeepsTheOrderOfMultipleValues(t *testing.T) {
	r := newRig(t)
	id := r.withServer(t)
	r.run(t, "ms", host, "+zimbraServiceEnabled", "mta", "+zimbraServiceEnabled", "amavis")

	got := r.output(t, "gs", id, "zimbraServiceEnabled")

	assert.Equal(t, "# name mail.example.com\nzimbraServiceEnabled: service\nzimbraServiceEnabled: mta\nzimbraServiceEnabled: amavis\n\n", got)
}

func TestRun_AttributeFilterIsCaseInsensitiveAndMatchesValues(t *testing.T) {
	r := newRig(t)
	r.withServer(t)
	r.run(t, "ms", host, "+zimbraServiceEnabled", "mta")

	assert.Equal(t, "# name mail.example.com\nzimbraServiceEnabled: mta\n\n",
		r.output(t, "gs", host, "ZIMBRASERVICEENABLED=mta"))

	var out bytes.Buffer

	err := r.Run([]string{"gs", host, "zimbraServiceEnabled="}, &out)
	require.Error(t, err)
	assert.True(t, hasCode(err, provision.CodeInvalidRequest))
	assert.Contains(t, err.Error(), "missing value for zimbraserviceenabled=")
}

func TestRun_NoSuchEntryErrors(t *testing.T) {
	r := newRig(t)
	r.withDomain(t)

	for name, tc := range map[string]struct {
		args []string
		code string
		msg  string
	}{
		"server":  {[]string{"gs", "nosuch"}, provision.CodeNoSuchServer, "no such server: nosuch"},
		"account": {[]string{"ga", "x@example.com"}, provision.CodeNoSuchAccount, "no such account: x@example.com"},
		"domain":  {[]string{"gd", "nosuch.org"}, provision.CodeNoSuchDomain, "no such domain: nosuch.org"},
		"cos":     {[]string{"gc", "nosuch"}, provision.CodeNoSuchCOS, "no such cos: nosuch"},
	} {
		err := r.Run(tc.args, &bytes.Buffer{})
		require.Error(t, err, name)
		assert.True(t, hasCode(err, tc.code), "%s: %v", name, err)
		assert.Contains(t, err.Error(), tc.msg, name)
	}
}

func TestRun_GetConfigPrintsNoHeaderNorBlankLine(t *testing.T) {
	r := newRig(t)
	r.dir.Set(globalDN, "zimbraDefaultDomainName", "example.com")

	assert.Equal(t, "zimbraDefaultDomainName: example.com\n", r.output(t, "gcf", "zimbraDefaultDomainName"))

	all := r.output(t, "gacf")
	assert.Contains(t, all, "zimbraDefaultDomainName: example.com\n")
	assert.Contains(t, all, "objectClass: zimbraGlobalConfig\n")
	assert.False(t, strings.HasPrefix(all, "#"))
	assert.Equal(t, "zimbraAccountExtraObjectClass: amavisAccount\n", r.output(t, "gacf", "zimbraAccountExtraObjectClass"))
}

func TestRun_BinaryAttributesAreBase64InLinesOf76WithCRLF(t *testing.T) {
	r := newRig(t)

	data := bytes.Repeat([]byte{0xde, 0xad, 0xbe, 0xef, 0x00}, 40) // 200 bytes: 268 base64 characters
	r.dir.Set(globalDN, "zimbraSSLDHParam", string(data))

	enc := base64.StdEncoding.EncodeToString(data)
	want := "zimbraSSLDHParam:: " + enc[:76] + "\r\n" + enc[76:152] + "\r\n" + enc[152:228] + "\r\n" + enc[228:] + "\r\n"

	assert.Equal(t, want, r.output(t, "gcf", "zimbraSSLDHParam"))

	short := base64.StdEncoding.EncodeToString([]byte("abc"))
	r.dir.Set(globalDN, "zimbraSSLDHParam", "abc")
	assert.Equal(t, "zimbraSSLDHParam:: "+short+"\r\n", r.output(t, "gcf", "zimbraSSLDHParam"))
}

func TestRun_GetDomainInheritsAndGetCOSShowsTheEntry(t *testing.T) {
	r := newRig(t)
	r.dir.Set(globalDN, "carbonioNotificationFrom", "noreply@example.com")
	r.withDomain(t)

	assert.Equal(t, "# name example.com\ncarbonioNotificationFrom: noreply@example.com\nzimbraDomainName: example.com\n\n",
		r.output(t, "gd", "example.com", "carbonioNotificationFrom", "zimbraDomainName"))
	assert.Equal(t, "# name example.com\nzimbraDomainName: example.com\n\n",
		r.output(t, "gd", "-e", "example.com", "carbonioNotificationFrom", "zimbraDomainName"))

	r.dir.Set(cosDN, "zimbraMailQuota", "1024")
	assert.Equal(t, "# name default\ncn: default\nzimbraMailQuota: 1024\n\n", r.output(t, "gc", "default", "cn", "zimbraMailQuota"))
}

func TestRun_GetAccountLayersOwnAttributesOverCOSOverDomain(t *testing.T) {
	r := newRig(t)
	r.dir.Set(cosDN, "zimbraMailQuota", "1024")
	r.dir.Set(cosDN, "zimbraPasswordMinLength", "6")
	r.withDomain(t)
	r.run(t, "md", "example.com", "zimbraPrefTimeZoneId", "Europe/Rome")
	r.withServer(t)
	r.run(t, "ca", adminAddr, "longenough-1", "zimbraPasswordMinLength", "10")

	attrs := []string{"zimbraPrefTimeZoneId", "zimbraMailQuota", "zimbraPasswordMinLength", "zimbraCOSId", "zimbraMailDeliveryAddress"}
	got := r.output(t, append([]string{"ga", adminAddr}, attrs...)...)

	assert.Equal(t, "# name admin@example.com\n"+
		"zimbraCOSId: 0f1e2d3c-0000-4000-8000-000000000001\n"+
		"zimbraMailDeliveryAddress: admin@example.com\n"+
		"zimbraMailQuota: 1024\n"+
		"zimbraPasswordMinLength: 10\n"+
		"zimbraPrefTimeZoneId: Europe/Rome\n\n", got)

	own := r.output(t, "ga", "-e", adminAddr, "zimbraMailQuota", "zimbraMailDeliveryAddress")
	assert.Equal(t, "# name admin@example.com\nzimbraMailDeliveryAddress: admin@example.com\n\n", own)
}

func TestRun_GetAccountUsesTheDomainDefaultCOS(t *testing.T) {
	r := newRig(t)

	r.dir.Set("cn=other,cn=cos,cn=zimbra", "objectClass", "zimbraCOS")
	r.dir.Set("cn=other,cn=cos,cn=zimbra", "zimbraId", "0f1e2d3c-0000-4000-8000-000000000002")
	r.dir.Set("cn=other,cn=cos,cn=zimbra", "cn", "other")
	r.dir.Set("cn=other,cn=cos,cn=zimbra", "zimbraMailQuota", "5")
	r.run(t, "cd", "example.com", "zimbraDomainDefaultCOSId", "0f1e2d3c-0000-4000-8000-000000000002")
	r.withServer(t)
	r.run(t, "ca", adminAddr, "x")

	assert.Equal(t, "# name admin@example.com\nzimbraMailQuota: 5\n\n", r.output(t, "ga", adminAddr, "zimbraMailQuota"))
}

func TestRun_GetAllServersListsNamesSortedAndFiltersByService(t *testing.T) {
	r := newRig(t)
	r.run(t, "cs", "b.example.com")
	r.run(t, "cs", "a.example.com")
	r.run(t, "ms", "a.example.com", "zimbraServiceEnabled", "proxy")

	assert.Equal(t, "a.example.com\nb.example.com\n", r.output(t, "gas"))
	assert.Equal(t, "a.example.com\n", r.output(t, "gas", "proxy"))
	assert.Equal(t, "", r.output(t, "gas", "mta"))

	verbose := r.output(t, "gas", "-v", "-e", "proxy")
	assert.True(t, strings.HasPrefix(verbose, "# name a.example.com\n"))
	assert.True(t, strings.HasSuffix(verbose, "\n\n"))

	err := r.Run([]string{"gas", "-e"}, &bytes.Buffer{})

	var usage *provision.UsageError

	require.True(t, errors.As(err, &usage))
}

func TestRun_ReverseProxyURLsAndMemcachedServers(t *testing.T) {
	r := newRig(t)
	r.dir.Set(globalDN, "zimbraReverseProxyLookupTarget", "TRUE")

	assert.Equal(t, "\n", r.output(t, "garpu"))
	assert.Equal(t, "\n", r.output(t, "gamcs"))

	r.run(t, "cs", "mail.example.com")
	r.run(t, "ms", "mail.example.com", "zimbraServiceEnabled", "service", "zimbraServiceHostname", "mail.example.com")
	r.run(t, "cs", "cache.example.com")
	r.run(t, "ms", "cache.example.com", "zimbraServiceEnabled", "memcached", "zimbraServiceHostname", "cache.example.com")
	r.run(t, "cs", "off.example.com")
	r.run(t, "ms", "off.example.com", "zimbraServiceEnabled", "service", "zimbraServiceHostname", "off.example.com", "zimbraReverseProxyLookupTarget", "FALSE")

	assert.Equal(t, "mail.example.com:7072/service/extension/nginx-lookup \n", r.output(t, "garpu"))
	assert.Equal(t, "cache.example.com:11211 \n", r.output(t, "gamcs"))

	r.run(t, "ms", "cache.example.com", "zimbraMemcachedBindPort", "11999")
	assert.Equal(t, "cache.example.com:11999 \n", r.output(t, "gamcs"))
}

func TestRun_ArgumentCountAndUnknownVerbAreUsageErrors(t *testing.T) {
	r := newRig(t)

	for _, args := range [][]string{{"gs"}, {"gcf"}, {"gcf", "a", "b"}, {"garpu", "x"}, {"gs", "-e"}, {"nosuch", "x"}, {}} {
		err := r.Run(args, &bytes.Buffer{})

		var usage *provision.UsageError

		require.True(t, errors.As(err, &usage), "%v: %v", args, err)
	}

	err := r.Run([]string{"zap"}, &bytes.Buffer{})
	assert.Contains(t, err.Error(), `unknown command: "zap"`)
}

func TestRun_WriteVerbsPrintTheNewID(t *testing.T) {
	r := newRig(t)

	got := r.output(t, "cs", "x.example.com")

	assert.Regexp(t, uuidPattern.String()[1:len(uuidPattern.String())-1]+`\n$`, got)
	assert.Equal(t, "", r.output(t, "ms", "x.example.com", "zimbraServiceEnabled", "mta"))
}

func TestModifyAccountAndDomain(t *testing.T) {
	r := newRig(t)
	r.withDomain(t)
	r.withServer(t)
	r.run(t, "ca", adminAddr, "x")

	r.run(t, "ma", adminAddr, "displayName", "The Admin")
	assert.Equal(t, []string{"The Admin"}, r.dir.Values(adminDN, "displayName"))
	assert.Equal(t, []string{"The Admin"}, r.dir.Values(adminDN, "cn"))

	r.run(t, "md", "example.com", "zimbraDomainStatus", "maintenance")
	assert.Equal(t, []string{"maintenance"}, r.dir.Values(domainDN, "zimbraDomainStatus"))

	r.run(t, "ma", adminAddr, "zimbraCOSId", "0f1e2d3c-0000-4000-8000-000000000001")
}

func TestModifyAccountAndDomainRefusals(t *testing.T) {
	r := newRig(t)
	r.withDomain(t)
	r.withServer(t)
	r.run(t, "ca", adminAddr, "x")
	r.dir.Set(domainDN, "zimbraDomainRenameInfo", "renaming")

	for name, tc := range map[string]struct {
		args []string
		code string
	}{
		"unported callback": {[]string{"ma", adminAddr, "zimbraAccountStatus", "closed"}, provision.CodeInvalidRequest},
		"password":          {[]string{"ma", adminAddr, "userPassword", "secret"}, provision.CodeInvalidRequest},
		"unknown cos":       {[]string{"ma", adminAddr, "zimbraCOSId", "0f1e2d3c-0000-4000-8000-0000000000ff"}, provision.CodeInvalidRequest},
		"renaming domain":   {[]string{"md", "example.com", "zimbraDomainStatus", "locked"}, provision.CodeInvalidRequest},
		"no account":        {[]string{"ma", "nobody@example.com", "description", "x"}, provision.CodeNoSuchAccount},
		"no domain":         {[]string{"md", "nowhere.org", "description", "x"}, provision.CodeNoSuchDomain},
	} {
		_, err := r.Exec(tc.args)
		require.Error(t, err, name)
		assert.True(t, hasCode(err, tc.code), "%s: %v", name, err)
	}
}
