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
    compileSectionRestarts(...)
}
```

The skip is **section-scoped**, not cycle-wide:

- On the first run no section restarts anything.
- A section named by `configd rewrite <section>` (forced) or by a TCP `REWRITE` command
  (requested) is rewritten but its own services are not restarted. Restarting them would
  trigger the `ExecStartPre` REWRITE of the very service being restarted.
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

## Reasons

Every plan restart carries the reasons that caused it, and so does every other plan entry:

| Kind | Meaning |
|---|---|
| `first-run` | first cycle of the process |
| `forced` | section named on the command line |
| `requested` | section named by a REWRITE command |
| `var-changed` | a variable the section requires changed (`old->new`; secret values redacted, long values truncated) |
| `service-enabled` / `service-disabled` | the service changed state on this node |
| `dependency` | rendered as `dependency of <name>`, where `<name>` is the `DEPENDS` key that restarted: the service restarts only because a service it depends on does |
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

`configd plan` prints the same reasons without changing anything; see the README.

## Restart settings

Source: `internal/restart/policy.go` (`Policy.Apply`)

`ConfigManager.ApplyRestarts` runs `restart.Policy.Apply` once per cycle, even when the plan
has no restarts, because it also publishes the DEPENDS graph and flushes due debounced
requests.

| Setting | Effect |
|---|---|
| `zmconfigd_enable_config_restarts` (default `true`) | When `false` no plan restart is consumed; they are carried to the next cycle |
| `zmconfigd_restart_debounce_seconds` (default `0` = off) | When > 0, requests are coalesced across cycles: each request restarts the service's quiet window and the service restarts once no new request arrived for that many seconds (trailing edge). State is in memory and lost on process restart |
| `MaxFailedRestarts` (service manager, `3`) | Retry limit, see Dependency cascading |
| `--disable-restarts` | Dry-run: the service manager skips every restart |

**Deliberate correction.** Config-driven restarts now get the DEPENDS cascade and honour
`--disable-restarts`. Previously the config manager used a separate service manager that
never received the dependencies and never received `--disable-restarts`: a config change
restarted only the services named in the section and `--disable-restarts` did not stop them.
Both the daemon and the restart policy now use the single service manager that
`buildServiceDependencies` fills and `SetDisableRestarts` configures.

## Service control priority

Source: `internal/services/cli.go` (`ServiceStart`, `ServiceStop`, `ServiceReload`, `ServiceStatus`),
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
| `antispam` | `carbonio-antispam.service` |
| `antivirus` | `carbonio-antivirus.service` |
| `cbpolicyd` | `carbonio-policyd.service` |
| `ldap` | `carbonio-openldap.service` |
| `mailbox` | `carbonio-appserver-db.service`, `carbonio-appserver.service` |
| `memcached` | `carbonio-memcached.service` |
| `milter` | `carbonio-milter.service` |
| `mta` | `carbonio-postfix.service` |
| `opendkim` | `carbonio-opendkim.service` |
| `proxy` | `carbonio-nginx.service` |
| `saslauthd` | `carbonio-saslauthd.service` |
| `stats` | `carbonio-stats.service` |

Alternative names — including LDAP `zimbraServiceEnabled` values (`directory-server`,
`service`, `zmconfigd`) and legacy CLI aliases (`clamd`, `mailboxd`, `directory`,
`config-service`) — resolve to these canonical entries through the single
`services.ServiceAliases` table (`registry.go`), consumed by `LookupService` and, for the
LDAP-facing names, by `services.MapLDAPServiceToRegistry` (`discovery.go`).

## MTA: restart converted to reload

Source: `internal/services/cli.go` (`ServiceRestart`)

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
passed along in `plan.Plan.Dependencies`. A dependent restarted this way is queued with the
reason `dependency of <service>`; `configd plan` shows the predicted cascade as
`restart <service> (predicted cascade)`.

For each dependent service:

- If the dependent is `amavis`, it is always queued regardless of its `SERVICE_*` status.
- Otherwise, the `SERVICE_<UPPERCASE_NAME>` key is looked up in the merged config. The
  dependent is only queued if that key is truthy or equals `"enabled"`.

Dependencies are processed in `StartOrder` sequence (lower number = earlier):

```
ldap(0) → configd(10) → mailbox(50) → memcached(60) → proxy(70) →
amavis(75) → antispam(80) → antivirus(90) → opendkim(100) →
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
left queued without progress. The watchdog logs that as a failed restart and leaves the service
untracked until a later status check sees it running (legacy parity); configmgr logs it at ERROR.
Services with equal start order (including all those missing from `StartOrder`) restart in name order.
