// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package parser

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
)

// parserSeeds are realistic zmconfigd.cf fragments covering every directive
// and the conditional forms.
var parserSeeds = []string{
	"SECTION test\n\tVAR zimbraMtaMyNetworks\n\tLOCAL ldap_url\n\tRESTART mta\n",
	"SECTION mta DEPENDS amavis\n\tREWRITE conf/postfix_header_checks.in conf/postfix_header_checks\n" +
		"\tREWRITE conf/test.in conf/test.out 0640\n\tRESTART mta\n",
	"SECTION mta\n\tif SERVICE antivirus\n\t\tPOSTCONF content_filter FILE zmconfigd/postfix_content_filter.cf\n\tfi\n",
	"SECTION mta\n\tif !SERVICE antispam\n\t\tPOSTCONF smtpd_milters\n\tfi\n",
	"SECTION mta\n\tif VAR zimbraMtaAuthEnabled\n\t\tif SERVICE amavis\n\t\t\tPOSTCONFD smtpd_tls_loglevel VAR zimbraMtaTlsLogLevel\n" +
		"\t\tfi\n\t\tLDAP zimbraMtaMyNetworks LOCAL ldap_url\n\tfi\n",
	"SECTION proxy\n\tPROXYGEN\n\tMAPFILE /opt/zextras/conf/x\n\tMAPLOCAL ldap_url\n\tMODE restart\n",
	"SECTION ldap DEPENDS mta\n\tLOCAL ldap_host\n\tFILE /opt/zextras/conf/abs.cf\n\tPOSTCONF relayhost VAR zimbraMtaRelayHost\n" +
		"\tPOSTCONF mynetworks VAR zimbraMtaMyNetworks\n",
	"# comment only\n",
	"\n\n\n",
	"SECTION\n",
	"SECTION a\nSECTION b\nSECTION a\n",
	"SECTION x\r\n\tVAR y\r\n\tRESTART z\r\n",
	"SECTION unicode-é☕\n\tVAR ключ\n",
	"SECTION t\n\tPOSTCONF a \"quoted string with spaces\" b\n\tPOSTCONF ipv6 [::1]/128\n",
	"SECTION t\n\tif SERVICE\n\tfi fi fi\n",
	"SECTION t\n\tif SERVICE a\n\t\tif SERVICE b\n\t\t\tif SERVICE c\n",
	"SECTION t\n\tREWRITE only-one\n",
	"",
}

// checkSections verifies that every map key matches the section's Name.
func checkSections(t *testing.T, cfg *config.MtaConfig) {
	t.Helper()

	for key, sec := range cfg.Sections {
		if sec == nil {
			t.Fatalf("section %q is nil", key)
		}

		if sec.Name != key {
			t.Fatalf("section map key %q != Name %q", key, sec.Name)
		}
	}
}

// FuzzParseString feeds arbitrary zmconfigd.cf text to the lexer+parser.
// Invariants: never panics, always terminates, an empty document is an error,
// a non-nil config is returned whenever the error is nil, parsing is
// deterministic, and section map keys match section names.
func FuzzParseString(f *testing.F) {
	for _, s := range parserSeeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		ctx := context.Background()

		cfg, err := NewParser(NewLexer(ctx, strings.NewReader(input))).ParseString(ctx, input)

		if input == "" {
			if err == nil {
				t.Fatal("empty input must be an error")
			}

			return
		}

		if err == nil && cfg == nil {
			t.Fatal("nil config with nil error")
		}

		if cfg != nil {
			checkSections(t, cfg)
		}

		// Deterministic: a fresh parser over the same text yields the same result.
		cfg2, err2 := NewParser(NewLexer(ctx, strings.NewReader(input))).ParseString(ctx, input)

		if (err == nil) != (err2 == nil) {
			t.Fatalf("non-deterministic error: %v vs %v", err, err2)
		}

		if err != nil && err.Error() != err2.Error() {
			t.Fatalf("non-deterministic error text: %v vs %v", err, err2)
		}

		if !reflect.DeepEqual(cfg, cfg2) {
			t.Fatalf("non-deterministic config:\n%+v\n%+v", cfg, cfg2)
		}
	})
}

// FuzzLexer drains the lexer on arbitrary input.
// Invariants: the token stream always reaches EOF or an error within a
// bounded number of tokens (each rune can yield at most one token), line
// numbers never decrease, and Peek never disagrees with the following
// NextToken.
func FuzzLexer(f *testing.F) {
	for _, s := range parserSeeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		ctx := context.Background()
		lex := NewLexer(ctx, strings.NewReader(input))

		// +2 for the terminating EOF and one slack token.
		limit := len([]rune(input)) + 2
		prevLine := 0

		for i := 0; ; i++ {
			if i > limit {
				t.Fatalf("lexer did not terminate within %d tokens", limit)
			}

			peeked, perr := lex.Peek()
			if perr != nil {
				// A lexical error ends the stream for this fuzz case.
				return
			}

			tok, err := lex.NextToken()
			if err != nil {
				t.Fatalf("NextToken failed after successful Peek: %v", err)
			}

			if peeked != tok {
				t.Fatalf("Peek %+v != NextToken %+v", peeked, tok)
			}

			if tok.Line < prevLine {
				t.Fatalf("line number went backwards: %d after %d (%+v)", tok.Line, prevLine, tok)
			}

			prevLine = tok.Line

			if tok.Type == TokenEOF {
				return
			}
		}
	})
}
