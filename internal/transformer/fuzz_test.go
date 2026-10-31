// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package transformer

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/testutil"
)

// newFuzzLookup returns a deterministic in-memory lookup whose keys cover every
// directive family (numeric for range/freq, whitespace lists, IPv6 networks).
func newFuzzLookup() *testutil.MockConfigLookup {
	return testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"zimbraLocalBindAddress":     "127.0.0.1",
			"zimbraLogHostname":          "log.example.com",
			"zimbraLogToSyslog":          "TRUE",
			"zimbraMtaBlockedExtension":  "exe bat com pif scr vbs",
			"zimbraMtaMyNetworks":        "127.0.0.0/8 [::1]/128 172.24.0.0/21",
			"zimbraServerHostname":       "mail.example.com",
			"zimbraThreadPoolPercentage": "50",
			"zimbraCheckInterval":        "5m",
			"zimbraLogLevel":             "info",
			"restrictions":               "reject_invalid_helo_hostname reject_unverified_recipient",
			"empty":                      "",
		},
		"LOCAL": {
			"ldap_url":               "ldap://ldap1.example.com:389 ldap://ldap2.example.com:389",
			"mysql_bind_address":     "127.0.0.1",
			"zimbra_server_hostname": "mail.local.example.com",
		},
		"SERVICE": {
			"antispam":  "TRUE",
			"antivirus": "TRUE",
			"webmail":   "FALSE",
		},
	})
}

// FuzzTransform feeds arbitrary template lines to Transformer.Transform.
//
// Invariants: never panics; deterministic; a line containing neither '@' nor
// '%' is returned verbatim; a single-line input without explode directives
// and whose lookup values never contain newlines never produces extra newlines
// (the keepOnOneLine contract).
func FuzzTransform(f *testing.F) {
	seeds := []string{
		"",
		"plain text without directives",
		"%%comment VAR:zimbraLogToSyslog%%appender.SYSLOG.type = Syslog",
		"%%uncomment SERVICE:webmail%%webmail_enabled = true",
		"%%uncomment VAR:zimbraFeatureDisabled,;%%feature = value",
		"%%comment VAR:zimbraLogLevel,#,debug,trace%%log.level = DEBUG",
		"  %%comment SERVICE:antispam%% @bypass_spam_checks_maps = (1);",
		"blocked = %%list VAR:zimbraMtaBlockedExtension |%%",
		"mynetworks = %%list VAR:zimbraMtaMyNetworks ,%%",
		"enabled = %%binary VAR:zimbraLogToSyslog%%",
		"enabled = %%truefalse SERVICE:webmail%%",
		"result = %%contains VAR:zimbraMtaBlockedExtension exe^FOUND^NOTFOUND%%",
		"result = %%exact VAR:restrictions reject_invalid_helo_hostname^REJECT^PERMIT%%",
		"workers = %%range VAR:zimbraThreadPoolPercentage 10 100%%",
		"freq = %%freq VAR:zimbraCheckInterval 3600%%",
		"%%explode reject_rbl_client VAR:zimbraMtaMyNetworks%%",
		"server = %%VAR:zimbraServerHostname%% local_bind = @@mysql_bind_address@@",
		"host = %%zimbraServerHostname%%",
		"%%LOCAL:ldap_url%%",
		"%%SERVICE:antivirus%%",
		"%%VAR%% %%VAR:%% %%binary%% %%list VAR:x%% %%explode basestring%%",
		"%%%%%% @@@@ %% @ %%@@%%",
		"@@ldap_url@@ ipv6 [::1]/128",
		"%%VAR:zimbraMtaMyNetworks%%",
		"crlf line\r",
		"unicode %%VAR:ключ%% caffè ☕ 日本語 @@é@@",
		"%%range VAR:zimbraThreadPoolPercentage 99999999999999999999 -5%%",
		"%%freq VAR:zimbraCheckInterval 0%%",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	// Directive warnings for unknown keys are expected noise on fuzz input.
	ctx := logger.ContextWithLogger(context.Background(), slog.New(slog.DiscardHandler))
	tr := NewTransformer(newFuzzLookup())

	f.Fuzz(func(t *testing.T, line string) {
		out, err := tr.Transform(ctx, line)

		if again, errAgain := tr.Transform(ctx, line); again != out || (err == nil) != (errAgain == nil) {
			t.Fatalf("non-deterministic Transform(%q): %q, %v vs %q, %v", line, out, err, again, errAgain)
		}

		// A raising directive aborts the file; there is no output to validate.
		if err != nil {
			if !errors.Is(err, ErrTransform) {
				t.Fatalf("Transform(%q) error does not wrap ErrTransform: %v", line, err)
			}

			return
		}

		if !strings.ContainsAny(line, "@%") && out != line {
			t.Fatalf("line without '@'/'%%' modified: %q -> %q", line, out)
		}

		// explode directives legitimately expand to one line per value.
		if !strings.ContainsAny(line, "\n\r") && !strings.Contains(line, "explode") {
			body := strings.TrimSuffix(out, "\n")
			if strings.Contains(body, "\n") {
				t.Fatalf("single-line input %q produced multi-line output %q", line, out)
			}
		}
	})
}
