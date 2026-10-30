<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Architecture

## Startup sequence

Source: `cmd/configd/cli.go` (`DaemonCmd.Run`), `cmd/configd/configd.go`, `cmd/configd/mainloop.go`

1. **User check** — `security.MustCheckUserPermissions(stderr)` enforces that the process runs as
   `zextras`. Root is rejected.
2. **Logging init** — structured logger configured from `CONFIGD_LOG_FORMAT` and
   `CONFIGD_LOG_LEVEL` environment variables (defaults: text, info).
3. **sd_notify init** — `sdnotify.New()` opens the socket named by `NOTIFY_SOCKET`. If the
   variable is unset the notifier is a no-op; no code path fails because of its absence.
4. **Profiling/tracing setup** — CPU, memory, and trace profiles started if flags are present
   (profiling build only).
5. **Config init** — `config.NewConfig()` reads `localconfig.xml`, merges defaults, resolves
   `${variable}` references, and populates `state.State`. An LDAP client is constructed but
   not yet connected.
6. **Main loop** — `RunMainLoop` is called. It does not return until the process is asked to
   shut down.

Inside `RunMainLoop` before the first iteration:

- `configmgr.NewConfigManager` and `services.NewServiceManager(services.NewController(env))` are
  constructed. `env` is the `sys.System` built once in `main` (`sys.Real()`).
- `systemd.NewManager(env).IsSystemdEnabled` checks whether Carbonio systemd targets exist;
  the service manager itself gates on this internally via `Controller.IsSystemdMode()` — there
  is no per-instance mode flag to set.
- A watchdog goroutine is started.
- Signal handlers are registered (SIGTERM/SIGINT → cancel context; SIGHUP → reload channel).
- The systemd watchdog keep-alive goroutine is started when `WATCHDOG_USEC` is set, pinging
  at half the declared interval.

## Side-effect boundary (`internal/sys`)

Every host side effect that production code performs — running a program (`sys.Exec`), touching
the filesystem (`sys.FS`), inspecting or signalling processes (`sys.Proc`) and behavioural time
(`sys.Clock`: tickers, timers, sleeps, retry back-off) — goes through the interfaces bundled in
`sys.System`. `main` builds `sys.Real()` once and passes it through constructors and struct fields
(`configmgr.NewConfigManager`, `services.NewController`, `mtaops.NewExecutor`,
`proxy.NewGenerator`, `watchdog.Config.Clock`, the `Run` entry points of `internal/cli/*`); there
are no package-level seam variables.

Tests use the recording fakes in `internal/sys/systest` (`systest.New()`): an in-memory
filesystem, a scripted `Exec`, a process table and a manual clock, all writing to one ordered
`Recorder`, so a test can assert the exact sequence of side effects of a cycle (see
`internal/configmgr/do_config_rewrites_sequence_test.go`). Tests that are about real OS behaviour
pass `sys.Real()` with `t.TempDir()`.

Deliberately outside the boundary: network I/O (LDAP, the TCP 7171 listener, the sd_notify
datagram socket), `os.Getenv`, and `time.Now()` used only to log durations.

## Main loop phases

Source: `cmd/configd/mainloop.go` (`RunMainLoop`)

Each iteration runs the following phases in order:

| Phase | Function | Notes |
|---|---|---|
| Shutdown check | — | Checks context cancellation before starting work |
| Idle check | — | Only with `zmconfigd_skip_idle_polls=TRUE` (off by default, as legacy): skips to sleep if no REWRITE events and not first run and no reload signal |
| Load configs | `configManager.LoadAllConfigs` | Four concurrent goroutines; see Config loading pipeline below |
| Start listener | `network.NewThreadedStreamServer` | First run only; skipped in `--once` mode |
| Parse MTA config | `configManager.ParseMtaConfig` | Reads `conf/zmconfigd.cf` |
| Build dependencies | `buildServiceDependencies` | Extracts DEPENDS entries from MTA sections for the watchdog's service manager (config-driven restarts do not cascade, see restart-behavior.md) |
| Snapshot + Observe | `configManager.Plan` (first half) | Freezes the loaded configs into an immutable `snapshot.Snapshot`, resolves every lookup `configd.cf` requires and publishes the result |
| Plan | `plan.Compute` (via `configManager.Plan`) | Pure, deterministic: previous snapshot vs. current snapshot gives a `plan.Plan` whose every entry carries its reasons |
| Execute | `configManager.Execute` | Mapfiles first (self-heal included), then proxygen, rewrites, postconf, postconfd and LDAP concurrently |
| Apply restarts | `configManager.ApplyRestarts` | `restart.Policy.Apply`: runs every cycle; honours `zmconfigd_enable_config_restarts`, debounce and `--disable-restarts` |
| READY=1 | `notifier.Ready()` | Sent after every loop; first iteration transitions systemd from activating → active |
| Sleep | `SleepWithContext` | Default 300 s; woken early by SIGHUP or TCP REWRITE |

Forced mode (`configd rewrite`-style standalone runs) uses the same Plan and Execute and
never applies restarts.

On errors in the load or MTA-parse phases, the loop sleeps 60 s and retries rather than
exiting. On context cancellation mid-loop, the loop exits immediately after the current
phase completes.

## Network protocol

Source: `internal/network/server.go`

The daemon listens on `127.0.0.1:7171` (IPv4) or `[::1]:7171` (IPv6, when
`zimbraIPMode=ipv6`). The port is overridable via `zmconfigd_listen_port` in localconfig.

**Wire format**: one line per request, newline-terminated. Each connection carries exactly
one request and receives exactly one response; the connection is closed immediately after
the response.

```
REQUEST  = COMMAND [SPACE ARG]* NEWLINE
RESPONSE = RESULT_STRING NEWLINE
```

**Commands**:

| Command | Example | Response |
|---|---|---|
| `STATUS` | `STATUS\n` | `SUCCESS ACTIVE\n` |
| `REWRITE [section ...]` | `REWRITE proxy mta\n` | `SUCCESS REWRITES COMPLETE\n` |
| _(unknown)_ | `FOO\n` | `ERROR UNKNOWN COMMAND\n` |

`REWRITE` with no section arguments triggers a rewrite of all sections. With arguments,
only the named sections are rewritten.

**REWRITE skips restarts**: the TCP REWRITE command queues the named sections as
`requested`. `plan.Compute` still renders them, but compiles no service restart for a
requested section. Config files are rewritten but the section's services are not restarted.
See [restart-behavior.md](restart-behavior.md) for the full rule.

## Config loading pipeline

Source: `internal/configmgr/manager_load.go` (`LoadAllConfigs`)

Four goroutines run concurrently, one per config source:

| Goroutine name | Function | What it loads |
|---|---|---|
| `lc` | `LoadLocalConfig` | `localconfig.xml` via the Go native reader |
| `gc` | `LoadGlobalConfig` | Global config attributes via native LDAP client |
| `mc` | `LoadMiscConfig` | Miscellaneous service states (SERVICE_* keys) |
| `sc` | `LoadServerConfig` | Per-server attributes via native LDAP client |

**Timeout**: derived from `ldap_read_timeout` in localconfig (default `60000` ms → 60 s).
The implementation uses a two-stage timeout matching the original Python behaviour: if the
first timeout fires, the loop waits another full interval before giving up. If all four
goroutines finish before the timeout, the loop continues immediately.

Each loader stages its own part (local, global, misc, server + services); when the attempt
ends, the parts that were delivered replace the published ones in a single new
`snapshot.Snapshot` (see below). A loader that failed leaves its part at the previous value,
like the legacy zmconfigd, and `LoadAllConfigs` still returns the error so the cycle is skipped. The MTA config (`ParseMtaConfig`) is loaded after all four
complete, because it reads from `conf/zmconfigd.cf` which may depend on the outputs of the
loaders above.

## Cycle model: Load, Snapshot, Plan, Execute, Apply restarts

A cycle is a pipeline of explicit values rather than mutations of shared state:

```
Load ─► Snapshot ─► Observe ─► Plan ─► Execute ─► ApplyRestarts
        (immutable)           (pure)   (side effects) (restart policy)
```

1. **Load** — the four loaders read localconfig, global config, server config and service
   states.
2. **Snapshot** (`internal/snapshot`) — `snapshot.New(snapshot.Parts{...})` builds an immutable
   value (Local, Global, Server, Misc, Services). `state.State` holds the current one in an
   `atomic.Pointer`: `State.Snapshot()` never returns nil, `Publish`/`UpdateSnapshot` swap it.
   Readers such as the watchdog and the TCP listener load it without locks and always see a
   consistent view; nobody can mutate a published snapshot.
3. **Observe** — `ConfigManager.Observe` resolves the lookups (`VAR`, `LOCAL`, `SERVICE`, ...)
   that `configd.cf` sections require against the snapshot and stores the results in a new
   snapshot, which is published.
4. **Plan** (`internal/plan`) — `plan.Compute(plan.Input{Prev, Cur, Conf, Forced, Requested,
   FirstRun, Tracked, Carry})` returns a `plan.Plan` with `Sections`, `Heal`, `Mapfiles`,
   `Rewrites`, `Postconf`, `Postconfd`, `Ldap`, `Proxygen`, `Restarts`,
   `Tracked` and `Enabled`. Compute performs no I/O and mutates nothing, so
   equal inputs give equal plans. Every entry carries `Reasons` (`first-run`, `forced`,
   `requested`, `var-changed`, `service-enabled`, `service-disabled`, `retry`,
   `mapfile-sync`, and for `configd plan` `evaluated` and `drift`). Reasons are redacted for
   secret keys and truncated, so they are safe to log and print. The same function backs
   `configd plan`, which only adds the observed host state (`plan.Input.Host`).
5. **Execute** — `ConfigManager.Execute` runs the mapped files first (serially, because other
   steps read them; the self-heal of deleted or hand-edited mapped files is part of that
   step), then proxygen, template rewrites, postconf, postconfd and LDAP writes concurrently.
   An action that fails is kept in the carry and re-enters the next plan with a `retry`
   reason, so failures are retried until they succeed.
6. **ApplyRestarts** (`internal/restart`) — `ConfigManager.ApplyRestarts` hands the plan's
   restarts to `restart.Policy.Apply`, which queues the
   restarts (without a DEPENDS cascade, as in the legacy zmconfigd), applies the enabled-only rule, the
   `zmconfigd_enable_config_restarts` switch, the failed-restart limit and the optional
   debounce. Restarts it does not consume are carried to the next cycle.

When every service looks disabled `Compute` returns `plan.ErrAllServicesDisabled`; nothing is
consumed, the previous snapshot stays the baseline and the requested sections stay queued.

## Shadow mode

`configd shadow` (`cmd/configd/shadow.go`, `internal/shadow`) runs Load, Snapshot and Plan on the
poll interval next to a real configd, and never Execute or ApplyRestarts. It is meant for QA
nodes: the plans and rendered files of this build are compared with what the legacy daemon, or
the previous Go release, actually does. Usage is in the README.

Side-effect freedom is enforced structurally, in three layers:

1. **No wiring.** The shadow command builds no listener, no watchdog, no restart policy run and no
   `sd_notify` notifier, and calls neither `Execute` nor `ApplyRestarts`. The only signal handling is
   `SetupSignalHandler` with a nil notifier (SIGHUP wakes the loop, SIGTERM stops it).
2. **A read-only `sys.System`.** The `ConfigManager` is built on `sys.ReadOnly(env)`
   (`internal/sys/readonly.go`). Its `FS` passes the reads (`ReadFile`, `Open`, `OpenFile` without
   a write flag, `Stat`, `ReadDir`, `WalkDir`, `Glob`, `Readlink`, `AvailBytes`) and refuses
   every mutation, its `Exec` refuses every command, its `Proc` passes the process-table reads and
   refuses signals. Each refusal is an error wrapping `sys.ErrReadOnly` that names the operation
   and target, never the command arguments (they may carry secrets). The wrappers forward
   method by method instead of embedding the interfaces, so a method added to `sys.FS` or
   `sys.Proc` does not compile until it is classified as read or write.
3. **A separate writable filesystem for the report.** The report directory is written through
   its own `sys.FS` that only `internal/shadow` holds, never through the read-only one, and
   `shadow.New` refuses a directory that is, or contains, the install tree. Rendered targets are
   cleaned as absolute paths before joining the directory, so a `..` in `configd.cf` cannot leave it.

`cmd/configd/shadow_test.go` pins this with the recording fakes: a shadow cycle on
`sys.ReadOnly(systest.New().System())` leaves the recorder empty (every `exec`, `fs.*` and
`proc.signal` op would be recorded if it got through), and running `ConfigManager.Execute` on the
same system fails with `sys.ErrReadOnly` and still records nothing.

Outside `sys` (see the boundary above): the LDAP connection (read-only queries during Load), the
log output on stderr and the signal registration. LDAP writes happen only in `Execute`, which a
shadow cycle never calls.

There is no per-subsystem cutover flag (proxy, MTA, watchdog) falling back to the legacy
implementation. On the releases that ship this daemon the legacy implementation is gone: the
native-configd migration of carbonio-core (CO-3358) removes the Jython `libexec/zmconfigd`,
`jylibs` and `zmpython`, and turns `zmproxyconfgen` into a wrapper around `configd proxy gen`.
Only nodes still on the pre-migration packages carry them, and even there the Jython daemon
cannot be partitioned by section (it owns the whole cycle and port 7171; a forced run per
section starts a JVM and never restarts services). Shadow mode is the safety net instead;
rolling back is installing the previous package.

## Package map

| Package | Role |
|---|---|
| `internal/snapshot` | Immutable configuration snapshot (values + accessors) |
| `internal/state` | Process state; holds the current snapshot behind an `atomic.Pointer` plus first-run, forced and requested bookkeeping |
| `internal/plan` | Pure `Compute`, plan types, reasons, text/JSON rendering |
| `internal/configmgr` | Loaders, `Observe`, `Plan`, `Execute`, `ApplyRestarts`, `PlanHost` (host observation for `configd plan`) |
| `internal/restart` | Restart policy: enabled-only, debounce, max failed restarts |
| `internal/services` | Service registry and manager (start order, retries, MTA reload) |
| `internal/cli/plancmd` | `configd plan` command |
| `internal/shadow` | `configd shadow`: side-effect-free Load/Plan cycle with a plan log and an optional report directory |
| `internal/sys` | Side-effect boundary (exec, fs, proc, clock); `sys.ReadOnly` is its mutation-refusing wrapper |
