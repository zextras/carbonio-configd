// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package snapshot

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleParts() Parts {
	return Parts{
		Local:    map[string]string{"zimbra_server_hostname": "mail.example.com"},
		Global:   map[string]string{"k": "global", "g": "g-only"},
		Server:   map[string]string{"k": "server", "s": "s-only"},
		Misc:     map[string]string{"k": "misc", "m": "m-only"},
		Services: map[string]string{"mailbox": "x", "antivirus": "y"},
	}
}

func TestNew_CopiesInputs(t *testing.T) {
	t.Parallel()

	p := sampleParts()
	s := New(p)

	p.Local["zimbra_server_hostname"] = "changed"
	p.Local["new"] = "v"
	p.Global["g"] = "changed"
	p.Server["s"] = "changed"
	p.Misc["m"] = "changed"
	p.Services["extra"] = "z"
	delete(p.Services, "mailbox")

	v, ok := s.Local("zimbra_server_hostname")
	assert.True(t, ok)
	assert.Equal(t, "mail.example.com", v)

	_, ok = s.Local("new")
	assert.False(t, ok)

	v, _ = s.Global("g")
	assert.Equal(t, "g-only", v)
	v, _ = s.Server("s")
	assert.Equal(t, "s-only", v)
	v, _ = s.Misc("m")
	assert.Equal(t, "m-only", v)
	assert.True(t, s.ServiceEnabled("mailbox"))
	assert.False(t, s.ServiceEnabled("extra"))
}

func TestNilAndZeroSnapshot_AreEmpty(t *testing.T) {
	t.Parallel()

	var nilSnap *Snapshot

	for name, s := range map[string]*Snapshot{"nil": nilSnap, "zero": {}, "empty parts": New(Parts{})} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, get := range []func(string) (string, bool){s.Local, s.Global, s.Server, s.Misc, s.Var} {
				v, ok := get("k")
				assert.False(t, ok)
				assert.Empty(t, v)
			}

			assert.False(t, s.ServiceEnabled("mailbox"))
			assert.Empty(t, s.ServiceNames())
			assert.Empty(t, s.ServiceValue("mailbox"))

			v, ok := s.Observed(TypeFile, "k")
			assert.False(t, ok)
			assert.Empty(t, v)

			for _, typ := range []string{TypeVar, TypeLocal, TypeFile, TypeMapFile, TypeMapLocal} {
				_, ok := s.Value(typ, "k")
				assert.False(t, ok, typ)
			}

			v, ok = s.Value(TypeService, "mailbox")
			assert.True(t, ok)
			assert.Equal(t, ServiceFalse, v)

			for _, m := range []map[string]string{s.LocalMap(), s.GlobalMap(), s.ServerMap(), s.ServicesMap()} {
				assert.NotNil(t, m)
				assert.Empty(t, m)
			}

			assert.NotNil(t, s.LocalConfig().Data)
			assert.NotNil(t, s.GlobalConfig().Data)
			assert.NotNil(t, s.ServerConfig().Data)
			assert.NotNil(t, s.ServerConfig().ServiceConfig)
		})
	}
}

func TestVar_Precedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		parts Parts
		key   string
		want  string
		found bool
	}{
		{"server wins", Parts{Server: map[string]string{"k": "s"}, Misc: map[string]string{"k": "m"}, Global: map[string]string{"k": "g"}}, "k", "s", true},
		{"misc over global", Parts{Misc: map[string]string{"k": "m"}, Global: map[string]string{"k": "g"}}, "k", "m", true},
		{"global only", Parts{Global: map[string]string{"k": "g"}}, "k", "g", true},
		{"empty server value still wins", Parts{Server: map[string]string{"k": ""}, Global: map[string]string{"k": "g"}}, "k", "", true},
		{"local not consulted", Parts{Local: map[string]string{"k": "l"}}, "k", "", false},
		{"missing", Parts{Global: map[string]string{"x": "g"}}, "k", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := New(tt.parts).Var(tt.key)
			assert.Equal(t, tt.found, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValue_ByType(t *testing.T) {
	t.Parallel()

	s := New(sampleParts()).WithObserved(map[Lookup]string{
		{Type: TypeFile, Key: "f"}:      "file-content",
		{Type: TypeMapFile, Key: "mf"}:  "mapfile-content",
		{Type: TypeMapLocal, Key: "ml"}: "maplocal-content",
	})

	tests := []struct {
		name, typ, key, want string
		found                bool
	}{
		{"var server", TypeVar, "k", "server", true},
		{"var missing", TypeVar, "nope", "", false},
		{"local", TypeLocal, "zimbra_server_hostname", "mail.example.com", true},
		{"local missing", TypeLocal, "nope", "", false},
		{"service enabled", TypeService, "mailbox", ServiceTrue, true},
		{"service disabled still found", TypeService, "nope", ServiceFalse, true},
		{"file", TypeFile, "f", "file-content", true},
		{"mapfile", TypeMapFile, "mf", "mapfile-content", true},
		{"maplocal", TypeMapLocal, "ml", "maplocal-content", true},
		{"file missing", TypeFile, "nope", "", false},
		{"file type mismatch", TypeMapFile, "f", "", false},
		{"unknown type", "BOGUS", "k", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := s.Value(tt.typ, tt.key)
			assert.Equal(t, tt.found, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestObserved_IndependentOfConfigMaps(t *testing.T) {
	t.Parallel()

	base := New(sampleParts())

	_, ok := base.Observed(TypeVar, "k")
	assert.False(t, ok, "config values are not observed values")

	s := base.WithObserved(map[Lookup]string{{Type: TypeVar, Key: "k"}: "observed", {Type: TypeVar, Key: "ghost"}: "boo"})

	v, ok := s.Observed(TypeVar, "k")
	assert.True(t, ok)
	assert.Equal(t, "observed", v)

	v, ok = s.Observed(TypeVar, "ghost")
	assert.True(t, ok)
	assert.Equal(t, "boo", v)

	cfg, _ := s.Var("k")
	assert.Equal(t, "server", cfg, "observed values do not alter config lookups")

	_, ok = s.Var("ghost")
	assert.False(t, ok)
}

func TestWithObserved_SharesNothingMutable(t *testing.T) {
	t.Parallel()

	orig := New(sampleParts())
	obs := map[Lookup]string{{Type: TypeFile, Key: "f"}: "v1"}
	derived := orig.WithObserved(obs)

	obs[Lookup{Type: TypeFile, Key: "f"}] = "mutated"
	obs[Lookup{Type: TypeFile, Key: "other"}] = "x"

	v, _ := derived.Observed(TypeFile, "f")
	assert.Equal(t, "v1", v)

	_, ok := derived.Observed(TypeFile, "other")
	assert.False(t, ok)

	_, ok = orig.Observed(TypeFile, "f")
	assert.False(t, ok, "original snapshot unchanged")

	// Config view carried over.
	v, ok = derived.Local("zimbra_server_hostname")
	assert.True(t, ok)
	assert.Equal(t, "mail.example.com", v)
	assert.Equal(t, orig.ServiceNames(), derived.ServiceNames())

	// Re-deriving replaces observed values and leaves the previous derivation alone.
	second := derived.WithObserved(nil)
	_, ok = second.Observed(TypeFile, "f")
	assert.False(t, ok)

	v, _ = derived.Observed(TypeFile, "f")
	assert.Equal(t, "v1", v)

	// WithObserved on nil snapshot works.
	var nilSnap *Snapshot

	fromNil := nilSnap.WithObserved(map[Lookup]string{{Type: TypeFile, Key: "f"}: "n"})
	v, ok = fromNil.Observed(TypeFile, "f")
	assert.True(t, ok)
	assert.Equal(t, "n", v)
}

func TestMapAccessors_ReturnCopies(t *testing.T) {
	t.Parallel()

	s := New(sampleParts())

	accessors := map[string]func() map[string]string{
		"local": s.LocalMap, "global": s.GlobalMap, "server": s.ServerMap, "services": s.ServicesMap,
	}

	for name, get := range accessors {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := get()
			require.NotEmpty(t, m)

			want := get()
			for k := range m {
				m[k] = "tampered"
			}

			m["injected"] = "x"

			assert.Equal(t, want, get(), "mutating a returned map must not affect the snapshot")
		})
	}
}

func TestServiceNames_Sorted(t *testing.T) {
	t.Parallel()

	s := New(Parts{Services: map[string]string{"zeta": "1", "alpha": "1", "mid": "1", "Beta": "1"}})

	assert.Equal(t, []string{"Beta", "alpha", "mid", "zeta"}, s.ServiceNames())
	assert.Equal(t, "1", s.ServiceValue("mid"))
	assert.Empty(t, s.ServiceValue("absent"))
}

func TestConfigAdapters_Independent(t *testing.T) {
	t.Parallel()

	s := New(sampleParts())

	local := s.LocalConfig()
	v, ok := local.Data.Get("zimbra_server_hostname")
	assert.True(t, ok)
	assert.Equal(t, "mail.example.com", v)

	local.Data.Set("zimbra_server_hostname", "changed")
	local.Data.Set("extra", "x")

	global := s.GlobalConfig()
	assert.Equal(t, s.GlobalMap(), global.Data.Snapshot())
	global.Data.Clear()

	server := s.ServerConfig()
	assert.Equal(t, s.ServerMap(), server.Data.Snapshot())
	assert.Equal(t, s.ServicesMap(), server.ServiceConfig.Snapshot())
	server.Data.Set("s", "changed")
	server.ServiceConfig.Delete("mailbox")

	// A second adapter call and the snapshot itself are unaffected.
	v, _ = s.LocalConfig().Data.Get("zimbra_server_hostname")
	assert.Equal(t, "mail.example.com", v)

	_, ok = s.LocalConfig().Data.Get("extra")
	assert.False(t, ok)
	assert.Equal(t, "g-only", s.GlobalConfig().Data.GetOr("g", ""))

	v, _ = s.Server("s")
	assert.Equal(t, "s-only", v)
	assert.True(t, s.ServiceEnabled("mailbox"))
	assert.True(t, s.ServerConfig().ServiceConfig.GetOr("mailbox", "") != "")
}
