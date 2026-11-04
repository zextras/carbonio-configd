<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Command-line reference

`configd` is one binary (`/opt/zextras/bin/configd`). Without a subcommand it
runs the daemon; every other behaviour is a subcommand. The legacy Carbonio
tools (`zmcontrol`, `zmlocalconfig`, `zm*ctl`, ...) are thin shell wrappers
shipped by the `carbonio-core` package that `exec` the matching subcommand, see
[Legacy wrappers](#legacy-wrappers).

Source of truth: the Kong structs in `cmd/configd/` and the packages under
`internal/cli/`. `configd --help` and `configd <command> --help` print the
same flags.

- [Global flags](#global-flags)
- [Who may run what](#who-may-run-what)
- [Exit codes](#exit-codes)
- [daemon (no subcommand)](#daemon-no-subcommand)
- [rewrite](#rewrite)
- [plan](#plan)
- [shadow](#shadow)
- [service](#service)
- [control](#control)
- [status](#status)
- [init](#init)
- [localconfig](#localconfig)
- [proxy](#proxy)
- [tls](#tls)
- [ldap](#ldap)
- [release](#release)
- [completion](#completion)
- [Legacy wrappers](#legacy-wrappers)

## Global flags

| Flag | Meaning |
|---|---|
| `-h`, `--help` | context-sensitive help |
| `-v`, `--version` | print the version (`configd <version> (<revision>[, dirty][, <time>])`) and exit |
| `--disable-restarts` | dry-run: never restart a service |

Builds with extra tags add root flags (not in the packaged binary):

| Build tag | Flags |
|---|---|
| `profiling` | `--once` (one config cycle, then exit), `--cpuprofile FILE`, `--memprofile FILE`, `--trace FILE`, `--profile-duration SECONDS` (`0` = whole run) |
| `tracing` | `--enable-tracing`, `--tracing-output FILE` (default `trace-spans.json`) |

See [performance.md](performance.md#profiling-tooling) for how to use them.

A command-line parse error exits `1` (not Kong's default `80`), like the legacy tools.

## Who may run what

The daemon and every command that changes the host require the `zextras` user
(root is refused too); they print `Please run this program as the 'zextras'
user.` and exit `1` otherwise.

| Requires `zextras` | No user check |
|---|---|
| daemon, `rewrite`, `plan`, `shadow`, `control` (all actions), `init`, `tls`, `service start/stop/kill/restart/reload/refresh/rotate` (and the hidden `*-systemd` leaves), `proxy enable`, `proxy disable`, `proxy` (rewrite), `proxy gen` unless `-n`, `-d` or `-D` | `status`, `service list`, `service status`, `proxy conf`, `proxy status`, `proxy gen -n/-d/-D`, `localconfig`, `ldap`, `release`, `completion`, `--help`, `--version` |

## Exit codes

| Code | Meaning |
|---|---|
| `0` | success |
| `1` | any failure: wrong user, command error, a service stopped (`status`, `service status`, `control status`), unknown service, parse error, an `ERROR` reply from the daemon |
| `2` | only `configd plan`: the plan could not be computed |

`configd plan` also uses `1` for "changes would be made", see [plan](#plan).

## daemon (no subcommand)

```bash
systemctl start carbonio-configd.service     # systemd hosts
systemctl reload carbonio-configd.service    # SIGHUP: run a full cycle now
configd                                      # legacy hosts: started by zmcontrol / zmconfigdctl
```

The daemon loads LDAP and localconfig, plans, renders configuration files,
applies Postfix settings and restarts services as configured, then repeats
every `zmconfigd_interval` seconds. It listens on the loopback port
`zmconfigd_listen_port` (default `7171`) for `STATUS` and `REWRITE` requests,
only after its first config pass has completed. See
[architecture.md](architecture.md) for the cycle model and
[configuration.md](configuration.md) for the keys that shape it.

Signals: `SIGTERM`/`SIGINT` stop it; `SIGHUP`, `SIGUSR2` and `SIGALRM` end the
current sleep and run a full cycle (always a full load, even with
`zmconfigd_skip_idle_polls=TRUE`). `SIGCHLD` is ignored.

Only `--disable-restarts` (and the build-tag flags above) apply to the
daemon. Under systemd see [systemd.md](systemd.md) for readiness, watchdog and
reload semantics.

## rewrite

```bash
configd rewrite <section>...            # e.g. configd rewrite proxy mta, or: all
```

Asks the running daemon to rewrite the named `zmconfigd.cf` sections and waits
for it (legacy `configrewrite`; `/opt/zextras/libexec/configrewrite` is the
wrapper).

- The request is **synchronous**: the daemon replies `SUCCESS REWRITES COMPLETE` only after its main loop has rewritten the sections, `ERROR REWRITE TIMEOUT` after 60 s (the request stays queued for the next cycle), `ERROR NO SERVICES LISTED` for an empty list.
- The client waits up to `zimbra_configrewrite_timeout` seconds for the reply (default `120`, longer than the daemon's 60 s so the daemon's timeout arrives first).
- It exits `0` on success and `1` on an `ERROR` reply, timeout or lost connection.
- Only when **nothing listens** on the daemon port (tried on both loopback families, IPv4 and IPv6) does it fall back to a standalone forced rewrite in its own process, as legacy `zmconfigd <sections>` did; the exit code is `1` if its plan or a rewrite failed. A daemon that is alive but failing is never bypassed.
- A rewrite never restarts the services of the sections it names in that cycle (their `ExecStartPre` sends `REWRITE`). When a variable of such a section changed in the same cycle, the daemon logs a WARN and runs the restarts in its next periodic cycle ([restart-behavior.md](restart-behavior.md#a-change-consumed-by-a-rewrite-cycle-deferred-restarts)). The standalone fallback has no daemon and no baseline, so it restarts nothing.

The same port speaks a tiny line protocol (`STATUS` → `SUCCESS ACTIVE`,
`REWRITE <sections...>`); there is no need to call it by hand.

## plan

```bash
configd plan [<section>...] [--json]
```

Dry run: loads the configuration like a forced rewrite, renders the templates
in memory, reads Postfix with `postconf -h`/`postconf -n`, compares with the
files on disk and prints one line per action with the reasons (or JSON). It
changes nothing. Without arguments every section is evaluated.

```
$ configd plan mta
rewrite      conf/postfix/master.cf.in -> conf/postfix/master.cf
    because: drift: /opt/zextras/conf/postfix/master.cf content differs from the rendered template
postconf -e  smtpd_tls_loglevel=1
    because: drift: main.cf smtpd_tls_loglevel = "0" would become "1"
restart      mta
    because: drift: configuration of section mta would change
```

(Illustrative; an empty plan prints `No changes.`)

Exit codes: `0` no changes, `1` changes would be made, `2` error (including a wrong user).

Limitations:

- LDAP `cn=config` attributes cannot be read back, so planned `ldap` writes are always listed.
- `proxygen` is listed only when another action of one of its sections drifted; the generated proxy files themselves are not rendered.
- The restarts shown are those the drifted sections would trigger. The running daemon restarts only on variable changes (a change consumed by a REWRITE cycle is restarted by the next periodic one and shown as a `deferred` line) and a forced rewrite never restarts. On a systemd host the heavy services (mailbox, appserver-db, store, ldap) appear as `pending-restart` ([restart-behavior.md](restart-behavior.md#systemd-hosts-hybrid-restart-policy)).

## shadow

```bash
configd shadow [--shadow-dir DIR] [--single-cycle]
```

Runs the normal daemon cycle (load, snapshot, plan) on the poll interval
**next to a real configd** (legacy Jython or another Go release) with no side
effect: it writes no file, runs no command (no `postconf`, no LDAP write, no
restart), takes no watchdog action, does not listen on 7171 and sends no
`sd_notify`. Use it on QA nodes to compare what this build *would* do with what
the real configd does. Details, output format and the `--shadow-dir` layout are
in [architecture.md](architecture.md#shadow-mode).

| Flag | Meaning |
|---|---|
| `--shadow-dir DIR` | write the would-be files and plan JSON under `DIR` (created `0700`, files `0600`); must not be or contain the install tree |
| `--single-cycle` | run one cycle and exit (`1` when it failed) |

`--cpuprofile`, `--memprofile`, `--trace` and `--enable-tracing` are refused
(their output files would be side effects). `SIGHUP` runs the next cycle now;
`SIGTERM`/`SIGINT` stop it.

## service

```bash
configd service <verb> <name> [norewrite]
```

Controls one service: the verbs of the legacy `zm*ctl` scripts.

| Verb | Effect |
|---|---|
| `list` | every service with its status |
| `start`, `restart` | start / restart; regenerate configuration first unless `norewrite`. Flag form: `--no-rewrite` |
| `stop` | stop |
| `kill` | alias of `stop` (legacy scripts treat them alike) |
| `reload` | reload; `norewrite` honoured |
| `status` | detailed status (pid, uptime); exit `1` and `<service> is not running.` when it is stopped |
| `refresh` | make the service re-read its configuration: defined only for `milter` (sends `SIGCONT` to the JVM) |
| `rotate` | make the service reopen its logs: defined only for `stats` (`SIGHUP` to every collector) |

`norewrite` is a positional word, accepted by every verb but meaningful only
for `start`, `restart` and `reload`. Any other trailing argument is accepted
and ignored, so a wrapper never fails on a stray argument. An empty or unknown
verb prints `Usage: configd service
start|stop|kill|restart|reload|status|refresh|rotate <service> [norewrite]`
on stderr and exits `1`. A verb a service does not have (e.g. `refresh` on
`mta`) fails with `Error: ...` and exit `1`.

On an LDAP replica whose `ldap_url` is its `ldap_master_url`, every verb for
`ldap` except `stop`/`kill` is refused (legacy `ldap.sh` guard).

Three hidden verbs, `start-systemd`, `stop-systemd` and `restart-systemd`, are
what the Carbonio systemd units run as `ExecStart`/`ExecStop`: they act
in-process and never call `systemctl`, so a unit cannot recurse into itself.

How a start/stop is carried out depends on the orchestration mode: with a
Carbonio systemd target enabled the verb goes through `systemctl` (polkit rule
`50-carbonio-configd.rules` allows the `zextras` user), otherwise through the
legacy launchers (binary, pid file, process identity). See
[restart-behavior.md](restart-behavior.md#cli-verbs).

### Service names

Registry names (what `service list` shows, start order top to bottom):

| Name | What it is | systemd unit(s) |
|---|---|---|
| `ldap` | OpenLDAP slapd (stop waits for LMDB flush, then SIGKILL) | `carbonio-openldap` |
| `configd` | this daemon | `carbonio-configd` |
| `service-discover` | observed only: start/stop/restart/reload are no-ops | `service-discover` |
| `appserver-db` | mailbox MariaDB (component of `store`) | `carbonio-appserver-db` |
| `store` | composite = `appserver-db` + `mailbox` (legacy `zmstorectl`) | both |
| `mailbox` | the mailboxd JVM alone (legacy `zmmailboxdctl`) | `carbonio-appserver` |
| `memcached` | memcached | `carbonio-memcached` |
| `proxy` | nginx | `carbonio-nginx` |
| `amavis` | amavisd + amavis-mc (component of `antivirus`/`antispam`) | `carbonio-mailthreat` |
| `antispam-db` | antispam MariaDB (component of `antispam`) | `carbonio-antispam` |
| `antispam` | composite = `antispam-db` (if local) + `amavis`; every verb is a successful no-op while the service is not enabled for the host (legacy `zmantispamctl`) | `carbonio-antispam`, `carbonio-mailthreat` |
| `antivirus` | composite = `amavis` + `clamd` + `freshclam` (legacy `zmantivirusctl`) | `carbonio-mailthreat`, `carbonio-antivirus`, `carbonio-freshclam` |
| `clamd` | clamd alone (legacy `zmclamdctl`) | `carbonio-antivirus` |
| `freshclam` | freshclam | `carbonio-freshclam` |
| `opendkim` | opendkim | `carbonio-opendkim` |
| `cbpolicyd` | cbpolicyd (creates its sqlite DB on first start) | `carbonio-policyd` |
| `saslauthd` | saslauthd | `carbonio-saslauthd` |
| `milter` | milter JVM; only started when enabled in `mta_milter_options` | `carbonio-milter` |
| `mta` | Postfix (depends on `saslauthd` and `milter`) | `carbonio-postfix` |
| `stats` | the zmstat collectors | `carbonio-stats` |

Accepted aliases (resolved before lookup; they never appear in listings):

| Alias | Resolves to |
|---|---|
| `mailboxd` | `mailbox` |
| `mysql`, `mariadb` | `appserver-db` |
| `service` | `store` |
| `zmconfigd`, `config-service` | `configd` |
| `directory`, `directory-server` | `ldap` |

`amavis`, `antispam-db`, `appserver-db`, `clamd` and `mailbox` are *components*:
addressable one by one, but `control` walks their composite (`store`,
`antispam`, `antivirus`) instead.

Composites start their members in order (with `norewrite` for the members of
`store` and `antivirus`, after regenerating the composite's own configuration
once). `store` stops in reverse order (JVM first, database last);
`antivirus` and `antispam` stop in start order, as the legacy scripts do.

#### MariaDB safe stop

`appserver-db` and `antispam-db` are stopped the way the legacy scripts did:
`SIGTERM` to `mariadbd`, then a bounded wait (`zimbra_mysql_shutdown_timeout`
seconds, default 60). configd **never escalates to `SIGKILL`**: a server that is
still flushing is reported as an error and left running, because killing it
needs InnoDB crash recovery at the next start. Before stopping `appserver-db`,
configd sets `innodb_max_dirty_pages_pct=0` and waits up to 600 s for the
buffer pool to drain (needs `zimbra_mysql_password`; best effort, a failed step
is logged and the stop continues). `mailbox` has a `Wants` on `appserver-db`:
starting the JVM brings the database up, stopping the JVM leaves it up.

## control

```bash
configd control start|startup|stop|shutdown|restart|status
```

`zmcontrol`: orchestrates every service enabled on this host (from
`zimbraServiceEnabled` in LDAP, plus services enabled by other means such as
`milter`), in dependency order. `startup` = `start`, `shutdown` = `stop`.
`restart` is stop then start, ignoring the result of the stop (as legacy
`control.pl`). `control status` exits `1` when a service is stopped. Start/stop
failures print `Error: some services failed to start|stop` and exit `1`.
Remote hosts (`zmcontrol -H`) are not supported here, the wrapper rejects `-H`;
use `/opt/zextras/libexec/zmrc`.

## status

```bash
configd status            # table of every service, exit 1 if any is not running
configd status <name>     # same output as: configd service status <name>
```

No user check. The summary also prints the Carbonio Advanced module section
when present.

## init

```bash
configd init mta|proxy [--force]
```

First-time initialisation of a component: refuses when its configuration
already exists (`conf/nginx/nginx.conf`, `common/conf/postfix/main.cf`) unless
`--force`; asks the **running** daemon to `REWRITE` the component (an
unreachable daemon is an error: `systemctl start carbonio-configd.service`);
then starts the service. A failed start exits `1` (the configs were still
generated: start the service manually). Every failure message on stderr starts
with `Error:`.

## localconfig

```bash
configd localconfig [flags] [key | key=value]...
```

Drop-in for the Java `zmlocalconfig`, without the 2-4 s JVM start. It reads the
effective localconfig (defaults, `localconfig.xml`, `CONFIGD_LC_<key>`
environment, see [configuration.md](configuration.md#effective-localconfig)),
resolves `${variable}` references and can edit `localconfig.xml`. Behaviour
details, option compatibility rules and messages are in
[localconfig.md](localconfig.md#cli-behavior-legacy-localconfigcli-parity).

```bash
eval "$(configd localconfig -q -m export)"          # load every key into the shell
configd localconfig -k mailboxd_java_options -k ldap_port
configd localconfig -m export -k zimbra_home        # export zimbra_home='/opt/zextras';
configd localconfig -e smtp_port=587                # edit
configd localconfig -e -r ldap_root_password        # random password
configd localconfig -u custom_key                   # unset
configd localconfig -p                              # path of localconfig.xml
```

| Flag | Long names | Meaning |
|---|---|---|
| `-m MODE` | `--mode`, `--format` | `plain` (default), `shell`, `export`, `nokey`, `xml` |
| `-k KEY` | `--key` | key to print (repeatable; positional keys work too; none = all) |
| `-c PATH` | `--config-path`, `--config` | `localconfig.xml` to use (default `$CARBONIO_BASE_DIR/conf/localconfig.xml`, i.e. `/opt/zextras/conf/localconfig.xml`) |
| `-q` | `--quiet` | suppress warnings |
| `-s` | `--show-passwords`, `--show` | show password values (default: keys containing `password` print `*` in plain/xml/nokey; shell and export never mask) |
| `-d` | `--show-defaults`, `--default` | show default values |
| `-n` | `--show-changed`, `--changed` | only keys that differ from the defaults |
| `-x` | `--expand` | expand `${var}` (shell and export always do) |
| `-e` | `--edit` | set `key=value` pairs (all validated first, saved once) |
| `-u` | `--unset` | remove keys (all-or-nothing) |
| `-r` | `--random` | with `-e`: random password value |
| `-f` | `--force` | allow editing protected keys |
| `-p` | `--show-path`, `--path` | print the file path and exit |
| `-i` | `--info` | documentation of the listed keys (all supported keys if none) |
| | `--all` | like `-i`, including unsupported keys |
| `-l` | `--reload` | ask mailboxd (admin SOAP `ReloadLocalConfigRequest`) to reload its local config |

Incompatible combinations fail with `option 'X' can not be used with option
'Y'` or `invalid option 'X'`, exit `1`. A key set through `CONFIGD_LC_<key>`
shows its environment value and is refused by `-e`/`-u`. A key with no value
(a null default such as `mysql_bind_address`) prints nothing and warns
`null valued key`.

## proxy

```bash
configd proxy gen [flags]               # zmproxyconfgen
configd proxy conf [flags] [nginx.conf] # zmproxyconf
configd proxy enable|disable <protocol> # http, https, mail, imap, imaps, pop3, pop3s
configd proxy status
configd proxy [<config>...]             # no subcommand: forced rewrite of the proxy section
```

`proxy gen` renders the nginx configuration synchronously in the current
process (it does not talk to the daemon) and takes the `ProxyConfGen` flags.
`proxy conf` prints the assembled nginx configuration by following the
`include` directives. The template language, `-c` variables and the files
written are described in [proxy-templates.md](proxy-templates.md).

`proxy gen` flags:

| Flag | Meaning |
|---|---|
| `-v`, `--verbose` | debug logging (the root `-v` is `--version`, so `-v` after `proxy gen` is rewritten to `--verbose`, bundles like `-vn` included) |
| `-w`, `--workdir DIR` | working directory (default `/opt/zextras`) |
| `-t`, `--templatedir DIR` | template directory (default `$workdir/conf/nginx/templates`) |
| `-n`, `--dry-run` | write nothing, list the files that would be written |
| `-d`, `--defaults` | print the default variable map |
| `-D`, `--definitions` | print the variable map after loading the configuration |
| `-p`, `--prefix NAME` | config file prefix (default `nginx.conf`) |
| `-P`, `--template-prefix NAME` | template file prefix (default: the prefix) |
| `-i`, `--include-dir DIR` | where included files go, relative to `$workdir/conf` (default `nginx/includes`) |
| `-s`, `--server NAME` | generate from this server object instead of the local one |
| `-f`, `--force-dns-resolution` | stop if the virtual hostname of a domain does not resolve |
| `-c`, `--config NAME=VALUE` | override a variable (repeatable; extra `name=value` words after `-c` count too) |

Exit `0` on success (also for `-d`, `-D`, and stray non-`name=value`
arguments, which print the usage), `1` on any error.

`proxy conf` flags: `-m`/`--markers` (print file inclusion markers),
`-i`/`--indent` (indent included files), `-n`/`--no-comments` (alias
`--nocomments`), `-e`/`--no-empty` (alias `--noempty`).

`proxy enable|disable <protocol>` sets the matching
`zimbraReverseProxy…Enabled` attribute in LDAP to `TRUE`/`FALSE`; `proxy
status` lists every protocol as `enabled`/`disabled`.

## tls

```bash
configd tls [both|http|https|mixed|redirect] [--force] [--host HOSTNAME]
```

Legacy `zmtlsctl`. With a mode: connects to the LDAP master, checks the mode
against the cluster's proxies (`zimbraReverseProxyMailMode`,
`zimbraReverseProxySSLToUpstreamEnabled`; skipped when the host is not a
reverse-proxy backend or no proxy exists; `--force` skips the check), sets
`zimbraMailMode` on the host's server entry, then rewrites the mailbox
configuration (`sasl webxml mailbox service zextras zextrasAdmin zimlet`), via
the daemon or in-process like [`rewrite`](#rewrite). Without a mode only the
rewrite runs. `--host` overrides `zimbra_server_hostname`. Exit `1` on any
failure. The `zmtlsctl` wrapper maps `help`, `--help`, `-help` to `configd tls
--help`.

## ldap

`zmprov` without the JVM, for the few verbs configd and its callers use. The
credentials come from the effective localconfig, so a container without
`localconfig.xml` sets them in the environment (see
[configuration.md](configuration.md#containers-and-kubernetes)).

```bash
configd ldap gacf                                   # zmprov gacf: the global config
configd ldap gcf zimbraMtaRestriction zimbraMailMode
configd ldap gs [server]                            # zmprov gs, global values inherited; default: zimbra_server_hostname
configd ldap mcf +zimbraMtaRestriction reject_x     # zmprov mcf: + adds, - removes, none replaces
configd ldap mcf -zimbraMtaRestriction reject_x
configd ldap wait --timeout 120s                     # block until ldap_url accepts connections (used by the systemd unit); exits 0 at once when no ldap_url is set
configd ldap search --base cn=servers,cn=zimbra --scope one --filter '(cn=mail*)' --attr zimbraServiceEnabled
```

Connection flags go **before** the verb and override the localconfig keys:

| Flag | Default |
|---|---|
| `--url URL` (repeatable, failover) | `ldap_master_url`, `ldap_url` |
| `--bind-dn DN` | `zimbra_ldap_userdn` |
| `--password-file FILE` | `zimbra_ldap_password` (the password is never a flag) |
| `--json` | print JSON instead of `attr: value` lines |

`search` flags: `--base` (default `cn=zimbra`), `--filter` (default
`(objectClass=*)`), `--scope base|one|sub` (default `sub`), `--attr` (repeatable;
default all). Output is one line per value, attribute names sorted. `mcf` is
idempotent (adding a present value or removing an absent one succeeds). `gs`
reads the inherited attributes from `conf/attrs` under `zmconfigd_template_dir`
(default `CARBONIO_BASE_DIR`). A failure prints `Error: <message>` and exits `1`.

StartTLS is used only when `ldap_starttls_supported=1` together with
`zimbra_require_interprocess_security=1`; see
[architecture.md](architecture.md#ldap-transport-security-and-timeouts).

## release

```bash
configd release        # Carbonio Release <contents of /opt/zextras/.version>
configd release -V     # also the installed carbonio packages (dpkg-query or rpm)
```

Always exits `0`, even with a missing `.version` (like legacy `zmcontrol -v`/`-V`).

## completion

`configd completion` installs shell completion; `--uninstall` removes it. The
package also installs bash completion files for `configd` and for the legacy
wrapper names.

## Legacy wrappers

`carbonio-core` installs these under `/opt/zextras/bin` (and `libexec`); each
is a `#!/bin/bash` one-liner around the binary, so scripts keep working.

| Legacy command | Runs |
|---|---|
| `zmcontrol start\|startup\|stop\|shutdown\|restart\|status` | `configd control <action>` |
| `zmcontrol -v` / `-V` | `configd release` / `configd release --packages` |
| `zmcontrol -H host …` | rejected (never worked in `control.pl`): use `/opt/zextras/libexec/zmrc` |
| `zmlocalconfig …` | `configd localconfig …` |
| `zmtlsctl …` | `configd tls …` |
| `zmproxyconf …` | `configd proxy conf …` |
| `libexec/zmproxyconfgen …` | `configd proxy gen …` |
| `libexec/configrewrite …` | `configd rewrite …` |
| `zmconfigdctl <verb>` | `configd service <verb> configd` |
| `ldap <verb>` | `configd service <verb> ldap` |
| `zmstorectl` | `configd service <verb> store` |
| `zmmailboxdctl` | `… mailbox` |
| `zmmtactl` | `… mta` |
| `zmproxyctl` | `… proxy` |
| `zmmemcachedctl` | `… memcached` |
| `zmopendkimctl` | `… opendkim` |
| `zmsaslauthdctl` | `… saslauthd` |
| `zmcbpolicydctl` | `… cbpolicyd` |
| `zmmilterctl` | `… milter` |
| `zmstatctl` | `… stats` |
| `zmamavisdctl` | `… amavis` |
| `zmantispamctl` | `… antispam` |
| `zmantivirusctl` | `… antivirus` |
| `zmclamdctl` | `… clamd` |
| `zmfreshclamctl` | `… freshclam` |

The `zm*ctl` wrappers forward the verb and trailing arguments unchecked
(`configd service "${1:-}" <name> "${@:2}"`), which is why `norewrite` and an
empty verb are handled by `configd service` itself.

The wrappers are part of `carbonio-core`, not of this repository.
