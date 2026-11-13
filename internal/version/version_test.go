// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package version

import "testing"

func TestMainPrefersStampedVersion(t *testing.T) {
	old := stamped
	t.Cleanup(func() { stamped = old })

	stamped = "1.11.1"
	if got := Main(); got != "1.11.1" {
		t.Errorf("Main() = %q, want the stamped 1.11.1", got)
	}

	stamped = ""
	if got := Main(); got == "" || got == "1.11.1" {
		t.Errorf("Main() without stamp = %q, want the build-info version", got)
	}
}
