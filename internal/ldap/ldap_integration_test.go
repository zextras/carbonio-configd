// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build integration

// Integration tests for native LDAP client against real Carbonio LDAP server.
// Run with: go test -tags=integration ./internal/ldap/

package ldap

import (
	"context"
	"github.com/zextras/carbonio-configd/internal/localconfig"
	"testing"
)

func TestNativeClient_RealLDAP_GlobalConfig(t *testing.T) {
	// Load connection info from localconfig
	config, err := localconfig.LoadLocalConfig()
	if err != nil {
		t.Fatalf("Failed to load localconfig: %v", err)
	}

	ldapURL := config["ldap_url"]
	bindDN := config["zimbra_ldap_userdn"]
	password := config["zimbra_ldap_password"]

	if ldapURL == "" || bindDN == "" || password == "" {
		t.Skip("LDAP connection info not available in localconfig")
	}

	// Create native LDAP client
	client, err := NewClient(&ClientConfig{
		URL:      ldapURL,
		BindDN:   bindDN,
		Password: password,
		BaseDN:   "cn=zimbra",
		PoolSize: 5,
	})
	if err != nil {
		t.Fatalf("Failed to create native LDAP client: %v", err)
	}
	defer client.Close()

	// Test GetGlobalConfig
	t.Run("GetGlobalConfig", func(t *testing.T) {
		globalConfig, err := client.GetGlobalConfig()
		if err != nil {
			t.Fatalf("GetGlobalConfig failed: %v", err)
		}

		if len(globalConfig) == 0 {
			t.Error("Expected global config to have attributes, got 0")
		}

		// Check for some expected attributes
		expectedAttrs := []string{
			"zimbraAccountClientAttr",
			"zimbraComponentAvailableMemory",
			"zimbraHttpDebugHandlerEnabled",
		}

		for _, attr := range expectedAttrs {
			if _, ok := globalConfig[attr]; !ok {
				t.Logf("Warning: Expected attribute %s not found in global config", attr)
			}
		}

		t.Logf("✓ GetGlobalConfig returned %d attributes", len(globalConfig))
	})
}

func TestNativeClient_RealLDAP_ServerConfig(t *testing.T) {
	// Load connection info
	config, err := localconfig.LoadLocalConfig()
	if err != nil {
		t.Fatalf("Failed to load localconfig: %v", err)
	}

	ldapURL := config["ldap_url"]
	bindDN := config["zimbra_ldap_userdn"]
	password := config["zimbra_ldap_password"]
	hostname := config["zimbra_server_hostname"]

	if ldapURL == "" || bindDN == "" || password == "" || hostname == "" {
		t.Skip("LDAP connection info not available in localconfig")
	}

	// Create client
	client, err := NewClient(&ClientConfig{
		URL:      ldapURL,
		BindDN:   bindDN,
		Password: password,
		BaseDN:   "cn=zimbra",
		PoolSize: 5,
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Test GetServerConfig
	t.Run("GetServerConfig", func(t *testing.T) {
		serverConfig, err := client.GetServerConfig(hostname)
		if err != nil {
			t.Fatalf("GetServerConfig failed: %v", err)
		}

		if len(serverConfig) == 0 {
			t.Error("Expected server config to have attributes, got 0")
		}

		// Check for some expected attributes
		expectedAttrs := []string{
			"zimbraServiceHostname",
			"zimbraServiceEnabled",
			"zimbraId",
		}

		for _, attr := range expectedAttrs {
			if _, ok := serverConfig[attr]; !ok {
				t.Errorf("Expected attribute %s not found in server config", attr)
			}
		}

		t.Logf("✓ GetServerConfig returned %d attributes for %s", len(serverConfig), hostname)
		t.Logf("  zimbraServiceEnabled: %s", serverConfig["zimbraServiceEnabled"])
	})
}

func TestNativeClient_RealLDAP_AllServers(t *testing.T) {
	// Load connection info
	config, err := localconfig.LoadLocalConfig()
	if err != nil {
		t.Fatalf("Failed to load localconfig: %v", err)
	}

	ldapURL := config["ldap_url"]
	bindDN := config["zimbra_ldap_userdn"]
	password := config["zimbra_ldap_password"]

	if ldapURL == "" || bindDN == "" || password == "" {
		t.Skip("LDAP connection info not available")
	}

	// Create client
	client, err := NewClient(&ClientConfig{
		URL:      ldapURL,
		BindDN:   bindDN,
		Password: password,
		BaseDN:   "cn=zimbra",
		PoolSize: 5,
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Test GetAllServers
	t.Run("GetAllServers", func(t *testing.T) {
		servers, err := client.GetAllServers()
		if err != nil {
			t.Fatalf("GetAllServers failed: %v", err)
		}

		if len(servers) == 0 {
			t.Error("Expected at least one server, got 0")
		}

		t.Logf("✓ GetAllServers returned %d server(s)", len(servers))
		for _, server := range servers {
			t.Logf("  - %s", server)
		}
	})

	// Test GetAllServersWithAttributes
	t.Run("GetAllServersWithAttributes", func(t *testing.T) {
		serversWithAttrs, err := client.GetAllServersWithAttributes()
		if err != nil {
			t.Fatalf("GetAllServersWithAttributes failed: %v", err)
		}

		if len(serversWithAttrs) == 0 {
			t.Error("Expected at least one server, got 0")
		}

		t.Logf("✓ GetAllServersWithAttributes returned %d server(s)", len(serversWithAttrs))

		for _, attrs := range serversWithAttrs {
			hostname := attrs["zimbraServiceHostname"]
			services := attrs["zimbraServiceEnabled"]
			t.Logf("  - %s: %s", hostname, services)
		}
	})
}

func TestNativeClient_RealLDAP_Domains(t *testing.T) {
	// Load connection info
	config, err := localconfig.LoadLocalConfig()
	if err != nil {
		t.Fatalf("Failed to load localconfig: %v", err)
	}

	ldapURL := config["ldap_url"]
	bindDN := config["zimbra_ldap_userdn"]
	password := config["zimbra_ldap_password"]

	if ldapURL == "" || bindDN == "" || password == "" {
		t.Skip("LDAP connection info not available")
	}

	// Create client
	client, err := NewClient(&ClientConfig{
		URL:      ldapURL,
		BindDN:   bindDN,
		Password: password,
		BaseDN:   "cn=zimbra",
		PoolSize: 5,
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Test GetAllDomains
	t.Run("GetAllDomains", func(t *testing.T) {
		domains, err := client.GetAllDomains()
		if err != nil {
			t.Fatalf("GetAllDomains failed: %v", err)
		}

		if len(domains) == 0 {
			t.Skip("No domains configured in test environment")
		}

		t.Logf("✓ GetAllDomains returned %d domain(s)", len(domains))
		for _, domain := range domains {
			t.Logf("  - %s", domain)
		}

		// Test GetDomain for the first domain
		if len(domains) > 0 {
			t.Run("GetDomain", func(t *testing.T) {
				domainAttrs, err := client.GetDomain(domains[0])
				if err != nil {
					t.Fatalf("GetDomain(%s) failed: %v", domains[0], err)
				}

				if len(domainAttrs) == 0 {
					t.Error("Expected domain attributes, got 0")
				}

				t.Logf("✓ GetDomain(%s) returned %d attributes", domains[0], len(domainAttrs))
			})
		}
	})
}

func TestLdap_QueryDomains_Integration(t *testing.T) {
	// Load connection info
	config, err := localconfig.LoadLocalConfig()
	if err != nil {
		t.Fatalf("Failed to load localconfig: %v", err)
	}

	ldapURL := config["ldap_url"]
	bindDN := config["zimbra_ldap_userdn"]
	password := config["zimbra_ldap_password"]

	if ldapURL == "" || bindDN == "" || password == "" {
		t.Skip("LDAP connection info not available")
	}

	// Create native client
	nativeClient, err := NewClient(&ClientConfig{
		URL:      ldapURL,
		BindDN:   bindDN,
		Password: password,
		BaseDN:   "cn=zimbra",
		PoolSize: 5,
	})
	if err != nil {
		t.Fatalf("Failed to create native client: %v", err)
	}
	defer nativeClient.Close()

	// Create Ldap manager with native client
	ldapMgr := NewLdap(context.Background(), nil)
	ldapMgr.SetNativeClient(context.Background(), nativeClient)

	// Test QueryDomains
	domains, err := ldapMgr.QueryDomains(context.Background())
	if err != nil {
		t.Fatalf("QueryDomains failed: %v", err)
	}

	t.Logf("✓ QueryDomains returned %d domain(s) with virtual hostnames", len(domains))
	for _, domain := range domains {
		t.Logf("  - %s -> %s (IP: %s)", domain.DomainName, domain.VirtualHostname, domain.VirtualIPAddress)
	}
}

func TestLdap_QueryServers_Integration(t *testing.T) {
	// Load connection info
	config, err := localconfig.LoadLocalConfig()
	if err != nil {
		t.Fatalf("Failed to load localconfig: %v", err)
	}

	ldapURL := config["ldap_url"]
	bindDN := config["zimbra_ldap_userdn"]
	password := config["zimbra_ldap_password"]

	if ldapURL == "" || bindDN == "" || password == "" {
		t.Skip("LDAP connection info not available")
	}

	// Create native client
	nativeClient, err := NewClient(&ClientConfig{
		URL:      ldapURL,
		BindDN:   bindDN,
		Password: password,
		BaseDN:   "cn=zimbra",
		PoolSize: 5,
	})
	if err != nil {
		t.Fatalf("Failed to create native client: %v", err)
	}
	defer nativeClient.Close()

	// Create Ldap manager
	ldapMgr := NewLdap(context.Background(), nil)
	ldapMgr.SetNativeClient(context.Background(), nativeClient)

	// Test QueryServers for different services
	services := []string{"mailbox", "proxy", "ldap", "mta"}

	for _, service := range services {
		t.Run("Service_"+service, func(t *testing.T) {
			servers, err := ldapMgr.QueryServers(context.Background(), service)
			if err != nil {
				t.Fatalf("QueryServers(%s) failed: %v", service, err)
			}

			t.Logf("✓ QueryServers(%s) returned %d server(s)", service, len(servers))
			for _, server := range servers {
				t.Logf("  - %s (ID: %s)", server.ServiceHostname, server.ServerID)
			}
		})
	}
}
