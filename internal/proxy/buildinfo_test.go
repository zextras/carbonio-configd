// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"strings"
	"testing"
)

func TestParseBuildInfo(t *testing.T) {
	t.Parallel()

	v, r := parseBuildInfo(strings.NewReader("BUILDNUM=1\nVERSION = 4.38.10_ZEXTRAS_202610\nRELEASE=carbonio\n"))
	if v != "4.38.10_ZEXTRAS_202610" || r != "carbonio" {
		t.Errorf("got %q %q", v, r)
	}

	v, r = parseBuildInfo(strings.NewReader("DATE=1\n"))
	if v != "unknown" || r != "unknown" {
		t.Errorf("missing keys should be unknown, got %q %q", v, r)
	}
}
