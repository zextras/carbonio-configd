// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/mtaops"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/redact"
	"github.com/zextras/carbonio-configd/internal/state"
)

// ErrUnknownSection is returned by PlanHost for a section that configd.cf
// does not define on this node.
var ErrUnknownSection = errors.New("unknown section")

// PlanHost computes what a forced rewrite of scope (every section when empty)
// would change on this host, without changing anything: it renders the
// templates in memory and reads the postfix configuration, and the plan keeps
// only the actions whose target differs from the result. The configuration must
// be loaded and parsed, as for a forced rewrite.
//
// It performs read-only host access only (file reads and stats, "postconf -h"
// and "postconf -n"): no file is written, no postfix parameter is set, no LDAP
// attribute is modified and no service is restarted.
func (cm *ConfigManager) PlanHost(ctx context.Context, scope []string) (*plan.Plan, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")

	conf := cm.State.GetMtaConfig()

	for _, name := range scope {
		if _, ok := conf.Sections[name]; !ok {
			return nil, fmt.Errorf("%w %q (known: %s)", ErrUnknownSection, name,
				strings.Join(slices.Sorted(maps.Keys(conf.Sections)), ", "))
		}
	}

	cur := cm.Observe(ctx, cm.State.Snapshot(), conf)

	tracked := make(map[string]string)
	for _, svc := range cur.ServiceNames() {
		tracked[svc] = "running"
	}

	in := plan.Input{
		Prev:          cur,
		Cur:           cur,
		Conf:          conf,
		Tracked:       tracked,
		Scope:         scope,
		AssumeChanged: true,
	}

	candidate, err := plan.Compute(in)
	if err != nil {
		return nil, fmt.Errorf("compute candidate plan: %w", err)
	}

	host, err := cm.observeHost(ctx, &candidate)
	if err != nil {
		return nil, err
	}

	in.Host = host

	// Proxygen output is not rendered here: its target is considered out of
	// date when another action of one of its sections is.
	pruned, err := plan.Compute(in)
	if err != nil {
		return nil, fmt.Errorf("compute plan: %w", err)
	}

	if candidate.Proxygen != nil {
		host.Proxygen = proxygenDrift(candidate.Proxygen, &pruned)

		if pruned, err = plan.Compute(in); err != nil {
			return nil, fmt.Errorf("compute plan: %w", err)
		}
	}

	return &pruned, nil
}

// proxygenDrift explains why proxygen would run, or returns "" when none of
// its sections has a drifted action.
func proxygenDrift(candidate *plan.Proxygen, pruned *plan.Plan) string {
	touched := make(map[string]bool)

	for i := range pruned.Sections {
		touched[pruned.Sections[i].Name] = true
	}

	for _, s := range candidate.Sections {
		if touched[s] {
			return "proxy configuration depends on section " + s + ", which would change " +
				"(generated proxy files are not rendered by plan)"
		}
	}

	return ""
}

// observeHost compares every action of the candidate plan with the host.
func (cm *ConfigManager) observeHost(ctx context.Context, cand *plan.Plan) (*plan.Host, error) {
	host := &plan.Host{
		Mapfiles:  map[string]string{},
		Rewrites:  map[string]string{},
		Postconf:  map[string]string{},
		Postconfd: map[string]string{},
		Ldap:      map[string]string{},
	}

	for _, m := range slices.Concat(cand.Heal, cand.Mapfiles) {
		if drift := cm.mapfileDrift(m); drift != "" {
			host.Mapfiles[m.Key] = drift
		}
	}

	if err := cm.observeActionDrift(ctx, cand, host); err != nil {
		return nil, err
	}

	// The cn=config attributes written through LDAP cannot be read back, so
	// they are always listed.
	for i := range cand.Ldap {
		host.Ldap[cand.Ldap[i].Key] = "cn=config value is not read by plan; the attribute would be written"
	}

	return host, nil
}

// observeActionDrift fills the rewrite, postconf and postconfd drift of host.
func (cm *ConfigManager) observeActionDrift(ctx context.Context, cand *plan.Plan, host *plan.Host) error {
	for i := range cand.Rewrites {
		r := &cand.Rewrites[i]

		drift, err := cm.rewriteDrift(ctx, r)
		if err != nil {
			return err
		}

		if drift != "" {
			host.Rewrites[r.Source] = drift
		}
	}

	for i := range cand.Postconf {
		p := &cand.Postconf[i]

		drift, err := cm.postconfDrift(ctx, p)
		if err != nil {
			return err
		}

		if drift != "" {
			host.Postconf[p.Key] = drift
		}
	}

	for i := range cand.Postconfd {
		key := cand.Postconfd[i].Key

		_, set, err := cm.mtaExecutor.ReadPostconf(ctx, key, true)
		if err != nil {
			return fmt.Errorf("read postconf %s: %w", key, err)
		}

		if set {
			host.Postconfd[key] = "main.cf sets " + key + ", which would be removed"
		}
	}

	return nil
}

// mapfileDrift describes how the mapped file of m differs from the configured
// value ("" when in sync). Local mapped files are only stat-ed by the
// executor, so they never drift.
func (cm *ConfigManager) mapfileDrift(m plan.Mapfile) string {
	relPath, known := state.MAPPEDFILES[m.Key]
	if m.Local || !known {
		return ""
	}

	path := cm.mainConfig.BaseDir + "/" + relPath

	want, err := base64.StdEncoding.DecodeString(cm.mappedFileBase64(m.Key))
	if err != nil {
		return ""
	}

	have, readErr := cm.env.FS.ReadFile(path)

	switch {
	case len(want) == 0 && readErr == nil:
		return path + " exists but its value is unset; it would be removed"
	case len(want) == 0:
		return ""
	case readErr != nil:
		return path + " is missing; it would be written"
	case !bytes.Equal(have, want):
		return path + " differs from " + m.Key + "; it would be rewritten"
	}

	return ""
}

// rewriteDrift renders the template of r in memory and describes how the
// target file differs from it ("" when content and mode already match).
func (cm *ConfigManager) rewriteDrift(ctx context.Context, r *plan.Rewrite) (string, error) {
	destPath := cm.mainConfig.BaseDir + "/" + r.Target

	rendered, mode, err := cm.RenderRewrite(ctx, r)
	if err != nil {
		return "", err
	}

	info, err := cm.env.FS.Stat(destPath)
	if err != nil {
		return destPath + " is missing; it would be written", nil //nolint:nilerr // a missing target is drift
	}

	have, err := cm.env.FS.ReadFile(destPath)
	if err != nil {
		return destPath + " is unreadable; it would be rewritten", nil //nolint:nilerr // an unreadable target is drift
	}

	switch {
	case !bytes.Equal(have, rendered):
		return destPath + " content differs from the rendered template", nil
	case info.Mode().Perm() != mode.Perm():
		return fmt.Sprintf("%s mode %04o would become %04o", destPath, info.Mode().Perm(), mode.Perm()), nil
	}

	return "", nil
}

// RenderRewrite renders the template of r in memory, exactly as the executor
// would write it, and returns the content and the file mode of the target. It
// reads the template and the lookups only: nothing is written.
func (cm *ConfigManager) RenderRewrite(
	ctx context.Context, r *plan.Rewrite,
) (content []byte, mode os.FileMode, err error) {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")

	mode, err = rewriteFileMode(r.Mode)
	if err != nil {
		return nil, 0, err
	}

	var out strings.Builder

	if _, err = cm.rewriteTransform(ctx, cm.mainConfig.BaseDir+"/"+r.Source, &out); err != nil {
		return nil, 0, fmt.Errorf("render %s: %w", r.Source, err)
	}

	return []byte(out.String()), mode, nil
}

// postconfDrift compares the effective value of the postfix parameter with the
// value the directive resolves to ("" when they match).
func (cm *ConfigManager) postconfDrift(ctx context.Context, p *plan.Postconf) (string, error) {
	resolved, err := cm.resolveValueSpec(ctx, p.Key, p.Value)
	if err != nil {
		return "value cannot be resolved: " + err.Error(), nil //nolint:nilerr // Execute would fail on it too
	}

	want := mtaops.NormalizePostconfValue(normalizePostconfBool(p.Value, resolved))

	have, _, err := cm.mtaExecutor.ReadPostconf(ctx, p.Key, false)
	if err != nil {
		return "", fmt.Errorf("read postconf %s: %w", p.Key, err)
	}

	if strings.Join(strings.Fields(have), " ") == strings.Join(strings.Fields(want), " ") {
		return "", nil
	}

	return fmt.Sprintf("main.cf %s = %q would become %q", p.Key,
		redact.ForLog(p.Key, have), redact.ForLog(p.Key, want)), nil
}
