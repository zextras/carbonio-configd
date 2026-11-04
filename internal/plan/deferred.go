// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package plan

import (
	"slices"
	"strings"
)

// MergeRestarts merges two restart lists into one sorted by service. A service
// in both keeps the action of fresh, because it was decided later (a service
// disabled in the meantime is stopped, not restarted), and the sections and
// reasons of both, stale first.
//
// The caller uses it to hold deferred restarts (stale: the ones already held,
// fresh: the ones this cycle deferred) and to release them (stale: the held
// ones, fresh: the restarts of this cycle, which win for the same reason). The
// actions are classified for systemd hosts (see Classify). Neither input is
// modified.
func MergeRestarts(stale, fresh []Restart, systemd bool) []Restart {
	merged := make([]Restart, 0, len(stale)+len(fresh))

	for _, list := range [][]Restart{stale, fresh} {
		for i := range list {
			merged = mergeRestart(merged, &list[i])
		}
	}

	slices.SortFunc(merged, func(a, b Restart) int { return strings.Compare(a.Service, b.Service) })

	for i := range merged {
		merged[i].Action = Classify(merged[i].Action, merged[i].Service, systemd)
	}

	return merged
}

// mergeRestart adds r to list, folding it into the entry of its service.
func mergeRestart(list []Restart, r *Restart) []Restart {
	i := slices.IndexFunc(list, func(have Restart) bool { return have.Service == r.Service })
	if i < 0 {
		return append(list, Restart{
			Service:  r.Service,
			Action:   r.Action,
			Sections: slices.Clone(r.Sections),
			Reasons:  slices.Clone(r.Reasons),
		})
	}

	have := &list[i]
	have.Action = r.Action

	for _, s := range r.Sections {
		have.Sections = appendUnique(have.Sections, s)
	}

	have.Reasons = appendReasons(have.Reasons, r.Reasons)

	return list
}
