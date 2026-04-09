// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"regexp"
	"testing"
)

// BenchmarkRegexInLoop demonstrates the old anti-pattern (compilation in loop)
func BenchmarkRegexInLoop(b *testing.B) {
	lines := []string{
		"    listen 143;",
		"    listen 110;",
		"    proxy_pass upstream;",
		"    server_name localhost;",
		"    imap_id         ;",
		"    proxy_issue_pop3_xoip   ;",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, line := range lines {
			// OLD PATTERN: compile regex on every line!
			enablerPattern := regexp.MustCompile(`^(\s*)\$\{([^}]+)\}(.+)$`)
			_ = enablerPattern.MatchString(line)

			emptyDirectivePattern := regexp.MustCompile(`^\s+[a-z0-9_]+\s+;`)
			_ = emptyDirectivePattern.MatchString(line)
		}
	}
}

// BenchmarkRegexPrecompiled demonstrates the optimized approach (pre-compiled)
func BenchmarkRegexPrecompiled(b *testing.B) {
	lines := []string{
		"    listen 143;",
		"    listen 110;",
		"    proxy_pass upstream;",
		"    server_name localhost;",
		"    imap_id         ;",
		"    proxy_issue_pop3_xoip   ;",
	}

	// NEW PATTERN: compile once, reuse many times
	enablerPattern := regexp.MustCompile(`^(\s*)\$\{([^}]+)\}(.+)$`)
	emptyDirectivePattern := regexp.MustCompile(`^\s+[a-z0-9_]+\s+;`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, line := range lines {
			_ = enablerPattern.MatchString(line)
			_ = emptyDirectivePattern.MatchString(line)
		}
	}
}
