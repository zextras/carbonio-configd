// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"testing"
)

func TestStatusCmd_Fields(t *testing.T) {
	tests := []struct {
		name     string
		cmd      StatusCmd
		wantName string
	}{
		{
			name:     "empty name for system-wide status",
			cmd:      StatusCmd{Name: ""},
			wantName: "",
		},
		{
			name:     "specific service name",
			cmd:      StatusCmd{Name: "mailbox"},
			wantName: "mailbox",
		},
		{
			name:     "mta service",
			cmd:      StatusCmd{Name: "mta"},
			wantName: "mta",
		},
		{
			name:     "proxy service",
			cmd:      StatusCmd{Name: "proxy"},
			wantName: "proxy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.cmd.Name != tt.wantName {
				t.Errorf("StatusCmd.Name = %q, want %q", tt.cmd.Name, tt.wantName)
			}
		})
	}
}
