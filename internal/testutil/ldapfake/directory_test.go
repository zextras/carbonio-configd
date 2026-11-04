// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldapfake_test

import (
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"
	"github.com/zextras/carbonio-configd/internal/testutil/ldapfake"
)

const seedLDIF = `version: 1
# comment
dn: cn=zimbra
objectClass: organizationalRole
cn: zimbra

dn: cn=config,cn=zimbra
objectClass: zimbraGlobalConfig
cn: config
zimbraMtaMyNetworks: 127.0.0.0/8
 [::1]/128
zimbraDescription:: aMOgIGLDqA==

dn: cn=servers,cn=zimbra
objectClass: organizationalRole
cn: servers

dn: cn=mail.example.com,cn=servers,cn=zimbra
objectClass: zimbraServer
cn: mail.example.com
zimbraServiceEnabled: mta
zimbraServiceEnabled: proxy

dn: cn=other.example.com,cn=servers,cn=zimbra
objectClass: zimbraServer
cn: other.example.com
zimbraServiceEnabled: mailbox
`

func dial(t *testing.T, d *ldapfake.Directory) *ldap.Conn {
	t.Helper()

	srv := ldapfake.New(t, ldapfake.Options{Directory: d})

	conn, err := ldap.DialURL("ldap://" + srv.Addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

func search(t *testing.T, c *ldap.Conn, base string, scope int, filter string, attrs ...string) []*ldap.Entry {
	t.Helper()

	res, err := c.Search(ldap.NewSearchRequest(base, scope, ldap.NeverDerefAliases, 0, 0, false, filter, attrs, nil))
	if err != nil {
		t.Fatalf("search %s %q: %v", base, filter, err)
	}

	return res.Entries
}

func TestDirectorySearchScopesAndFilters(t *testing.T) {
	d := ldapfake.NewDirectory()
	if err := d.ApplyLDIF(strings.NewReader(seedLDIF)); err != nil {
		t.Fatalf("ApplyLDIF: %v", err)
	}

	c := dial(t, d)

	cfg := search(t, c, "cn=config,cn=zimbra", ldap.ScopeBaseObject, "(objectClass=*)")
	if len(cfg) != 1 {
		t.Fatalf("base search returned %d entries", len(cfg))
	}

	if got := cfg[0].GetAttributeValues("zimbraMtaMyNetworks"); len(got) != 1 || got[0] != "127.0.0.0/8[::1]/128" {
		t.Errorf("continuation line not folded: %q", got)
	}

	if got := cfg[0].GetAttributeValue("zimbraDescription"); got != "hà bè" {
		t.Errorf("base64 value = %q", got)
	}

	one := search(t, c, "cn=servers,cn=zimbra", ldap.ScopeSingleLevel, "(objectClass=zimbraServer)")
	if len(one) != 2 {
		t.Errorf("one-level search returned %d entries, want 2", len(one))
	}

	sub := search(t, c, "cn=zimbra", ldap.ScopeWholeSubtree, "(&(objectClass=zimbraServer)(zimbraServiceEnabled=PROXY))")
	if len(sub) != 1 || sub[0].GetAttributeValue("cn") != "mail.example.com" {
		t.Errorf("subtree AND filter returned %v", sub)
	}

	not := search(t, c, "cn=zimbra", ldap.ScopeWholeSubtree, "(&(objectClass=zimbraServer)(!(zimbraServiceEnabled=proxy)))")
	if len(not) != 1 || not[0].GetAttributeValue("cn") != "other.example.com" {
		t.Errorf("NOT filter returned %v", not)
	}

	pres := search(t, c, "cn=zimbra", ldap.ScopeWholeSubtree, "(|(zimbraMtaMyNetworks=*)(cn=other*))")
	if len(pres) != 2 {
		t.Errorf("presence/substring OR returned %d entries, want 2", len(pres))
	}

	only := search(t, c, "cn=config,cn=zimbra", ldap.ScopeBaseObject, "(objectClass=*)", "cn")
	if len(only[0].Attributes) != 1 {
		t.Errorf("attribute selection returned %d attributes, want 1", len(only[0].Attributes))
	}

	if _, err := c.Search(ldap.NewSearchRequest("cn=missing,cn=zimbra", ldap.ScopeBaseObject, ldap.NeverDerefAliases,
		0, 0, false, "(objectClass=*)", nil, nil)); !ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) {
		t.Errorf("missing base: err = %v, want noSuchObject", err)
	}
}

func TestDirectoryModify(t *testing.T) {
	d := ldapfake.NewDirectory()
	if err := d.ApplyLDIF(strings.NewReader(seedLDIF)); err != nil {
		t.Fatalf("ApplyLDIF: %v", err)
	}

	c := dial(t, d)

	req := ldap.NewModifyRequest("cn=mail.example.com,cn=servers,cn=zimbra", nil)
	req.Replace("zimbraServiceEnabled", []string{"mta"})
	req.Add("zimbraNew", []string{"v"})

	if err := c.Modify(req); err != nil {
		t.Fatalf("modify: %v", err)
	}

	e := d.Get("CN=mail.example.com, cn=servers, cn=zimbra")
	if e == nil {
		t.Fatal("entry vanished")
	}

	if got := e.Values("zimbraServiceEnabled"); len(got) != 1 || got[0] != "mta" {
		t.Errorf("replace: %v", got)
	}

	if got := e.Values("zimbraNew"); len(got) != 1 {
		t.Errorf("add: %v", got)
	}

	if err := d.ApplyLDIF(strings.NewReader("dn: cn=mail.example.com,cn=servers,cn=zimbra\nchangetype: modify\ndelete: zimbraNew\n-\nadd: zimbraServiceEnabled\nzimbraServiceEnabled: sasl\n")); err != nil {
		t.Fatalf("ldif modify: %v", err)
	}

	e = d.Get("cn=mail.example.com,cn=servers,cn=zimbra")
	if got := e.Values("zimbraNew"); got != nil {
		t.Errorf("delete: %v", got)
	}

	if got := e.Values("zimbraServiceEnabled"); len(got) != 2 {
		t.Errorf("ldif add: %v", got)
	}

	if err := d.ApplyLDIF(strings.NewReader("dn: cn=gone,cn=zimbra\nchangetype: delete\n")); err == nil {
		t.Error("deleting a missing entry must fail")
	}
}
