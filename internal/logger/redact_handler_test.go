// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package logger

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/redact"
)

const (
	pemBody = "MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcw"
	testPEM = "-----BEGIN PRIVATE KEY-----\n" + pemBody + "\n-----END PRIVATE KEY-----"
)

// emit sends one record with a fixed time through (optionally) the redacting
// handler and returns what the underlying handler wrote, so wrapped and
// unwrapped output can be compared byte for byte.
func emit(t *testing.T, wrap bool, format LogFormat, msg string, attrs ...any) string {
	t.Helper()

	var buf bytes.Buffer

	opts := &slog.HandlerOptions{Level: slog.LevelDebug}

	var h slog.Handler
	if format == FormatJSON {
		h = slog.NewJSONHandler(&buf, opts)
	} else {
		h = slog.NewTextHandler(&buf, opts)
	}

	if wrap {
		h = newRedactHandler(h)
	}

	r := slog.NewRecord(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), slog.LevelInfo, msg, 0)
	r.Add(attrs...)
	require.NoError(t, h.Handle(context.Background(), r))

	return buf.String()
}

func TestRedactHandler_NonSecretOutputByteIdentical(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		msg   string
		attrs []any
	}{
		{"plain", "Restarting service", []any{"service", "mta", "reason", "config changed"}},
		{"key value", "Loaded", []any{"key", "zimbraMailPort", "value", "7071"}},
		{"numbers", "Cycle", []any{"duration_ms", 12, "ok", true, "d", 3 * time.Second}},
		{"error", "boom", []any{"error", errors.New("exit status 1")}},
		{"empty secret", "unset", []any{"key", "ldap_root_password", "value", ""}},
		{"group", "g", []any{slog.Group("g", "a", 1, "b", "x")}},
		{"substring key", "k", []any{"key", "zimbraMailKeyPort", "value", "1"}},
		{"public pem", "cert", []any{"pem", "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----"}},
		{"numeric secret key", "n", []any{"token_count", 3}},
	}

	for _, tc := range cases {
		for _, format := range []LogFormat{FormatText, FormatJSON} {
			t.Run(tc.name+"/"+string(format), func(t *testing.T) {
				t.Parallel()

				want := emit(t, false, format, tc.msg, tc.attrs...)
				got := emit(t, true, format, tc.msg, tc.attrs...)
				assert.Equal(t, want, got)
			})
		}
	}
}

func TestRedactHandler_Redacts(t *testing.T) {
	t.Parallel()

	const secret = "hunter2-s3cret"

	cases := []struct {
		name  string
		msg   string
		attrs []any
		keep  []string
	}{
		{"secret attr key", "m", []any{"ldap_amavis_password", secret}, []string{"ldap_amavis_password"}},
		{"ldap attr", "m", []any{"zimbraSSLPrivateKey", secret}, []string{"zimbraSSLPrivateKey"}},
		{"userPassword", "m", []any{"userPassword", secret}, nil},
		{"token attr", "m", []any{"authToken", secret}, nil},
		{"credential attr", "m", []any{"credentials", secret}, nil},
		{"key/value pair", "m", []any{"key", "mailboxd_keystore_password", "value", secret}, []string{"mailboxd_keystore_password"}},
		{"previous/current", "m", []any{"key", "zimbra_ldap_password", "previous_value", secret, "current_value", secret}, nil},
		{"attr/value pair", "m", []any{"attr", "zimbraLdapPassword", "value", secret}, nil},
		{"any value", "m", []any{"ssl_key", []byte(secret)}, nil},
		{"group attr", "m", []any{slog.Group("cfg", "db_password", secret, "port", 7071)}, []string{"port"}},
		{"pem value", "m", []any{"blob", testPEM}, []string{"blob"}},
		{"pem inside text", "m", []any{"detail", "failed to load " + testPEM + " (bad)"}, []string{"failed to load", "(bad)"}},
		{"pem in error", "m", []any{"error", errors.New("parse: " + testPEM)}, []string{"parse:"}},
		{"pem in message", "loaded " + testPEM, nil, []string{"loaded"}},
		{"pem rsa", "m", []any{"v", "-----BEGIN RSA " + "PRIVATE KEY-----\n" + pemBody + "\n-----END RSA " + "PRIVATE KEY-----"}, nil},
		{"pem truncated", "m", []any{"v", "x -----BEGIN EC PRIVATE KEY-----\n" + pemBody}, []string{"x "}},
	}

	for _, tc := range cases {
		for _, format := range []LogFormat{FormatText, FormatJSON} {
			t.Run(tc.name+"/"+string(format), func(t *testing.T) {
				t.Parallel()

				out := emit(t, true, format, tc.msg, tc.attrs...)
				assert.NotContains(t, out, secret)
				assert.NotContains(t, out, pemBody)
				assert.Contains(t, out, redact.Placeholder)

				for _, k := range tc.keep {
					assert.Contains(t, out, k)
				}
			})
		}
	}
}

func TestRedactHandler_WithAttrsAndGroup(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	h := newRedactHandler(slog.NewTextHandler(&buf, nil))
	l := slog.New(h).With("db_password", "pw-1", "component", "x").WithGroup("g")
	l.Info("hello", "token", "tok-2", "plain", "ok")

	out := buf.String()
	assert.NotContains(t, out, "pw-1")
	assert.NotContains(t, out, "tok-2")
	assert.Contains(t, out, "component=x")
	assert.Contains(t, out, "g.plain=ok")
}

func TestInitStructuredLogging_RedactsByDefault(t *testing.T) {
	var buf bytes.Buffer

	InitStructuredLogging(&Config{Format: FormatJSON, Level: slog.LevelDebug, Output: &buf, AddSource: true})
	buf.Reset()

	ctx := context.Background()
	InfoContext(ctx, "Loaded", "key", "zimbraSSLPrivateKey", "value", "topsecret")
	ErrorContext(ctx, "bad", "error", errors.New(testPEM))
	InfoContext(ctx, "Plain", "key", "zimbraMailPort", "value", "7071")

	out := buf.String()
	assert.NotContains(t, out, "topsecret")
	assert.NotContains(t, out, pemBody)
	assert.Contains(t, out, `"value":"7071"`)
	assert.Contains(t, out, "redact_handler_test.go", "source attribution must still point at the caller")
}
