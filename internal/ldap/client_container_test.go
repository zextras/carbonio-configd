// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"fmt"
	"os"
	"testing"

	"github.com/go-ldap/ldap/v3"

	"github.com/zextras/carbonio-configd/test"
)

var containerURL string

const (
	testServerHostname = "mail.test.local"
	testServerID       = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	testDomainName     = "test.local"
	testBindDN         = "uid=zimbra,cn=admins,cn=zimbra"
	testBindPassword   = "password"
)

func TestMain(m *testing.M) {
	if !test.ContainerRuntimeAvailable() {
		os.Exit(m.Run())
	}

	lc, err := test.StartLdapContainer()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start LDAP container: %v\n", err)
		os.Exit(1)
	}

	containerURL = lc.URL()

	if err := seedTestData(containerURL); err != nil {
		fmt.Fprintf(os.Stderr, "failed to seed test data: %v\n", err)
		lc.Stop()
		os.Exit(1)
	}

	code := m.Run()

	lc.Stop()
	os.Exit(code)
}

func seedTestData(url string) error {
	conn, err := ldap.DialURL(url)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	if err := conn.Bind(testBindDN, testBindPassword); err != nil {
		return fmt.Errorf("bind: %w", err)
	}

	serverDN := fmt.Sprintf("cn=%s,cn=servers,cn=zimbra", testServerHostname)
	addServer := ldap.NewAddRequest(serverDN, nil)
	addServer.Attribute("objectClass", []string{"zimbraServer"})
	addServer.Attribute("cn", []string{testServerHostname})
	addServer.Attribute("zimbraId", []string{testServerID})
	addServer.Attribute("zimbraServiceHostname", []string{testServerHostname})
	addServer.Attribute("zimbraServiceEnabled", []string{"ldap", "proxy", "mailbox"})
	addServer.Attribute("zimbraReverseProxyLookupTarget", []string{"TRUE"})
	addServer.Attribute("zimbraMailMode", []string{"https"})
	addServer.Attribute("zimbraMailSSLPort", []string{"443"})

	if err := conn.Add(addServer); err != nil {
		return fmt.Errorf("add server: %w", err)
	}

	domainDN := fmt.Sprintf("dc=%s,cn=zimbra", testDomainName)
	addDomain := ldap.NewAddRequest(domainDN, nil)
	addDomain.Attribute("objectClass", []string{"zimbraDomain", "dcObject", "organization"})
	addDomain.Attribute("dc", []string{testDomainName})
	addDomain.Attribute("o", []string{testDomainName})
	addDomain.Attribute("zimbraDomainName", []string{testDomainName})
	addDomain.Attribute("zimbraDomainType", []string{"local"})
	addDomain.Attribute("zimbraId", []string{"d1e2f3a4-b5c6-7890-abcd-ef0987654321"})

	if err := conn.Add(addDomain); err != nil {
		return fmt.Errorf("add domain: %w", err)
	}

	return nil
}



func newTestClient(t *testing.T) *Client {
	t.Helper()

	if containerURL == "" {
		t.Skip("no container runtime available")
	}

	client, err := NewClient(&ClientConfig{
		URL:      containerURL,
		BindDN:   "uid=zimbra,cn=admins,cn=zimbra",
		Password: "password",
		StartTLS: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	return client
}

func TestContainer_Search(t *testing.T) {
	client := newTestClient(t)

	result, err := client.Search("cn=config,cn=zimbra", "(objectClass=*)", []string{"*"}, ldap.ScopeBaseObject)
	if err != nil {
		t.Fatalf("Search ScopeBaseObject: %v", err)
	}
	if len(result.Entries) == 0 {
		t.Fatal("Search ScopeBaseObject returned no entries")
	}

	result, err = client.Search("cn=servers,cn=zimbra", "(objectClass=zimbraServer)", []string{"cn"}, ldap.ScopeSingleLevel)
	if err != nil {
		t.Fatalf("Search ScopeSingleLevel: %v", err)
	}
	if len(result.Entries) == 0 {
		t.Fatal("Search ScopeSingleLevel for zimbraServer returned no entries")
	}
}

func TestContainer_GetEntry(t *testing.T) {
	client := newTestClient(t)

	entry, err := client.GetEntry("cn=config,cn=zimbra", []string{"*"})
	if err != nil {
		t.Fatalf("GetEntry cn=config,cn=zimbra: %v", err)
	}
	if entry == nil {
		t.Fatal("GetEntry returned nil")
	}

	_, err = client.GetEntry("cn=nonexistent,cn=zimbra", []string{"*"})
	if err == nil {
		t.Fatal("GetEntry nonexistent: expected error, got nil")
	}
}

func TestContainer_GetGlobalConfig(t *testing.T) {
	client := newTestClient(t)

	config, err := client.GetGlobalConfig()
	if err != nil {
		t.Fatalf("GetGlobalConfig: %v", err)
	}
	if len(config) == 0 {
		t.Fatal("GetGlobalConfig returned empty map")
	}
}

func TestContainer_GetServerConfig(t *testing.T) {
	client := newTestClient(t)

	servers, err := client.GetAllServers()
	if err != nil {
		t.Fatalf("GetAllServers: %v", err)
	}
	if len(servers) == 0 {
		t.Fatal("no servers in container")
	}

	config, err := client.GetServerConfig(servers[0])
	if err != nil {
		t.Fatalf("GetServerConfig(%s): %v", servers[0], err)
	}
	if len(config) == 0 {
		t.Fatalf("GetServerConfig(%s) returned empty map", servers[0])
	}
}

func TestContainer_GetAllServers(t *testing.T) {
	client := newTestClient(t)

	servers, err := client.GetAllServers()
	if err != nil {
		t.Fatalf("GetAllServers: %v", err)
	}
	if len(servers) < 1 {
		t.Fatal("GetAllServers returned 0 servers, want >=1")
	}
}

func TestContainer_GetAllServersWithAttributes(t *testing.T) {
	client := newTestClient(t)

	servers, err := client.GetAllServersWithAttributes()
	if err != nil {
		t.Fatalf("GetAllServersWithAttributes: %v", err)
	}
	if len(servers) == 0 {
		t.Fatal("GetAllServersWithAttributes returned empty map")
	}
	for hostname, attrs := range servers {
		if len(attrs) == 0 {
			t.Fatalf("server %s has empty attribute map", hostname)
		}
	}
}

func TestContainer_GetAllDomains(t *testing.T) {
	client := newTestClient(t)

	domains, err := client.GetAllDomains()
	if err != nil {
		t.Fatalf("GetAllDomains: %v", err)
	}
	if len(domains) < 1 {
		t.Fatal("GetAllDomains returned 0 domains, want >=1")
	}
}

func TestContainer_GetDomain(t *testing.T) {
	client := newTestClient(t)

	domains, err := client.GetAllDomains()
	if err != nil {
		t.Fatalf("GetAllDomains: %v", err)
	}
	if len(domains) == 0 {
		t.Fatal("no domains in container")
	}

	config, err := client.GetDomain(domains[0])
	if err != nil {
		t.Fatalf("GetDomain(%s): %v", domains[0], err)
	}
	if len(config) == 0 {
		t.Fatalf("GetDomain(%s) returned empty map", domains[0])
	}

	_, err = client.GetDomain("nonexistent.invalid.tld")
	if err == nil {
		t.Fatal("GetDomain nonexistent: expected error, got nil")
	}
}

func TestContainer_GetAllDomainsWithAttributes(t *testing.T) {
	client := newTestClient(t)

	domains, err := client.GetAllDomainsWithAttributes()
	if err != nil {
		t.Fatalf("GetAllDomainsWithAttributes: %v", err)
	}
	if len(domains) == 0 {
		t.Fatal("GetAllDomainsWithAttributes returned empty map")
	}
}

func TestContainer_GetEnabledServices(t *testing.T) {
	client := newTestClient(t)

	servers, err := client.GetAllServers()
	if err != nil {
		t.Fatalf("GetAllServers: %v", err)
	}
	if len(servers) == 0 {
		t.Fatal("no servers in container")
	}

	services, err := client.GetEnabledServices(servers[0])
	if err != nil {
		t.Fatalf("GetEnabledServices(%s): %v", servers[0], err)
	}
	if len(services) == 0 {
		t.Fatalf("GetEnabledServices(%s) returned no services", servers[0])
	}
}

func TestContainer_ModifyAttribute(t *testing.T) {
	client := newTestClient(t)

	const testDN = "cn=config,cn=zimbra"
	const testAttr = "description"
	const testValue = "configd-test-marker"

	config, err := client.GetGlobalConfig()
	if err != nil {
		t.Fatalf("GetGlobalConfig: %v", err)
	}
	original := config[testAttr]

	err = client.ModifyAttribute(testDN, testAttr, testValue)
	if err != nil {
		t.Fatalf("ModifyAttribute: %v", err)
	}

	config, err = client.GetGlobalConfig()
	if err != nil {
		t.Fatalf("GetGlobalConfig after modify: %v", err)
	}
	if got := config[testAttr]; got != testValue {
		t.Fatalf("%s = %q, want %q", testAttr, got, testValue)
	}

	if original != "" {
		if err := client.ModifyAttribute(testDN, testAttr, original); err != nil {
			t.Fatalf("ModifyAttribute restore: %v", err)
		}
	}
}

func TestContainer_ConnectionPoolReuse(t *testing.T) {
	client := newTestClient(t)

	for i := range 5 {
		_, err := client.GetGlobalConfig()
		if err != nil {
			t.Fatalf("GetGlobalConfig iteration %d: %v", i, err)
		}
	}
}
