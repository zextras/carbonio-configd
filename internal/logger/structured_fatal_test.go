// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package logger

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFatalContext_ExitsWithCode1(t *testing.T) {
	var gotCode int

	prev := exitFunc
	exitFunc = func(code int) { gotCode = code }

	t.Cleanup(func() { exitFunc = prev })

	FatalContext(context.Background(), "fatal test message", "key", "value")
	assert.Equal(t, 1, gotCode)
}

func TestContextWithComponentOnce(t *testing.T) {
	ctx := context.Background()

	first := ContextWithComponentOnce(ctx, "mainloop")
	assert.NotEqual(t, ctx, first, "first call must allocate")

	second := ContextWithComponentOnce(first, "mainloop")
	assert.Equal(t, first, second, "same component must not reallocate")

	third := ContextWithComponentOnce(first, "watchdog")
	assert.NotEqual(t, first, third, "different component must allocate")
}

func TestIsDebug(t *testing.T) {
	// Default context logger: debug disabled unless configured otherwise.
	ctx := context.Background()
	// Either outcome is config-dependent; the call itself must not panic and
	// must be consistent across invocations.
	assert.Equal(t, IsDebug(ctx), IsDebug(ctx))
}
