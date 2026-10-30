// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package template

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/state"
)

// scannerMaxToken is bufio.Scanner's default token limit.
const scannerMaxToken = 64 * 1024

// splitScannerLines mirrors bufio.ScanLines for inputs shorter than the token
// limit: split on '\n', drop one trailing '\r' per line and the empty tail.
func splitScannerLines(input string) []string {
	lines := strings.Split(input, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}

	return lines
}

// FuzzRewriteReader feeds arbitrary multi-line .in templates to
// Rewriter.RewriteReader.
//
// Invariants: never panics; deterministic (including malformed-directive errors); every emitted line is
// newline-terminated (so the output is empty or ends with '\n'); a template
// without '@'/'%' passes through unchanged line by line; a plain-text input
// yields exactly one output line per input line.
func FuzzRewriteReader(f *testing.F) {
	seeds := []string{
		"",
		"\n",
		"plain line\nsecond line\n",
		"myhostname = %%VAR:zimbraServerHostname%%\n",
		"mynetworks = %%zimbraMtaMyNetworks%%\nmyorigin = %%VAR:zimbraMtaMyOrigin%%\n",
		"%%comment SERVICE:antivirus%%content_filter = smtp-amavis:[127.0.0.1]:10024\n",
		"%%uncomment SERVICE:webmail%%webmail_enabled = true\n",
		"antispam = %%binary SERVICE:antispam%%\nlist = %%list VAR:zimbraMtaMyNetworks ,%%\n",
		"owner = @@postfix_mail_owner@@\r\ngroup = @@postfix_setgid_group@@\r\n",
		"no trailing newline %%LOCAL:hostname%%",
		"ipv6 = [::1]/128 %%VAR:zimbraMtaMyNetworks%%\n",
		"unicode caffè ☕ 日本語 %%VAR:empty%%\n\n\n",
		"%%\n@@\n%%%%\n@@@@\n",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	ctx := logger.ContextWithLogger(context.Background(), slog.New(slog.DiscardHandler))
	rw := NewRewriter("/opt/zextras", newMockLookup(), &state.State{})

	f.Fuzz(func(t *testing.T, input string) {
		if len(input) >= scannerMaxToken {
			t.Skip("line may exceed the scanner token limit")
		}

		var out bytes.Buffer
		if err := rw.RewriteReader(ctx, strings.NewReader(input), &out); err != nil {
			// Malformed directives (e.g. "%%list%%") are rejected like the
			// legacy rewriter does; the rejection itself must be deterministic.
			var again bytes.Buffer
			if err2 := rw.RewriteReader(ctx, strings.NewReader(input), &again); err2 == nil || err2.Error() != err.Error() {
				t.Fatalf("non-deterministic error on %q: %v vs %v", input, err, err2)
			}

			return
		}

		got := out.String()

		if got != "" && !strings.HasSuffix(got, "\n") {
			t.Fatalf("output not newline-terminated: %q", got)
		}

		var again bytes.Buffer
		if err := rw.RewriteReader(ctx, strings.NewReader(input), &again); err != nil || again.String() != got {
			t.Fatalf("non-deterministic output: %q vs %q (err %v)", got, again.String(), err)
		}

		if !strings.ContainsAny(input, "@%") {
			var want strings.Builder
			for _, l := range splitScannerLines(input) {
				want.WriteString(l + "\n")
			}

			if got != want.String() {
				t.Fatalf("plain template altered: %q -> %q", input, got)
			}
		}
	})
}
