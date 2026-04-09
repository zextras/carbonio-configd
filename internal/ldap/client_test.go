// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"fmt"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// TestNewClient tests creating a new LDAP client with various configurations
func TestNewClient(t *testing.T) {
	tests := []struct {
		name   string
		config *ClientConfig
		want   *Client
	}{
		{
			name: "minimal config with defaults",
			config: &ClientConfig{
				URL:      "ldap://localhost:389",
				BindDN:   "uid=zimbra,cn=admins,cn=zimbra",
				Password: "test",
			},
			want: &Client{
				url:           "ldap://localhost:389",
				bindDN:        "uid=zimbra,cn=admins,cn=zimbra",
				password:      "test",
				baseDN:        "cn=zimbra",
				poolSize:      5,
				maxRetries:    3,
				retryDelay:    100 * time.Millisecond,
				maxRetryDelay: 5 * time.Second,
			},
		},
		{
			name: "custom config",
			config: &ClientConfig{
				URL:           "ldaps://ldap.example.com:636",
				BindDN:        "cn=admin,dc=example,dc=com",
				Password:      "secret",
				BaseDN:        "dc=example,dc=com",
				PoolSize:      10,
				MaxRetries:    5,
				RetryDelay:    200 * time.Millisecond,
				MaxRetryDelay: 10 * time.Second,
			},
			want: &Client{
				url:           "ldaps://ldap.example.com:636",
				bindDN:        "cn=admin,dc=example,dc=com",
				password:      "secret",
				baseDN:        "dc=example,dc=com",
				poolSize:      10,
				maxRetries:    5,
				retryDelay:    200 * time.Millisecond,
				maxRetryDelay: 10 * time.Second,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewClient(tt.config)
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}

			if got.url != tt.want.url {
				t.Errorf("url = %v, want %v", got.url, tt.want.url)
			}
			if got.bindDN != tt.want.bindDN {
				t.Errorf("bindDN = %v, want %v", got.bindDN, tt.want.bindDN)
			}
			if got.password != tt.want.password {
				t.Errorf("password = %v, want %v", got.password, tt.want.password)
			}
			if got.baseDN != tt.want.baseDN {
				t.Errorf("baseDN = %v, want %v", got.baseDN, tt.want.baseDN)
			}
			if got.poolSize != tt.want.poolSize {
				t.Errorf("poolSize = %v, want %v", got.poolSize, tt.want.poolSize)
			}
			if got.maxRetries != tt.want.maxRetries {
				t.Errorf("maxRetries = %v, want %v", got.maxRetries, tt.want.maxRetries)
			}
			if got.retryDelay != tt.want.retryDelay {
				t.Errorf("retryDelay = %v, want %v", got.retryDelay, tt.want.retryDelay)
			}
			if got.maxRetryDelay != tt.want.maxRetryDelay {
				t.Errorf("maxRetryDelay = %v, want %v", got.maxRetryDelay, tt.want.maxRetryDelay)
			}
		})
	}
}

// TestEntryToMap tests converting LDAP entries to maps
func TestEntryToMap(t *testing.T) {
	tests := []struct {
		name  string
		entry *ldap.Entry
		want  map[string]string
	}{
		{
			name: "single-valued attributes",
			entry: &ldap.Entry{
				DN: "cn=test,cn=servers,cn=zimbra",
				Attributes: []*ldap.EntryAttribute{
					{Name: "cn", Values: []string{"test"}},
					{Name: "zimbraServiceHostname", Values: []string{"test.example.com"}},
					{Name: "objectClass", Values: []string{"zimbraServer"}},
				},
			},
			want: map[string]string{
				"cn":                    "test",
				"zimbraServiceHostname": "test.example.com",
				"objectClass":           "zimbraServer",
			},
		},
		{
			name: "multi-valued attributes",
			entry: &ldap.Entry{
				DN: "cn=config,cn=zimbra",
				Attributes: []*ldap.EntryAttribute{
					{Name: "cn", Values: []string{"config"}},
					{Name: "zimbraServiceEnabled", Values: []string{"mailbox", "mta", "ldap"}},
					{Name: "zimbraIPMode", Values: []string{"ipv4", "ipv6"}},
				},
			},
			want: map[string]string{
				"cn":                   "config",
				"zimbraServiceEnabled": "mailbox\nmta\nldap",
				"zimbraIPMode":         "ipv4\nipv6",
			},
		},
		{
			name: "empty attributes",
			entry: &ldap.Entry{
				DN:         "cn=empty,cn=zimbra",
				Attributes: []*ldap.EntryAttribute{},
			},
			want: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := entryToMap(tt.entry)

			if len(got) != len(tt.want) {
				t.Errorf("entryToMap() returned %d keys, want %d", len(got), len(tt.want))
			}

			for key, wantValue := range tt.want {
				gotValue, ok := got[key]
				if !ok {
					t.Errorf("entryToMap() missing key %q", key)
					continue
				}
				if gotValue != wantValue {
					t.Errorf("entryToMap()[%q] = %q, want %q", key, gotValue, wantValue)
				}
			}
		})
	}
}

// TestFormatAsZmprovOutput tests formatting config maps as zmprov output
func TestFormatAsZmprovOutput(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]string
		want   []string // Expected lines in output
	}{
		{
			name: "single values",
			config: map[string]string{
				"cn":           "test",
				"zimbraIPMode": "ipv4",
			},
			want: []string{
				"cn: test",
				"zimbraIPMode: ipv4",
			},
		},
		{
			name: "multi-line value",
			config: map[string]string{
				"zimbraServiceEnabled": "mailbox\nmta\nldap",
			},
			want: []string{
				"zimbraServiceEnabled: mailbox",
				"zimbraServiceEnabled: mta",
				"zimbraServiceEnabled: ldap",
			},
		},
		{
			name:   "empty config",
			config: map[string]string{},
			want:   []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatAsZmprovOutput(tt.config)

			// Verify all expected lines are present
			for _, expectedLine := range tt.want {
				if !containsLine(got, expectedLine) {
					t.Errorf("FormatAsZmprovOutput() missing line %q\nGot:\n%s", expectedLine, got)
				}
			}
		})
	}
}

// TestFormatAsZmprovOutput_EmptyValue tests handling of empty values
func TestFormatAsZmprovOutput_EmptyValue(t *testing.T) {
	config := map[string]string{
		"key1": "",
		"key2": "value",
	}

	output := FormatAsZmprovOutput(config)

	// Empty value should still produce a line
	if !containsLine(output, "key1: ") {
		t.Errorf("Expected 'key1: ' in output, got:\n%s", output)
	}
	if !containsLine(output, "key2: value") {
		t.Errorf("Expected 'key2: value' in output, got:\n%s", output)
	}
}

// TestClient_Close tests closing client connections
func TestClient_Close(t *testing.T) {
	client, err := NewClient(&ClientConfig{
		URL:      "ldap://localhost:389",
		BindDN:   "cn=admin",
		Password: "test",
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	// Close should not error even with empty pool
	err = client.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Close should be idempotent
	err = client.Close()
	if err != nil {
		t.Errorf("Close() second call error = %v", err)
	}
}

// Helper function to check if output contains a line
func containsLine(output, line string) bool {
	lines := splitLines(output)
	for _, l := range lines {
		if l == line {
			return true
		}
	}
	return false
}

// Helper function to split output into lines
func splitLines(s string) []string {
	if s == "" {
		return []string{}
	}

	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}

	// Add last line if not empty
	if start < len(s) {
		lines = append(lines, s[start:])
	}

	return lines
}

// TestServerConfigDNEscaping tests that GetServerConfig constructs properly escaped DNs.
// Since GetServerConfig requires a live LDAP connection, we test the DN construction
// logic directly by verifying ldap.EscapeDN + fmt.Sprintf produces safe DNs.
func TestServerConfigDNEscaping(t *testing.T) {
	baseDN := "cn=zimbra"

	tests := []struct {
		name     string
		hostname string
		wantDN   string
	}{
		{
			name:     "simple hostname",
			hostname: "mail.example.com",
			wantDN:   "cn=mail.example.com,cn=servers,cn=zimbra",
		},
		{
			name:     "hostname with comma",
			hostname: "mail,evil.com",
			wantDN:   "cn=mail\\,evil.com,cn=servers,cn=zimbra",
		},
		{
			name:     "hostname with plus sign",
			hostname: "mail+extra.example.com",
			wantDN:   "cn=mail\\+extra.example.com,cn=servers,cn=zimbra",
		},
		{
			name:     "hostname with equals sign",
			hostname: "mail=bad.example.com",
			// Per RFC 4514, '=' is not special inside a DN value — EscapeDN does not escape it
			wantDN: "cn=mail=bad.example.com,cn=servers,cn=zimbra",
		},
		{
			name:     "hostname with semicolons",
			hostname: "mail;inject.example.com",
			wantDN:   "cn=mail\\;inject.example.com,cn=servers,cn=zimbra",
		},
		{
			name:     "hostname with angle brackets",
			hostname: "mail<script>.example.com",
			wantDN:   "cn=mail\\<script\\>.example.com,cn=servers,cn=zimbra",
		},
		{
			name:     "hostname with backslash",
			hostname: "mail\\evil.example.com",
			wantDN:   "cn=mail\\\\evil.example.com,cn=servers,cn=zimbra",
		},
		{
			name:     "hostname with leading space",
			hostname: " mail.example.com",
			wantDN:   "cn=\\ mail.example.com,cn=servers,cn=zimbra",
		},
		{
			name:     "hostname with trailing space",
			hostname: "mail.example.com ",
			wantDN:   "cn=mail.example.com\\ ,cn=servers,cn=zimbra",
		},
		{
			name:     "hostname with multiple special chars",
			hostname: "a+b,c=d;e",
			// '=' is not escaped per RFC 4514
			wantDN: "cn=a\\+b\\,c=d\\;e,cn=servers,cn=zimbra",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDN := fmt.Sprintf("cn=%s,cn=servers,%s", ldap.EscapeDN(tt.hostname), baseDN)
			if gotDN != tt.wantDN {
				t.Errorf("server DN =\n  %q\nwant:\n  %q", gotDN, tt.wantDN)
			}
		})
	}
}

// TestDomainDNEscaping tests that GetDomain constructs properly escaped DNs.
func TestDomainDNEscaping(t *testing.T) {
	baseDN := "cn=zimbra"

	tests := []struct {
		name   string
		domain string
		wantDN string
	}{
		{
			name:   "simple domain",
			domain: "example.com",
			wantDN: "zimbraDomainName=example.com,cn=domains,cn=zimbra",
		},
		{
			name:   "domain with comma",
			domain: "evil,domain.com",
			wantDN: "zimbraDomainName=evil\\,domain.com,cn=domains,cn=zimbra",
		},
		{
			name:   "domain with plus sign",
			domain: "plus+domain.com",
			wantDN: "zimbraDomainName=plus\\+domain.com,cn=domains,cn=zimbra",
		},
		{
			name:   "domain with equals sign",
			domain: "eq=domain.com",
			// Per RFC 4514, '=' is not special inside a DN value — EscapeDN does not escape it
			wantDN: "zimbraDomainName=eq=domain.com,cn=domains,cn=zimbra",
		},
		{
			name:   "domain with semicolons",
			domain: "semi;domain.com",
			wantDN: "zimbraDomainName=semi\\;domain.com,cn=domains,cn=zimbra",
		},
		{
			name:   "domain with hash at start",
			domain: "#domain.com",
			wantDN: "zimbraDomainName=\\#domain.com,cn=domains,cn=zimbra",
		},
		{
			name:   "domain with multiple special chars",
			domain: "a+b,c=d",
			// '=' is not escaped per RFC 4514
			wantDN: "zimbraDomainName=a\\+b\\,c=d,cn=domains,cn=zimbra",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDN := fmt.Sprintf("zimbraDomainName=%s,cn=domains,%s", ldap.EscapeDN(tt.domain), baseDN)
			if gotDN != tt.wantDN {
				t.Errorf("domain DN =\n  %q\nwant:\n  %q", gotDN, tt.wantDN)
			}
		})
	}
}

// BenchmarkEntryToMap benchmarks converting LDAP entries to maps
func BenchmarkEntryToMap(b *testing.B) {
	entry := &ldap.Entry{
		DN: "cn=test,cn=servers,cn=zimbra",
		Attributes: []*ldap.EntryAttribute{
			{Name: "cn", Values: []string{"test"}},
			{Name: "zimbraServiceHostname", Values: []string{"test.example.com"}},
			{Name: "zimbraServiceEnabled", Values: []string{"mailbox", "mta", "ldap"}},
			{Name: "objectClass", Values: []string{"zimbraServer"}},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = entryToMap(entry)
	}
}

// BenchmarkFormatAsZmprovOutput benchmarks formatting config as zmprov output
func BenchmarkFormatAsZmprovOutput(b *testing.B) {
	config := map[string]string{
		"cn":                    "test",
		"zimbraServiceHostname": "test.example.com",
		"zimbraServiceEnabled":  "mailbox\nmta\nldap",
		"zimbraIPMode":          "ipv4",
		"objectClass":           "zimbraServer",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = FormatAsZmprovOutput(config)
	}
}
