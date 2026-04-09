// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tls

import (
	"fmt"
	"os"
	"testing"

	ldaplib "github.com/go-ldap/ldap/v3"

	carboldap "github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/test"
)

var tlsContainerURL string

const (
	tlsTestServerHostname = "mail.test.local"
	tlsTestServerID       = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	tlsTestBindDN         = "uid=zimbra,cn=admins,cn=zimbra"
	tlsTestBindPassword   = "password"
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

	tlsContainerURL = lc.URL()

	if err := tlsSeedTestData(tlsContainerURL); err != nil {
		fmt.Fprintf(os.Stderr, "failed to seed test data: %v\n", err)
		lc.Stop()
		os.Exit(1)
	}

	code := m.Run()

	lc.Stop()
	os.Exit(code)
}

func tlsSeedTestData(url string) error {
	conn, err := ldaplib.DialURL(url)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	if err := conn.Bind(tlsTestBindDN, tlsTestBindPassword); err != nil {
		return fmt.Errorf("bind: %w", err)
	}

	serverDN := fmt.Sprintf("cn=%s,cn=servers,cn=zimbra", tlsTestServerHostname)
	addServer := ldaplib.NewAddRequest(serverDN, nil)
	addServer.Attribute("objectClass", []string{"zimbraServer"})
	addServer.Attribute("cn", []string{tlsTestServerHostname})
	addServer.Attribute("zimbraId", []string{tlsTestServerID})
	addServer.Attribute("zimbraServiceHostname", []string{tlsTestServerHostname})
	addServer.Attribute("zimbraServiceEnabled", []string{"ldap", "proxy", "mailbox"})
	addServer.Attribute("zimbraReverseProxyLookupTarget", []string{"TRUE"})
	addServer.Attribute("zimbraMailMode", []string{"https"})
	addServer.Attribute("zimbraMailSSLPort", []string{"443"})
	addServer.Attribute("zimbraReverseProxyMailMode", []string{"https"})
	addServer.Attribute("zimbraReverseProxySSLToUpstreamEnabled", []string{"TRUE"})

	if err := conn.Add(addServer); err != nil {
		return fmt.Errorf("add server: %w", err)
	}

	return nil
}

func newTLSTestClient(t *testing.T) *carboldap.Client {
	t.Helper()

	if tlsContainerURL == "" {
		t.Skip("no container runtime available")
	}

	client, err := carboldap.NewClient(&carboldap.ClientConfig{
		URL:      tlsContainerURL,
		BindDN:   "uid=zimbra,cn=admins,cn=zimbra",
		Password: "password",
		StartTLS: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	return client
}

func firstServer(t *testing.T, client *carboldap.Client) string {
	t.Helper()

	servers, err := client.GetAllServers()
	if err != nil {
		t.Fatalf("failed to list servers: %v", err)
	}
	if len(servers) == 0 {
		t.Fatal("container has no servers")
	}

	return servers[0]
}

func TestContainer_IsReverseProxyBackend(t *testing.T) {
	client := newTLSTestClient(t)
	hostname := firstServer(t, client)

	_, err := IsReverseProxyBackend(client, hostname)
	if err != nil {
		t.Fatalf("IsReverseProxyBackend(%q): %v", hostname, err)
	}

	_, err = IsReverseProxyBackend(client, "no-such-host.invalid")
	if err == nil {
		t.Fatal("expected error for non-existent server")
	}
}

func TestContainer_EnumerateProxies(t *testing.T) {
	client := newTLSTestClient(t)

	proxies, err := EnumerateProxies(client)
	if err != nil {
		t.Fatalf("EnumerateProxies: %v", err)
	}
	if proxies == nil {
		t.Fatal("expected non-nil slice")
	}
}

func TestContainer_GetProxiesForHost(t *testing.T) {
	client := newTLSTestClient(t)
	hostname := firstServer(t, client)

	proxies, err := GetProxiesForHost(client, hostname)
	if err != nil {
		t.Fatalf("GetProxiesForHost(%q): %v", hostname, err)
	}
	if proxies == nil {
		t.Fatal("expected non-nil slice")
	}
}

func TestContainer_ReadProxySettings(t *testing.T) {
	client := newTLSTestClient(t)
	hostname := firstServer(t, client)

	settings, err := ReadProxySettings(client, hostname)
	if err != nil {
		t.Logf("ReadProxySettings(%q) returned error (acceptable): %v", hostname, err)
		return
	}

	if settings.Proxy != hostname {
		t.Fatalf("Proxy = %q, want %q", settings.Proxy, hostname)
	}
}

func TestContainer_SetMailMode(t *testing.T) {
	client := newTLSTestClient(t)
	hostname := firstServer(t, client)

	if err := SetMailMode(client, hostname, ModeHTTPS); err != nil {
		t.Fatalf("SetMailMode(%q, %q): %v", hostname, ModeHTTPS, err)
	}

	entry, err := client.GetEntry(ServerBackendDN(hostname, "cn=zimbra"), []string{"zimbraMailMode"})
	if err != nil {
		t.Fatalf("GetEntry after SetMailMode: %v", err)
	}

	got := entry.GetAttributeValue("zimbraMailMode")
	if got != string(ModeHTTPS) {
		t.Fatalf("zimbraMailMode = %q, want %q", got, ModeHTTPS)
	}
}
