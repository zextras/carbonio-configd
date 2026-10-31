// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package systemd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func TestNewManager(t *testing.T) {
	t.Parallel()

	require.NotNil(t, NewManager(systest.New().System()))
}

func TestIsBooted_FollowsSdBooted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(f *systest.Fakes)
		want  bool
	}{
		{"missing", func(*systest.Fakes) {}, false},
		{"directory", func(f *systest.Fakes) { f.FS.PutDir(runSystemdDir, 0o755) }, true},
		{"regular file", func(f *systest.Fakes) { f.FS.Put(runSystemdDir, []byte("x"), 0o644) }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := systest.New()
			tt.setup(f)

			assert.Equal(t, tt.want, NewManager(f.System()).IsBooted())
		})
	}
}

func TestIsBooted_CachedPerManager(t *testing.T) {
	t.Parallel()

	f := systest.New()
	m := NewManager(f.System())

	assert.False(t, m.IsBooted())

	f.FS.PutDir(runSystemdDir, 0o755)

	assert.False(t, m.IsBooted(), "the first answer is cached for the Manager's lifetime")
	assert.True(t, NewManager(f.System()).IsBooted(), "a fresh Manager re-detects")
}

func TestIsEnabled(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Exec.OnCmdline("systemctl is-enabled bad.target", systest.Fail(1, "disabled"))
	m := NewManager(f.System())

	assert.True(t, m.IsEnabled(context.Background(), "good.target"))
	assert.False(t, m.IsEnabled(context.Background(), "bad.target"))
	assert.Equal(t, []string{
		"systemctl is-enabled good.target",
		"systemctl is-enabled bad.target",
	}, f.Exec.Lines())
}

func TestIsSystemdEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		enabled map[string]bool
		want    bool
		probes  int
	}{
		{"none enabled", nil, false, len(carbonioTargets)},
		{"first enabled short-circuits", map[string]bool{carbonioTargets[0]: true}, true, 1},
		{"last enabled", map[string]bool{carbonioTargets[len(carbonioTargets)-1]: true}, true, len(carbonioTargets)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := systest.New()
			f.Exec.OnCmdline("systemctl is-enabled", systest.Fail(1, ""))

			for target := range tt.enabled {
				f.Exec.OnCmdline("systemctl is-enabled "+target, systest.Reply("enabled"))
			}

			m := NewManager(f.System())

			assert.Equal(t, tt.want, m.IsSystemdEnabled(context.Background()))
			assert.Len(t, f.Exec.Calls(), tt.probes)
		})
	}
}

func TestCarbonioTargets(t *testing.T) {
	t.Parallel()

	assert.ElementsMatch(t, []string{
		"carbonio-directory-server.target",
		"carbonio-appserver.target",
		"carbonio-proxy.target",
		"carbonio-mta.target",
		"service-discover.target",
	}, carbonioTargets, "carbonio.target is deliberately excluded")
}
