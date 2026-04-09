// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build unix

package services

import "syscall"

// detachedSysProcAttr returns SysProcAttr that puts the child in its own
// session (PGID = PID), severing the parent's controlling terminal so the
// daemon survives the parent CLI's exit.
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
