// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package tui is the full-screen terminal interface of carbonio-bootstrap:
// the configuration screen (sections, role chips, inline validation, a
// review page) and the apply screen (preflight, plan, step progress). The
// models are plain Bubble Tea models whose Update and View are driven by
// messages only, so they are tested without a terminal. The line wizard
// (bootstrap.Wizard) stays the fallback when there is no terminal.
package tui
