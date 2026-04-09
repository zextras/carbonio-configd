// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build unix

package services

import (
	"testing"
)

func TestDetachedSysProcAttr_Setsid(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	attr := detachedSysProcAttr()
	if attr == nil {
		t.Fatal("detachedSysProcAttr() returned nil")
	}
	if !attr.Setsid {
		t.Error("expected Setsid=true for detached daemon")
	}
}
