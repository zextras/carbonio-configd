// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/snapshot"
)

// setLocal replaces the local config of the published snapshot.
func setLocal(cm *ConfigManager, data map[string]string) {
	cm.State.UpdateSnapshot(func(s *snapshot.Snapshot) *snapshot.Snapshot { return s.WithLocal(data) })
}

// setGlobal replaces the global config of the published snapshot.
func setGlobal(cm *ConfigManager, data map[string]string) {
	cm.State.UpdateSnapshot(func(s *snapshot.Snapshot) *snapshot.Snapshot { return s.WithGlobal(data) })
}

// setMisc replaces the misc config of the published snapshot.
func setMisc(cm *ConfigManager, data map[string]string) {
	cm.State.UpdateSnapshot(func(s *snapshot.Snapshot) *snapshot.Snapshot { return s.WithMisc(data) })
}

// setServer replaces the server config and the enabled services (service name
// -> enablement source, e.g. "zimbraServiceEnabled") of the published snapshot.
func setServer(cm *ConfigManager, data, services map[string]string) {
	cm.State.UpdateSnapshot(func(s *snapshot.Snapshot) *snapshot.Snapshot { return s.WithServer(data, services) })
}

// runCycle runs the Plan, Execute and ApplyRestarts steps of one daemon cycle
// on the already loaded and parsed configuration. It returns the plan and the
// error of Plan or Execute (a failed Plan skips the other steps).
func runCycle(t *testing.T, cm *ConfigManager) (*plan.Plan, error) {
	t.Helper()

	ctx := context.Background()

	pl, err := cm.Plan(ctx)
	if err != nil {
		return nil, err
	}

	execErr := cm.Execute(ctx, pl)

	cm.ApplyRestarts(ctx, pl)

	return pl, execErr
}

// mustCycle is runCycle for cycles that must succeed.
func mustCycle(t *testing.T, cm *ConfigManager) *plan.Plan {
	t.Helper()

	pl, err := runCycle(t, cm)
	require.NoError(t, err)

	return pl
}
