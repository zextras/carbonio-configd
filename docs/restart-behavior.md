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

Source: `internal/services/manager.go` (`executeSystemdCommand`, `ControlProcess`)

When a systemd environment is detected (Carbonio systemd targets present), the service
manager sets `UseSystemd = true` and uses a two-tier control path:

1. **systemctl** (preferred) — invoked directly; the polkit policy installed at
   `build/configd/` grants the `zextras` user permission to manage `carbonio-*` units
   without sudo.
2. **`zm*ctl` fallback** — if `systemctl` fails, the corresponding script under
   `/opt/zextras/bin/` is tried.

When systemd is not detected (`UseSystemd = false`), only the `zm*ctl` scripts are used.

**Service name → control script mapping** (used when `UseSystemd = false` or as fallback):

| Service name | Script |
|---|---|
| `amavis` | `zmamavisdctl` |
| `antispam` | `zmantispamctl` |
| `antivirus` | `zmclamdctl` |
| `cbpolicyd` | `zmcbpolicydctl` |
| `ldap` | `ldap` |
| `mailbox` | `zmstorectl` |
| `mailboxd` | `zmmailboxdctl` |
| `memcached` | `zmmemcachedctl` |
| `mta` | `zmmtactl` |
| `opendkim` | `zmopendkimctl` |
| `proxy` | `zmproxyctl` |
| `sasl` | `zmsaslauthdctl` |
| `stats` | `zmstatctl` |

**Service name → systemd unit mapping** (used when `UseSystemd = true`):

| Service name | Unit |
|---|---|
| `amavis` | `carbonio-mailthreat.service` |
| `antispam` | `carbonio-antispam.service` |
| `antivirus` | `carbonio-antivirus.service` |
| `cbpolicyd` | `carbonio-policyd.service` |
| `ldap` | `carbonio-openldap.service` |
| `mailbox` | `carbonio-appserver.service` |
| `memcached` | `carbonio-memcached.service` |
| `milter` | `carbonio-milter.service` |
| `mta` | `carbonio-postfix.service` |
| `opendkim` | `carbonio-opendkim.service` |
| `proxy` | `carbonio-nginx.service` |
| `sasl` | `carbonio-saslauthd.service` |
| `stats` | `carbonio-stats.service` |

## MTA: restart converted to reload

Source: `internal/services/manager.go` (`ControlProcess`)

Any restart request for the `mta` service is silently converted to a `reload` action:

```go
if service == serviceMTA && action == ActionRestart {
    actionStr = actionReload
}
```

This triggers a Postfix graceful reload (`postfix reload`) rather than a full stop/start,
which preserves in-flight mail delivery. Hard restarts of Postfix are never performed by
configd.

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
