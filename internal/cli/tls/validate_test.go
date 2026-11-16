// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tls

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configtls "github.com/zextras/carbonio-configd/internal/tls"
)

// failingLDAP is a directory whose every read fails.
type failingLDAP struct{ err error }

func (f failingLDAP) GetEntry(context.Context, string, []string) (*ldap.Entry, error) {
	return nil, f.err
}

func (f failingLDAP) Search(context.Context, string, string, []string, int) (*ldap.SearchResult, error) {
	return nil, f.err
}

func (f failingLDAP) ModifyAttribute(context.Context, string, string, string) error { return f.err }

// A failed backend lookup (a transient LDAP error, say) must not let the mode
// through unvalidated: only --force skips the validation.
func TestValidateAgainstProxiesFailsClosedOnLookupError(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	err := validateAgainstProxies(context.Background(), &stdout, &stderr,
		failingLDAP{err: errors.New("ldap down")}, "mail.example.com", configtls.ModeHTTPS)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ldap down")
	assert.Contains(t, err.Error(), "mail.example.com")
}
