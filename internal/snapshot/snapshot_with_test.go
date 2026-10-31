// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package snapshot

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWith_ReplacesOneSectionAndLeavesTheOriginalAlone(t *testing.T) {
	t.Parallel()

	orig := New(sampleParts())

	tests := []struct {
		name   string
		derive func(s *Snapshot, in map[string]string) *Snapshot
		get    func(s *Snapshot, key string) (string, bool)
		// untouched is a lookup of another section that must survive.
		untouched func(s *Snapshot) (string, bool)
		key       string
		before    string
	}{
		{
			"local",
			func(s *Snapshot, in map[string]string) *Snapshot { return s.WithLocal(in) },
			(*Snapshot).Local,
			func(s *Snapshot) (string, bool) { return s.Global("g") },
			"zimbra_server_hostname", "mail.example.com",
		},
		{
			"global",
			func(s *Snapshot, in map[string]string) *Snapshot { return s.WithGlobal(in) },
			(*Snapshot).Global,
			func(s *Snapshot) (string, bool) { return s.Misc("m") },
			"g", "g-only",
		},
		{
			"misc",
			func(s *Snapshot, in map[string]string) *Snapshot { return s.WithMisc(in) },
			(*Snapshot).Misc,
			func(s *Snapshot) (string, bool) { return s.Server("s") },
			"m", "m-only",
		},
		{
			"server",
			func(s *Snapshot, in map[string]string) *Snapshot { return s.WithServer(in, nil) },
			(*Snapshot).Server,
			func(s *Snapshot) (string, bool) { return s.Local("zimbra_server_hostname") },
			"s", "s-only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			in := map[string]string{tt.key: "new"}
			got := tt.derive(orig, in)

			in[tt.key] = "mutated after the call"

			v, ok := tt.get(got, tt.key)
			assert.True(t, ok)
			assert.Equal(t, "new", v, "the derived snapshot owns a copy of the input")

			v, _ = tt.get(orig, tt.key)
			assert.Equal(t, tt.before, v, "the original is unchanged")

			_, ok = tt.untouched(got)
			assert.True(t, ok, "other sections are carried over")
		})
	}
}

func TestWithServer_ReplacesTheEnabledServices(t *testing.T) {
	t.Parallel()

	got := New(sampleParts()).WithServer(map[string]string{"s": "v"}, map[string]string{"proxy": "x"})

	assert.True(t, got.ServiceEnabled("proxy"))
	assert.False(t, got.ServiceEnabled("mailbox"))
	assert.Equal(t, []string{"proxy"}, got.ServiceNames())
}

func TestWith_NilReceiverYieldsAnEmptySnapshotWithTheNewValues(t *testing.T) {
	t.Parallel()

	var nilSnap *Snapshot

	v, ok := nilSnap.WithGlobal(map[string]string{"k": "v"}).Global("k")

	assert.True(t, ok)
	assert.Equal(t, "v", v)
}
