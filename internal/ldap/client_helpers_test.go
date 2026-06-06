// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"testing"
)

func TestServerHasService(t *testing.T) {
	tests := []struct {
		name           string
		serviceEnabled string
		serviceName    string
		want           bool
	}{
		{
			name:           "exact match",
			serviceEnabled: "mailbox",
			serviceName:    "mailbox",
			want:           true,
		},
		{
			name:           "multi-valued exact match",
			serviceEnabled: "mailbox\nldap\nmta",
			serviceName:    "ldap",
			want:           true,
		},
		{
			name:           "no match",
			serviceEnabled: "mailbox\nldap\nmta",
			serviceName:    "zimbra",
			want:           false,
		},
		{
			name:           "substring not matched",
			serviceEnabled: "zimbraAdmin\nmailbox",
			serviceName:    "zimbra",
			want:           false,
		},
		{
			name:           "empty input",
			serviceEnabled: "",
			serviceName:    "mailbox",
			want:           false,
		},
		{
			name:           "whitespace trimmed",
			serviceEnabled: " mailbox \n ldap ",
			serviceName:    "ldap",
			want:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := serverHasService(tt.serviceEnabled, tt.serviceName)
			if got != tt.want {
				t.Errorf("serverHasService(%q, %q) = %v, want %v",
					tt.serviceEnabled, tt.serviceName, got, tt.want)
			}
		})
	}
}
