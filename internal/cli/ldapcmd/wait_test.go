// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldapcmd

import (
	"bytes"
	"context"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/testutil/ldapfake"
	"github.com/zextras/carbonio-configd/internal/testutil/pki"
)

// waitConfig writes a localconfig.xml holding ldapURL (none when empty) and
// clears the environment overrides, so the test reads only that file.
func waitConfig(t *testing.T, ldapURL string) string {
	t.Helper()

	for _, key := range []string{"ldap_url", "ldap_master_url", "ssl_allow_untrusted_certs"} {
		t.Setenv("CONFIGD_LC_"+key, "")
		require.NoError(t, os.Unsetenv("CONFIGD_LC_"+key))
	}

	xml := `<?xml version="1.0" encoding="UTF-8"?><localconfig>`
	if ldapURL != "" {
		xml += `<key name="ldap_url"><value>` + ldapURL + `</value></key>`
	}

	xml += `</localconfig>`

	path := filepath.Join(t.TempDir(), "localconfig.xml")
	require.NoError(t, os.WriteFile(path, []byte(xml), 0o600))

	return path
}

func runWait(t *testing.T, opts *WaitOptions) (int, string, time.Duration) {
	t.Helper()

	var stderr bytes.Buffer

	start := time.Now()
	code := Wait(context.Background(), &stderr, opts)

	return code, stderr.String(), time.Since(start)
}

func TestWait_ReachableFromLocalconfig(t *testing.T) {
	srv := ldapfake.New(t, ldapfake.Options{})
	cfg := waitConfig(t, srv.URL("ldap", "127.0.0.1"))

	code, out, _ := runWait(t, &WaitOptions{ConfigPath: cfg, Timeout: 5 * time.Second})

	assert.Equal(t, 0, code)
	assert.Contains(t, out, "LDAP is reachable at ldap://127.0.0.1:")
}

func TestWait_FailsOverToSecondURL(t *testing.T) {
	srv := ldapfake.New(t, ldapfake.Options{})
	cfg := waitConfig(t, "ldap://"+closedAddr(t)+" "+srv.URL("ldap", "127.0.0.1"))

	code, out, _ := runWait(t, &WaitOptions{ConfigPath: cfg, Timeout: 5 * time.Second})

	assert.Equal(t, 0, code)
	assert.Contains(t, out, srv.Addr())
}

func TestWait_LDAPS(t *testing.T) {
	pk := pki.NewServer(t, "127.0.0.1")
	srv := ldapfake.New(t, ldapfake.Options{
		TLS:         &tls.Config{Certificates: []tls.Certificate{pk.Certificate}, MinVersion: tls.VersionTLS12},
		ImplicitTLS: true,
	})
	cfg := waitConfig(t, srv.URL("ldaps", "127.0.0.1"))

	code, _, _ := runWait(t, &WaitOptions{ConfigPath: cfg, Timeout: 5 * time.Second})

	assert.Equal(t, 0, code)
}

func TestWait_URLFlagOverridesLocalconfig(t *testing.T) {
	srv := ldapfake.New(t, ldapfake.Options{})
	cfg := waitConfig(t, "ldap://"+closedAddr(t))

	code, _, _ := runWait(t, &WaitOptions{ConfigPath: cfg, URLs: []string{srv.URL("ldap", "127.0.0.1")}, Timeout: 5 * time.Second})

	assert.Equal(t, 0, code)
}

// The daemon starts without an LDAP URL, so the wait must not hold it back.
func TestWait_NoURLReturnsAtOnce(t *testing.T) {
	cfg := waitConfig(t, "")

	code, out, took := runWait(t, &WaitOptions{ConfigPath: cfg, Timeout: time.Minute})

	assert.Equal(t, 0, code)
	assert.Contains(t, out, "no LDAP URL configured")
	assert.Less(t, took, 5*time.Second)
}

func TestWait_UnreachableTimesOut(t *testing.T) {
	cfg := waitConfig(t, "ldap://"+closedAddr(t))

	code, out, took := runWait(t, &WaitOptions{ConfigPath: cfg, Timeout: 500 * time.Millisecond, Interval: 50 * time.Millisecond})

	assert.Equal(t, 1, code)
	assert.Contains(t, out, "not reachable after 500ms")
	assert.Less(t, took, 5*time.Second)
}

func TestWait_MalformedLocalconfigFails(t *testing.T) {
	waitConfig(t, "")

	bad := filepath.Join(t.TempDir(), "localconfig.xml")
	require.NoError(t, os.WriteFile(bad, []byte("<localconfig><key"), 0o600))

	code, out, _ := runWait(t, &WaitOptions{ConfigPath: bad, Timeout: time.Second})

	assert.Equal(t, 1, code)
	assert.Contains(t, out, "loading localconfig")
}

// closedAddr returns a loopback host:port nothing listens on.
func closedAddr(t *testing.T) string {
	t.Helper()

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	return addr
}
