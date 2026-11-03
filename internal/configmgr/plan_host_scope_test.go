// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNarrowScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configured []string
		requested  []string
		want       []string
		wantErr    bool
	}{
		{"unrestricted host keeps the request", nil, []string{"mta", "proxy"}, []string{"mta", "proxy"}, false},
		{"unrestricted host with no request plans all", nil, nil, nil, false},
		{"no request plans every configured section", []string{"amavis", "mta"}, nil, []string{"amavis", "mta"}, false},
		{"request inside the scope", []string{"amavis", "mta"}, []string{"mta"}, []string{"mta"}, false},
		{"request outside the scope", []string{"amavis"}, []string{"mta"}, nil, true},
		{"one section outside the scope rejects the request", []string{"amavis"}, []string{"amavis", "mta"}, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newPlanHostFixture(t)
			f.cm.mainConfig.Sections = tt.configured

			got, err := f.cm.narrowScope(tt.requested)

			if tt.wantErr {
				require.ErrorIs(t, err, ErrOutOfScope)
				assert.ErrorContains(t, err, "configured: "+tt.configured[0])

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPlanHost_SectionOutsideZmconfigdSectionsFailsBeforeObserving(t *testing.T) {
	t.Parallel()

	f := newPlanHostFixture(t)
	f.cm.mainConfig.Sections = []string{"amavis"}

	pl, err := f.cm.PlanHost(context.Background(), []string{"mta"})

	require.ErrorIs(t, err, ErrOutOfScope)
	assert.Nil(t, pl)
	assert.Empty(t, f.fakes.Exec.Lines(), "the host was not probed")
}
