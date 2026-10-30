// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zextras/carbonio-configd/internal/plan"
)

// TestApplyRestarts_DebounceSetting pins that zmconfigd_restart_debounce_seconds
// reaches the restart policy: off (0) restarts in the same cycle, a positive
// period holds the restart back.
func TestApplyRestarts_DebounceSetting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		debounce int
		want     []string
	}{
		{name: "off by default", debounce: 0, want: []string{"mta"}},
		{name: "positive period holds the restart", debounce: 3600, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cm, queued := newApplyRestartsCM(t, true)
			cm.mainConfig.RestartDebounce = tt.debounce

			cm.ApplyRestarts(context.Background(), &plan.Plan{
				Restarts: []plan.Restart{{Service: "mta", Action: plan.ActionRestart}},
				Enabled:  []string{"mta"},
			})

			assert.Equal(t, tt.want, *queued)
		})
	}
}
