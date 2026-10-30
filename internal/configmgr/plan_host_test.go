// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/mtaops"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/restart"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/snapshot"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
	"github.com/zextras/carbonio-configd/internal/testutil"
	"github.com/zextras/carbonio-configd/internal/transformer"
)

const planHostBase = "/opt/zextras"

// planHostFixture is a node with an "mta" section holding one directive of
// every kind: a mapped file, a template rewrite, a postconf, a postconfd and
// an LDAP write.
type planHostFixture struct {
	cm    *ConfigManager
	fakes *systest.Fakes
	// svcCalls records every service-manager call.
	svcCalls *[]string
}

func newPlanHostFixture(t *testing.T) *planHostFixture {
	t.Helper()

	fakes := systest.New()
	fakes.Proc.UmaskValue = 0o027
	fakes.FS.PutDir(planHostBase+"/conf", 0o750)
	fakes.FS.Put(planHostBase+"/conf/main.cf.in", []byte("alpha = 1\nbeta = 2\n"), 0o644)

	st := state.NewState()
	st.MtaConfig.Sections["mta"] = &config.MtaConfigSection{
		Name:         "mta",
		Depends:      map[string]bool{},
		Restarts:     map[string]bool{"mta": true},
		RequiredVars: map[string]string{"zimbraSSLDHParam": configTypeMAPFILE},
		Rewrites:     map[string]config.RewriteEntry{"conf/main.cf.in": {Value: "conf/main.cf", Mode: "0640"}},
		Postconf:     map[string]string{"myhostname": "mail.example.com"},
		Postconfd:    map[string]string{"smtpd_banner": ""},
		Ldap:         map[string]string{"ldap_common_loglevel": "256"},
	}
	st.Publish(snapshot.New(snapshot.Parts{
		Global:   map[string]string{"zimbraSSLDHParam": base64.StdEncoding.EncodeToString([]byte("dh params"))},
		Services: map[string]string{"mta": "zimbraServiceEnabled"},
	}))

	var svcCalls []string

	env := fakes.System()
	svcMgr := &testutil.MockServiceManager{
		AddRestartWithReasonsFn: func(_ context.Context, svc string, _ []string) error {
			svcCalls = append(svcCalls, "add-restart "+svc)

			return nil
		},
		ProcessRestartsFn: func(context.Context, func(string) string) error {
			svcCalls = append(svcCalls, "process-restarts")

			return nil
		},
		ControlProcessFn: func(_ context.Context, svc string, _ services.ServiceAction) error {
			svcCalls = append(svcCalls, "control "+svc)

			return nil
		},
	}
	cm := &ConfigManager{
		env:         env,
		mainConfig:  &config.Config{BaseDir: planHostBase, Hostname: "testhost", RestartConfig: true},
		State:       st,
		ServiceMgr:  svcMgr,
		mtaExecutor: mtaops.NewExecutor(planHostBase, ldapRecorder{fakes.Rec}, env),
		mtaResolver: mtaops.NewResolver(planHostBase, env),
	}
	cm.Transformer = transformer.NewTransformer(cm)
	cm.policy = restart.New(svcMgr, env.Clock)

	return &planHostFixture{cm: cm, fakes: fakes, svcCalls: &svcCalls}
}

// syncHost makes the fake host match the configuration of the fixture,
// except for the LDAP write whose target cannot be read back.
func (f *planHostFixture) syncHost() {
	f.fakes.FS.Put(planHostBase+"/conf/dhparam.pem", []byte("dh params"), 0o640)
	f.fakes.FS.Put(planHostBase+"/conf/main.cf", []byte("alpha = 1\nbeta = 2\n"), 0o640)
	f.fakes.Exec.OnCmdline(planHostBase+"/common/sbin/postconf -h myhostname", systest.Reply("mail.example.com\n"))
	f.fakes.Exec.OnCmdline(planHostBase+"/common/sbin/postconf -n smtpd_banner", systest.Reply(""))
}

// driftHost makes the fake host differ from the configuration everywhere.
func (f *planHostFixture) driftHost() {
	f.fakes.FS.Put(planHostBase+"/conf/main.cf", []byte("stale\n"), 0o600)
	f.fakes.Exec.OnCmdline(planHostBase+"/common/sbin/postconf -h myhostname", systest.Reply("other.example.com\n"))
	f.fakes.Exec.OnCmdline(planHostBase+"/common/sbin/postconf -n smtpd_banner", systest.Reply("smtpd_banner = hi\n"))
}

func TestPlanHost_ReportsDriftWithoutSideEffects(t *testing.T) {
	t.Parallel()

	f := newPlanHostFixture(t)
	f.driftHost()

	pl, err := f.cm.PlanHost(context.Background(), nil)
	require.NoError(t, err)

	assert.False(t, pl.IsEmpty())
	assert.Equal(t, []string{"conf/dhparam.pem"}, mapfilePaths(pl))
	require.Len(t, pl.Rewrites, 1)
	assert.Equal(t, "conf/main.cf.in", pl.Rewrites[0].Source)
	require.Len(t, pl.Postconf, 1)
	assert.Equal(t, "myhostname", pl.Postconf[0].Key)
	require.Len(t, pl.Postconfd, 1)
	assert.Equal(t, "smtpd_banner", pl.Postconfd[0].Key)
	require.Len(t, pl.Ldap, 1)
	assert.Equal(t, "ldap_common_loglevel", pl.Ldap[0].Key)

	// Every action carries a drift reason naming what differs.
	for _, r := range pl.Rewrites[0].Reasons {
		assert.Equal(t, plan.ReasonDrift, r.Kind)
	}

	f.assertReadOnlyReads(t)
	assert.NotEmpty(t, f.fakes.Rec.Strings(), "plan must have read postfix state")

	// Nothing was written, set, removed or restarted.
	main, _ := f.fakes.FS.Data(planHostBase + "/conf/main.cf")
	assert.Equal(t, "stale\n", string(main))
	assert.False(t, f.fakes.FS.Exists(planHostBase+"/conf/dhparam.pem"))
	assert.Empty(t, *f.svcCalls, "no restart may be requested")
}

// assertReadOnlyReads allows exactly the postconf reads.
func (f *planHostFixture) assertReadOnlyReads(t *testing.T) {
	t.Helper()

	for _, op := range f.fakes.Rec.Strings() {
		isRead := strings.HasPrefix(op, "exec "+planHostBase+"/common/sbin/postconf -h ") ||
			strings.HasPrefix(op, "exec "+planHostBase+"/common/sbin/postconf -n ")
		assert.True(t, isRead, "PlanHost performed a mutating or unexpected side effect: %q", op)
		assert.NotContains(t, op, "postconf -e")
		assert.NotContains(t, op, "postconf -X")
		assert.False(t, strings.HasPrefix(op, "fs."), "no filesystem mutation is allowed: %q", op)
		assert.False(t, strings.HasPrefix(op, "ldap"), "no LDAP write is allowed: %q", op)
	}
}

func TestPlanHost_InSyncHostIsEmptyExceptLdap(t *testing.T) {
	t.Parallel()

	f := newPlanHostFixture(t)
	f.syncHost()

	pl, err := f.cm.PlanHost(context.Background(), nil)
	require.NoError(t, err)

	assert.Empty(t, pl.Mapfiles)
	assert.Empty(t, pl.Rewrites)
	assert.Empty(t, pl.Postconf)
	assert.Empty(t, pl.Postconfd)
	assert.Empty(t, pl.Restarts)
	// cn=config cannot be read back, so the LDAP write is always listed.
	assert.Len(t, pl.Ldap, 1)

	f.assertReadOnlyReads(t)
}

func TestPlanHost_UnknownSection(t *testing.T) {
	t.Parallel()

	f := newPlanHostFixture(t)

	pl, err := f.cm.PlanHost(context.Background(), []string{"nope"})
	require.ErrorIs(t, err, ErrUnknownSection)
	assert.Nil(t, pl)
	assert.Empty(t, f.fakes.Rec.Strings())
}

func mapfilePaths(pl *plan.Plan) []string {
	var out []string

	for _, m := range pl.Mapfiles {
		out = append(out, state.MAPPEDFILES[m.Key])
	}

	return out
}
