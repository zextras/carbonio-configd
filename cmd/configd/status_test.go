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

func TestParseSystemctlShow_TableDriven(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  map[string]string
	}{
		{
			name:  "standard properties",
			input: "MainPID=456\nActiveState=active\n",
			want:  map[string]string{"MainPID": "456", "ActiveState": "active"},
		},
		{
			name:  "empty input",
			input: "",
			want:  map[string]string{},
		},
		{
			name:  "line without equals is ignored",
			input: "NoEqualsHere\nKey=Val\n",
			want:  map[string]string{"Key": "Val"},
		},
		{
			name:  "value with equals sign",
			input: "ExecStart=/usr/bin/java -Xms=512m\n",
			want:  map[string]string{"ExecStart": "/usr/bin/java -Xms=512m"},
		},
		{
			name:  "empty value",
			input: "EmptyVal=\n",
			want:  map[string]string{"EmptyVal": ""},
		},
		{
			name:  "whitespace only lines",
			input: "\n\n",
			want:  map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSystemctlShow(tt.input)
			for k, wantV := range tt.want {
				if gotV, ok := got[k]; !ok {
					t.Errorf("missing key %q", k)
				} else if gotV != wantV {
					t.Errorf("key %q = %q, want %q", k, gotV, wantV)
				}
			}
			for k := range got {
				if _, ok := tt.want[k]; !ok {
					t.Errorf("unexpected key %q in result", k)
				}
			}
		})
	}
}
