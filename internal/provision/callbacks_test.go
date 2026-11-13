// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package provision_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zextras/carbonio-configd/internal/provision"
)

func TestCallbacks_RefuseWhatTheServerWouldRefuse(t *testing.T) {
	r := newRig(t)
	r.withServer(t)
	r.withDomain(t)
	r.run(t, "cs", "idle.example.com")

	for name, args := range map[string][]string{
		"two relay hosts":       {"mcf", "zimbraMtaRelayHost", "a.example.com:25", "zimbraMtaRelayHost", "b.example.com:25"},
		"unknown mail host":     {"ca", "x@example.com", "", "zimbraMailHost", "nosuch.example.com"},
		"mail host without web": {"ca", "x@example.com", "", "zimbraMailHost", "idle.example.com"},
		"mail host and transport": {
			"ca", "x@example.com", "", "zimbraMailHost", host, "zimbraMailTransport", "lmtp:x:7025",
		},
		"two ports": {"ms", host, "zimbraMailPort", "8080", "zimbraMailPort", "8081"},
		"port taken": {
			"ms", host, "zimbraMailPort", "8080", "zimbraMailSSLPort", "8080",
		},
	} {
		_, err := r.Exec(args)

		assert.True(t, hasCode(err, provision.CodeInvalidRequest), "%s: %v", name, err)
	}
}

func TestCallbacks_AccountStatusDrivesTheMailStatus(t *testing.T) {
	r := newRig(t)
	r.withServer(t)
	r.withDomain(t)

	r.run(t, "ca", "closed@example.com", "", "zimbraAccountStatus", "closed")
	r.run(t, "ca", "pending@example.com", "", "zimbraAccountStatus", "pending")
	r.run(t, "ca", "active@example.com", "", "zimbraAccountStatus", "active")

	assert.Equal(t, []string{"disabled"}, r.dir.Values("uid=closed,ou=people,dc=example,dc=com", "zimbraMailStatus"))
	assert.Equal(t, []string{"disabled"}, r.dir.Values("uid=pending,ou=people,dc=example,dc=com", "zimbraMailStatus"))
	assert.Equal(t, []string{"enabled"}, r.dir.Values("uid=active,ou=people,dc=example,dc=com", "zimbraMailStatus"))
}
