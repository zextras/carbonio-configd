// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package lookup

import (
	"context"
	"errors"
	"testing"
)

// mockConfigLookup is a test implementation of ConfigLookup.
type mockConfigLookup struct {
	data map[string]string
	err  error
}

func (m *mockConfigLookup) LookUpConfig(_ context.Context, cfgType, key string) (string, error) {
	if m.err != nil {
		return "", m.err
	}

	v, ok := m.data[cfgType+":"+key]
	if !ok {
		return "", errors.New("key not found: " + cfgType + ":" + key)
	}

	return v, nil
}

func TestConfigLookup_InterfaceContract(t *testing.T) {
	tests := []struct {
		name    string
		mock    *mockConfigLookup
		cfgType string
		key     string
		want    string
		wantErr bool
	}{
		{
			name: "successful lookup",
			mock: &mockConfigLookup{
				data: map[string]string{
					"global:zimbra_server_hostname": "mail.example.com",
				},
			},
			cfgType: "global",
			key:     "zimbra_server_hostname",
			want:    "mail.example.com",
			wantErr: false,
		},
		{
			name: "key not found",
			mock: &mockConfigLookup{
				data: map[string]string{},
			},
			cfgType: "server",
			key:     "missing_key",
			want:    "",
			wantErr: true,
		},
		{
			name: "underlying error",
			mock: &mockConfigLookup{
				err: errors.New("connection refused"),
			},
			cfgType: "global",
			key:     "any_key",
			want:    "",
			wantErr: true,
		},
		{
			name: "empty value is valid",
			mock: &mockConfigLookup{
				data: map[string]string{
					"local:empty_key": "",
				},
			},
			cfgType: "local",
			key:     "empty_key",
			want:    "",
			wantErr: false,
		},
		{
			name: "different config types same key",
			mock: &mockConfigLookup{
				data: map[string]string{
					"global:port": "7071",
					"server:port": "443",
				},
			},
			cfgType: "server",
			key:     "port",
			want:    "443",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify the mock satisfies the interface at compile time.
			var cl ConfigLookup = tt.mock

			got, err := cl.LookUpConfig(context.Background(), tt.cfgType, tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("LookUpConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if got != tt.want {
				t.Errorf("LookUpConfig() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestConfigLookup_ContextPropagation verifies context is accepted by the interface.
func TestConfigLookup_ContextPropagation(t *testing.T) {
	var cl ConfigLookup = &mockConfigLookup{
		data: map[string]string{"misc:key": "val"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	got, err := cl.LookUpConfig(ctx, "misc", "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "val" {
		t.Errorf("expected %q, got %q", "val", got)
	}
}
