// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package plan

import (
	"cmp"
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/snapshot"
)

// ErrAllServicesDisabled is returned when every tracked service is disabled,
// mirroring the legacy "all services detected disabled" guard: the cycle is
// skipped rather than stopping the whole node on a transient LDAP answer.
var ErrAllServicesDisabled = errors.New("all services detected disabled")

// Input is everything Compute depends on. Compute never mutates it.
type Input struct {
	// Prev is the snapshot of the previous cycle (nil on the first run).
	Prev *snapshot.Snapshot
	// Cur is the snapshot of this cycle, with observed lookup values.
	Cur *snapshot.Snapshot
	// Conf is the parsed configd.cf.
	Conf *config.MtaConfig
	// Forced are the sections named on the command line.
	Forced []string
	// Requested are the sections asked for by a REWRITE command.
	Requested []string
	// FirstRun marks the first cycle of the process.
	FirstRun bool
	// Tracked is the service tracking map returned by the previous Compute.
	Tracked map[string]string
	// Carry holds the actions the previous Execute did not complete.
	Carry Carry

	// Scope, when non-empty, limits the plan to these sections.
	Scope []string
	// AssumeChanged treats every considered section as changed. "configd
	// plan" uses it, together with Host, to evaluate sections against the
	// host state instead of against the previous snapshot.
	AssumeChanged bool
	// Host, when set, prunes the plan to the actions whose target differs
	// from what the host currently holds.
	Host *Host
}

// Host is the read-only observation of the host that "configd plan" compares
// the candidate plan against. Each map is keyed like the plan entries
// (rewrite source, postconf key, ...) and holds a human description of the
// difference; a missing key means the target is already in sync.
type Host struct {
	Mapfiles  map[string]string
	Rewrites  map[string]string
	Postconf  map[string]string
	Postconfd map[string]string
	// Ldap drift never marks its section as drifted (see pruneToHost).
	Ldap map[string]string
	// Proxygen describes the proxy configuration drift ("" when in sync).
	Proxygen string
}

// Compute derives the plan of a cycle. It is pure and deterministic.
func Compute(in Input) (Plan, error) { //nolint:gocritic // hugeParam: Input is a value object Compute never mutates
	b := newBuilder(&in)

	if err := b.guardAllDisabled(); err != nil {
		return Plan{}, err
	}

	b.processSections()
	b.trackServices()
	b.mergeCarry()
	b.pruneToHost()

	return b.finish(), nil
}

type mapfileKey struct {
	key   string
	local bool
}

type builder struct {
	in   *Input
	cur  *snapshot.Snapshot
	prev *snapshot.Snapshot

	forced, requested, scope map[string]bool

	sections  []Section
	mapfiles  map[mapfileKey]*Mapfile
	heal      map[string]*Mapfile
	rewrites  map[string]*Rewrite
	postconf  map[string]*Postconf
	postconfd map[string]*Postconfd
	ldap      map[string]*Ldap
	proxygen  *Proxygen
	restarts  map[string]*Restart
	tracked   map[string]string
}

func newBuilder(in *Input) *builder {
	return &builder{
		in:        in,
		cur:       in.Cur,
		prev:      in.Prev,
		forced:    toSet(in.Forced),
		requested: toSet(in.Requested),
		scope:     toSet(in.Scope),
		mapfiles:  map[mapfileKey]*Mapfile{},
		heal:      map[string]*Mapfile{},
		rewrites:  map[string]*Rewrite{},
		postconf:  map[string]*Postconf{},
		postconfd: map[string]*Postconfd{},
		ldap:      map[string]*Ldap{},
		restarts:  map[string]*Restart{},
		tracked:   maps.Clone(in.Tracked),
	}
}

func toSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}

	return set
}

// guardAllDisabled mirrors the legacy guard: with more than one tracked
// service, all of them disabled aborts the cycle.
func (b *builder) guardAllDisabled() error {
	if len(b.in.Tracked) <= 1 {
		return nil
	}

	for svc := range b.in.Tracked {
		if b.cur.ServiceEnabled(svc) {
			return nil
		}
	}

	return ErrAllServicesDisabled
}

// SectionEnabled reports whether a section is processed on a node where
// enabled(service) tells whether a service is enabled, mirroring the legacy
// getServiceMap + checkConditional("SERVICE", ...): a section gates on the
// service named in sectionServiceMap, or on a service of its own name, and a
// leading "!" negates the check.
func SectionEnabled(name string, enabled func(service string) bool) bool {
	svc, ok := sectionServiceMap[name]
	if !ok {
		svc = name
	}

	if rest, negated := strings.CutPrefix(svc, "!"); negated {
		return !enabled(rest)
	}

	return enabled(svc)
}

// sectionServiceMap maps a configd.cf SECTION name to the node service that
// must be enabled for the section to be processed. Ported verbatim from the
// legacy zmconfigd (jylibs/mtaconfig.py sectionMap).
var sectionServiceMap = map[string]string{
	"amavis":  "mta",
	"dhparam": "!dhparam",
	"sasl":    "mta",
	"webxml":  "mailbox",
	"nginx":   "proxy",
}

func (b *builder) inScope(name string) bool {
	return len(b.scope) == 0 || b.scope[name]
}

// processSections decides per section whether it is processed and gathers its
// actions, in sorted section order so conflicts between sections resolve the
// same way every time.
func (b *builder) processSections() {
	if b.in.Conf == nil {
		return
	}

	names := slices.Sorted(maps.Keys(b.in.Conf.Sections))
	for _, sn := range names {
		sec := b.in.Conf.Sections[sn]
		if sec == nil || !b.inScope(sn) || !SectionEnabled(sn, b.cur.ServiceEnabled) {
			continue
		}

		// Forced mode compares only the forced sections.
		compared := len(b.forced) == 0 || b.forced[sn]

		var changes []Reason
		if compared {
			changes = b.diffSection(sn, sec)
		}

		b.healMapfiles(sn, sec, compared)

		reasons := b.sectionReasons(sn, changes)
		if len(reasons) == 0 {
			continue
		}

		b.sections = append(b.sections, Section{Name: sn, Reasons: reasons})

		if len(changes) > 0 || b.in.FirstRun {
			b.mapfileOps(sn, sec, reasons)
		}

		b.compileSection(sn, sec, reasons)
	}
}

// sectionReasons collects why a section is processed; empty means it is not.
func (b *builder) sectionReasons(sn string, changes []Reason) []Reason {
	var reasons []Reason

	if b.forced[sn] {
		reasons = append(reasons, Reason{Kind: ReasonForced, Section: sn})
	}

	if b.requested[sn] {
		reasons = append(reasons, Reason{Kind: ReasonRequested, Section: sn})
	}

	if b.in.FirstRun {
		reasons = append(reasons, Reason{Kind: ReasonFirstRun, Section: sn})
	} else {
		reasons = append(reasons, changes...)
	}

	if b.in.AssumeChanged {
		reasons = append(reasons, Reason{Kind: ReasonEvaluated, Section: sn})
	}

	return reasons
}

// diffSection returns one var-changed reason per required lookup whose
// observed value differs between the previous and the current snapshot. An
// empty value is treated as undefined, exactly like the legacy comparison:
// a value turning empty only counts when it was set before.
func (b *builder) diffSection(sn string, sec *config.MtaConfigSection) []Reason {
	var reasons []Reason

	for _, key := range slices.Sorted(maps.Keys(sec.RequiredVars)) {
		lookupType := sec.RequiredVars[key]
		lookupKey := strings.TrimPrefix(key, "!")

		prevVal, _ := b.prev.Observed(lookupType, lookupKey)
		curVal, _ := b.cur.Observed(lookupType, lookupKey)

		if curVal == "" {
			if prevVal != "" {
				reasons = append(reasons, varChanged(sn, lookupType, lookupKey, prevVal, ""))
			}

			continue
		}

		if prevVal != curVal {
			reasons = append(reasons, varChanged(sn, lookupType, lookupKey, prevVal, curVal))
		}
	}

	return reasons
}

// healMapfiles registers the per-cycle mapped-file sync for every MAPFILE
// variable of a compared section (legacy lookUpConfig(MAPFILE) wrote the file
// as a side effect of every comparison).
func (b *builder) healMapfiles(sn string, sec *config.MtaConfigSection, compared bool) {
	if !compared {
		return
	}

	for _, key := range slices.Sorted(maps.Keys(sec.RequiredVars)) {
		if sec.RequiredVars[key] != snapshot.TypeMapFile {
			continue
		}

		lookupKey := strings.TrimPrefix(key, "!")

		m := b.heal[lookupKey]
		if m == nil {
			m = &Mapfile{Key: lookupKey}
			b.heal[lookupKey] = m
		}

		m.Sections = appendUnique(m.Sections, sn)
		m.Reasons = appendReason(m.Reasons, Reason{Kind: ReasonMapfileSync, Section: sn, Key: lookupKey})
	}
}

// mapfileOps registers the mapped-file syncs of a changed (or first-run)
// section.
func (b *builder) mapfileOps(sn string, sec *config.MtaConfigSection, reasons []Reason) {
	for _, key := range slices.Sorted(maps.Keys(sec.RequiredVars)) {
		lookupType := sec.RequiredVars[key]
		if lookupType != snapshot.TypeMapFile && lookupType != snapshot.TypeMapLocal {
			continue
		}

		lookupKey := strings.TrimPrefix(key, "!")
		mk := mapfileKey{key: lookupKey, local: lookupType == snapshot.TypeMapLocal}

		m := b.mapfiles[mk]
		if m == nil {
			m = &Mapfile{Key: lookupKey, Local: mk.local}
			b.mapfiles[mk] = m
		}

		m.Sections = appendUnique(m.Sections, sn)
		m.Reasons = appendReasons(m.Reasons, reasons)
	}
}

// compileSection gathers the actions of a processed section.
func (b *builder) compileSection(sn string, sec *config.MtaConfigSection, reasons []Reason) {
	for _, src := range slices.Sorted(maps.Keys(sec.Rewrites)) {
		entry := sec.Rewrites[src]

		r := b.rewrites[src]
		if r == nil {
			r = &Rewrite{Source: src}
			b.rewrites[src] = r
		}

		r.Target, r.Mode = entry.Value, entry.Mode
		r.Sections = appendUnique(r.Sections, sn)
		r.Reasons = appendReasons(r.Reasons, reasons)
	}

	for _, key := range slices.Sorted(maps.Keys(sec.Postconf)) {
		b.setPostconf(key, sec.Postconf[key], sn, reasons)
	}

	for _, key := range slices.Sorted(maps.Keys(sec.Postconfd)) {
		b.setPostconfd(key, sec.Postconfd[key], sn, reasons)
	}

	if sec.Proxygen {
		if b.proxygen == nil {
			b.proxygen = &Proxygen{}
		}

		b.proxygen.Sections = appendUnique(b.proxygen.Sections, sn)
		b.proxygen.Reasons = appendReasons(b.proxygen.Reasons, reasons)
	}

	for _, key := range slices.Sorted(maps.Keys(sec.Ldap)) {
		// An empty LDAP value means "no change" (legacy curLdap).
		if sec.Ldap[key] == "" {
			continue
		}

		l := b.ldap[key]
		if l == nil {
			l = &Ldap{Key: key}
			b.ldap[key] = l
		}

		l.Value = sec.Ldap[key]
		l.Sections = appendUnique(l.Sections, sn)
		l.Reasons = appendReasons(l.Reasons, reasons)
	}

	for i := range sec.Conditionals {
		b.compileConditional(sn, &sec.Conditionals[i], reasons)
	}

	// Restarts are not triggered on first run, nor for a section that was
	// forced or requested: that restart would trigger the ExecStartPre REWRITE
	// of the very service being restarted. The skip is section-scoped; a
	// collateral section whose variables changed still restarts its services.
	if !b.in.FirstRun && !b.forced[sn] && !b.requested[sn] {
		b.compileSectionRestarts(sn, sec, reasons)
	}
}

func (b *builder) setPostconf(key, val, sn string, reasons []Reason) {
	p := b.postconf[key]
	if p == nil {
		p = &Postconf{Key: key}
		b.postconf[key] = p
	}

	p.Value = strings.ReplaceAll(val, "\n", " ")
	p.Sections = appendUnique(p.Sections, sn)
	p.Reasons = appendReasons(p.Reasons, reasons)
}

func (b *builder) setPostconfd(key, val, sn string, reasons []Reason) {
	p := b.postconfd[key]
	if p == nil {
		p = &Postconfd{Key: key}
		b.postconfd[key] = p
	}

	p.Value = strings.ReplaceAll(val, "\n", " ")
	p.Sections = appendUnique(p.Sections, sn)
	p.Reasons = appendReasons(p.Reasons, reasons)
}

// compileConditional applies the POSTCONF/POSTCONFD lines of an if/fi block
// whose condition holds. A failed lookup is the legacy "false", then negated.
func (b *builder) compileConditional(sn string, cond *config.Conditional, reasons []Reason) {
	if !b.conditionHolds(cond) {
		return
	}

	for _, key := range slices.Sorted(maps.Keys(cond.Postconf)) {
		b.setPostconf(key, cond.Postconf[key], sn, reasons)
	}

	for _, key := range slices.Sorted(maps.Keys(cond.Postconfd)) {
		b.setPostconfd(key, cond.Postconfd[key], sn, reasons)
	}
}

// conditionHolds evaluates "if [!]TYPE key": the "!" on the key negates the
// truthiness, a failed lookup yields the negation flag alone, and cond.Negated
// inverts the result.
func (b *builder) conditionHolds(cond *config.Conditional) bool {
	key := cond.Key
	negate := false

	if rest, ok := strings.CutPrefix(key, "!"); ok {
		negate, key = true, rest
	}

	var holds bool

	if val, ok := b.cur.Value(cond.Type, key); ok {
		holds = config.IsTrueValue(val) != negate
	} else {
		holds = negate
	}

	if cond.Negated {
		holds = !holds
	}

	return holds
}

// compileSectionRestarts derives the restart/stop entries of a changed
// section from its RESTART services.
func (b *builder) compileSectionRestarts(sn string, sec *config.MtaConfigSection, reasons []Reason) {
	for _, svc := range slices.Sorted(maps.Keys(sec.Restarts)) {
		name := strings.ToLower(svc)

		switch {
		case b.cur.ServiceEnabled(name):
			b.addRestart(name, ActionRestart, sn, reasons)
		case name == "archiving":
			// archiving is never stopped here when disabled, mirroring
			// jylibs/state.py ("archiving not enabled, skipping stop").
		case name == "opendkim" && b.cur.ServiceEnabled("mta"):
			// With the MTA enabled opendkim is restarted rather than stopped.
			b.addRestart(name, ActionRestart, sn, reasons)
		default:
			b.addRestart(name, ActionStop, sn, reasons)
		}
	}
}

func (b *builder) addRestart(service string, action Action, section string, reasons []Reason) {
	r := b.restarts[service]
	if r == nil {
		r = &Restart{Service: service}
		b.restarts[service] = r
	}

	r.Action = action
	if section != "" {
		r.Sections = appendUnique(r.Sections, section)
	}

	r.Reasons = appendReasons(r.Reasons, reasons)
}

// trackServices queues stops for tracked services that are no longer enabled
// and starts for services that became enabled, and extends the tracking map
// on first run. Section-derived restarts of the same service take precedence,
// as they did when they were recorded after the status changes.
func (b *builder) trackServices() {
	if b.tracked == nil {
		b.tracked = map[string]string{}
	}

	stopped := map[string]Restart{}

	for _, svc := range slices.Sorted(maps.Keys(b.tracked)) {
		if !b.cur.ServiceEnabled(svc) {
			stopped[svc] = Restart{
				Service: svc, Action: ActionStop,
				Reasons: []Reason{{Kind: ReasonServiceDisabled, Service: svc}},
			}
		}
	}

	for _, svc := range b.cur.ServiceNames() {
		if _, known := b.tracked[svc]; known {
			continue
		}

		if b.in.FirstRun {
			if config.IsTrueValue(b.cur.ServiceValue(svc)) {
				b.tracked[svc] = "running"
			} else {
				b.tracked[svc] = "stopped"
			}

			continue
		}

		stopped[svc] = Restart{
			Service: svc, Action: ActionStart,
			Reasons: []Reason{{Kind: ReasonServiceEnabled, Service: svc}},
		}
	}

	for _, svc := range slices.Sorted(maps.Keys(stopped)) {
		st := stopped[svc]

		if have := b.restarts[svc]; have != nil {
			// A section-derived entry wins; keep the status reason too.
			have.Reasons = appendReasons(have.Reasons, st.Reasons)

			continue
		}

		b.restarts[svc] = &st
	}
}

// mergeCarry re-includes the actions the previous Execute did not complete.
// A freshly computed action of the same key wins, as the legacy accumulated
// map was overwritten by the new compile.
func (b *builder) mergeCarry() {
	retry := Reason{Kind: ReasonRetry}
	c := &b.in.Carry

	for i := range c.Rewrites {
		it := c.Rewrites[i]
		if have := b.rewrites[it.Source]; have != nil {
			have.Reasons = appendReason(have.Reasons, retry)

			continue
		}

		it.Reasons = appendReason(slices.Clone(it.Reasons), retry)
		b.rewrites[it.Source] = &it
	}

	for i := range c.Postconf {
		it := c.Postconf[i]
		if have := b.postconf[it.Key]; have != nil {
			have.Reasons = appendReason(have.Reasons, retry)

			continue
		}

		it.Reasons = appendReason(slices.Clone(it.Reasons), retry)
		b.postconf[it.Key] = &it
	}

	for i := range c.Postconfd {
		it := c.Postconfd[i]
		if have := b.postconfd[it.Key]; have != nil {
			have.Reasons = appendReason(have.Reasons, retry)

			continue
		}

		it.Reasons = appendReason(slices.Clone(it.Reasons), retry)
		b.postconfd[it.Key] = &it
	}

	for i := range c.Ldap {
		it := c.Ldap[i]
		if have := b.ldap[it.Key]; have != nil {
			have.Reasons = appendReason(have.Reasons, retry)

			continue
		}

		it.Reasons = appendReason(slices.Clone(it.Reasons), retry)
		b.ldap[it.Key] = &it
	}

	if c.Proxygen != nil {
		if b.proxygen == nil {
			p := *c.Proxygen
			b.proxygen = &p
		}

		b.proxygen.Reasons = appendReason(slices.Clone(b.proxygen.Reasons), retry)
	}

	for i := range c.Restarts {
		it := c.Restarts[i]
		if have := b.restarts[it.Service]; have != nil {
			have.Reasons = appendReason(have.Reasons, retry)

			continue
		}

		it.Reasons = appendReason(slices.Clone(it.Reasons), retry)
		b.restarts[it.Service] = &it
	}
}

// drifted records the sections that own a drifted action.
type drifted map[string]bool

// keep turns a drift description into the reasons of the action that fixes it,
// remembering the sections behind it; ok is false when the target is in sync.
func (d drifted) keep(drift string, sections []string) (reasons []Reason, ok bool) {
	if drift == "" {
		return nil, false
	}

	for _, s := range sections {
		d[s] = true
	}

	return []Reason{{Kind: ReasonDrift, Detail: drift}}, true
}

// pruneToHost keeps only the actions whose target the host observation says
// is out of date, replacing their reasons with the drift description.
func (b *builder) pruneToHost() {
	h := b.in.Host
	if h == nil {
		return
	}

	touched := drifted{}

	b.pruneMapfiles(h, touched)
	b.pruneFiles(h, touched)
	b.pruneDirectives(h, touched)

	b.sections = slices.DeleteFunc(b.sections, func(s Section) bool { return !touched[s.Name] })

	// Restarts are organic consequences of a drifted section only.
	for svc, r := range b.restarts {
		if !slices.ContainsFunc(r.Sections, func(s string) bool { return touched[s] }) {
			delete(b.restarts, svc)

			continue
		}

		r.Reasons = []Reason{{Kind: ReasonDrift, Detail: "configuration of section " +
			strings.Join(r.Sections, ", ") + " would change"}}
	}
}

// pruneMapfiles keeps the mapped files that drifted; heal entries that
// drifted become regular mapfile writes.
func (b *builder) pruneMapfiles(h *Host, touched drifted) {
	for key, m := range b.heal {
		if reasons, ok := touched.keep(h.Mapfiles[key], m.Sections); ok {
			mk := mapfileKey{key: key}
			if have := b.mapfiles[mk]; have != nil {
				have.Reasons = reasons
			} else {
				m.Reasons = reasons
				b.mapfiles[mk] = m
			}
		}
	}

	b.heal = map[string]*Mapfile{}

	for mk, m := range b.mapfiles {
		if reasons, ok := touched.keep(h.Mapfiles[mk.key], m.Sections); ok {
			m.Reasons = reasons
		} else {
			delete(b.mapfiles, mk)
		}
	}
}

// pruneFiles keeps the rewrites and the proxygen run that drifted.
func (b *builder) pruneFiles(h *Host, touched drifted) {
	for k, r := range b.rewrites {
		if reasons, ok := touched.keep(h.Rewrites[k], r.Sections); ok {
			r.Reasons = reasons
		} else {
			delete(b.rewrites, k)
		}
	}

	if b.proxygen != nil {
		if reasons, ok := touched.keep(h.Proxygen, b.proxygen.Sections); ok {
			b.proxygen.Reasons = reasons
		} else {
			b.proxygen = nil
		}
	}
}

// pruneDirectives keeps the postconf, postconfd and LDAP directives that
// drifted.
func (b *builder) pruneDirectives(h *Host, touched drifted) {
	for k, p := range b.postconf {
		if reasons, ok := touched.keep(h.Postconf[k], p.Sections); ok {
			p.Reasons = reasons
		} else {
			delete(b.postconf, k)
		}
	}

	for k, p := range b.postconfd {
		if reasons, ok := touched.keep(h.Postconfd[k], p.Sections); ok {
			p.Reasons = reasons
		} else {
			delete(b.postconfd, k)
		}
	}

	// LDAP drift never marks a section as drifted: the attributes live in
	// cn=config, which cannot be read back, so they are reported
	// unconditionally and must not make the restarts of their section
	// look needed.
	for k, l := range b.ldap {
		if reasons, ok := touched.keep(h.Ldap[k], nil); ok {
			l.Reasons = reasons
		} else {
			delete(b.ldap, k)
		}
	}
}

// finish sorts everything and assembles the Plan.
func (b *builder) finish() Plan {
	p := Plan{
		FirstRun:  b.in.FirstRun,
		Sections:  b.sections,
		Heal:      []Mapfile{},
		Mapfiles:  []Mapfile{},
		Rewrites:  []Rewrite{},
		Postconf:  []Postconf{},
		Postconfd: []Postconfd{},
		Ldap:      []Ldap{},
		Restarts:  []Restart{},
		Tracked:   b.tracked,
		Enabled:   b.cur.ServiceNames(),
	}

	if p.Sections == nil {
		p.Sections = []Section{}
	}

	for _, key := range slices.Sorted(maps.Keys(b.heal)) {
		if b.hasMapfile(key) {
			continue
		}

		p.Heal = append(p.Heal, *b.heal[key])
	}

	p.Mapfiles = collectSorted(b.mapfiles, p.Mapfiles, func(a, c *Mapfile) int {
		return cmp.Or(strings.Compare(a.Key, c.Key), boolCmp(a.Local, c.Local))
	})
	p.Rewrites = collectSorted(b.rewrites, p.Rewrites, func(a, c *Rewrite) int {
		return strings.Compare(a.Source, c.Source)
	})
	p.Postconf = collectSorted(b.postconf, p.Postconf, func(a, c *Postconf) int {
		return strings.Compare(a.Key, c.Key)
	})
	p.Postconfd = collectSorted(b.postconfd, p.Postconfd, func(a, c *Postconfd) int {
		return strings.Compare(a.Key, c.Key)
	})
	p.Ldap = collectSorted(b.ldap, p.Ldap, func(a, c *Ldap) int {
		return strings.Compare(a.Key, c.Key)
	})
	p.Restarts = collectSorted(b.restarts, p.Restarts, func(a, c *Restart) int {
		return strings.Compare(a.Service, c.Service)
	})

	if b.proxygen != nil {
		pg := *b.proxygen
		p.Proxygen = &pg
	}

	return p
}

func (b *builder) hasMapfile(key string) bool {
	_, remote := b.mapfiles[mapfileKey{key: key}]

	return remote
}

func boolCmp(a, b bool) int {
	switch {
	case a == b:
		return 0
	case !a:
		return -1
	default:
		return 1
	}
}

// collectSorted appends the values of m to dst, ordered by compare.
func collectSorted[K comparable, V any](m map[K]*V, dst []V, compare func(a, b *V) int) []V {
	ptrs := make([]*V, 0, len(m))
	for _, v := range m {
		ptrs = append(ptrs, v)
	}

	slices.SortFunc(ptrs, compare)

	for _, v := range ptrs {
		dst = append(dst, *v)
	}

	return dst
}

func appendUnique(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}

	return append(list, s)
}
