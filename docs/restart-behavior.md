<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Restart Behavior

## REWRITE commands vs. automatic restarts

Source: `internal/configmgr/manager_actions.go` (`compileSectionActions`)

Service restarts only happen during automatic polling cycles where config changes are
detected. They do **not** happen when a REWRITE command triggers the cycle.

When the TCP server receives a `REWRITE` command (sent by `configd rewrite <section>...`
or directly via netcat), it calls `State.AddRequestedConfigs`, which populates
`RequestedConfig`. In `compileSectionActions`, the restart-compilation block is guarded by:

```go
if firstRun || len(forcedConfig) > 0 || len(requestedConfigs) > 0 {
    return  // skip restarts
}
```

So a REWRITE cycle rewrites config files and runs postconf/LDAP operations, but no service
is restarted. This makes `configd rewrite proxy` safe to run on a live system — nginx
config files are updated but nginx itself is not touched.

Automatic restarts happen when `RequestedConfig` is empty, `ForcedConfig` is empty, and
it is not the first run. This means only organic polling cycles that detect a config
change can trigger restarts.

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
   of shelling out to the old `/opt/zextras/bin/zm*ctl` scripts.

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
in `zmconfigd.cf` sections, loaded by `buildServiceDependencies` at the start of each loop.

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
service is removed from the queue and processing continues.
