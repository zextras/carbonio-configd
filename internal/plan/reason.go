// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package plan

import (
	"fmt"
	"slices"
	"strings"

	"github.com/zextras/carbonio-configd/internal/redact"
)

// ReasonKind classifies why a plan entry exists.
type ReasonKind string

// Reason kinds.
const (
	// ReasonFirstRun: first cycle of the process, every enabled section is rendered.
	ReasonFirstRun ReasonKind = "first-run"
	// ReasonForced: the section was named on the command line (standalone rewrite).
	ReasonForced ReasonKind = "forced"
	// ReasonRequested: a REWRITE command arrived on the listener.
	ReasonRequested ReasonKind = "requested"
	// ReasonVarChanged: a variable the section requires changed value.
	ReasonVarChanged ReasonKind = "var-changed"
	// ReasonEvaluated: the section is evaluated against the host state ("configd plan").
	ReasonEvaluated ReasonKind = "evaluated"
	// ReasonServiceEnabled: a service became enabled on this node.
	ReasonServiceEnabled ReasonKind = "service-enabled"
	// ReasonServiceDisabled: a tracked service is no longer enabled on this node.
	ReasonServiceDisabled ReasonKind = "service-disabled"
	// ReasonDependency: a service restarts because a service it depends on
	// does. Raised by the service manager's DEPENDS cascade (watchdog restarts);
	// config-driven restarts do not cascade, as in the legacy zmconfigd.
	ReasonDependency ReasonKind = "dependency"
	// ReasonRetry: the previous attempt of this action did not complete.
	ReasonRetry ReasonKind = "retry"
	// ReasonDrift: the host differs from what the plan would produce.
	ReasonDrift ReasonKind = "drift"
	// ReasonMapfileSync: mapped files are re-synced every cycle so a deleted or
	// hand-edited file is restored without counting as a variable change.
	ReasonMapfileSync ReasonKind = "mapfile-sync"
)

// maxReasonValue bounds a value rendered in a reason; certificates and keys
// would otherwise flood the log and the plan output.
const maxReasonValue = 120

// Reason says why a plan entry exists. Values in Old/New are redacted for
// secret keys and truncated, so a Reason is always safe to log and print.
type Reason struct {
	Kind    ReasonKind `json:"kind"`
	Section string     `json:"section,omitempty"`
	// Type and Key identify the lookup for var-changed.
	Type string `json:"type,omitempty"`
	Key  string `json:"key,omitempty"`
	// Old and New are the previous and current values (empty means unset).
	Old string `json:"old,omitempty"`
	New string `json:"new,omitempty"`
	// Service is the service a service-*, dependency or retry reason is about
	// (for dependency, the service whose restart caused this one).
	Service string `json:"service,omitempty"`
	// Detail is free text (drift description).
	Detail string `json:"detail,omitempty"`
}

// varChanged builds the reason for a changed required variable.
func varChanged(section, lookupType, key, oldVal, newVal string) Reason {
	return Reason{
		Kind:    ReasonVarChanged,
		Section: section,
		Type:    lookupType,
		Key:     key,
		Old:     displayValue(key, oldVal),
		New:     displayValue(key, newVal),
	}
}

func displayValue(key, val string) string {
	val = redact.ForLog(key, val)
	if len(val) > maxReasonValue {
		val = strings.ToValidUTF8(val[:maxReasonValue], "") + "..."
	}

	return val
}

// String renders the reason for humans, e.g.
// "section mta changed because VAR zimbraMtaRestriction changed 'a'->'b'".
func (r Reason) String() string {
	switch r.Kind {
	case ReasonFirstRun:
		return "first run: section " + r.Section + " rendered for the first time"
	case ReasonForced:
		return "section " + r.Section + " forced by REWRITE request"
	case ReasonRequested:
		return "section " + r.Section + " requested by REWRITE command"
	case ReasonEvaluated:
		return "section " + r.Section + " evaluated against the host state"
	case ReasonVarChanged:
		return fmt.Sprintf("section %s changed because %s %s changed %s->%s",
			r.Section, r.Type, r.Key, quoteValue(r.Old), quoteValue(r.New))
	case ReasonServiceEnabled:
		return "service " + r.Service + " was enabled"
	case ReasonServiceDisabled:
		return "service " + r.Service + " was disabled"
	case ReasonDependency:
		return "dependency of " + r.Service
	case ReasonRetry:
		return "retry: previous attempt did not complete"
	case ReasonDrift:
		return "drift: " + r.Detail
	case ReasonMapfileSync:
		return "mapped file kept in sync with the configuration"
	default:
		return string(r.Kind)
	}
}

func quoteValue(v string) string {
	if v == "" {
		return "<unset>"
	}

	return "'" + v + "'"
}

// Strings renders reasons as human text lines.
func Strings(reasons []Reason) []string {
	out := make([]string, len(reasons))
	for i := range reasons {
		out[i] = reasons[i].String()
	}

	return out
}

// appendReason appends r unless an identical reason is already present.
func appendReason(reasons []Reason, r Reason) []Reason { //nolint:gocritic // hugeParam: call sites pass literals
	if slices.Contains(reasons, r) {
		return reasons
	}

	return append(reasons, r)
}

// appendReasons appends every reason of add not already present.
func appendReasons(reasons, add []Reason) []Reason {
	for i := range add {
		reasons = appendReason(reasons, add[i])
	}

	return reasons
}
