// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/mtaops"
	"github.com/zextras/carbonio-configd/internal/restart"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
	"github.com/zextras/carbonio-configd/internal/testutil"
	"github.com/zextras/carbonio-configd/internal/transformer"
)

// ldapRecorder is an ldap.AttributeModifier that logs into the same Recorder
// as the host fakes, so LDAP writes appear in the one ordered side-effect log.
type ldapRecorder struct{ rec *systest.Recorder }

func (l ldapRecorder) ModifyAttribute(_ context.Context, key, value string) error {
	l.rec.Record("ldap", key+"="+value)

	return nil
}

// subsequence returns the recorded ops that contain substr, in order.
func subsequence(ops []string, substr string) []string {
	var out []string

	for _, op := range ops {
		if strings.Contains(op, substr) {
			out = append(out, op)
		}
	}

	return out
}

// TestDoConfigRewrites_SideEffectSequence drives one whole cycle (Plan, Execute, ApplyRestarts) (a
// mapped file, a file rewrite, postconf, postconfd and an LDAP write) against
// the fake host and pins every side effect configd performs, in order.
//
// Execute writes mapped files first and serially; afterwards the
// rewrite, postconf, postconfd and LDAP steps run concurrently, so their
// relative interleaving is not defined. The test therefore pins the exact
// serial prefix, then the exact order within each concurrent step.
func TestDoConfigRewrites_SideEffectSequence(t *testing.T) {
	t.Parallel()

	const base = "/opt/zextras"

	fakes := systest.New()
	fakes.Proc.UmaskValue = 0o027
	fakes.FS.PutDir(base+"/conf", 0o750)
	fakes.FS.Put(base+"/conf/main.cf.in", []byte("alpha = 1\nbeta = 2\n"), 0o644)

	st := state.NewState()
	st.SetMtaConfig(&config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
		"mta": {
			Name:         "mta",
			RequiredVars: map[string]string{"zimbraSSLDHParam": configTypeMAPFILE},
			Rewrites:     map[string]config.RewriteEntry{"conf/main.cf.in": {Value: "conf/main.cf", Mode: "0640"}},
			Postconf:     map[string]string{"myhostname": "mail.example.com"},
			Postconfd:    map[string]string{"smtpd_banner": ""},
			Ldap:         map[string]string{"zimbraMtaMyNetworks": "127.0.0.0/8"},
		},
	}})

	env := fakes.System()
	cm := &ConfigManager{
		env:         env,
		mainConfig:  &config.Config{BaseDir: base, Hostname: "testhost"},
		State:       st,
		ServiceMgr:  &testutil.MockServiceManager{},
		mtaExecutor: mtaops.NewExecutor(base, ldapRecorder{fakes.Rec}, env),
		mtaResolver: mtaops.NewResolver(base, env),
	}
	cm.Transformer = transformer.NewTransformer(cm)
	cm.policy = restart.New(cm.ServiceMgr, env.Clock)

	setServer(cm, nil, map[string]string{"mta": "zimbraServiceEnabled"})
	setGlobal(cm, map[string]string{
		"zimbraSSLDHParam": base64.StdEncoding.EncodeToString([]byte("dh params")),
	})

	pl := mustCycle(t, cm)
	require.Len(t, pl.Sections, 1)

	ops := fakes.Rec.Strings()
	require.Len(t, ops, 7, "unexpected side effects:\n%s", strings.Join(ops, "\n"))

	// Serial phase: the mapped file is written before anything else runs.
	assert.Equal(t, "fs.write-atomic "+base+"/conf/dhparam.pem mode=0640", ops[0])

	// Concurrent phase: each step's own side effects keep their order.
	const tmp = "/tmp/zmconfigd-rewrite-000001"

	assert.Equal(t, []string{
		"fs.create-temp " + tmp,
		"fs.chmod " + tmp + " mode=0640",
		"fs.rename " + tmp + " -> " + base + "/conf/main.cf",
	}, subsequence(ops[1:], "zmconfigd-rewrite"), "rewrite step")
	assert.Equal(t, []string{"exec " + base + "/common/sbin/postconf -e myhostname=mail.example.com"},
		subsequence(ops[1:], "postconf -e"), "postconf step")
	assert.Equal(t, []string{"exec " + base + "/common/sbin/postconf -X smtpd_banner"},
		subsequence(ops[1:], "postconf -X"), "postconfd step")
	assert.Equal(t, []string{"ldap zimbraMtaMyNetworks=127.0.0.0/8"}, subsequence(ops[1:], "ldap "), "ldap step")

	// Results of the cycle.
	dh, ok := fakes.FS.Data(base + "/conf/dhparam.pem")
	require.True(t, ok)
	assert.Equal(t, "dh params", string(dh))

	out, ok := fakes.FS.Data(base + "/conf/main.cf")
	require.True(t, ok)
	assert.Equal(t, "alpha = 1\nbeta = 2\n", string(out))

	mode, _ := fakes.FS.Mode(base + "/conf/main.cf")
	assert.Equal(t, os.FileMode(0o640), mode.Perm())
	assert.False(t, fakes.FS.Exists(tmp), "temp file must be gone after the rename")

	// A done cycle leaves nothing to retry: the next one plans nothing.
	st.SetFirstRun(false)

	next := mustCycle(t, cm)
	assert.True(t, next.IsEmpty(), "done actions must not be planned again")
	assert.Len(t, fakes.Rec.Strings(), 7, "second cycle must not touch the host")
}
