<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# carbonio-configd

High-performance configuration daemon for Carbonio, written in Go. Drop-in replacement for the legacy Jython-based `zmconfigd`.

Manages service configurations by monitoring LDAP for changes, generating config files from templates, applying postfix settings, and orchestrating service restarts via systemd.

## Features

- **Native LDAP client** with connection pooling and retry logic
- **Concurrent configuration loading** for fast startup
- **Smart change detection** via MD5 fingerprinting
- **systemd integration** with `sd_notify` readiness, watchdog, and reload support
- **Proxy config generation** (replaces `zmproxyconfgen`)
- **Batch postconf** operations without sudo
- **Service restart orchestration** via systemctl with polkit authorization
- **Native localconfig reader** (replaces Java `LocalConfigCLI`) with defaults registry and `${variable}` interpolation
- **Zero external dependencies** — single static binary, no JVM required

## Project Structure

```
cmd/configd/         Main entry point, Kong flag structs and daemon loop
internal/
  cli/               One package per subcommand: Run(ctx, stdout, stderr, opts) int
                     (cliutil = shared console/logging/permission helpers)
  cache/             In-memory config cache (intra-loop dedup)
  commands/          External command wrappers
  config/            Application configuration and types
  configmgr/         Config loading, change detection, action compilation
  executor/          Command execution wrapper
  ldap/              Native LDAP client and attribute management
  localconfig/       Local config XML parser (replaces zmlocalconfig)
  logger/            Structured logging (JSON/text)
  mtaops/            MTA operations (postconf, LDAP writes, mapfiles)
  network/           TCP server for REWRITE commands (port 7171)
  parser/            Template syntax parser
  postfix/           Postfix configuration management
  proxy/             Nginx proxy config generator (replaces zmproxyconfgen)
  sdnotify/          Pure-Go sd_notify (no CGo, no external deps)
  security/          User permission checks
  services/          Service lifecycle management (systemctl + zm*ctl)
  state/             Global state tracking
  systemd/           systemd target detection
  template/          Config file template rewriting
  transformer/       Variable substitution engine
  tracing/           Span-based execution tracing
  watchdog/          Service health monitoring
build/
  configd/           PKGBUILD, systemd unit, polkit policy
  yap.json           YAP packaging configuration
```

## Prerequisites

- Go 1.24+
- `make`
- Access to a Carbonio environment (for full functionality)

## Building

```bash
# Build the binary
make build

# Run tests
make test

# Run linting
make lint
```

## Packaging

Build packages for supported distributions using [yap](https://github.com/M0Rf30/yap):

```bash
make build TARGET=ubuntu-noble
make build TARGET=rocky-9
```

## Usage

### As a daemon (managed by systemd)

```bash
systemctl start carbonio-configd.service
systemctl status carbonio-configd.service
systemctl reload carbonio-configd.service   # SIGHUP — re-evaluate configs
```

The unit is shipped only on distributions with a systemd-orchestrated Carbonio
(ubuntu-noble, rocky-9). On ubuntu-jammy and rocky-8 the package installs no
unit: the daemon runs in legacy mode, started by `zmcontrol`.

### Client mode (trigger config rewrite)

`configd rewrite <section>...` sends a REWRITE command to the already-running daemon
via TCP (port 7171) and exits. It does not start a daemon.

```bash
# Rewrite proxy and MTA configs on the running daemon
configd rewrite proxy mta

# Replaces: echo "REWRITE proxy mta" | netcat -w 120 localhost 7171
```

### Plan subcommand (dry run)

`configd plan [sections...] [--json]` shows what a rewrite would change on this host and why,
without changing anything. It loads the configuration like a forced rewrite, renders the
templates in memory, reads Postfix with `postconf -h` / `postconf -n`, compares everything with
the files on disk and prints one line per action with its reasons (or JSON with `--json`).
Every entry's reason is the host drift found. Without arguments all sections are evaluated; with arguments only the named ones.

```
$ configd plan mta
rewrite      conf/postfix/master.cf.in -> conf/postfix/master.cf
    because: drift: /opt/zextras/conf/postfix/master.cf content differs from the rendered template
postconf -e  smtpd_tls_loglevel=1
    because: drift: main.cf smtpd_tls_loglevel = "0" would become "1"
restart      mta
    because: drift: configuration of section mta would change
```

(Output is illustrative; with nothing to do it prints `No changes.`)

Exit codes: `0` no changes, `1` changes would be made, `2` error.

Limitations:

- LDAP `cn=config` attributes cannot be read back, so planned `ldap` writes are always listed.
- `proxygen` is listed only when another action of one of its sections drifted; the generated
  proxy files themselves are not rendered by `plan`.
- Restarts shown are those the drifted sections would trigger. The running daemon restarts
  services only on variable changes, and a forced rewrite (`configd rewrite`) never restarts.

### Flags

```
--disable-restarts   Dry-run mode (no service restarts)
--help, -h           Show help message
```

Profiling flags (only available when built with `-tags profiling`):

```
--once               Run one config cycle and exit
--cpuprofile file    Write CPU profile to file
--memprofile file    Write memory profile to file
--trace file         Write execution trace to file
--profile-duration s Profile for s seconds (0 = entire run)
```

### Polling behavior

The daemon polls every **300 seconds** by default. Override with `zmconfigd_interval` in
`localconfig.xml`:

```xml
<key name="zmconfigd_interval">
  <value>60</value>
</key>
```

Like the legacy daemon, every poll reloads LDAP and localconfig, so changes made with
`zmprov` or the admin console are applied on the next cycle. Setting
`zmconfigd_skip_idle_polls=TRUE` skips the config-load phase when no REWRITE events have
been received since the last cycle; changes made only in LDAP then wait for a REWRITE
request or SIGHUP, which always forces a full cycle.

To apply LDAP changes without waiting for the interval, set
`zmconfigd_ldap_change_trigger=TRUE` (off by default): a watcher polls the global config,
server and domain entries a cycle reads and starts a full cycle as soon as one changes. The
interval poll keeps running as the safety net. See
[docs/architecture.md](docs/architecture.md#ldap-change-detection-opt-in) and
[docs/localconfig.md](docs/localconfig.md#configd-only-keys-not-in-the-defaults-registry).

### Localconfig subcommand

Drop-in replacement for the Java `zmlocalconfig` CLI. Reads `localconfig.xml`,
applies hardcoded defaults, resolves `${variable}` references, and supports
read/write operations. Eliminates the 2-4 second JVM startup on every call.

A `zmlocalconfig` compatibility wrapper is installed at `/opt/zextras/bin/zmlocalconfig`.

```bash
# Read keys (shell-eval format)
eval "$(configd localconfig -q -m export)"

# Query specific keys
configd localconfig -k mailboxd_java_options -k ldap_port

# Output modes: plain, shell, export, nokey, xml
configd localconfig -m export -k zimbra_home
# Output: export zimbra_home='/opt/zextras';

# Edit keys
configd localconfig -e smtp_port=587
configd localconfig -e -r ldap_root_password    # random password

# Unset a key
configd localconfig -u custom_key

# Show config file path
configd localconfig -p
```

Flags:

```
-m mode          Output mode: plain (default), shell, export, nokey, xml
-k key           Key to retrieve (repeatable; omit for all keys)
-q               Quiet mode (suppress warnings)
-c path          Path to localconfig.xml (default: /opt/zextras/conf/localconfig.xml)
-s               Show password values (default: masked)
-d               Show default values instead of current
-n               Show only keys changed from defaults
-x               Expand variables (accepted for compatibility, always on)
-e               Edit: set key=value pairs (all keys are validated first, then saved once)
-u               Unset (remove) keys (all-or-nothing, like -e)
-r               Set key to random password (use with -e)
-f, --force      Allow editing protected keys (same as legacy zmlocalconfig -f)
-p               Print config file path and exit
-i, --info       Show documentation for the keys listed in [args] (all supported keys if none)
--all            Like -i, including keys not marked as supported
-l, --reload     Ask mailboxd (admin SOAP ReloadLocalConfigRequest) to reload its local config
```

Every flag also has its legacy long name (`--config`, `--path`, `--default`, `--changed`,
`--show`, `--format`, `--edit`, `--random`, `--unset`, `--force`, `--expand`, `--quiet`, `--info`,
`--all`, `--reload`); the longer names `--config-path`, `--show-path`, `--show-defaults`,
`--show-changed`, `--show-passwords` and `--mode` keep working.

Option combinations follow the legacy `LocalConfigCLI.checkCompatibleOptions`; anything else
fails with `option 'X' can not be used with option 'Y'` (or `invalid option 'X'`):

| Main option | Also allowed |
|---|---|
| `-i`, `--all` | `-q` |
| `-e` | `-q -f -r -c` |
| `-u` | `-q -f -c` |
| `-p` | `-q -c` |
| `-n`, `-d`, none (print) | `-q -s -c -m -x` |
| `-l` | not checked |

## systemd Integration

- **Type=notify** with `READY=1` after first successful config cycle
- **WatchdogSec=120** with automatic keep-alive pings
- **ExecReload** via SIGHUP for immediate config re-evaluation
- **Security hardening** (score 1.5 OK): syscall filtering, empty capability bounding set, restricted namespaces
- **Polkit policy** grants `zextras` user permission to manage `carbonio-*` systemd units

## License

See [COPYING](COPYING) file — released under the **GNU Affero General Public License v3.0**.

Copyright 2026 Zextras <https://www.zextras.com>
