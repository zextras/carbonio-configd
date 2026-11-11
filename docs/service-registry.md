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
| Configuration | `ConfigRewrite`, `Setup` steps, `PreStart` / `PostStart` / `PreStop` hooks |
| Helper daemons | `Companions` (process identity and, to launch it, the binary), `Direct` |
| Legacy-mode launch | exactly one of the strategies below |
| Readiness | `Ready`, or `UseSDNotify` |

## Launch strategies (legacy, non-systemd mode)

`startWithoutSystemd` picks the first strategy the entry declares; an entry
declares at most one (checked for the whole registry by
`TestDefaultRegistry_EntriesAreValid`).

| Strategy | Used by | Start | Stop |
|---|---|---|---|
| `BinaryPath` (+ `BinaryArgs` or `ArgsFromConfig`, `Detached`, `LogFile`) | memcached, opendkim, saslauthd, nginx, clamd, ldap, configd, ... | spawn the binary; `Detached` gives it its own session and a log file | `GracefulStop` (SIGTERM, bounded wait, SIGKILL through the pidfile) or a kill by `ProcessName` |
| `Java` (`JavaSpec`) | mailbox, milter | java binary of localconfig, `Args`, log file, `Mkdirs`, `Touch` | kill by `ProcessName` |
| `Mysqld` (`MysqldSpec`) | appserver-db, antispam-db | `mysqld_safe` with the defaults file of localconfig | SIGTERM and a bounded wait, **never SIGKILL** (InnoDB crash recovery) |
| `Commands` (`CommandSpec`) | mta (`postfix`) | `Status` probe, `Prepare` steps, `Start` verb | `Stop` verb |
| `Collectors` (`CollectorSpec`) | stats (`zmstat-*`) | one process per collector | pidfiles (process group, sudo when root-owned), then identity |
| `BinaryPath` + `Direct` (`DirectSpec`) + `Companions` | amavis | `Companions` through sudo, skip when running, `Prepare` steps, spawn, bounded wait for the process | `ProcessName`, then each companion |
| `CustomStart` / `CustomStop` | none today | named hook | named hook |

`Ready` names the probe a launch waits for: `ReadyLDAP` (anonymous rootDSE
search, fails fast when the daemon exits), `ReadyConfigd` (`STATUS`),
`ReadyMailboxPort`. `UseSDNotify` waits for `READY=1` instead.

## Steps

The work around a launch is an ordered list of `Step`s (`spec.go`, run by
`steps.go`). A step does exactly one thing; a failing step stops the sequence
(a best-effort `Run` is only logged):

| Step | Does | Used by |
|---|---|---|
| `Mkdir` | create directories (0755) | clamd, amavis, cbpolicyd |
| `RemoveStale` | remove the sub-directories of `Dir` named `Prefix*` | amavis (`tmp/amavis-*`) |
| `Run` | run a command, with `Sudo`, `IfExists` (skip quietly when the path is absent), `SkipOnNoRewrite` (`norewrite` starts), `BestEffort` | amavis (`zmaltermimeconfig`), mta (`postalias`), cbpolicyd (database init) |
| `Hook` | a named function, see below | mta |

`UnlessExists` makes any step a one-time initialization (the cbpolicyd database).
The lists live in `ServiceDef.Setup` (every start path, before the `PreStart`
hooks), `CommandSpec.Prepare` (before the `Start` verb of a stopped service) and
`DirectSpec.Prepare` (between the companions and the spawn).

## Procedural exceptions

What cannot be data stays a small named function with one job. The list is
closed: `TestDefaultRegistry_ProceduralExceptions` fails when a hook appears or
disappears, so growing it is a decision taken here.

| Where | Function | Why it is not data |
|---|---|---|
| mta `Prepare` | `prepareMainCf` | Seeds `main.cf` from localconfig: the owner and group are validated (`postfixAccountNameRe`) before anything runs, and are applied (creating `main.cf`) through the root helper `postfix-conf`: `common/conf` is root-owned and there is no sudo grant for `touch` or `postconf` ([systemd.md](systemd.md#postfix-configuration-is-root-owned)). |
| mta `Prepare` | `prepareLDAPTables` | Renders the eight `ldap-*.cf` tables from localconfig (URL, port, bind password, STARTTLS rule), writes them atomically at 0640 and chgrps them to the postfix group when it resolves. |
| mailbox `PostStart` | `MailboxAdvancedStatusHook` | Polls the Carbonio Advanced modules through the admin API until they are ready (legacy `advanced_status 2`), best effort. |
| appserver-db `PreStop` | `appserverDBFlushHook` | Flushes InnoDB before the stop, in both orchestration modes, by polling the database for its dirty pages. |
| `ServiceDef.CustomStart` / `CustomStop` | none | Escape hatch for a launch no spec expresses. No entry uses it; prefer a spec. |
| `Java.Args`, `ArgsFromConfig`, `MemberApplies`, `EnableCheck`, `MysqldSpec.Active` | the builders and predicates | Computed from localconfig, LDAP or the host. |

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
