// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"context"
	"testing"
)

// benchExecutor returns an executor over canned LDAP data so the benchmarks
// measure command processing, not network dial failures.
func benchExecutor() *CommandExecutor {
	return &CommandExecutor{ldapClient: &stubProvisioningLDAP{
		serverConfig: map[string]string{"zimbraServiceHostname": "mail.example.com", "zimbraServiceEnabled": "proxy\nmta"},
		globalConfig: map[string]string{"zimbraDefaultDomainName": "example.com"},
		allServers: map[string]map[string]string{
			"mail1": {"cn": "mail1", "zimbraServiceHostname": "mail1.example.com"},
			"mail2": {"cn": "mail2", "zimbraServiceHostname": "mail2.example.com"},
		},
	}}
}

// BenchmarkGetServer benchmarks the ServerAttrs function.
func BenchmarkGetServer(b *testing.B) {
	executor := benchExecutor()

	b.ResetTimer()

	for b.Loop() {
		if _, err := executor.ServerAttrs(context.Background(), "localhost"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGetGlobal benchmarks the GlobalAttrs function.
func BenchmarkGetGlobal(b *testing.B) {
	executor := benchExecutor()

	b.ResetTimer()

	for b.Loop() {
		if _, err := executor.GlobalAttrs(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGetAllServersWithAttribute benchmarks the garpb query.
func BenchmarkGetAllServersWithAttribute(b *testing.B) {
	executor := benchExecutor()

	b.ResetTimer()

	for b.Loop() {
		if _, err := executor.garpb(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}
