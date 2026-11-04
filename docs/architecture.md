<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Architecture

## Startup sequence

Source: `cmd/configd/cli.go` (`DaemonCmd.Run`), `cmd/configd/configd.go`, `cmd/configd/mainloop.go`

1. **User check** — `cliutil.RequireZextras` (`security.MustCheckUserPermissions`) enforces that
   the process runs as `zextras`. Root is rejected; the process exits with status 1.
2. **Logging init** — `cliutil.InitLogging`: structured logger configured from the
   `CONFIGD_LOG_FORMAT` and `CONFIGD_LOG_LEVEL` environment variables (defaults: text, info).
3. **sd_notify init** — `sdnotify.New()` opens the socket named by `NOTIFY_SOCKET`. If the
   variable is unset the notifier is a no-op; no code path fails because of its absence (a failure
   to open the socket is logged and the daemon carries on).
4. **Profiling/tracing setup** — CPU, memory, and trace profiles started if their flags are present.
   The flags exist only in binaries built with `-tags profiling` / `-tags tracing`, see
   [performance.md](performance.md#profiling-tooling).
5. **Runtime** — `buildRuntime` (`cmd/configd/runtime.go`) is the one builder shared by the
   daemon, `shadow`, `plan`, `proxy gen` and the forced/standalone rewrite.
   `config.NewConfigWithHostname()` only sets defaults (base dir, intervals) and the hostname
   (the `zimbra_server_hostname` environment variable, then the `CONFIGD_LC_zimbra_server_hostname`
   overlay or the `zimbra_server_hostname` of `localconfig.xml`, then `/etc/hostname`); it does not
   read the rest of localconfig. `buildRuntime` loads the effective localconfig (`localconfig.xml` over
   the built-in defaults, under the `CONFIGD_LC_<key>` environment, `${variable}` expanded) and applies the keys that shape the wiring
   (`zmconfigd_template_dir`, `zmconfigd_sections`, `zmconfigd_service_hooks`); the remaining
   keys are applied by the first cycle's load. It also builds the (not yet connected) LDAP
   client, the service backend and the one `services.ServiceManager`, applies
   `--disable-restarts`, and constructs `state.State` (seeded with a snapshot that only carries
   the default listen port and IP mode) and the `ConfigManager` (with the `internal/cache` LDAP
   result cache, which every cycle's load invalidates).
6. **Main loop** — `RunMainLoop` is called. It does not return until the process is asked to
   shut down.

Inside `RunMainLoop` before the first iteration:

- The one `services.ServiceManager` (built by `buildRuntime` and injected into
  `configmgr.NewConfigManager`) serves the restart policy, the watchdog and status checks. Its
  `services.Backend` is the host `*Controller`, or a `services.HookBackend` when
  `zmconfigd_service_hooks` is set (see below). `env` is the `sys.System` built once in `main`
  (`sys.Real()`).
- On a host, the manager gates on `Controller.IsSystemdMode()` (a Carbonio systemd target is
  enabled) — there is no per-instance mode flag to set. The mode selects the restart policy
  (heavy services only flagged, others `try-reload-or-restart`) and turns the watchdog into a
  no-op; see [restart-behavior.md](restart-behavior.md). A hook backend is never in systemd mode:
  the hooks decide what a restart does and the watchdog checks the services through their
  status hooks.
- The watchdog (`bootstrapWatchdog`, interval `zmconfigd_watchdog_interval`, default 120 s) is
  started unless the run is a forced rewrite; it does nothing on systemd hosts. A service the
  manager restarts on purpose is removed from the watchdog first.
- Signal handlers are registered (`cmd/configd/signals.go`): SIGTERM/SIGINT send `STOPPING=1` and
  cancel the context; SIGHUP, SIGUSR2 and SIGALRM send `RELOADING=1`, zero the sleep timer and
  wake the loop through the one-slot reload channel; SIGCHLD is only logged.
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

Each iteration runs the following phases in order. A panic inside an iteration is recovered,
logged, and followed by a normal-interval sleep, so a deterministic panic cannot spin the loop.

| Phase | Function | Notes |
|---|---|---|
| Shutdown / reload check | `consumeReloadSignal` | Exits when the context is cancelled; drains a pending reload signal and remembers it (`reloadSignaled`) |
| Idle check | `isIdlePoll` | Only with `zmconfigd_skip_idle_polls=TRUE` (off by default, as legacy): skips to sleep if no REWRITE events and not first run and no reload signal |
| Load configs | `configManager.LoadAllConfigs` | Four concurrent goroutines; see Config loading pipeline below |
| Parse MTA config | `configManager.ParseMtaConfig` | Reads `conf/zmconfigd.cf`, right after the load (`runLoadAndParse`) |
| Validate hooks | `checkHooks` | Once, on the first parsed `zmconfigd.cf`; only with `zmconfigd_service_hooks`. A gap ends the daemon with status 1 |
| LDAP watcher | `syncLDAPWatch` | Every iteration: with `zmconfigd_ldap_change_trigger=TRUE` starts the LDAP change watcher (once) or stops it when the key turns `FALSE`; with the key off (default) it does nothing and no goroutine exists. See [LDAP change detection](#ldap-change-detection-opt-in) |
| Build dependencies | `buildServiceDependencies` | Extracts DEPENDS entries from MTA sections for the shared service manager's watchdog cascade (config-driven restarts do not cascade, see restart-behavior.md) |
| Claim REWRITE clients | `MainLoopActionTrigger.claimWaiters` | The TCP REWRITE clients registered so far now depend on this cycle: `Plan` takes their requested sections |
| Snapshot + Observe | `configManager.Plan` (first half) | Freezes the loaded configs into an immutable `snapshot.Snapshot`, resolves every lookup `configd.cf` requires and publishes the result |
| Plan | `plan.Compute` (via `configManager.Plan`) | Pure, deterministic: previous snapshot vs. current snapshot gives a `plan.Plan` whose every entry carries its reasons. If it fails, the claimed clients are requeued (`requeueWaiters`), their sections stay queued, and the loop backs off |
| Execute | `configManager.Execute` | Mapfiles first (self-heal included), then proxygen, rewrites, postconf, postconfd and LDAP concurrently |
| Release REWRITE clients | `MainLoopActionTrigger.completeWaiters` | Right after Execute (also when a rewrite step failed, as legacy `notifyAll` in `finally`): the clients claimed for this cycle get `SUCCESS REWRITES COMPLETE`. A client that arrived mid-cycle waits for the next one |
| Back-off | `backOff` | A failed load or MTA parse, a rejected plan or a failed rewrite sleeps 60 s (interruptible by a reload), exports the metrics textfile, and retries without applying restarts, starting the listener or sending the first `READY=1`; a reload being handled is still acknowledged with `READY=1` |
| Apply restarts | `configManager.ApplyRestarts` | `restart.Policy.Apply`: runs every successful cycle; honours `zmconfigd_enable_config_restarts`, debounce and `--disable-restarts`. Skipped when the rewrite phase failed (legacy skips `doRestarts`); the plan's restarts stay in the carry for the next cycle |
| Start listener | `network.NewThreadedStreamServer` | Only after the first pass completed (legacy `configd.py` starts it after the first rewrite and restart pass); never in `--once` mode (profiling builds) |
| First pass bookkeeping | `finalizeIteration` | Clears the first-run flag, sends `READY=1`, updates the `STATUS=` line, logs the timings, records the cycle metrics |
| READY=1 | `signalReady` | First time once the listener is up, i.e. after the first completed pass (so systemd stays `activating` until the host is configured); sent again when a cycle ends after a reload signal (`RELOADING=1`), also when that cycle backed off |
| Sleep | `SleepWithContext` | Default 300 s (`zmconfigd_interval`); woken early by SIGHUP/SIGUSR2/SIGALRM, TCP REWRITE or, when enabled, an LDAP change |

Forced mode (`configd rewrite` falling back to an in-process run, see below) uses the same Plan and
Execute, never applies restarts and never starts the listener or watchdog. It exits 0 when the
rewrite ran and 1 when planning or a rewrite step failed.

On errors in the load or MTA-parse phases, the loop sleeps 60 s and retries rather than
exiting. On context cancellation mid-loop, the loop exits immediately after the current
phase completes.

## Network protocol

Source: `internal/network/server.go`, `cmd/configd/mainloop.go` (`MainLoopActionTrigger`),
`internal/cli/rewrite`

The listener is started by the main loop after the first completed config pass (see the table
above), not at process start. It binds `127.0.0.1` (IPv4) or `[::1]` (IPv6, when the server's
`zimbraIPMode` is `ipv6`). The port is `zmconfigd_listen_port` in localconfig (default 7171).
A bind failure is fatal.

**Wire format**: one request per connection, answered with exactly one line; the connection is then
closed. The daemon does a single read of at most 4096 bytes and takes the request up to the first
newline, or the whole chunk when the client sends no newline (the legacy client sends none and does
not half-close). A chunk that fills the buffer without a newline, more than 256 arguments, or no
request within 120 s drops the connection without a reply. At most 64 connections are handled at
once; the rest wait in the accept backlog.

```
REQUEST  = COMMAND [SPACE ARG]* [NEWLINE]
RESPONSE = RESULT_STRING NEWLINE
```

**Commands** (case-sensitive; fields are split on whitespace):

| Command | Example | Response |
|---|---|---|
| `STATUS` | `STATUS\n` | `SUCCESS ACTIVE\n` |
| `REWRITE section [section ...]` | `REWRITE proxy mta\n` | `SUCCESS REWRITES COMPLETE\n` once the main loop has rewritten the sections; `ERROR REWRITE TIMEOUT\n` after 60 s (the request stays queued); `ERROR SHUTTING DOWN\n` if the daemon stops meanwhile |
| `REWRITE` (no sections) | `REWRITE\n` | `ERROR NO SERVICES LISTED\n` (as the legacy listener) |
| _(anything else, empty line)_ | `FOO\n` | `ERROR UNKNOWN COMMAND\n` |

Because the listener only starts after the first pass, a `STATUS` that answers `SUCCESS ACTIVE`
means the host has been configured once; the systemd `READY=1` is sent at the same point.

**REWRITE is synchronous** (`listener.py` `lAction.wait`, 60 s). Callers such as
`configrewrite X && start X` rely on it: the reply must not precede the written files. The
handler queues the named sections as `requested`, registers the client with
`MainLoopActionTrigger` and wakes the loop through the reload channel. The loop claims the clients
registered so far right before planning (`claimWaiters`), and releases them after the cycle's
Execute phase (`completeWaiters`) whether or not a rewrite step failed, as legacy `notifyAll` did.
A cycle whose plan fails requeues the clients, a client that arrives mid-cycle waits for the next
cycle, and a client that gives up (timeout, shutdown) leaves its sections queued for the next
cycle. A cycle triggered by `REWRITE` is a full cycle: sections that changed are rewritten too.

**Clients.** `configd rewrite`, the `configd proxy` and `tls` writers, and `configd init` (which
sends `REWRITE <component>` and does not fall back) use the same exchange
(`internal/cli/rewrite.Request`): they dial `zmconfigd_listen_port` on both loopback families (the
family matching `zimbraIPMode` first, 5 s dial timeout) and wait for the reply up to
`zimbra_configrewrite_timeout` (default 120 s, beyond the daemon's own 60 s). `configd service`
uses the same helper for `STATUS` readiness probes. When nothing listens on either family,
`configd rewrite` (and the proxy/tls writers) rewrite the sections in-process (forced mode, no
restarts), like legacy `configrewrite` falling back to `zmconfigd <configs>`. A daemon that is up
but answers `ERROR ...` is never bypassed (a second rewriter would race it): the client exits 1.

**REWRITE skips restarts**: the TCP REWRITE command queues the named sections as
`requested`. `plan.Compute` still renders them, but compiles no service restart for a
requested section in that cycle. Config files are rewritten but the section's services are not
restarted; when the section also had a variable change (consumed by that cycle), its restarts
are reported as `Plan.DeferredRestarts`, held by the config manager and run by the next cycle
that is not a REWRITE. See [restart-behavior.md](restart-behavior.md) for the full rule.

## Service backend and hooks

`services.ServiceManager` (restart queue, dependency cascade, start order, failed-restart limit,
`--disable-restarts`) drives a `services.Backend` (`ServiceStart`/`Stop`/`Restart`/`Status` and
`LookupService`). `*services.Controller` is the host backend. `services.HookBackend`
(`internal/services/hooks.go`) is selected by `zmconfigd_service_hooks` and implements the actions
with `sys.Proc.Signal` and `sys.Exec` instead, so it is covered by `sys.ReadOnly` (shadow) and the
`systest` fakes like everything else. `buildRuntime` creates the one `ServiceManager` and injects it
into `configmgr.NewConfigManager`; the config-driven restarts, the watchdog and status checks share
it. A hooks file with an unknown service, action or hook is a startup error. The hooks are also
validated against the first parsed `zmconfigd.cf` (`runtime.validateHooks`, called from the main loop
as `checkHooks`): every service a `RESTART` line of an in-scope section names or cascades into
through `DEPENDS` needs a `restart` hook, and every service the watchdog monitors a `status` hook; a
gap ends the daemon with status 1. File format and hook kinds (`signal:`, `exec:`, `skip`,
`process`) are in [localconfig.md](localconfig.md#service-hooks-zmconfigd_service_hooks).

## Config loading pipeline

The server hostname used to select this host's LDAP server entry comes from the
`zimbra_server_hostname` environment variable, else the `zimbra_server_hostname` key of
`localconfig.xml` (legacy `zmhostname`), else `/etc/hostname`.

Source: `internal/configmgr/manager_load.go` (`LoadAllConfigs`)

Four goroutines run concurrently, one per config source:

| Goroutine name | Function | What it loads |
|---|---|---|
| `lc` | `LoadLocalConfig` | the effective localconfig (`localconfig.LoadEffective`): defaults, `localconfig.xml` (optional) and the `CONFIGD_LC_<key>` environment, via the Go native reader |
| `gc` | `LoadGlobalConfig` | Global config attributes via native LDAP client |
| `mc` | `LoadMiscConfig` | Miscellaneous service states (SERVICE_* keys) |
| `sc` | `LoadServerConfig` | Per-server attributes via native LDAP client |

**Timeout**: derived from `ldap_read_timeout` in localconfig (ms; default `60000` → 60 s). A value
below `60000` still waits 60 s, a larger one scales as `ldap_read_timeout / 1000` seconds.
The implementation uses a two-stage timeout matching the original Python behaviour: if the
first timeout fires, the loop waits another full interval before giving up and counts a
`timeout` load failure. If all four goroutines finish before the timeout, the loop continues
immediately. A cancelled context (shutdown) ends the wait at once.

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
   `mapfile-sync`, `dependency`, and for `configd plan` `evaluated` and `drift`). Reasons are redacted for
   secret keys and truncated, so they are safe to log and print. The same function backs
   `configd plan`, which only adds the observed host state (`plan.Input.Host`).
5. **Execute** — `ConfigManager.Execute` runs the mapped files first (serially, because other
   steps read them; the self-heal of deleted or hand-edited mapped files is part of that
   step), then proxygen, template rewrites, postconf, postconfd and LDAP writes concurrently.
   An action that fails is kept in the carry and re-enters the next plan with a `retry`
   reason, so failures are retried until they succeed.
6. **ApplyRestarts** (`internal/restart`) — `ConfigManager.ApplyRestarts` hands the plan's
   restarts to `restart.Policy.Apply`. On legacy hosts it queues the
   restarts (without a DEPENDS cascade, as in the legacy zmconfigd); on systemd hosts it only
   flags heavy services as restart pending and runs `systemctl try-reload-or-restart` for the
   others. It applies the `zmconfigd_enable_config_restarts` switch, the failed-restart limit and
   the optional debounce. Restarts it does not consume are carried to the next cycle.

When every service looks disabled `Compute` returns `plan.ErrAllServicesDisabled`; nothing is
consumed, the previous snapshot stays the baseline and the requested sections stay queued.

## Observability

### Log redaction (always on)

`logger.InitStructuredLogging` wraps the text/JSON handler in a redacting handler
(`internal/logger/redact_handler.go`), so no call site can leak a secret. Before a record
reaches the output:

- an attribute whose **key** names a secret (`redact.IsSecretKey`: `password`, `passwd`,
  `passphrase`, `secret`, `token`, `credential`, `privatekey`/`private_key`, `apikey`/`api_key`, and the
  suffixes `_key`, `_pass`, `_pw`; e.g. `ldap_amavis_password`, `zimbraSSLPrivateKey`,
  `userPassword`, `zimbraAuthTokenKey`) has its value replaced by `[REDACTED]`;
- a value attribute (`value`, `previous_value`, `current_value`, `old`, `new`, `have`, `want`,
  ...) logged next to a key attribute (`key`, `attr`, `attribute`, `variable`, ...) that names a
  secret is replaced too (`"key", "ldap_root_password", "value", x`);
- PEM private key blocks (`-----BEGIN [... ]PRIVATE KEY-----` up to the END line, or to the end
  of the text when it is truncated) are replaced by `[REDACTED]` wherever they appear: string
  attributes, `error` attributes and the message. Certificates are not touched.

Empty values stay visible (an unset key is useful to see) and records without a secret are
forwarded unchanged, so ordinary log output is byte-identical. Only string and `any`/error
attributes are inspected; numbers, booleans, durations and times cannot carry a credential.
The per-call `redact.ForLog` calls remain: they also protect loggers injected with
`logger.ContextWithLogger` (which bypass the default handler) and the non-log output of
`configd plan`, so they are not removable duplicates.

### Metrics (opt-in)

`internal/metrics` keeps counters and gauges in memory (no network listener) and renders them
in the Prometheus text format. Nothing is written unless localconfig
`zmconfigd_metrics_textfile` names a path; the registry is then written atomically
(`sys.FS.WriteFileAtomic`, mode `0644`) at the end of every cycle, and also after a failed load
or plan so the failure counters are exported. Point it at a `*.prom` file in the
node_exporter textfile collector directory (`--collector.textfile.directory`).

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `configd_cycle_duration_seconds` | gauge | | Duration of the last completed cycle |
| `configd_cycle_phase_duration_seconds` | gauge | `phase` (`load`, `parse`, `build_deps`, `plan`, `execute`, `restarts`) | Duration of each phase of the last cycle |
| `configd_cycles_total` | counter | | Completed cycles since process start |
| `configd_last_cycle_timestamp_seconds` | gauge | | Unix time the last cycle completed |
| `configd_plan_actions` | gauge | `kind` (`mapfile`, `rewrite`, `postconf`, `postconfd`, `ldap`, `proxygen`, `restart`) | Plan size of the last cycle |
| `configd_plan_actions_total` | counter | `kind` | Actions planned since process start |
| `configd_restart_requests_total` | counter | `service`, `reason` (plan reason kind) | Restarts the restart policy queued; a restart with several reasons counts once under each |
| `configd_watchdog_restarts_total` | counter | `service`, `result` (`success`, `failure`) | Restarts attempted by the watchdog |
| `configd_load_failures_total` | counter | `source` (`lc`, `gc`, `mc`, `sc`, `timeout`, `mta_config`) | Failed configuration loads |
| `configd_ldap_change_triggers_total` | counter | `component` (`global`, `servers`, `domains`), `result` (`cycle_requested`, `already_pending`) | Changes the [LDAP change watcher](#ldap-change-detection-opt-in) reported, once per changed component. `cycle_requested`: the main loop was woken and a cycle follows; `already_pending`: a wake-up (SIGHUP, REWRITE or an earlier change) was already queued and the change rides on that cycle. Only exists with `zmconfigd_ldap_change_trigger=TRUE` |
| `configd_ldap_watch_poll_failures_total` | counter | | Failed polls of the watcher (each is also a `WARN`) |

Output is sorted (metrics, then label sets) with one `# HELP` and `# TYPE` line per metric, so
equal state renders to identical bytes. Counters reset when the process restarts, as
Prometheus expects. A failed write logs a `WARN` (`Failed to write metrics textfile`) and never
affects the cycle.

`zmconfigd_cycle_summary=TRUE` additionally logs one structured INFO line per cycle,
`Cycle summary`, with `cycle`, `first_run`, `duration_seconds`, per-phase `*_seconds` and the
plan size by kind (`mapfiles`, `rewrites`, `postconf`, `postconfd`, `ldap`, `proxygen`,
`restarts`). It is off by default because it adds a line to the log.

## Shadow mode

`configd shadow` (`cmd/configd/shadow.go`, `internal/shadow`) runs Load, Snapshot and Plan on the
poll interval next to a real configd, and never Execute or ApplyRestarts. It is meant for QA
nodes: the plans and rendered files of this build are compared with what the legacy daemon, or
the previous Go release, actually does. Flags: `--shadow-dir DIR` writes the would-be files and the
plan JSON under `DIR`; `--single-cycle` runs one cycle and exits (status 1 when it failed). Without
it the command runs until SIGINT/SIGTERM. The side-effect global flags (`--cpuprofile`,
`--memprofile`, `--trace`, `--enable-tracing`) are rejected. Usage is in [cli.md](cli.md).

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
implementation. On the releases that ship this daemon the legacy Jython implementation is gone:
the `zm*` wrappers installed by carbonio-core (`zmcontrol`, `zmlocalconfig`, `zmproxyconfgen`, ...)
exec `configd` subcommands. The Jython daemon could not be partitioned by section either (it owns
the whole cycle and port 7171; a forced run per section starts a JVM and never restarts services).
Shadow mode is the safety net instead; rolling back is installing the previous package.

## LDAP transport security and timeouts

Source: `internal/ldap/tls.go` (`ResolveTLS`), `internal/ldap/client.go`

Every native LDAP client (daemon, `configd proxy`/`tls` writers, service discovery) derives its
transport from localconfig with the rule of the legacy Java client
(`LdapServerConfig.ZimbraLdapConfig`, `LdapSSLUtil`):

| Setting | Meaning |
|---|---|
| `ldap_starttls_supported` = `1` **and** `ldap_starttls_required` (default `true`) **and** `zimbra_require_interprocess_security` = `1` | `ldap://` URLs are upgraded with StartTLS. A failed upgrade fails the connection: there is no clear-text fallback |
| `ldaps://` URL | TLS from the first byte; StartTLS is never issued on it |
| `ssl_allow_untrusted_certs` (default `true`) | `true` skips certificate verification, like the Java dummy trust manager. `false` verifies the server against the system roots plus `conf/ca/ca.pem` and `ssl/carbonio/commercial/commercial_ca.crt` under `zimbra_home` (the CAs `zmcertmgr` adds to the Java truststore) |

Booleans follow Java `Boolean.valueOf`: only `true` (any case) is true; `ldap_starttls_supported` and
`zimbra_require_interprocess_security` are compared with `"1"`. TLS 1.2 is the minimum and the TLS server
name is the host of each URL. The `cn=config` client talks over `ldapi://` and never uses TLS.

Timeouts: dial (`DialTimeout`, 10 s, ldaps handshake included), StartTLS plus bind (`BindTimeout`, 10 s,
enforced by a watchdog that closes the connection, because go-ldap's StartTLS has no deadline), and each
request (`OpTimeout`, 30 s). The pool lock is held only to pop or push an idle connection, never across
network I/O, and `Client.Close` aborts connection attempts in flight. `Ldap.withRetry` backoff sleeps stop
when the caller's context is cancelled.

## LDAP change detection (opt-in)

Source: `internal/ldapwatch`, `cmd/configd/ldapwatch.go`

By default configd notices an LDAP change at the next cycle, up to `zmconfigd_interval`
(300 s) later. With `zmconfigd_ldap_change_trigger=TRUE` (off by default, see
[localconfig.md](localconfig.md#configd-only-keys-not-in-the-defaults-registry)) a watcher
wakes the main loop as soon as an entry the cycle reads changes. Everything else is
unchanged: the woken iteration is the normal cycle (Load, Snapshot, Plan, Execute, Apply
restarts), rendered files, restart decisions and log lines are the same, and the regular
interval poll keeps running as the safety net. A failing or stopped watcher therefore only
costs latency.

**What is watched.** The entries the cycle reads from LDAP: the global config
(`cn=config,cn=zimbra`, base search), the server entries (`cn=servers,cn=zimbra`, one level,
`zimbraServer`) and the `zimbraDomain` entries (subtree from the root, as the proxy
generator does). The server entries cover this server's own attributes and services, the
upstream servers the proxy generates and the servers that `gamau`, `garpu` and `garpb`
enumerate; those three lookups read the *servers* list, not the domains. Domains are read by
the proxy generator only. Each search asks for `entryCSN` and `modifyTimestamp` only; the
watcher hashes DN plus those values per component, so additions and deletions change the
digest as well as modifications. The LDAP writes of a cycle go to slapd's own configuration
database (the `LDAP` section, over ldapi), which is not watched, so a cycle cannot trigger
itself.

**Why polling a fingerprint.** Measured on the QA container (carbonio-core 4.6.7):

- OpenLDAP has no persistent search control.
- RFC 4533 syncrepl `refreshAndPersist` (available in go-ldap v3) needs the `syncprov` overlay
  on the data database. The QA directory loads only `dynlist`, `unique` and `noopsrch`
  there: the root DSE has no `contextCSN`, and a search with the sync control is accepted
  and then ends in a plain success without a Sync Done control and without ever streaming a
  change. A watcher built on it would silently never fire.
- `contextCSN`, where it exists, moves on every write (every mailbox account change), so it
  cannot distinguish a relevant change from noise; the `entryCSN` of the few entries a cycle
  reads can. `entryCSN` and `modifyTimestamp` are readable by the `zimbra` admin on all three
  queries.

Polling works on every topology (single node, replicas, failover) and needs nothing from the
server. The cost is the poll: three small searches per `zmconfigd_ldap_change_poll_seconds`
(default 5 s). The domains search is the one that scales with the directory, which is why the
interval is a key.

**Behaviour** (`ldapwatch.Watcher.Run`, a single goroutine, all waits and time reads through
the injected `sys.Clock`):

| Aspect | Behaviour |
|---|---|
| Baseline | The first successful poll only records the fingerprint and never triggers. A change between the first cycle's load and that poll is picked up by the interval poll |
| Coalescing | A change is reported once it has been quiet for `zmconfigd_ldap_change_debounce_seconds` (default 2 s), or after ten windows of continuous changes at the latest, so one burst of writes yields one cycle and a steady stream cannot postpone it forever. `0` reports it in the poll that sees it. Further tokens while a cycle is pending collapse in the one-slot reload channel |
| Trigger | `MainLoopActionTrigger.TriggerReload`: a non-blocking send on the reload channel SIGHUP and REWRITE use. Unlike SIGHUP it does not send `RELOADING=1` and does not zero the sleep timer, and unlike REWRITE it requests no section; the cycle is a full one (`reloadSignaled`, so `zmconfigd_skip_idle_polls` does not skip it). With no requested section it is a non-REWRITE cycle, so it also releases the restarts a REWRITE cycle deferred (see [restart-behavior.md](restart-behavior.md#a-change-consumed-by-a-rewrite-cycle-deferred-restarts)). It runs on the main loop goroutine like every other cycle, so it can never overlap a REWRITE cycle: a REWRITE that arrives while it runs waits for the next iteration |
| Errors | A failed poll is logged as a warning (never an error) and retried after 1 s, 2 s, 4 s, ... capped at 60 s; the first good poll resets the wait. The fingerprint is compared with the last good one, so a change made while LDAP was unreachable triggers after the reconnect |
| Client | The watcher reuses the config manager's native LDAP client (pool, retries, timeouts). Its searches take no context, so `LDAPSource` runs them in a helper goroutine and abandons the wait on cancel; the helper ends with the in-flight search, bounded by the client's operation timeout |
| Lifecycle | Started from the main loop goroutine after the first successful load (the LDAP client and the parameters come from the loaded localconfig), stopped and joined when the key turns `FALSE` and when the daemon loop exits; cancelling the daemon context stops it too. Poll and debounce seconds are copied at start. Never started in `--once` mode |
| Default off | With the key off `syncLDAPWatch` returns without starting anything: no goroutine, no timer, no LDAP query. It stays opt-in on purpose: legacy zmconfigd only polls, so cycles and the restarts they apply would otherwise start at arbitrary moments instead of at most `zmconfigd_interval` after a change, and three searches every 5 s are new LDAP load on every node. Failure already degrades cleanly (warning, exponential back-off, the interval poll keeps running), so turning it on is safe; making it the default is a behaviour change for the cutover to decide |
| Directory read | The watcher polls the replica configd itself reads from: it reuses the config manager's LDAP client, so URL, bind, StartTLS/ldaps and CA handling are the ones `ldap.ResolveTLS` gives the cycle |

Logs (component `ldapwatch`): `LDAP change watcher started` and `... stopped` at info,
`LDAP change detected, requesting configuration reload` at info with the changed components
and how many changes were coalesced, the baseline and each observed change at debug, and
`LDAP change poll failed, backing off` at warn. The same events are counted in the
[metrics](#metrics-opt-in).

Tests: the watcher against a fake source on a manual clock (`internal/ldapwatch`, `cmd/configd`),
and against a real slapd through the daemon's LDAP client (`make test-integration-ldap`,
`TestOpenLDAP_ChangeTriggersAWatcherReload`: a change to the global config, a server entry or a
domain is reported once, within seconds, and a change to an entry no cycle reads is not).

## Package map

| Package | Role |
|---|---|
| `cmd/configd` | The binary: kong CLI (`cli.go`), `buildRuntime`, main loop, signals, systemd bootstrap, shadow/plan/proxy/ldap/tls/localconfig/service/control commands |
| `internal/cache` | In-memory TTL cache of LDAP-derived configuration with MD5 change detection; invalidated at the start of every load, disabled for `configd plan` |
| `internal/cli` | Subcommand implementations, one package each: `cliutil` (user check, logging, base dir), `control`, `initcmd`, `ldapcmd`, `localconfig`, `plancmd`, `proxy`, `release`, `rewrite` (daemon client, standalone fallback), `service`, `status`, `tls` |
| `internal/commands` | LDAP-backed provisioning lookups (`garpu`, `garpb`, `gamau`, typed server/global attributes) the config manager reads |
| `internal/config` | Daemon configuration (`config.Config`), defaults, the parsed `zmconfigd.cf` types, and the localconfig keys that shape the runtime |
| `internal/configmgr` | Loaders, `Observe`, `Plan`, `Execute`, `ApplyRestarts`, `PlanHost` (host observation for `configd plan`) |
| `internal/errors` | Typed errors shared by the components |
| `internal/fileutil` | Atomic writes, copies and other shared file helpers |
| `internal/intern` | Canonical (interned) strings for hot-path keys, attribute names and service names |
| `internal/ldap` | Native LDAP client: pool, retries and error classification, TLS/StartTLS resolution, timeouts |
| `internal/ldapwatch` | Opt-in LDAP change watcher: polls `entryCSN`/`modifyTimestamp` fingerprints, coalesces bursts, backs off on errors, requests an early cycle |
| `internal/localconfig` | Native `localconfig.xml` reader, defaults registry, `CONFIGD_LC_<key>` overlay (`LoadEffective`), `${var}` expansion; replaces the `zmlocalconfig` subprocess |
| `internal/logger` | `log/slog` setup, component/correlation context, redacting handler |
| `internal/lookup` | `ConfigLookup` interface for local/global/server/misc value lookups |
| `internal/metrics` | In-memory counters/gauges and the Prometheus textfile renderer/writer |
| `internal/mtaops` | MTA operations: `postconf`/`postconfd`, LDAP write-back, MAPFILE handling |
| `internal/network` | TCP 7171 listener (`STATUS`, `REWRITE`), bounded connections, synchronous REWRITE wait |
| `internal/parser` | Parser interfaces and types for `zmconfigd.cf` |
| `internal/plan` | Pure `Compute`, plan types, reasons, text/JSON rendering |
| `internal/proxy` | Proxy (nginx) configuration generator behind `configd proxy gen` and the `proxy` rewrite step |
| `internal/redact` | Secret-key and PEM detection used by the redacting log handler and plan rendering |
| `internal/restart` | Restart policy: systemd hybrid (pending-restart / try-reload-or-restart) or legacy queue, debounce, max failed restarts |
| `internal/sdnotify` | systemd `sd_notify` protocol: `READY=1`, `RELOADING=1`, `STOPPING=1`, `STATUS=`, watchdog pings |
| `internal/security` | Runtime user validation (must be `zextras`, not root) |
| `internal/services` | Service registry, `ServiceManager` (start order, retries, restart queue), host `Controller`, `HookBackend`, service launchers |
| `internal/shadow` | `configd shadow`: side-effect-free Load/Plan cycle with a plan log and an optional report directory |
| `internal/snapshot` | Immutable configuration snapshot (values + accessors) |
| `internal/state` | Process state; holds the current snapshot behind an `atomic.Pointer` plus first-run, forced and requested bookkeeping |
| `internal/sys` | Side-effect boundary (exec, fs, proc, clock); `sys.ReadOnly` is its mutation-refusing wrapper, `sys/systest` the recording fakes |
| `internal/systemd` | `systemctl` detection and unit management, with the legacy init-script fallback |
| `internal/template` | Atomic template-to-file rewrites |
| `internal/testutil` | Shared test mocks |
| `internal/tls` | Mail-mode (`zimbraMailMode`) management behind `configd tls` (legacy `zmtlsctl`) |
| `internal/tracing` | Span-based execution tracing (`-tags tracing`) |
| `internal/transformer` | Line-by-line expansion of `%%...%%` directives in templates |
| `internal/watchdog` | Service monitoring and restart on legacy hosts (no-op on systemd hosts) |
| `internal/zxadmin` | Minimal Carbonio admin HTTP client (replaces JVM-based `carbonio core` calls) |
