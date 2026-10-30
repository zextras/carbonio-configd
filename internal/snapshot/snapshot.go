// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package snapshot defines the immutable view of the configuration a single
// configd cycle works on: local config, global config, server config, misc
// config, the set of enabled services and the lookup values observed for the
// variables configd.cf cares about.
//
// A Snapshot is built once per cycle, never mutated afterwards and published
// through an atomic pointer, so the watchdog, the listener and the planner read
// it without locks. The planner (internal/plan) is a pure function of two
// snapshots.
package snapshot

import (
	"maps"
	"sort"

	"github.com/zextras/carbonio-configd/internal/config"
)

// Lookup types as spelled in configd.cf ("VAR", "LOCAL", ...).
const (
	TypeVar      = "VAR"
	TypeLocal    = "LOCAL"
	TypeFile     = "FILE"
	TypeMapFile  = "MAPFILE"
	TypeMapLocal = "MAPLOCAL"
	TypeService  = "SERVICE"
)

// ServiceTrue and ServiceFalse are the values a SERVICE lookup yields.
const (
	ServiceTrue  = "TRUE"
	ServiceFalse = "FALSE"
)

// Lookup identifies one configuration lookup: a type and a key.
type Lookup struct {
	Type string
	Key  string
}

// Parts are the raw maps a cycle's loaders produce. New copies them, so the
// caller may keep and reuse its maps.
type Parts struct {
	Local  map[string]string
	Global map[string]string
	Server map[string]string
	Misc   map[string]string
	// Services maps each enabled service name (including the derived
	// "mailboxd" and "sasl" aliases) to its enablement source.
	Services map[string]string
}

// Snapshot is the immutable per-cycle configuration view. The zero value and
// a nil *Snapshot are both a valid empty snapshot.
type Snapshot struct {
	local    map[string]string
	global   map[string]string
	server   map[string]string
	misc     map[string]string
	services map[string]string
	observed map[Lookup]string
}

// New builds a Snapshot owning copies of parts.
func New(parts Parts) *Snapshot {
	return &Snapshot{
		local:    maps.Clone(parts.Local),
		global:   maps.Clone(parts.Global),
		server:   maps.Clone(parts.Server),
		misc:     maps.Clone(parts.Misc),
		services: maps.Clone(parts.Services),
	}
}

// WithObserved returns a copy of s that additionally carries the observed
// lookup values. The (immutable) configuration maps are shared with s; obs is
// copied.
func (s *Snapshot) WithObserved(obs map[Lookup]string) *Snapshot {
	out := Snapshot{}
	if s != nil {
		out = *s
	}

	out.observed = maps.Clone(obs)

	return &out
}

// Local returns the localconfig value for key.
func (s *Snapshot) Local(key string) (string, bool) {
	if s == nil {
		return "", false
	}

	v, ok := s.local[key]

	return v, ok
}

// Global returns the global config value for key.
func (s *Snapshot) Global(key string) (string, bool) {
	if s == nil {
		return "", false
	}

	v, ok := s.global[key]

	return v, ok
}

// Server returns the server config value for key.
func (s *Snapshot) Server(key string) (string, bool) {
	if s == nil {
		return "", false
	}

	v, ok := s.server[key]

	return v, ok
}

// Misc returns the misc config value for key.
func (s *Snapshot) Misc(key string) (string, bool) {
	if s == nil {
		return "", false
	}

	v, ok := s.misc[key]

	return v, ok
}

// Var resolves a VAR lookup: server config first, then misc, then global.
func (s *Snapshot) Var(key string) (string, bool) {
	if v, ok := s.Server(key); ok {
		return v, true
	}

	if v, ok := s.Misc(key); ok {
		return v, true
	}

	return s.Global(key)
}

// ServiceEnabled reports whether the named service is enabled on this node.
func (s *Snapshot) ServiceEnabled(name string) bool {
	if s == nil {
		return false
	}

	_, ok := s.services[name]

	return ok
}

// ServiceNames returns the enabled service names, sorted.
func (s *Snapshot) ServiceNames() []string {
	if s == nil {
		return nil
	}

	names := make([]string, 0, len(s.services))
	for n := range s.services {
		names = append(names, n)
	}

	sort.Strings(names)

	return names
}

// ServiceValue returns the enablement source recorded for a service.
func (s *Snapshot) ServiceValue(name string) string {
	if s == nil {
		return ""
	}

	return s.services[name]
}

// Value resolves a lookup the planner can answer purely: VAR, LOCAL and
// SERVICE come from the configuration maps, every other type (FILE, MAPFILE,
// MAPLOCAL) from the observed values. The boolean is false when the lookup
// has no value (the legacy "lookup failed").
func (s *Snapshot) Value(lookupType, key string) (string, bool) {
	switch lookupType {
	case TypeVar:
		return s.Var(key)
	case TypeLocal:
		return s.Local(key)
	case TypeService:
		if s.ServiceEnabled(key) {
			return ServiceTrue, true
		}

		return ServiceFalse, true
	default:
		return s.Observed(lookupType, key)
	}
}

// Observed returns the value recorded for the lookup when the snapshot was
// completed, whatever its type. This is the "last value" the diff compares.
func (s *Snapshot) Observed(lookupType, key string) (string, bool) {
	if s == nil {
		return "", false
	}

	v, ok := s.observed[Lookup{Type: lookupType, Key: key}]

	return v, ok
}

// LocalMap returns a copy of the localconfig values.
func (s *Snapshot) LocalMap() map[string]string {
	if s == nil {
		return map[string]string{}
	}

	return cloneNonNil(s.local)
}

// GlobalMap returns a copy of the global config values.
func (s *Snapshot) GlobalMap() map[string]string {
	if s == nil {
		return map[string]string{}
	}

	return cloneNonNil(s.global)
}

// ServerMap returns a copy of the server config values.
func (s *Snapshot) ServerMap() map[string]string {
	if s == nil {
		return map[string]string{}
	}

	return cloneNonNil(s.server)
}

// ServicesMap returns a copy of the enabled-services map.
func (s *Snapshot) ServicesMap() map[string]string {
	if s == nil {
		return map[string]string{}
	}

	return cloneNonNil(s.services)
}

func cloneNonNil(m map[string]string) map[string]string {
	out := maps.Clone(m)
	if out == nil {
		out = map[string]string{}
	}

	return out
}

// LocalConfig returns a fresh config.LocalConfig holding a copy of the
// localconfig values (for consumers still written against the mutable types).
func (s *Snapshot) LocalConfig() *config.LocalConfig {
	cm := config.NewConfigMap()
	cm.Replace(s.LocalMap())

	return &config.LocalConfig{Data: cm}
}

// GlobalConfig returns a fresh config.GlobalConfig holding a copy of the
// global config values.
func (s *Snapshot) GlobalConfig() *config.GlobalConfig {
	cm := config.NewConfigMap()
	cm.Replace(s.GlobalMap())

	return &config.GlobalConfig{Data: cm}
}

// ServerConfig returns a fresh config.ServerConfig holding copies of the
// server config values and of the enabled-services map.
func (s *Snapshot) ServerConfig() *config.ServerConfig {
	data := config.NewConfigMap()
	data.Replace(s.ServerMap())

	svc := config.NewConfigMap()
	svc.Replace(s.ServicesMap())

	return &config.ServerConfig{Data: data, ServiceConfig: svc}
}

// WithLocal returns a copy of s with the localconfig values replaced by a
// copy of local. The other maps are shared (they are immutable).
func (s *Snapshot) WithLocal(local map[string]string) *Snapshot {
	out := s.shallow()
	out.local = maps.Clone(local)

	return out
}

// WithGlobal returns a copy of s with the global config values replaced.
func (s *Snapshot) WithGlobal(global map[string]string) *Snapshot {
	out := s.shallow()
	out.global = maps.Clone(global)

	return out
}

// WithMisc returns a copy of s with the misc config values replaced.
func (s *Snapshot) WithMisc(misc map[string]string) *Snapshot {
	out := s.shallow()
	out.misc = maps.Clone(misc)

	return out
}

// WithServer returns a copy of s with the server config values and the
// enabled-services map replaced.
func (s *Snapshot) WithServer(server, services map[string]string) *Snapshot {
	out := s.shallow()
	out.server = maps.Clone(server)
	out.services = maps.Clone(services)

	return out
}

// shallow copies the Snapshot struct; the maps are shared, which is safe as
// they are never mutated.
func (s *Snapshot) shallow() *Snapshot {
	if s == nil {
		return &Snapshot{}
	}

	out := *s

	return &out
}
