<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Restart Behavior

## When a section triggers restarts

Source: `internal/plan/compute.go` (`compileSection`, `compileSectionRestarts`)

`plan.Compute` decides, section by section, which services the plan restarts. A section
that is processed (first run, forced, requested, or one of its required variables changed)
compiles its `RESTART` services into plan restarts **unless** the section itself is
excluded:

```go
if !firstRun && !forced[section] && !requested[section] {
    compileSectionRestarts(restarts, ...)        // restart now
} else if !firstRun && hasVarChange(section) {
    compileSectionRestarts(deferred, ...)        // restart in a later non-REWRITE cycle
}
```

The skip is **section-scoped**, not cycle-wide:

- On the first run no section restarts anything.
- A section named by `configd rewrite <section>` (forced) or by a TCP `REWRITE` command
  (requested) is rewritten but its own services are not restarted in that cycle. Restarting
  them would trigger the `ExecStartPre` REWRITE of the very service being restarted. If the
  section also had a real variable change, the restart is deferred to the next periodic cycle
  instead of being lost (see below).
- Any other section in the same cycle whose variables changed still restarts its services.
  A cycle that carries a REWRITE request for `proxy` therefore still restarts `mta` if an
  `mta` variable changed in that cycle.

So `configd rewrite proxy` is safe on a live system: nginx configuration is rewritten but
nginx is not touched by that request.

For each `RESTART` service of a restarting section the plan records:

- `restart` when the service is enabled on the node;
- `opendkim`: `restart` when the MTA is enabled, even if opendkim itself is not;
- `archiving`: never stopped when disabled;
- `stop` for any other disabled service.

Services that became enabled or disabled since the previous cycle add `start`/`stop`
entries with the reasons `service-enabled`/`service-disabled`.

On a **systemd host** (a Carbonio systemd target is enabled, `IsSystemdMode()`) the plan also
classifies each `restart` entry (`plan.Classify`, input `plan.Input.Systemd`): a restart of a
**heavy service** becomes the action `pending-restart` (see
[Systemd hosts](#systemd-hosts-hybrid-restart-policy)). `configd plan` and its JSON show it as a
distinct action:

```
pending-restart mailboxd
    because: section mailbox changed because VAR zimbraX changed 'a'->'b'
restart      memcached
```

### A change consumed by a REWRITE cycle: deferred restarts

The skip above must not swallow a real configuration change. A variable change is detected
once: the cycle that sees it stores the new value as the baseline of the next cycle. If that
very cycle is a REWRITE of the section (for example `zmprov ms <mta> zimbraMtaMyNetworks ...`
followed at once by `configd rewrite mta amavis`), the files are rewritten, the change is
consumed, and no later cycle would ever restart the services that must reload the new
configuration.

So when a forced or requested section **also has a `var-changed` reason**, `plan.Compute`
does not drop its restarts; it reports them in `Plan.DeferredRestarts` (JSON
`deferred_restarts`, shown by `configd plan` as `deferred` lines) with the reasons of the real
change plus a `deferred` reason. The daemon (`ConfigManager.Plan`, `holdDeferred`) then:

1. logs once per section, at WARN, why:

   ```
   level=WARN msg="Config changed in a REWRITE cycle; restart deferred to next cycle" section=amavis services="[amavis antivirus mta]" reasons="[section amavis changed because VAR zimbraMtaMyNetworks changed 'a'->'b']"
   ```

2. holds them in memory (merged per service across REWRITE cycles; a later REWRITE cycle never
   drops them);
3. in the next cycle that is **not** a REWRITE (no requested or forced section, not the first
   run) merges them into that plan's restarts through `plan.MergeRestarts` (the plan's own
   entry of a service wins, with the reasons of both) and logs an INFO line
   `Executing restart deferred from a REWRITE cycle` per service. From there they take the
   normal path: the hybrid systemd policy (heavy services are only flagged `pending-restart`,
   the others are try-reload-or-restart), or the service manager queue on legacy hosts. They
   leave the held set when merged into the plan; the plan's restarts are carried by `Execute`
   until `ApplyRestarts` consumes them, so a failed rewrite or disabled config restarts
   (`zmconfigd_enable_config_restarts=false`) do not lose them.

**No REWRITE loop.** A section that is forced or requested with **no** variable change defers
nothing, and that is the `ExecStartPre` REWRITE a unit sends when it starts. The deferred
restart that a periodic cycle runs triggers exactly such a REWRITE (the variable is already the
baseline), which restarts nothing and defers nothing, so the sequence ends after one restart.

**Limits.** The held restarts live in memory, like the carry and the debounce state: a daemon
restart forgets them (the new process starts with a first run, which re-renders every section
and restarts nothing). configd keeps no state file between runs (the snapshot baseline, the
carried actions and the debounce state are all in memory), so persisting this one set would add
a new on-disk format and failure mode to cover a window of one poll interval. A standalone
`configd rewrite` (no daemon listening) is a first run with no
baseline: it renders everything, has no variable change to consume and defers nothing.

## Reasons

Every plan restart carries the reasons that caused it, and so does every other plan entry:

| Kind | Meaning |
|---|---|
| `first-run` | first cycle of the process |
| `forced` | section named on the command line |
| `requested` | section named by a REWRITE command |
| `deferred` | rendered as `restart deferred from the REWRITE of section <name>`: the restart was held back from a REWRITE cycle of that section and runs in a later cycle; listed next to the `var-changed` reasons that caused it |
| `var-changed` | a variable the section requires changed (`old->new`; secret values redacted, long values truncated) |
| `service-enabled` / `service-disabled` | the service changed state on this node |
| `dependency` | rendered as `dependency of <name>`: the service manager restarted it because `<name>` was restarted through `DEPENDS` (watchdog restarts only, see Dependency cascading) |
| `retry` | the previous attempt did not complete |
| `mapfile-sync` | a mapped file is re-synced every cycle |

When a service is restarted, one INFO line is logged:

```
msg="Restarting service" service=amavis reasons=[...]
```

The `reasons` attribute holds the human-readable reasons, for example
`section mta changed because VAR zimbraMtaRestriction changed 'a'->'b'` or
`dependency of antivirus`. A service queued several times in one cycle is logged once with
the merged reasons.

`configd plan` prints the same reasons without changing anything; see [cli.md](cli.md) and the [README](../README.md).

## Systemd hosts: hybrid restart policy

Source: `internal/plan/heavy.go`, `internal/restart/policy.go` (`Policy.Apply`, `dispatch`),
`internal/services/systemd_restart.go`

How a plan entry is carried out depends on `IsSystemdMode()`:

| Host | `restart` of a light service | `restart` of a heavy service | `start` / `stop` |
|---|---|---|---|
| systemd | `systemctl try-reload-or-restart <units>` (one call with all units of the service; units that support reload reload, the others restart, a stopped unit stays stopped) | **never executed**, flagged *restart pending* | `systemctl start` / `systemctl stop` of the units |
| legacy (no systemd target: jammy, rocky-8, containers) | queued on the service manager: stop+start, `mta` converted to reload | same as light: restarted | queued as restarts (unchanged) |

**Heavy services** (`plan.HeavyService`, any alias): `mailbox` (`mailboxd`, `service`),
`appserver-db` (`mysql`, `mariadb`), `store` and `ldap` (`directory`, `directory-server`).
Restarting them interrupts every user or risks the data store, so a config change never does it
on a systemd host. The policy logs one INFO line per service and cycle and consumes the entry (it
is not carried):

```
level=INFO msg="Restart pending: service is not restarted automatically, restart it manually" service=mailboxd reasons=[...]
```

`zmconfigd_enable_config_restarts=false` still disables everything: no plan restart (pending
ones included) is consumed, they are carried to the next cycle, and nothing is logged or run.
`--disable-restarts` is a dry-run: nothing is executed through systemctl either.

A failing systemd action (`try-reload-or-restart`, `start`, `stop`) is carried and retried by the
next cycles, up to 3 attempts, then dropped with an ERROR line; the units' own `Restart=` handles
crashes. Debounce applies as for legacy restarts (the action is decided once the quiet period
elapsed). Systemd actions run in plan order (sorted by service name), not in start order; ordering
between units is systemd's (`After=`/`Requires=`).

## Restart settings

Source: `internal/restart/policy.go` (`Policy.Apply`)

`ConfigManager.ApplyRestarts` runs `restart.Policy.Apply` once per cycle, even when the plan
has no restarts, because it also flushes due debounced requests and lets the service
manager drain what it already holds.

| Setting | Effect |
|---|---|
| `zmconfigd_enable_config_restarts` (default `true`) | When `false` no plan restart is consumed; they are carried to the next cycle |
| `zmconfigd_restart_debounce_seconds` (default `0` = off) | When > 0, requests are coalesced across cycles: each request restarts the service's quiet window and the service restarts once no new request arrived for that many seconds (trailing edge). State is in memory and lost on process restart |
| `MaxFailedRestarts` (service manager, `3`) | Retry limit, see Dependency cascading |
| `--disable-restarts` | Dry-run: no restart is executed, on legacy (service manager) and systemd (systemctl) hosts |

**Config-driven restarts do not cascade.** The legacy zmconfigd queues the `DEPENDS` of a
service only after a successful stop or start, never after a restart, and a config change
restarts exactly the services of the changed sections, once each. The restart policy therefore
calls `ProcessRestarts` without a service-enabled lookup, which turns the cascade off for that
run (the `DEPENDS` map is set on the shared service manager for the watchdog), so a config
change restarts the services named by `RESTART` and nothing else.

**One service manager.** The restart policy and the watchdog share a single
`services.ServiceManager` (built in `cmd/configd/runtime.go` and passed to `configmgr.NewConfigManager`
and the watchdog). Its exported methods are safe for concurrent use and
`ProcessRestarts` serializes whole passes, so the two never restart the same service
concurrently and the main loop's `SetDependencies` does not race the watchdog's reads. When the
manager restarts a service it calls `BeforeRestart`, which makes the watchdog stop watching it
(see Watchdog).

**Deliberate correction.** `--disable-restarts` now also reaches the config-driven restarts;
previously only the watchdog honoured it and a config change still restarted services.

## Watchdog

Source: `internal/watchdog/watchdog.go`

- **systemd host:** the watchdog is a no-op (`Start`/`Stop` do nothing, one INFO line says so):
  the units' `Restart=` recovers crashed services.
- **legacy host:** unless `zmconfigd_watchdog=false`, every `zmconfigd_watchdog_interval` seconds (default 120) it probes the services of
  `zmconfigd_watchdog_services` (default `antivirus`) enabled on the node and restarts a tracked
  one that is down (`AddRestart` + `ProcessRestarts`, which cascades through `DEPENDS`).
  Tracking rules (legacy `state.py`/`configd.py`):
  - a restart run by the service manager (a config change, or the watchdog itself) untracks the
    service first (`BeforeRestart`), so the watchdog never queues a second restart while one runs;
  - a tick that sees a monitored service **running** tracks it again;
  - after the watchdog's own restart attempt, successful or not, the service is tracked again: a
    service still down after a failed restart is retried by the next tick (previously it was left
    untracked and never retried).
- The services watched are exactly the ones named by `zmconfigd_watchdog_services`; the watchdog
  carries no list of its own.
- `Stop` followed by `Start` never overlaps two loops: the new loop waits for the old one to exit
  before arming its ticker, and a stop wins over a simultaneous tick.

## Service control priority

Source: `internal/services/cli.go` (`Controller.ServiceStart`, `ServiceStop`, `ServiceReload`, `ServiceStatus`),
`internal/services/registry.go` (`ServiceDef`, `Registry`)

`services.ServiceManager.ControlProcess` (`internal/services/manager.go`) is a thin
dispatcher: it delegates every action to the same Registry-backed lifecycle functions the
CLI uses (`ServiceStart`/`ServiceStop`/`ServiceRestart`/`ServiceStatus`). There is a single
control path, not a per-instance flag — each of those functions bifurcates internally on
`IsSystemdMode()`:

1. **strict systemd** (`IsSystemdMode() == true`, i.e. a Carbonio systemd target is
   enabled) — every start/stop/status goes through `systemctl` against the units listed in
   `ServiceDef.SystemdUnits`. No direct binary spawn, no fallback script.
2. **pure legacy** (`IsSystemdMode() == false`) — direct binary execution
   (`ServiceDef.CustomStart`/`CustomStop`, then `BinaryPath`/`ProcessName`). `systemctl` is
   never invoked. There is no `zm*ctl` fallback: services whose legacy launch is too dynamic
   for a static `BinaryPath` (mailbox, stats, mta, ldap, milter, antispam, ...) get a native
   `CustomStart`/`CustomStop` implementation in the corresponding `*_launcher.go` file instead
   of shelling out to the old `/opt/zextras/bin/zm*ctl` scripts. Stops that are only a
   bounded SIGTERM wait (slapd/LMDB) are data too: `ServiceDef.GracefulStop`.

   **Process identity.** Legacy status and stop recognise a service's processes by what the
   kernel records, never by a cmdline substring (`internal/services/procident.go`). The
   `ServiceDef.PidFile` PID is believed only if it is alive and its `/proc/<pid>/exe` or argv
   matches `ServiceDef.ProcessName` (an exact path, glob over a whole path, Java main class or
   bare daemon name); otherwise `/proc` is scanned for the same identity (exe, then exact argv
   tokens, then `comm` only for processes with an empty argv). A process that only mentions
   the service (`tail -f .../clamd.log`, a consul sidecar `-sidecar-for carbonio-x`) never
   matches, and a stale pidfile naming a recycled PID is ignored. A service that is not
   running is `(false, nil)`. Every status probe of one `zmcontrol status` run or one
   watchdog tick shares a single process-table snapshot (`services.WithProcessSnapshot`);
   start/stop paths always read live state.

**Service name → systemd unit(s)** (`ServiceDef.SystemdUnits`, used when `IsSystemdMode()`):

| Service name | Unit(s) |
|---|---|
| `amavis` | `carbonio-mailthreat.service` |
| `antispam` (composite: `antispam-db` + `amavis`) | `carbonio-antispam.service`, `carbonio-mailthreat.service` |
| `antispam-db` | `carbonio-antispam.service` |
| `antivirus` (composite: `amavis` + `clamd` + `freshclam`) | `carbonio-mailthreat.service`, `carbonio-antivirus.service`, `carbonio-freshclam.service` |
| `appserver-db` | `carbonio-appserver-db.service` |
| `cbpolicyd` | `carbonio-policyd.service` |
| `clamd` | `carbonio-antivirus.service` |
| `freshclam` | `carbonio-freshclam.service` |
| `ldap` | `carbonio-openldap.service` |
| `mailbox` (the JVM only) | `carbonio-appserver.service` |
| `store` (composite: `appserver-db` + `mailbox`) | `carbonio-appserver-db.service`, `carbonio-appserver.service` |
| `memcached` | `carbonio-memcached.service` |
| `milter` | `carbonio-milter.service` |
| `mta` | `carbonio-postfix.service` |
| `opendkim` | `carbonio-opendkim.service` |
| `proxy` | `carbonio-nginx.service` |
| `saslauthd` | `carbonio-saslauthd.service` |
| `stats` | `carbonio-stats.service` |

Alternative names — including LDAP `zimbraServiceEnabled` values (`directory-server`,
`service`, `zmconfigd`) and legacy CLI aliases (`mailboxd`→`mailbox`,
`mysql`/`mariadb`→`appserver-db`, `directory`→`ldap`, `config-service`/`zmconfigd`→`configd`,
`service`→`store`) — resolve to these canonical entries through the single
`services.ServiceAliases` table (`registry.go`), consumed by `LookupService` and, for the
LDAP-facing names, by `services.MapLDAPServiceToRegistry` (`discovery.go`). The LDAP value
`mailbox` (the whole store host) maps to the `store` composite, not to the `mailbox` JVM.
`clamd` is a service of its own (`zmclamdctl`), not an alias of `antivirus`.

**Composites and components.** `store`, `antivirus` and `antispam` are composites
(`ServiceDef.Members`): `start` runs the members in order, `stop` in reverse, a failed
member stop does not prevent the others from stopping (the errors are joined), and
`status` reports the members. Their members (`appserver-db`, `mailbox`, `amavis`, `clamd`,
`freshclam`, `antispam-db`) are *components*: addressable with `configd service <verb>
<name>` but not iterated by `configd control` (`ControlServiceNames` excludes them).
`mailbox` declares `appserver-db` as `Wants`: a failing database start is logged and the JVM
start goes on, as legacy `zmmailboxdctl` does. `antispam` is idle (every verb a successful
no-op, silent `status`) when LDAP does not list `antispam` for this host.

**Safe MariaDB stop.** `appserver-db` (and `antispam-db`) stop with SIGTERM and a bounded wait
(`zimbra_mysql_shutdown_timeout`, default 60 s) and are never sent SIGKILL: a MariaDB killed
mid-flush needs InnoDB crash recovery. A server that outlives the wait is an error, and is
left running. Before the stop, `appserver-db` flushes InnoDB dirty pages
(`innodb_max_dirty_pages_pct=0`, polling `show engine innodb status` for up to 600 s) as a
`PreStop` hook, so both orchestration modes get it.

**Mailbox start.** Legacy mode and the systemd leaf wait for `zimbraMailPort` to accept
connections (zmprov, then TCP, up to 3 minutes). `service start mailbox` regenerates the
zmtlsctl set of configs (`sasl webxml mailbox service zextras zextrasAdmin zimlet`) unless
`norewrite` is given.

## CLI verbs

Full command reference: [cli.md](cli.md).

`configd service <verb> <name> [args...]` mirrors the legacy `zm*ctl` verbs; the core
wrappers run `exec configd service "${1:-}" <name> "${@:2}"`.

| Verb | Behavior |
|---|---|
| `start`, `restart`, `reload` | `norewrite` (or `--no-rewrite`) skips the config regeneration |
| `stop`, `kill` | stop (kill is a legacy alias) |
| `status` | stopped: `<name> is not running.` on stdout, exit 1, nothing on stderr |
| `refresh` | signals the service to re-read its config: `milter` gets SIGCONT; a stopped service prints `<name> is not currently running` (exit 0) |
| `rotate` | `stats`: SIGHUP to every collector in the stats pidfile directory; no pidfile → exit 1 |
| anything else | usage line on stderr, exit 1 |

Trailing arguments are accepted on every verb; only `norewrite` is interpreted. `ldap` on a
replica whose `ldap_url` equals `ldap_master_url` refuses every verb but `stop`/`kill` with
`ldap_url and ldap_master_url cannot be the same on an ldap replica`.

`configd control start` regenerates the configuration once for the union of the services it
is going to start, then starts each of them `norewrite`; it runs `zmresolverctl` before
`proxy`, and waits for the freshly started configd to report `SUCCESS ACTIVE`.
`configd control status` fails (exit 1) when a service listed in the localconfig key
`zmcontrol_service_status_list` is stopped — every service but `stats` and `logger` when the key
is empty. `configd control -H <host>` (remote control over SSH) no longer exists: `zmcontrol
-H` is the wrapper's job.

## MTA: restart converted to reload

Source: `internal/services/cli.go` (`Controller.ServiceRestart`)

Any restart request for the `mta` service is converted to a reload:

```go
if name == serviceMTA {
    ...
    return ServiceReload(ctx, name)
}
```

This triggers a Postfix graceful reload (`postfix reload` in legacy mode, `systemctl
reload` in strict-systemd mode) rather than a full stop/start, preserving in-flight mail
delivery. In strict-systemd mode, a reload failure (e.g. the unit was inactive) falls back
to a full stop+start so a dead MTA still ends up running; in legacy mode the native
`postfix reload` has no such fallback by design — see `ServiceReload` and
`reloadWithoutSystemd` (`cli_process.go`).

## Dependency cascading

Source: `internal/services/manager.go` (`AddDependencyRestarts`, `ProcessRestarts`)

After a service restarts successfully, the service manager checks whether that service has
any dependents registered in the `Dependencies` map. Dependents come from `DEPENDS` entries
in `zmconfigd.cf` sections, loaded by `buildServiceDependencies` at the start of each loop and
set on the shared service manager. A dependent restarted this way is queued with the
reason `dependency of <service>`. Only watchdog restarts cascade: restarts derived from a config
change run without the enabled-service lookup and never cascade, as in the legacy zmconfigd;
`configd plan` therefore lists exactly the services the sections name.

For each dependent service:

- If the dependent is `amavis`, it is always queued regardless of its `SERVICE_*` status.
- Otherwise, the `SERVICE_<UPPERCASE_NAME>` key is looked up in the merged config. The
  dependent is only queued if that key is truthy or equals `"enabled"`.

Dependencies are processed in `StartOrder` sequence (lower number = earlier):

```
ldap(0) → configd(10) → appserver-db(48) → store(49) → mailbox(50) → memcached(60) →
proxy(70) → amavis(75) → antispam-db(79) → antispam(80) → antivirus(90) → clamd(91) →
freshclam(92) → opendkim(100) →
cbpolicyd(120) → saslauthd(130) → milter(140) → mta(150) → stats(160)
```

Key dependency chains in a default Carbonio deployment (as encoded in `zmconfigd.cf`):

```
antivirus  → amavis → mta → opendkim
antispam   → amavis → mta
```

So when an antivirus config change is detected, the restart sequence can expand to:
antivirus → amavis → mta (reloaded, not restarted) → opendkim.

Failed restarts are retried up to `MaxFailedRestarts` times (default: 3). After that the
service is removed from the queue and processing continues. `ProcessRestarts` then returns an
error wrapping `ErrRestartsFailed` that lists, sorted by name, every service that was dropped or
left queued without progress. The watchdog logs that as a failed restart and keeps watching the service so the next tick
retries it; configmgr logs it at ERROR.
Services with equal start order (including all those missing from `StartOrder`) restart in name order.

**Shutdown.** When the context is cancelled (SIGTERM/SIGINT) while restarts are being processed,
`ProcessRestarts` stops at once: the interrupted restart is not retried and does not count toward
`MaxFailedRestarts`, no further service is restarted, and one INFO line
`Shutdown during service restarts` lists the pending services. The error it returns wraps the
context error (not `ErrRestartsFailed`), and neither the restart policy nor the watchdog log it as
an ERROR or count a failed restart. The legacy zmconfigd also stops immediately on these signals
(its handler calls `sys.exit(0)`).
