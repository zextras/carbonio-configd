// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/restart"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/snapshot"
	"github.com/zextras/carbonio-configd/internal/state"
)

// Observe completes base with the value of every lookup the parsed configd.cf
// depends on (the required variables of each section and the conditions of its
// if-blocks). This is the only lookup I/O of the diff: the plan is computed
// from the observed values, never by calling back into the manager.
//
// Missing and empty values are both recorded as "" (legacy treats an empty
// value as undefined). A MAPFILE is observed as the path it must have: the
// path when a value is configured and "" when it is not, independently of what
// is currently on disk, because restoring the file is an action of the plan.
func (cm *ConfigManager) Observe(
	ctx context.Context, base *snapshot.Snapshot, conf *config.MtaConfig,
) *snapshot.Snapshot {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")

	obs := make(map[snapshot.Lookup]string)

	for _, name := range slices.Sorted(maps.Keys(conf.Sections)) {
		section := conf.Sections[name]

		for key, lookupType := range section.RequiredVars {
			cm.observeOne(ctx, obs, lookupType, strings.TrimPrefix(key, "!"))
		}

		for i := range section.Conditionals {
			cond := &section.Conditionals[i]
			cm.observeOne(ctx, obs, cond.Type, strings.TrimPrefix(cond.Key, "!"))
		}
	}

	return base.WithObserved(obs)
}

// observeOne records the value of one lookup unless it is already known.
func (cm *ConfigManager) observeOne(ctx context.Context, obs map[snapshot.Lookup]string, lookupType, key string) {
	id := snapshot.Lookup{Type: lookupType, Key: key}
	if _, seen := obs[id]; seen {
		return
	}

	if lookupType == configTypeMAPFILE {
		obs[id] = cm.mapfilePath(key)

		return
	}

	value, err := cm.LookUpConfig(ctx, lookupType, key)
	if err != nil {
		value = ""
	}

	obs[id] = value
}

// mapfilePath returns the path a MAPFILE key must have: the mapped file when a
// value is configured, "" otherwise (or when the key is not a mapped file).
func (cm *ConfigManager) mapfilePath(key string) string {
	relPath, ok := state.MAPPEDFILES[key]
	if !ok || cm.mappedFileBase64(key) == "" {
		return ""
	}

	return cm.mainConfig.BaseDir + "/" + relPath
}

// Plan completes the current snapshot with the observed lookup values,
// publishes it and computes the plan of this cycle against the previous
// snapshot. It does not touch the host. On success the current snapshot
// becomes the baseline of the next cycle; when every service looks disabled
// (plan.ErrAllServicesDisabled) nothing is consumed, so the next cycle
// compares against the same baseline and the requested sections stay queued.
func (cm *ConfigManager) Plan(ctx context.Context) (*plan.Plan, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")
	logger.DebugContext(ctx, "Planning cycle")

	conf := cm.State.GetMtaConfig()
	cur := cm.Observe(ctx, cm.State.Snapshot(), conf)
	cm.State.Publish(cur)

	requested := cm.State.TakeRequestedConfigs()
	firstRun := cm.State.IsFirstRun()

	pl, err := plan.Compute(plan.Input{
		Prev:      cm.prev,
		Cur:       cur,
		Conf:      conf,
		Forced:    cm.State.ForcedSections(),
		Requested: requested,
		FirstRun:  firstRun,
		Tracked:   cm.tracked,
		Carry:     cm.carry,
	})
	if err != nil {
		// Keep the requests for the next attempt.
		cm.State.AddRequestedConfigs(ctx, requested)

		return nil, err
	}

	cm.prev = cur
	cm.tracked = pl.Tracked

	logPlan(ctx, &pl, firstRun)

	return &pl, nil
}

// logPlan reports the variable changes behind the plan, as the comparison
// used to (the first run only reports the sections it renders).
func logPlan(ctx context.Context, pl *plan.Plan, firstRun bool) {
	for i := range pl.Sections {
		for j := range pl.Sections[i].Reasons {
			logVarChange(ctx, &pl.Sections[i].Reasons[j], firstRun)
		}
	}

	for i := range pl.Restarts {
		r := &pl.Restarts[i]

		for j := range r.Reasons {
			logServiceToggle(ctx, r.Service, r.Reasons[j].Kind)
		}
	}
}

// logVarChange reports one variable-changed reason.
func logVarChange(ctx context.Context, r *plan.Reason, firstRun bool) {
	if r.Kind != plan.ReasonVarChanged {
		return
	}

	switch {
	case r.New == "":
		logger.InfoContext(ctx, "Variable changed to undefined",
			"key", r.Key,
			"previous_value", r.Old)
	case !firstRun:
		logger.InfoContext(ctx, "Variable changed",
			"key", r.Key,
			"previous_value", r.Old,
			"current_value", r.New)
	}
}

// logServiceToggle reports a service enabled/disabled restart reason.
func logServiceToggle(ctx context.Context, service string, kind plan.ReasonKind) {
	switch kind {
	case plan.ReasonServiceDisabled:
		logger.InfoContext(ctx, "Service was disabled, need to stop", "service", service)
	case plan.ReasonServiceEnabled:
		logger.InfoContext(ctx, "Service was enabled, need to start", "service", service)
	}
}

// Execute carries out the plan: the mapped files first (serially, as other
// steps read them), then proxygen, the template rewrites, postconf, postconfd
// and the LDAP writes concurrently. Restarts are not run here; see
// ApplyRestarts. What did not complete is remembered and re-included by the
// next Plan, so failed actions are retried until they succeed.
func (cm *ConfigManager) Execute(ctx context.Context, pl *plan.Plan) error {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")
	logger.DebugContext(ctx, "Executing config rewrites")

	var (
		wg      sync.WaitGroup
		pending plan.Carry
	)

	errChan := make(chan error, 6) // Buffer matches step count to prevent blocking

	// Mapped files first: legacy writes them while compiling actions
	// (state.py lookUpConfig MAPFILE), before any rewrite, and the other steps
	// read them: proxygen enables ssl_dhparam only if dhparam.pem exists.
	cm.healMapfiles(ctx, pl.Heal)

	if err := cm.doMapfile(ctx, pl.Mapfiles); err != nil {
		errChan <- err
	}

	// Proxygen takes longest, so launch it first. Each step owns one field of
	// pending; they are read only after wg.Wait.
	cm.runStep(ctx, &wg, errChan, func(ctx context.Context) error {
		var err error

		pending.Proxygen, err = cm.runProxygen(ctx, pl.Proxygen)

		return err
	})
	cm.runStep(ctx, &wg, errChan, func(ctx context.Context) error {
		var err error

		pending.Rewrites, err = cm.doRewrites(ctx, pl.Rewrites)

		return err
	})
	cm.runStep(ctx, &wg, errChan, func(ctx context.Context) error {
		var err error

		pending.Postconf, err = cm.doPostconf(ctx, pl.Postconf)

		return err
	})
	cm.runStep(ctx, &wg, errChan, func(ctx context.Context) error {
		var err error

		pending.Postconfd, err = cm.doPostconfd(ctx, pl.Postconfd)

		return err
	})
	cm.runStep(ctx, &wg, errChan, func(ctx context.Context) error {
		var err error

		pending.Ldap, err = cm.doLdap(ctx, pl.Ldap)

		return err
	})

	wg.Wait()
	close(errChan)

	// The restarts have not been consumed yet; ApplyRestarts updates them.
	pending.Restarts = pl.Restarts
	cm.carry = pending

	return cm.joinRewriteErrors(ctx, errChan)
}

// runProxygen regenerates the proxy configuration when the plan asks for it
// and returns the request again when it failed.
func (cm *ConfigManager) runProxygen(ctx context.Context, req *plan.Proxygen) (pending *plan.Proxygen, err error) {
	if req == nil {
		return pending, nil
	}

	logger.DebugContext(ctx, "Running proxygen")

	// Use the method that passes the loaded configs.
	if err := cm.RunProxygenWithConfigs(ctx); err != nil {
		return req, fmt.Errorf("proxygen failed: %w", err)
	}

	logger.DebugContext(ctx, "Proxygen executed successfully")

	return pending, nil
}

// ApplyRestarts hands the plan's restarts to the restart policy and remembers
// the ones it did not consume. It must run every cycle, after Execute, even
// when restarts are disabled or the plan has none: the policy also publishes
// the DEPENDS graph and flushes debounced requests.
func (cm *ConfigManager) ApplyRestarts(ctx context.Context, pl *plan.Plan) {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")

	cm.carry.Restarts = cm.policy.Apply(ctx, pl, restart.Settings{
		Enabled:  cm.mainConfig.RestartConfig,
		Debounce: debounceOf(cm.mainConfig),
	})
}

// SetDisableRestarts makes the manager that runs config-driven restarts skip
// every restart ("--disable-restarts").
func (cm *ConfigManager) SetDisableRestarts(disable bool) {
	if sm, ok := cm.ServiceMgr.(*services.ServiceManager); ok {
		sm.DisableRestarts = disable
	}
}

// debounceOf returns the restart debounce period configured in localconfig.
func debounceOf(cfg *config.Config) time.Duration {
	return time.Duration(cfg.RestartDebounce) * time.Second
}
