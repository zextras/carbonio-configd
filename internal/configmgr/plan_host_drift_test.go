// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/snapshot"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const (
	hostDhparam  = planHostBase + "/conf/dhparam.pem"
	hostMainCf   = planHostBase + "/conf/main.cf"
	hostPostconf = planHostBase + "/common/sbin/postconf"
)

func TestMapfileDrift(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		m     plan.Mapfile
		setup func(f *planHostFixture)
		want  string
	}{
		{"local files are never compared", plan.Mapfile{Key: "zimbraSSLDHParam", Local: true}, nil, ""},
		{"unmapped key is ignored", plan.Mapfile{Key: "notMapped"}, nil, ""},
		{"missing file", plan.Mapfile{Key: "zimbraSSLDHParam"}, nil, hostDhparam + " is missing; it would be written"},
		{
			"different content", plan.Mapfile{Key: "zimbraSSLDHParam"},
			func(f *planHostFixture) { f.fakes.FS.Put(hostDhparam, []byte("other"), 0o640) },
			hostDhparam + " differs from zimbraSSLDHParam; it would be rewritten",
		},
		{
			"same content", plan.Mapfile{Key: "zimbraSSLDHParam"},
			func(f *planHostFixture) { f.fakes.FS.Put(hostDhparam, []byte("dh params"), 0o640) },
			"",
		},
		{
			"unset value with a leftover file", plan.Mapfile{Key: "zimbraSSLDHParam"},
			func(f *planHostFixture) {
				f.clearDhparam()
				f.fakes.FS.Put(hostDhparam, []byte("leftover"), 0o640)
			},
			hostDhparam + " exists but its value is unset; it would be removed",
		},
		{
			"unset value and no file", plan.Mapfile{Key: "zimbraSSLDHParam"},
			func(f *planHostFixture) { f.clearDhparam() },
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newPlanHostFixture(t)
			if tt.setup != nil {
				tt.setup(f)
			}

			assert.Equal(t, tt.want, f.cm.mapfileDrift(tt.m))
		})
	}
}

func (f *planHostFixture) clearDhparam() {
	f.cm.State.UpdateSnapshot(func(s *snapshot.Snapshot) *snapshot.Snapshot {
		g := s.GlobalMap()
		g["zimbraSSLDHParam"] = ""

		return s.WithGlobal(g)
	})
}

func TestRewriteDrift(t *testing.T) {
	t.Parallel()

	rw := &plan.Rewrite{Source: "conf/main.cf.in", Target: "conf/main.cf", Mode: "0640"}

	tests := []struct {
		name  string
		setup func(f *planHostFixture)
		want  string
	}{
		{"missing target", nil, hostMainCf + " is missing; it would be written"},
		{
			"unreadable target",
			func(f *planHostFixture) {
				f.fakes.FS.Put(hostMainCf, []byte("alpha = 1\nbeta = 2\n"), 0o640)
				f.fakes.FS.Fail = func(op, path string) error {
					if op == "ReadFile" && path == hostMainCf {
						return errors.New("permission denied")
					}

					return nil
				}
			},
			hostMainCf + " is unreadable; it would be rewritten",
		},
		{
			"content differs",
			func(f *planHostFixture) { f.fakes.FS.Put(hostMainCf, []byte("stale\n"), 0o640) },
			hostMainCf + " content differs from the rendered template",
		},
		{
			"mode differs",
			func(f *planHostFixture) { f.fakes.FS.Put(hostMainCf, []byte("alpha = 1\nbeta = 2\n"), 0o600) },
			hostMainCf + " mode 0600 would become 0640",
		},
		{
			"in sync",
			func(f *planHostFixture) { f.fakes.FS.Put(hostMainCf, []byte("alpha = 1\nbeta = 2\n"), 0o640) },
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newPlanHostFixture(t)
			if tt.setup != nil {
				tt.setup(f)
			}

			got, err := f.cm.rewriteDrift(context.Background(), rw)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRenderRewrite(t *testing.T) {
	t.Parallel()

	f := newPlanHostFixture(t)

	content, mode, err := f.cm.RenderRewrite(context.Background(),
		&plan.Rewrite{Source: "conf/main.cf.in", Target: "conf/main.cf", Mode: "0640"})
	require.NoError(t, err)
	assert.Equal(t, "alpha = 1\nbeta = 2\n", string(content))
	assert.Equal(t, "-rw-r-----", mode.String())

	_, _, err = f.cm.RenderRewrite(context.Background(), &plan.Rewrite{Source: "conf/main.cf.in", Mode: "rw"})
	require.ErrorContains(t, err, "invalid file mode rw")

	_, _, err = f.cm.RenderRewrite(context.Background(), &plan.Rewrite{Source: "conf/missing.in", Target: "x"})
	require.ErrorContains(t, err, "render conf/missing.in")
}

func TestPlanHost_FailuresAreReturned(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setup   func(f *planHostFixture)
		wantErr string
	}{
		{
			"template cannot be rendered",
			func(f *planHostFixture) { f.cm.State.MtaConfig.Sections["mta"].Rewrites["conf/main.cf.in"] = config.RewriteEntry{Value: "conf/main.cf", Mode: "bad"} },
			"invalid file mode bad",
		},
		{
			"postconf read fails",
			func(f *planHostFixture) {
				f.fakes.Exec.OnCmdline(hostPostconf+" -h myhostname", systest.Fail(1, "unknown parameter"))
			},
			"read postconf myhostname",
		},
		{
			"postconfd read fails",
			func(f *planHostFixture) {
				f.syncHost()
				f.fakes.Exec.OnCmdline(hostPostconf+" -n smtpd_banner", systest.Fail(1, "boom"))
			},
			"read postconf smtpd_banner",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newPlanHostFixture(t)
			tt.setup(f)

			pl, err := f.cm.PlanHost(context.Background(), nil)

			require.ErrorContains(t, err, tt.wantErr)
			assert.Nil(t, pl)
		})
	}
}

func TestPlanHost_UnresolvableValueIsReportedAsDrift(t *testing.T) {
	t.Parallel()

	f := newPlanHostFixture(t)
	f.syncHost()
	f.cm.State.MtaConfig.Sections["mta"].Postconf["broken"] = "MAPLOCAL:noSuchKey"

	pl, err := f.cm.PlanHost(context.Background(), nil)
	require.NoError(t, err)

	require.Len(t, pl.Postconf, 1)
	assert.Equal(t, "broken", pl.Postconf[0].Key)
	assert.Contains(t, pl.Postconf[0].Reasons[0].Detail, "value cannot be resolved")
}

func TestPlanHost_ProxygenFollowsItsSections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		setup     func(f *planHostFixture)
		wantProxy bool
	}{
		{"section drifted", func(f *planHostFixture) { f.driftHost() }, true},
		{"section in sync", func(f *planHostFixture) { f.syncHost() }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newPlanHostFixture(t)
			f.cm.State.MtaConfig.Sections["mta"].Proxygen = true
			tt.setup(f)

			pl, err := f.cm.PlanHost(context.Background(), nil)
			require.NoError(t, err)

			if !tt.wantProxy {
				assert.Nil(t, pl.Proxygen)

				return
			}

			require.NotNil(t, pl.Proxygen)
			assert.Equal(t, []string{"mta"}, pl.Proxygen.Sections)
			assert.Contains(t, pl.Proxygen.Reasons[0].Detail, "proxy configuration depends on section mta")
		})
	}
}
