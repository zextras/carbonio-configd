<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Configuration

configd has no configuration file of its own. Everything comes from three
places: the **effective localconfig**, a few **environment variables** and the
**command-line flags** (see [cli.md](cli.md)). What the daemon *renders* is
defined by `conf/zmconfigd.cf` and its templates, which belong to
`carbonio-core`.

- [Effective localconfig](#effective-localconfig)
- [Keys the daemon reads](#keys-the-daemon-reads)
- [Polling and reload](#polling-and-reload)
- [Environment variables](#environment-variables)
- [Containers and Kubernetes](#containers-and-kubernetes)

## Effective localconfig

Every command and the daemon read localconfig through one function. The
layers, lowest precedence first:

1. the compiled-in defaults, a port of the `LC.java` key table (so the daemon needs no JVM and `configd localconfig -d` can show them);
2. `localconfig.xml` (`$CARBONIO_BASE_DIR/conf/localconfig.xml`, default `/opt/zextras/conf/localconfig.xml`); a **missing file is an empty layer**, an unreadable or malformed one is an error;
3. environment variables `CONFIGD_LC_<key>=<value>`, which set the key `<key>`;
4. per-command flags (for example `configd ldap --url`).

`${key}` references are expanded once over the merged map. Keys set by the
environment are read-only for `configd localconfig -e`/`-u`. Full semantics,
conversion rules from Java and the key registry are in
[localconfig.md](localconfig.md).

The installation root is `CARBONIO_BASE_DIR` (default `/opt/zextras`);
`ZEXTRAS_HOME` is not read.

## Keys the daemon reads

Defaults below are the **effective** ones (layer 1 included): they are what
`configd localconfig -k <key>` prints on a host without an override. Detailed
descriptions of the opt-in and container keys are in
[localconfig.md](localconfig.md#configd-only-keys-not-in-the-defaults-registry).

### Daemon behaviour

| Key | Effective default | Effect |
|---|---|---|
| `zmconfigd_interval` | `60` | seconds between cycles |
| `zmconfigd_listen_port` | `7171` | loopback port of the `STATUS`/`REWRITE` listener, also used by `configd rewrite`, `init`, `tls` and `proxy` to find the daemon |
| `zmconfigd_skip_idle_polls` | off | `TRUE`: skip the config-load phase of a cycle when no `REWRITE` arrived since the last one; changes made only in LDAP then wait for a `REWRITE`, a `SIGHUP` or the LDAP-change wake-up (on by default, see `zmconfigd_ldap_change_trigger`), which always force a full cycle |
| `zmconfigd_enable_config_restarts` | on | `FALSE`: the restarts a cycle plans are not performed (they are carried over, not dropped) |
| `zmconfigd_restart_debounce_seconds` | `0` (off) | coalesce restarts across cycles |
| `zmconfigd_watchdog` | on | `FALSE` turns the service watchdog off |
| `zmconfigd_watchdog_interval` | `120` | seconds between watchdog checks (compiled-in fallback `120`) |
| `zmconfigd_watchdog_services` | `antivirus` | space-separated service names the watchdog keeps running |
| `zmconfigd_ldap_change_trigger` | on | wake the main loop as soon as a watched LDAP entry changes (content-sync stream when the directory has `syncprov`, polling otherwise); `false`, `no`, `0` or `off` (case-insensitive) opts out. Counted in `configd_ldap_change_triggers_total` |
| `zmconfigd_ldap_change_poll_seconds` | `5` | poll interval of that watcher in poll mode (unused while a stream is live) |
| `zmconfigd_ldap_change_debounce_seconds` | `2` | quiet window of that watcher |
| `zmconfigd_cycle_summary` | off | `TRUE`: one `Cycle summary` log line per cycle |
| `zmconfigd_metrics_textfile` | empty | path of a Prometheus textfile written every cycle |

The keys `zmconfigd_log_level` (`3`) and `zmconfigd_debug` are parsed but do
not change the log level; use `CONFIGD_LOG_LEVEL` below.

The two keys of the legacy table `zmconfigd_startup_pause` and
`zmconfigd_max_failed_restarts` exist in the defaults registry but nothing in
this daemon reads them.

Restart behaviour (what restarts, the hybrid policy on systemd hosts, the
watchdog's retry rules) is described in [restart-behavior.md](restart-behavior.md).

### Scope and containers

| Key | Default | Effect |
|---|---|---|
| `zmconfigd_sections` | empty (all) | limit every cycle (daemon, forced rewrite, `shadow`, `plan`) to these sections |
| `zmconfigd_template_dir` | `CARBONIO_BASE_DIR` | read templates (`REWRITE` sources, `conf/zmconfigd/*.cf`, `conf/zmconfigd.cf`, `conf/attrs`) from this directory; outputs stay under `CARBONIO_BASE_DIR` |
| `zmconfigd_service_hooks` | empty | JSON file replacing systemd / `zm*ctl` with `signal:`, `exec:`, `skip` hooks per service and action |

### Keys read from the host's localconfig

| Key | Used for |
|---|---|
| `ldap_url`, `ldap_master_url`, `zimbra_ldap_userdn`, `zimbra_ldap_password`, `ldap_is_master`, `ldap_root_password` | LDAP connection and master/replica decisions |
| `ldap_starttls_supported`, `ldap_starttls_required`, `zimbra_require_interprocess_security` | StartTLS rule: used only when `ldap_starttls_supported=1`, `ldap_starttls_required` (default true) **and** `zimbra_require_interprocess_security=1` ([architecture.md](architecture.md#ldap-transport-security-and-timeouts)) |
| `zimbra_server_hostname` | which server entry this node renders (the process environment variable of the same name wins, then `/etc/hostname`) |
| `zimbra_configrewrite_timeout` | seconds a client waits for a `REWRITE` reply (default `120`) |
| `zimbraIPMode` | only if an operator set it as a localconfig key: picks the loopback family the client tries first; the attribute itself is read from LDAP |
| `zimbra_mysql_password`, `zimbra_mysql_shutdown_timeout` | MariaDB flush and safe stop ([cli.md](cli.md#mariadb-safe-stop)) |
| everything else in `LC.java` | available to templates as `${key}` and to the service launchers (heap sizes, paths, ...) |

## Polling and reload

The daemon runs one cycle at start and then every `zmconfigd_interval` seconds
(effective default `60`). Every cycle reloads LDAP and localconfig, so
`zmprov` and admin-console changes apply on the next cycle. These end the
sleep early and run a **full** cycle:

- `SIGHUP`, `SIGUSR2`, `SIGALRM` (`systemctl reload carbonio-configd.service` sends `SIGHUP`);
- a `REWRITE` request on the listener;
- an LDAP change, unless `zmconfigd_ldap_change_trigger` is `false`, `no`, `0` or `off` (the default is on: a cycle starts within seconds of a change to the global config, a server entry or a domain, not up to `zmconfigd_interval` later, see [architecture.md](architecture.md#ldap-change-detection)).

What a cycle does (load, snapshot, plan, execute, apply restarts) is in
[architecture.md](architecture.md#cycle-model-load-snapshot-plan-execute-apply-restarts).

## Environment variables

| Variable | Read by | Meaning |
|---|---|---|
| `CARBONIO_BASE_DIR` | all commands | installation root (default `/opt/zextras`) |
| `CONFIGD_LC_<key>` | all commands | localconfig key override, see above |
| `CONFIGD_LOG_LEVEL` | daemon and commands | `debug`, `info` (default), `warn`, `error`. `service`, `control`, `status` and `release` log nothing but errors (to `/dev/null`) unless this is set; then they log to stderr |
| `CONFIGD_LOG_FORMAT` | daemon and commands | `text` (default) or `json` |
| `zimbra_server_hostname` | daemon | overrides the server hostname |
| `PERL5LIB`, `PERLLIB` | child processes | set by the daemon when `PERL5LIB` is unset so amavisd finds the bundled Perl modules |
| `NOTIFY_SOCKET`, `WATCHDOG_USEC` | daemon | set by systemd; enable `sd_notify` and watchdog pings |

## Containers and Kubernetes

A pod needs no `localconfig.xml`: set the keys in the environment.

```bash
export CONFIGD_LC_ldap_url=ldap://carbonio-openldap:1389
export CONFIGD_LC_zimbra_ldap_userdn=uid=zimbra,cn=admins,cn=zimbra
export CONFIGD_LC_zimbra_ldap_password=...
export CONFIGD_LC_ldap_starttls_supported=0   # plain ldap:// is the default
export CONFIGD_LC_zmconfigd_sections="proxy"  # optional: only these sections
```

The image sets `CARBONIO_BASE_DIR` and `CONFIGD_LC_zmconfigd_template_dir`
itself; see [systemd.md](systemd.md#container-image). Use
`zmconfigd_service_hooks` to replace systemd/`zm*ctl` restarts with signals
(see [localconfig.md](localconfig.md#service-hooks-zmconfigd_service_hooks)); the
daemon refuses to start, listing the missing hooks, if a restart or watchdog
status of an in-scope service is not covered.
