<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Architecture

## Startup sequence

Source: `cmd/configd/cli.go` (`DaemonCmd.Run`), `cmd/configd/configd.go`, `cmd/configd/mainloop.go`

1. **User check** — `security.MustCheckUserPermissions()` enforces that the process runs as
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

- `configmgr.NewConfigManager` and `services.NewServiceManager` are constructed.
- `systemd.NewManager().IsSystemdEnabled` checks whether Carbonio systemd targets exist;
  sets `ServiceManager.UseSystemd` accordingly.
- A watchdog goroutine is started.
- Signal handlers are registered (SIGTERM/SIGINT → cancel context; SIGHUP → reload channel).
- The systemd watchdog keep-alive goroutine is started when `WATCHDOG_USEC` is set, pinging
  at half the declared interval.

## Main loop phases

Source: `cmd/configd/mainloop.go` (`RunMainLoop`)

Each iteration runs the following phases in order:

| Phase | Function | Notes |
|---|---|---|
| Shutdown check | — | Checks context cancellation before starting work |
| Idle check | — | Skips to sleep if no REWRITE events and not first run and no reload signal |
| Load configs | `configManager.LoadAllConfigs` | Four concurrent goroutines; see Config loading pipeline below |
| Start listener | `network.NewThreadedStreamServer` | First run only; skipped in `--once` mode |
| Parse MTA config | `configManager.ParseMtaConfig` | Reads `conf/zmconfigd.cf` |
| Build dependencies | `buildServiceDependencies` | Extracts DEPENDS entries from MTA sections into service manager |
| Compare keys | `configManager.CompareKeys` | MD5 fingerprint comparison; marks sections as changed |
| Compile actions | `configManager.CompileActions` | Enqueues rewrites, postconf ops, restarts |
| Do rewrites | `configManager.DoConfigRewrites` | Parallel file writes + postconf + proxygen + LDAP |
| Do restarts | `configManager.DoRestarts` | Only when `RestartConfig=true` (default) |
| READY=1 | `notifier.Ready()` | Sent after every loop; first iteration transitions systemd from activating → active |
| Sleep | `SleepWithContext` | Default 300 s; woken early by SIGHUP or TCP REWRITE |

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

**REWRITE skips restarts**: the TCP REWRITE command populates `State.RequestedConfig`,
which causes `compileSectionActions` to return before enqueueing any service restarts.
Config files are rewritten but no service is restarted. See
[restart-behavior.md](restart-behavior.md) for the full rule.

## Config loading pipeline

Source: `internal/configmgr/manager_load.go` (`LoadAllConfigs`)

Four goroutines run concurrently, one per config source:

| Goroutine name | Function | What it loads |
|---|---|---|
| `lc` | `LoadLocalConfig` | `localconfig.xml` via the Go native reader |
| `gc` | `LoadGlobalConfig` | Global config attributes via LDAP `zmprov gacf` |
| `mc` | `LoadMiscConfig` | Miscellaneous service states (SERVICE_* keys) |
| `sc` | `LoadServerConfig` | Per-server attributes via LDAP `zmprov gs` |

**Timeout**: derived from `ldap_read_timeout` in localconfig (default `60000` ms → 60 s).
The implementation uses a two-stage timeout matching the original Python behaviour: if the
first timeout fires, the loop waits another full interval before giving up. If all four
goroutines finish before the timeout, the loop continues immediately.

Results are merged into `state.State` under per-field mutexes. The MTA config
(`ParseMtaConfig`) is loaded after all four complete, because it reads from
`conf/zmconfigd.cf` which may depend on the outputs of the loaders above.
