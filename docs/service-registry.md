<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Service registry

Every service configd controls is one `ServiceDef` entry of the registry
(`defaultRegistry` in `internal/services/registry.go`). The entry is data: what
the service is, how it is launched, stopped and proven ready. A small set of
generic executors does the work, so adding or changing a service is a
declaration, not a new launcher.

## What an entry declares

| Concern | Fields |
|---|---|
| Identity | `Name`, `DisplayName`, `ProcessName` (exe/argv identity, never a cmdline substring), `PidFile` |
| systemd | `SystemdUnits`, `UseSystemdForStatus`, `ExternallyManaged` |
| Order | `Dependencies`, `Wants`, `Members` (composites: `store`, `antivirus`, `antispam`), `StopMembersForward`, `MemberApplies`, `EnableCheck` |
| Privilege | `NeedsRoot` (runs through `sudo`) |
| Restart policy | `Heavy` (a configuration change flags it restart-pending on systemd hosts instead of restarting it; `internal/plan.HeavyService` lists the same names and a test keeps both in step) |
| Configuration | `ConfigRewrite`, `PreStart` / `PostStart` / `PreStop` hooks |
| Legacy-mode launch | exactly one of the strategies below |
| Readiness | `Ready`, or `UseSDNotify` |

## Launch strategies (legacy, non-systemd mode)

`startWithoutSystemd` picks the first strategy the entry declares; an entry
declares at most one (`ServiceDef.validate`, checked for the whole registry by
`TestDefaultRegistry_EntriesAreValid`).

| Strategy | Used by | Start | Stop |
|---|---|---|---|
| `BinaryPath` (+ `BinaryArgs` or `ArgsFromConfig`, `Detached`, `LogFile`) | memcached, opendkim, saslauthd, nginx, clamd, ldap, configd, ... | spawn the binary; `Detached` gives it its own session and a log file | `GracefulStop` (SIGTERM, bounded wait, SIGKILL through the pidfile) or a kill by `ProcessName` |
| `Java` (`JavaSpec`) | mailbox, milter | java binary of localconfig, `Args`, log file, `Mkdirs`, `Touch` | kill by `ProcessName` |
| `Mysqld` (`MysqldSpec`) | appserver-db, antispam-db | `mysqld_safe` with the defaults file of localconfig | SIGTERM and a bounded wait, **never SIGKILL** (InnoDB crash recovery) |
| `Commands` (`CommandSpec`) | mta (`postfix`) | `Status` probe, `Prepare` steps, `Start` verb | `Stop` verb |
| `Collectors` (`CollectorSpec`) | stats (`zmstat-*`) | one process per collector | pidfiles (process group, sudo when root-owned), then identity |
| `CustomStart` / `CustomStop` | amavis | named hook | named hook |

`Ready` names the probe a launch waits for: `ReadyLDAP` (anonymous rootDSE
search, fails fast when the daemon exits), `ReadyConfigd` (`STATUS`),
`ReadyMailboxPort`. `UseSDNotify` waits for `READY=1` instead.

`CustomStart` / `CustomStop` are the escape hatch for logic no spec expresses:
amavis starts a companion daemon and clears stale temporary directories. Keep a
hook small and give it one job; reach for a spec field first.

## Adding a service

1. Add its `svc…` constant (`constants.go`) and an entry to `defaultRegistry`.
2. Declare the strategy; if none fits, extend the closest spec with a field
   rather than writing a launcher.
3. If it is heavy, set `Heavy` and add its names to `heavyServices` in
   `internal/plan/heavy.go`.
4. Run `go test ./internal/services`. `TestServiceTraces` records every legacy
   verb of every registered service (commands, signals, filesystem mutations)
   in `testdata/service_traces.golden`; a new service appears there, and any
   difference for an existing one is a behavior change to review.
   Regenerate with `go test -run TestServiceTraces -update-traces ./internal/services`.
