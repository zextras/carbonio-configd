// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// cmd/configd/ldapwatch.go
package main

import (
	"context"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/ldapwatch"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// ldapWatch owns the goroutine of a running LDAP change watcher. The zero
// value is a watcher that never started (see syncLDAPWatch); stop is nil-safe.
type ldapWatch struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// stop cancels the watcher and waits for its goroutine to exit.
func (w *ldapWatch) stop() {
	if w == nil || w.cancel == nil {
		return
	}

	w.cancel()
	<-w.done
}

// TriggerReload wakes the main loop for an immediate cycle. It is the reload
// path SIGHUP and the TCP REWRITE command use: a token on ReloadChan, dropped
// when one is already pending. No section is requested, so the woken cycle is
// a normal one, exactly as if the interval had elapsed.
func (t *MainLoopActionTrigger) TriggerReload(ctx context.Context, reason string) {
	ctx = logger.ContextWithComponent(ctx, "mainloop")

	select {
	case t.ReloadChan <- struct{}{}:
		logger.DebugContext(ctx, "Reload signal sent to main loop", "reason", reason)
	default:
		logger.DebugContext(ctx, "Reload channel blocked, main loop already processing or not ready",
			"reason", reason)
	}
}

// syncLDAPWatch makes the running watcher match zmconfigd_ldap_change_trigger.
// It is called once per loop iteration, after the configuration has been
// loaded: the first call with the key on starts the watcher, and turning the
// key off stops it. With the key off (the default) nothing is ever started.
// A change of the poll or debounce seconds takes effect on the next start.
//
// No watcher runs in single-run mode, which exits after one cycle.
func syncLDAPWatch(ctx context.Context, deps *daemonLoopDeps, cur *ldapWatch) *ldapWatch {
	enabled := deps.cfg.LdapChangeTrigger && !deps.args.Once

	switch {
	case enabled && cur == nil:
		return startLDAPWatch(ctx, deps)
	case !enabled && cur != nil:
		ctx = logger.ContextWithComponent(ctx, "ldapwatch")
		logger.InfoContext(ctx, "LDAP change watcher stopped", "reason", "disabled")

		cur.stop()

		return nil
	default:
		return cur
	}
}

// ldapWatchSource returns the source the watcher polls: the injected one, or
// the native LDAP client of the config manager. It returns nil when neither is
// available.
func ldapWatchSource(deps *daemonLoopDeps) ldapwatch.Source {
	if deps.ldapSource != nil {
		return deps.ldapSource
	}

	client := deps.configManager.NativeLdapClient
	if client == nil {
		return nil
	}

	return ldapwatch.NewLDAPSource(client, client.BaseDN())
}

// startLDAPWatch starts the watcher goroutine. When there is no LDAP client it
// logs a warning and returns a handle that never started, so the warning is
// not repeated every cycle.
func startLDAPWatch(ctx context.Context, deps *daemonLoopDeps) *ldapWatch {
	ctx = logger.ContextWithComponent(ctx, "ldapwatch")

	src := ldapWatchSource(deps)
	if src == nil {
		logger.WarnContext(ctx, "zmconfigd_ldap_change_trigger is on but there is no LDAP client, "+
			"LDAP change watcher not started (the regular interval still applies)")

		return &ldapWatch{}
	}

	interval := time.Duration(deps.cfg.LdapChangePollSeconds) * time.Second
	debounce := time.Duration(deps.cfg.LdapChangeDebounceSeconds) * time.Second

	w := ldapwatch.New(&ldapwatch.Options{
		Source:   src,
		Clock:    deps.env.Clock,
		Interval: interval,
		Debounce: debounce,
		Trigger: func(ctx context.Context, changed []string) {
			deps.trigger.TriggerReload(ctx, "ldap-change:"+strings.Join(changed, ","))
		},
	})

	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer recoverGoroutine(wctx, "ldap-watch")

		w.Run(wctx)
	}()

	logger.InfoContext(ctx, "LDAP change watcher started",
		"poll_seconds", deps.cfg.LdapChangePollSeconds,
		"debounce_seconds", deps.cfg.LdapChangeDebounceSeconds)

	return &ldapWatch{cancel: cancel, done: done}
}
