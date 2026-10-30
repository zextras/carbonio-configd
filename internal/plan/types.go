// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package plan is the pure middle of a configd cycle:
//
//	Load -> Snapshot -> Diff -> Plan -> Execute
//
// Compute is a deterministic function of the previous and current immutable
// snapshots (internal/snapshot), the parsed configd.cf, the forced/requested
// sections and whether this is the first run. It performs no I/O and returns a
// Plan: every rewrite, mapfile write, postconf/postconfd edit, LDAP write and
// service restart the cycle must carry out, each with the Reasons that caused
// it. Execution (internal/configmgr) and the restart policy (internal/restart)
// only consume the Plan.
package plan

// Action is what a Restart entry asks of a service.
type Action string

// Restart actions. The executor treats every entry as a restart (legacy
// behavior); the action documents why the entry exists.
const (
	ActionRestart Action = "restart"
	ActionStop    Action = "stop"
	ActionStart   Action = "start"
)

// Plan is the complete set of effects of one cycle. Every slice is non-nil and
// sorted, so equal inputs give deep-equal plans and identical JSON.
type Plan struct {
	// FirstRun marks the first cycle of the process: every enabled section is
	// rendered and no service is restarted for it.
	FirstRun bool `json:"first_run"`
	// Sections are the sections processed this cycle, with their reasons.
	Sections []Section `json:"sections"`
	// Heal are the mapped files re-synced every cycle so a deleted or
	// hand-edited file is restored without counting as a variable change.
	// They do not make a plan non-empty; "configd plan" reports a drifted
	// one as a regular Mapfiles entry.
	Heal []Mapfile `json:"heal"`
	// Mapfiles are the mapped-file syncs of changed sections. Heal and
	// Mapfiles run first, serially.
	Mapfiles []Mapfile `json:"mapfiles"`
	// Rewrites are the template renders.
	Rewrites []Rewrite `json:"rewrites"`
	// Postconf are the "postconf -e" settings.
	Postconf []Postconf `json:"postconf"`
	// Postconfd are the "postconf -X" removals.
	Postconfd []Postconfd `json:"postconfd"`
	// Ldap are the LDAP attribute writes.
	Ldap []Ldap `json:"ldap"`
	// Proxygen asks for the proxy configuration to be regenerated.
	Proxygen *Proxygen `json:"proxygen,omitempty"`
	// Restarts are the services to restart (or stop/start) directly.
	Restarts []Restart `json:"restarts"`
	// Tracked is the service tracking map after this cycle (service ->
	// "running"/"stopped"); the caller feeds it to the next Compute.
	Tracked map[string]string `json:"-"`
	// Enabled is the sorted list of enabled services of the current
	// snapshot, used by the restart policy to answer the service manager's
	// "is this service enabled" lookups.
	Enabled []string `json:"-"`
}

// IsEmpty reports whether the plan carries no effect at all.
func (p *Plan) IsEmpty() bool {
	return len(p.Mapfiles) == 0 && len(p.Rewrites) == 0 && len(p.Postconf) == 0 &&
		len(p.Postconfd) == 0 && len(p.Ldap) == 0 && p.Proxygen == nil && len(p.Restarts) == 0
}

// Section is a section the cycle processes.
type Section struct {
	Name    string   `json:"name"`
	Reasons []Reason `json:"reasons"`
}

// Mapfile syncs one mapped file (MAPFILE) or only checks a local one
// (MAPLOCAL). The content is resolved from the snapshot at execution time.
type Mapfile struct {
	Key      string   `json:"key"`
	Local    bool     `json:"local,omitempty"`
	Sections []string `json:"sections"`
	Reasons  []Reason `json:"reasons"`
}

// Rewrite renders the template Source to Target.
type Rewrite struct {
	Source   string   `json:"source"`
	Target   string   `json:"target"`
	Mode     string   `json:"mode,omitempty"`
	Sections []string `json:"sections"`
	Reasons  []Reason `json:"reasons"`
}

// Postconf sets the postfix parameter Key. Value is the value spec as written
// in configd.cf (e.g. "VAR:zimbraMtaMyNetworks"), resolved at execution time.
type Postconf struct {
	Key      string   `json:"key"`
	Value    string   `json:"value"`
	Sections []string `json:"sections"`
	Reasons  []Reason `json:"reasons"`
}

// Postconfd removes (or resets) the postfix parameter Key; see Postconf.
type Postconfd struct {
	Key      string   `json:"key"`
	Value    string   `json:"value"`
	Sections []string `json:"sections"`
	Reasons  []Reason `json:"reasons"`
}

// Ldap writes the LDAP attribute Key from the value spec Value.
type Ldap struct {
	Key      string   `json:"key"`
	Value    string   `json:"value"`
	Sections []string `json:"sections"`
	Reasons  []Reason `json:"reasons"`
}

// Proxygen regenerates the proxy configuration.
type Proxygen struct {
	Sections []string `json:"sections"`
	Reasons  []Reason `json:"reasons"`
}

// Restart asks the restart policy to act on Service.
type Restart struct {
	Service  string   `json:"service"`
	Action   Action   `json:"action"`
	Sections []string `json:"sections,omitempty"`
	Reasons  []Reason `json:"reasons"`
}

// Carry holds the actions of a previous Execute that did not complete. The
// next Compute re-includes them, which keeps the legacy "retry until it
// succeeds" behavior now that the plan is not accumulated in State.
type Carry struct {
	Rewrites  []Rewrite
	Postconf  []Postconf
	Postconfd []Postconfd
	Ldap      []Ldap
	Proxygen  *Proxygen
	Restarts  []Restart
}

// IsEmpty reports whether nothing is carried.
func (c *Carry) IsEmpty() bool {
	return len(c.Rewrites) == 0 && len(c.Postconf) == 0 && len(c.Postconfd) == 0 &&
		len(c.Ldap) == 0 && c.Proxygen == nil && len(c.Restarts) == 0
}
