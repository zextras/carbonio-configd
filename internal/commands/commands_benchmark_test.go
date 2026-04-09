// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"context"
	"crypto/tls"
	"testing"

	"github.com/zextras/carbonio-configd/internal/ldap"
)

// BenchmarkGetServer benchmarks the native LDAP getserver function.
func BenchmarkGetServer(b *testing.B) {
	// Setup: Create a mock LDAP client for benchmarking
	// In real usage, this would connect to actual LDAP server
	client, err := ldap.NewClient(&ldap.ClientConfig{
		URL:      "ldap://localhost:389",
		BindDN:   "uid=zimbra,cn=admins,cn=zimbra",
		Password: "test",
		BaseDN:   "cn=zimbra",
		PoolSize: 5,
		// #nosec G402 - InsecureSkipVerify is intentional for benchmarks
		TLSConfig: &tls.Config{InsecureSkipVerify: true},
	})
	if err != nil {
		b.Skipf("Cannot create LDAP client for benchmark: %v", err)
	}

	executor := NewCommandExecutor(client)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = executor.getserver(context.Background(), "localhost")
	}
}

// BenchmarkGetGlobal benchmarks the native LDAP getglobal function.
func BenchmarkGetGlobal(b *testing.B) {
	client, err := ldap.NewClient(&ldap.ClientConfig{
		URL:      "ldap://localhost:389",
		BindDN:   "uid=zimbra,cn=admins,cn=zimbra",
		Password: "test",
		BaseDN:   "cn=zimbra",
		PoolSize: 5,
		// #nosec G402 - InsecureSkipVerify is intentional for benchmarks
		TLSConfig: &tls.Config{InsecureSkipVerify: true},
	})
	if err != nil {
		b.Skipf("Cannot create LDAP client for benchmark: %v", err)
	}

	executor := NewCommandExecutor(client)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = executor.getglobal(context.Background())
	}
}

// BenchmarkGetLocal benchmarks the native XML parser for localconfig.
func BenchmarkGetLocal(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = getlocal(context.Background())
	}
}

// BenchmarkGetAllServersWithAttribute benchmarks the native LDAP query
// for servers with specific attributes (e.g., garpb).
func BenchmarkGetAllServersWithAttribute(b *testing.B) {
	client, err := ldap.NewClient(&ldap.ClientConfig{
		URL:      "ldap://localhost:389",
		BindDN:   "uid=zimbra,cn=admins,cn=zimbra",
		Password: "test",
		BaseDN:   "cn=zimbra",
		PoolSize: 5,
		// #nosec G402 - InsecureSkipVerify is intentional for benchmarks
		TLSConfig: &tls.Config{InsecureSkipVerify: true},
	})
	if err != nil {
		b.Skipf("Cannot create LDAP client for benchmark: %v", err)
	}

	executor := NewCommandExecutor(client)

	b.Run("garpb", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = executor.garpb(context.Background())
		}
	})
}
