// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"errors"
	"testing"

	"github.com/go-ldap/ldap/v3"
)

func TestIsConnectionError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "server down",
			err:      ldap.NewError(ldap.LDAPResultServerDown, errors.New("server down")),
			expected: true,
		},
		{
			name:     "timeout",
			err:      ldap.NewError(ldap.LDAPResultTimeout, errors.New("timeout")),
			expected: true,
		},
		{
			name:     "unavailable",
			err:      ldap.NewError(ldap.LDAPResultUnavailable, errors.New("unavailable")),
			expected: true,
		},
		{
			name:     "connect error",
			err:      ldap.NewError(ldap.LDAPResultConnectError, errors.New("connect error")),
			expected: true,
		},
		{
			name:     "no such object - not connection error",
			err:      ldap.NewError(ldap.LDAPResultNoSuchObject, errors.New("not found")),
			expected: false,
		},
		{
			name:     "invalid credentials - not connection error",
			err:      ldap.NewError(ldap.LDAPResultInvalidCredentials, errors.New("bad creds")),
			expected: false,
		},
		{
			name:     "network error - connection reset",
			err:      errors.New("connection reset by peer"),
			expected: true,
		},
		{
			name:     "network error - broken pipe",
			err:      errors.New("broken pipe"),
			expected: true,
		},
		{
			name:     "network error - EOF",
			err:      errors.New("unexpected EOF"),
			expected: true,
		},
		{
			name:     "generic error - not connection error",
			err:      errors.New("some other error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isConnectionError(tt.err)
			if result != tt.expected {
				t.Errorf("isConnectionError() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIsLDAPErrorRetryable(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "no such object - not retryable",
			err:      ldap.NewError(ldap.LDAPResultNoSuchObject, errors.New("not found")),
			expected: false,
		},
		{
			name:     "invalid DN syntax - not retryable",
			err:      ldap.NewError(ldap.LDAPResultInvalidDNSyntax, errors.New("bad DN")),
			expected: false,
		},
		{
			name:     "invalid credentials - not retryable",
			err:      ldap.NewError(ldap.LDAPResultInvalidCredentials, errors.New("bad creds")),
			expected: false,
		},
		{
			name:     "insufficient access - not retryable",
			err:      ldap.NewError(ldap.LDAPResultInsufficientAccessRights, errors.New("no access")),
			expected: false,
		},
		{
			name:     "server down - retryable",
			err:      ldap.NewError(ldap.LDAPResultServerDown, errors.New("server down")),
			expected: true,
		},
		{
			name:     "timeout - retryable",
			err:      ldap.NewError(ldap.LDAPResultTimeout, errors.New("timeout")),
			expected: true,
		},
		{
			name:     "busy - retryable",
			err:      ldap.NewError(ldap.LDAPResultBusy, errors.New("busy")),
			expected: true,
		},
		{
			name:     "unavailable - retryable",
			err:      ldap.NewError(ldap.LDAPResultUnavailable, errors.New("unavailable")),
			expected: true,
		},
		{
			name:     "unwilling to perform - retryable",
			err:      ldap.NewError(ldap.LDAPResultUnwillingToPerform, errors.New("unwilling")),
			expected: true,
		},
		{
			name:     "network error - retryable",
			err:      errors.New("network error"),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isLDAPErrorRetryable(tt.err)
			if result != tt.expected {
				t.Errorf("isLDAPErrorRetryable() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestNewClient_Defaults(t *testing.T) {
	config := &ClientConfig{
		URL:      "ldap://localhost:389",
		BindDN:   "cn=admin",
		Password: "password",
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if client.baseDN != defaultBaseDN {
		t.Errorf("expected baseDN %s, got %s", defaultBaseDN, client.baseDN)
	}
	if client.poolSize != 5 {
		t.Errorf("expected poolSize 5, got %d", client.poolSize)
	}
	if client.maxRetries != 3 {
		t.Errorf("expected maxRetries 3, got %d", client.maxRetries)
	}
}

func TestNewClient_CustomConfig(t *testing.T) {
	config := &ClientConfig{
		URL:        "ldaps://localhost:636",
		BindDN:     "uid=zimbra,cn=admins,cn=zimbra",
		Password:   "secret",
		BaseDN:     "dc=example,dc=com",
		PoolSize:   10,
		MaxRetries: 5,
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if client.url != "ldaps://localhost:636" {
		t.Errorf("expected url ldaps://localhost:636, got %s", client.url)
	}
	if client.baseDN != "dc=example,dc=com" {
		t.Errorf("expected baseDN dc=example,dc=com, got %s", client.baseDN)
	}
	if client.poolSize != 10 {
		t.Errorf("expected poolSize 10, got %d", client.poolSize)
	}
}

func TestClient_ReturnConnection_NilConnection(t *testing.T) {
	config := &ClientConfig{
		URL:      "ldap://localhost:389",
		BindDN:   "cn=admin",
		Password: "password",
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	client.returnConnection(nil)

	if len(client.pool) != 0 {
		t.Errorf("expected empty pool, got %d connections", len(client.pool))
	}
}
