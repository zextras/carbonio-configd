// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zextras/carbonio-configd/internal/metrics"
)

func TestCountFailures(t *testing.T) {
	t.Parallel()

	boom := errors.New("ldap down")

	t.Run("failure is counted under the loader name and still returned", func(t *testing.T) {
		t.Parallel()

		cm := &ConfigManager{metrics: metrics.New()}
		lf := cm.countFailures("gc", func(context.Context) error { return boom })

		assert.Equal(t, "gc", lf.name)
		assert.ErrorIs(t, lf.fn(t.Context()), boom)
		assert.Contains(t, string(cm.Metrics().Render()), `configd_load_failures_total{source="gc"} 1`)
	})

	t.Run("success is not counted", func(t *testing.T) {
		t.Parallel()

		cm := &ConfigManager{metrics: metrics.New()}
		lf := cm.countFailures("lc", func(context.Context) error { return nil })

		assert.NoError(t, lf.fn(t.Context()))
		assert.Empty(t, cm.Metrics().Render())
	})

	t.Run("failure caused by shutdown is not counted", func(t *testing.T) {
		t.Parallel()

		cm := &ConfigManager{metrics: metrics.New()}
		lf := cm.countFailures("sc", func(ctx context.Context) error { return ctx.Err() })

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		assert.Error(t, lf.fn(ctx))
		assert.Empty(t, cm.Metrics().Render())
	})

	t.Run("manager without registry records nothing", func(t *testing.T) {
		t.Parallel()

		cm := &ConfigManager{}
		lf := cm.countFailures("mc", func(context.Context) error { return boom })

		assert.ErrorIs(t, lf.fn(t.Context()), boom)
	})
}
