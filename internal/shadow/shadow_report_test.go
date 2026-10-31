// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package shadow_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/shadow"
	"github.com/zextras/carbonio-configd/internal/sys"
)

func allKindsPlan() *plan.Plan {
	pl := fullPlan()
	pl.Mapfiles = []plan.Mapfile{{Key: "zimbraSSLDHParam", Sections: []string{"mta"}, Reasons: drift("mta")}}
	pl.Postconfd = []plan.Postconfd{{Key: "old_param", Sections: []string{"mta"}, Reasons: drift("mta")}}
	pl.Ldap = []plan.Ldap{{Key: "zimbraMtaMyNetworks", Value: "10.0.0.0/8", Sections: []string{"mta"}, Reasons: drift("mta")}}

	return pl
}

func TestCycle_LogsEveryActionKind(t *testing.T) {
	logs := captureLogs(t)
	h := newHarness(t, "", &fakeSource{plans: []*plan.Plan{allKindsPlan()}}, nil)

	require.NoError(t, h.sh.Cycle(t.Context()))

	subjects := map[string]string{}
	for _, l := range logLines(t, logs, "Shadow plan") {
		subjects[l["action"].(string)] = l["subject"].(string)
	}

	assert.Equal(t, "zimbraSSLDHParam", subjects["mapfile"])
	assert.Equal(t, "old_param", subjects["postconfd"])
	assert.Equal(t, "zimbraMtaMyNetworks=10.0.0.0/8", subjects["ldap"])
	assert.Len(t, subjects, 7)

	done := logLines(t, logs, "Shadow cycle completed")
	require.Len(t, done, 1)
	assert.InEpsilon(t, 8.0, done[0]["actions"], 0)
}

func TestCycle_WriteFailuresAreJoinedAndReported(t *testing.T) {
	tests := []struct {
		name    string
		failOp  string
		wantErr string
	}{
		{"mkdir fails", "MkdirAll", "create /var/shadow"},
		{"atomic write fails", "WriteFileAtomic", "write /var/shadow/plan-latest.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			h := newHarness(t, shadowDir, &fakeSource{plans: []*plan.Plan{fullPlan()}}, nil)
			h.out.FS.Fail = func(op, _ string) error {
				if op == tt.failOp {
					return errors.New("disk full")
				}

				return nil
			}

			err := h.sh.Cycle(t.Context())

			require.ErrorContains(t, err, tt.wantErr)
			assert.NotEmpty(t, logLines(t, logs, "Shadow report failed"))
			assert.Equal(t, 1, h.src.done, "the cycle still completes")
		})
	}
}

// firedClock hands out timers that have already fired.
type firedClock struct{ sys.Clock }

type firedTimer struct{ ch chan time.Time }

func (f firedTimer) C() <-chan time.Time { return f.ch }
func (f firedTimer) Stop() bool          { return false }

func (firedClock) NewTimer(time.Duration) sys.Timer {
	ch := make(chan time.Time, 1)
	ch <- time.Time{}

	return firedTimer{ch: ch}
}

func TestRun_NextCycleStartsWhenTheIntervalElapses(t *testing.T) {
	captureLogs(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	src := &fakeSource{plans: []*plan.Plan{emptyPlan()}}
	src.onPlan = func(n int) {
		if n == 3 {
			cancel()
		}
	}

	h := newHarness(t, "", src, func(o *shadow.Options) {
		o.Once = false
		o.Clock = firedClock{Clock: o.Clock}
	})

	require.NoError(t, h.sh.Run(ctx))
	assert.Equal(t, 3, src.plansTaken)
}
