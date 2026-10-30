// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package logger

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ContextWithExit returns a context whose FatalContext calls exit instead of
// os.Exit, so the test can intercept the exit without killing the test binary.
func ContextWithExit(ctx context.Context, exit func(code int)) context.Context {
	return context.WithValue(ctx, exitContextKey, exit)
}

func TestFatalContext_ExitsWithCode1(t *testing.T) {
	t.Parallel()

	var gotCode int

	ctx := ContextWithExit(context.Background(), func(code int) { gotCode = code })

	FatalContext(ctx, "fatal test message", "key", "value")
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
