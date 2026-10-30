// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package metrics keeps configd's operational counters and gauges in memory
// and renders them in the Prometheus text exposition format, for the
// node_exporter textfile collector. There is no network listener: the file is
// written only when localconfig zmconfigd_metrics_textfile names a path.
//
// Every method of a nil *Registry is a no-op, so components that were not
// handed a registry (tests, standalone commands) record nothing.
package metrics

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Metric names. Counters end in _total, durations in _seconds.
const (
	cycleDuration    = "configd_cycle_duration_seconds"
	cyclePhase       = "configd_cycle_phase_duration_seconds"
	cyclesTotal      = "configd_cycles_total"
	lastCycle        = "configd_last_cycle_timestamp_seconds"
	planActions      = "configd_plan_actions"
	planActionsTotal = "configd_plan_actions_total"
	restartRequests  = "configd_restart_requests_total"
	watchdogRestarts = "configd_watchdog_restarts_total"
	loadFailures     = "configd_load_failures_total"
)

type metricType string

const (
	counter metricType = "counter"
	gauge   metricType = "gauge"
)

// family is one metric name with its HELP/TYPE and labelled samples.
type family struct {
	help    string
	typ     metricType
	samples map[string]float64 // rendered label set -> value
}

// catalog describes every metric the registry can hold.
var catalog = map[string]struct {
	help string
	typ  metricType
}{
	cycleDuration:    {"Wall-clock duration of the last completed configd cycle in seconds.", gauge},
	cyclePhase:       {"Duration of one phase of the last completed configd cycle in seconds.", gauge},
	cyclesTotal:      {"Number of completed configd cycles since the process started.", counter},
	lastCycle:        {"Unix time at which the last configd cycle completed.", gauge},
	planActions:      {"Actions in the plan of the last completed cycle, by kind.", gauge},
	planActionsTotal: {"Actions planned since the process started, by kind.", counter},
	restartRequests: {
		"Service restarts queued by the restart policy, by service and reason kind; " +
			"a restart with several reasons counts once under each kind.", counter,
	},
	watchdogRestarts: {"Restarts attempted by the service watchdog, by service and result.", counter},
	loadFailures: {
		"Failed configuration loads, by source (lc, gc, mc, sc loaders, timeout, mta_config).", counter,
	},
}

// Registry is a set of metrics safe for concurrent use.
type Registry struct {
	mu       sync.Mutex
	families map[string]*family
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{families: make(map[string]*family)}
}

// label is one name="value" pair of a sample.
type label struct{ name, value string }

// labelSet renders labels sorted by name as {a="1",b="2"} ("" without labels).
func labelSet(labels ...label) string {
	if len(labels) == 0 {
		return ""
	}

	slices.SortFunc(labels, func(a, b label) int { return strings.Compare(a.name, b.name) })

	var b strings.Builder

	b.WriteByte('{')

	for i, l := range labels {
		if i > 0 {
			b.WriteByte(',')
		}

		b.WriteString(l.name)
		b.WriteString(`="`)
		b.WriteString(escapeLabelValue(l.value))
		b.WriteByte('"')
	}

	b.WriteByte('}')

	return b.String()
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func escapeLabelValue(v string) string { return labelEscaper.Replace(v) }

func (r *Registry) family(name string) *family {
	f := r.families[name]
	if f == nil {
		meta := catalog[name]
		f = &family{help: meta.help, typ: meta.typ, samples: make(map[string]float64)}
		r.families[name] = f
	}

	return f
}

func (r *Registry) add(name string, delta float64, labels ...label) {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.family(name).samples[labelSet(labels...)] += delta
}

func (r *Registry) set(name string, v float64, labels ...label) {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.family(name).samples[labelSet(labels...)] = v
}

// Phase is the duration of one named step of a cycle.
type Phase struct {
	Name     string
	Duration time.Duration
}

// CycleDone records a completed cycle: its total duration, the duration of
// each phase and the time it finished.
func (r *Registry) CycleDone(total time.Duration, phases []Phase, at time.Time) {
	if r == nil {
		return
	}

	r.set(cycleDuration, total.Seconds())

	for _, p := range phases {
		r.set(cyclePhase, p.Duration.Seconds(), label{"phase", p.Name})
	}

	r.add(cyclesTotal, 1)
	r.set(lastCycle, float64(at.Unix()))
}

// PlanSize records the number of actions of the cycle's plan by kind. Every
// kind present in counts is exported, zero included, so a series does not
// vanish when a plan is empty.
func (r *Registry) PlanSize(counts map[string]int) {
	if r == nil {
		return
	}

	for kind, n := range counts {
		r.set(planActions, float64(n), label{"kind", kind})
		r.add(planActionsTotal, float64(n), label{"kind", kind})
	}
}

// RestartRequested records a restart queued for service, once under each of
// the given reason kinds (or "unknown" when there is none).
func (r *Registry) RestartRequested(service string, reasonKinds []string) {
	if r == nil {
		return
	}

	if len(reasonKinds) == 0 {
		reasonKinds = []string{"unknown"}
	}

	for _, kind := range reasonKinds {
		r.add(restartRequests, 1, label{"service", service}, label{"reason", kind})
	}
}

// WatchdogRestart records a restart attempt of the service watchdog.
func (r *Registry) WatchdogRestart(service string, ok bool) {
	result := "success"
	if !ok {
		result = "failure"
	}

	r.add(watchdogRestarts, 1, label{"service", service}, label{"result", result})
}

// LoadFailed records a failed configuration load from source.
func (r *Registry) LoadFailed(source string) {
	r.add(loadFailures, 1, label{"source", source})
}

// Render returns the registry in the Prometheus text exposition format: one
// HELP and TYPE line per metric, metrics and samples sorted, so equal state
// renders to identical bytes.
func (r *Registry) Render() []byte {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	names := make([]string, 0, len(r.families))
	for name := range r.families {
		names = append(names, name)
	}

	slices.Sort(names)

	var b strings.Builder

	for _, name := range names {
		f := r.families[name]

		b.WriteString("# HELP " + name + " " + f.help + "\n")
		b.WriteString("# TYPE " + name + " " + string(f.typ) + "\n")

		sets := make([]string, 0, len(f.samples))
		for ls := range f.samples {
			sets = append(sets, ls)
		}

		slices.Sort(sets)

		for _, ls := range sets {
			b.WriteString(name + ls + " " + formatValue(f.samples[ls]) + "\n")
		}
	}

	return []byte(b.String())
}

// formatValue renders integral values without an exponent (timestamps, counts)
// and everything else with the shortest round-tripping representation.
func formatValue(v float64) string {
	const maxExactInt = 1 << 53

	if v == math.Trunc(v) && math.Abs(v) < maxExactInt {
		return strconv.FormatInt(int64(v), 10)
	}

	return strconv.FormatFloat(v, 'g', -1, 64)
}
