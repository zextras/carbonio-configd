<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Local Configuration

## Defaults registry

Source: `internal/localconfig/lcdefaults.go` (map `lcDefaults`), exposed via
`internal/localconfig/defaults.go` as `Defaults = buildDefaults()` (a copy of `lcDefaults`);
`IsProtected` and `IsNullKey` answer for the `.protect()` set and `lcNullKeys`. The Kubernetes
keys of a pod and the other `zmconfigd_*` settings the daemon honors are summarized in
[configuration.md](configuration.md).

`lcDefaults` is a verbatim port of **all 629** `KnownKey` defaults (610 in `lcDefaults` plus 19 with a Java `null` default in `lcNullKeys`) declared in
carbonio-mailbox's `common/src/main/java/com/zimbra/common/localconfig/LC.java`
(tag `v4.38.16`, pinned in `scripts/lc-mailbox-tag`). configd is now the only local-config implementation present on a
node: the Java `LocalConfigCLI` has been retired, and the `zmlocalconfig` wrapper (installed by
carbonio-core, not by this repo) execs `configd localconfig`; so does `zmsetvars`
(`bin/shutil.sh`). There is no fallback path to a Java-backed
default anymore, so the registry MUST stay a complete mirror of `LC.java` — every key
it declares belongs in `lcDefaults`, not a curated subset. When `LC.java` gains a key,
changes a default, or removes one, mirror the change in `lcdefaults.go` in the same
change.

### Java-to-Go value conversion rules

The port applies these conversions to each `KnownKey` default:

- Java `null` -> no default (19 keys, `lcNullKeys`): legacy `LocalConfig` skips such a key in
  every listing and warns `null valued key '<key>'` when it is named, unless `localconfig.xml`
  sets it.
- Numeric and boolean literals -> decimal text (e.g. `7025`) or `"true"`/`"false"`.
- `Constants.MILLIS_PER_*`, `Integer.MAX_VALUE`, and constant arithmetic expressions are
  evaluated to their literal result (e.g. `Constants.MILLIS_PER_MINUTE * 30` becomes the
  computed millisecond count as a decimal string).
- `${key}` references inside a default value are left intact — they are resolved later
  by `Interpolate`, not at port time.
- `KnownKey.protect(...)` chaining (masking) is ignored; only the underlying default
  value is ported, since protection state is not part of `Defaults`.

To add or change a default, edit the `lcDefaults` map in
`internal/localconfig/lcdefaults.go` to match the corresponding `KnownKey` entry in
`LC.java` exactly.

## Effective localconfig layers

Source: `internal/localconfig/effective.go` (`LoadEffective`, `LoadEffectiveFrom`). Every
consumer (the cycle's loaders, the service controller, `configd rewrite/proxy/tls/status/init`,
`configd ldap`, the `getlocal` command) reads localconfig through this one function. The layers,
lowest precedence first:

1. the `LC.java` defaults (`lcDefaults`);
2. `localconfig.xml` (`${CARBONIO_BASE_DIR:-/opt/zextras}/conf/localconfig.xml`). A **missing file
   is an empty layer**, so a pod needs none; an unreadable or malformed file is an error;
3. the environment: `CONFIGD_LC_<key>=<value>` sets the key `<key>`. Values are used verbatim (not
   trimmed), and an empty value counts as set;
4. per-command flag overrides (for example `configd ldap --url/--bind-dn/--password-file`).

`${key}` references are then expanded **once** over the merged map, so a value of any layer may
refer to a key of any other layer. With no `CONFIGD_LC_*` variable set and a `localconfig.xml`
present, the result is exactly the former `defaults + xml + interpolation`.

The installation root is `CARBONIO_BASE_DIR` (default `/opt/zextras`); it locates
`localconfig.xml`, `conf/zmconfigd.cf`, `conf/attrs`, the templates and the service scripts.
`ZEXTRAS_HOME` is not read.

Environment-set keys are read-only for the CLI. `configd localconfig -e`/`-u` refuse such a key
(`Error: key <key> is set by environment CONFIGD_LC_<key>`) and save nothing, and the read modes
print the effective value, environment included.

Keys a pod without `localconfig.xml` sets (the Kubernetes deployment):

| Variable | Used for |
|---|---|
| `CONFIGD_LC_ldap_url` | LDAP URL(s) of the cycle's data client and of `configd ldap` (space-separated for failover) |
| `CONFIGD_LC_ldap_master_url` | Write URL(s); falls back to `ldap_url` |
| `CONFIGD_LC_zimbra_ldap_userdn` / `CONFIGD_LC_zimbra_ldap_password` | Bind DN and password |
| `CONFIGD_LC_ldap_starttls_supported`, `CONFIGD_LC_zimbra_require_interprocess_security` | A plain `ldap://` directory is the default (`0`). StartTLS is used only when `ldap_starttls_supported=1`, `ldap_starttls_required` (default true) **and** `zimbra_require_interprocess_security=1`; every LDAP client of configd (cycle data, writes, discovery, `configd ldap`) follows that one rule |
| `CONFIGD_LC_zimbra_server_hostname` | The server entry this node renders (otherwise `/etc/hostname`) |

## configd-only keys (not in the defaults registry)

`lcDefaults` mirrors `LC.java`, so keys that exist only in configd are read straight from
`localconfig.xml` with a built-in fallback (`internal/config/types.go`) and are **not** listed
in `lcdefaults.go` (generated by `scripts/gen-lc.py`; `make generate` needs a carbonio-mailbox
clone, and its output must not gain keys that `LC.java` lacks). The daemon's own tuning keys
(`zmconfigd_interval`, `zmconfigd_log_level`, `zmconfigd_debug`, `zmconfigd_watchdog`,
`zmconfigd_watchdog_interval`, `zmconfigd_enable_config_restarts`, `zmconfigd_skip_idle_polls`,
`zmconfigd_listen_port`, ...) are in this class too. The opt-in and container keys are:

| Key | Default | Effect |
|---|---|---|
| `zmconfigd_restart_debounce_seconds` | `0` (off) | When > 0, service restarts are coalesced across cycles: each new request for a service restarts its quiet window, and the service is restarted once no request arrived for that many seconds (trailing edge). The state is kept in memory only and is lost when configd restarts. With `0`, every restart is queued in the cycle it appears in. See [restart-behavior.md](restart-behavior.md). |
| `zmconfigd_metrics_textfile` | empty (off) | When set to a path, the operational metrics (cycle and phase durations, plan size by kind, restarts by service and reason, watchdog restarts, load failures) are written there every cycle in the Prometheus text format, atomically, for the node_exporter textfile collector (the file name must end in `.prom`). No network listener is opened. Empty writes nothing. See [architecture.md](architecture.md#metrics-opt-in). |
| `zmconfigd_cycle_summary` | `FALSE` (off) | `TRUE` logs one extra structured INFO line, `Cycle summary`, at the end of every cycle (durations and plan size by kind). Off by default so the log output is unchanged. |
| `zmconfigd_ldap_change_trigger` | `FALSE` (off) | When `TRUE` (case-insensitive; any other value is off), a watcher polls the LDAP entries a cycle reads and wakes the main loop for an immediate full cycle as soon as one changes, instead of waiting for the next `zmconfigd_interval`. The interval poll keeps running as the safety net. Watched: the global config (`cn=config,cn=zimbra`), the server entries under `cn=servers,cn=zimbra` (this server's own attributes and services, the upstream servers the proxy generates, and the servers `gamau`, `garpu` and `garpb` enumerate) and the `zimbraDomain` entries the proxy generator reads. Off, no watcher goroutine exists and nothing about a cycle changes. Read every cycle: `FALSE` stops a running watcher, but a key that is merely deleted keeps its last value until configd restarts (as for `zmconfigd_skip_idle_polls`). Ignored in single-run (`--once`) mode, and with a warning when configd has no native LDAP client. See [architecture.md](architecture.md#ldap-change-detection-opt-in). |
| `zmconfigd_ldap_change_poll_seconds` | `5` | Poll interval of the change watcher (values below 1 mean 5). Each poll is three searches that return only `entryCSN` and `modifyTimestamp`; the domains search walks every `zimbraDomain`, so on a directory with many thousands of domains raise this value. Read when the watcher starts: a change needs a configd restart (or the key set to `FALSE` for one cycle and back to `TRUE`). |
| `zmconfigd_ldap_change_debounce_seconds` | `2` | Quiet window that coalesces a burst of directory writes into one cycle: the cycle is requested once nothing changed for that long, or after ten windows of continuous writes at the latest. `0` requests the cycle in the poll that sees the change. Read when the watcher starts, like the poll seconds. |
| `zmconfigd_template_dir` | the base dir (`CARBONIO_BASE_DIR`) | Directory the templates are read from: the `REWRITE` sources, the `FILE` fragments (`conf/zmconfigd/*.cf`), `conf/zmconfigd.cf` and the LDAP attribute schemas (`conf/attrs`). Outputs (rewritten files, mapped files, postfix configuration) always stay under the base dir. Unset, nothing changes. For containers whose output volume hides the templates. |
| `zmconfigd_sections` | empty (all sections) | Space-separated section names. Every cycle (daemon, forced rewrite, `shadow`, `plan`) is limited to them (`plan.Input.Scope`); `configd plan <sections>` must name sections inside it, otherwise it fails. |
| `zmconfigd_service_hooks` | empty (host service control) | Path of a JSON file that replaces the host service control (systemd / legacy `zm*ctl`) with signals and commands; see below. |

### Service hooks (`zmconfigd_service_hooks`)

The file maps a registry service name (or `*`) to an action list, for the actions `start`, `stop`,
`restart` and `status`. A service entry wins over `*` for the same action. Each action is an ordered
list; the first failing hook fails the action.

```json
{
  "antivirus": { "restart": ["signal:TERM", "signal:TERM@freshclam"], "status": ["process"] },
  "amavis":    { "restart": ["signal:TERM"] },
  "mta":       { "restart": ["skip"] },
  "*":         { "start": ["skip"], "stop": ["skip"] }
}
```

| Hook | Valid for | Effect |
|---|---|---|
| `signal:<SIG>[@<service>]` | start, stop, restart | Sends `<SIG>` (HUP, INT, QUIT, TERM, KILL, USR1, USR2, optional `SIG` prefix) to the main process of the service, or of `<service>`. The PID comes from the registry's pid file validated against its process identity, or a scan of the process table (needs a shared process namespace and the same UID). No process is an error, so the restart is retried like any failed restart. |
| `exec:<argv...>` | all | Runs the command (whitespace-split, no shell) with a 30 s timeout. For `status`, exit 0 is running and a non-zero exit is not running. |
| `skip` | start, stop, restart | Logs `service action not managed by this process` at INFO and succeeds. Never implied: it only applies where declared. |
| `process` | status | The service is running when its process is found as above. |

An unknown service, action or hook is a startup error. After the first parse of `zmconfigd.cf` the
daemon also checks that every service named by a `RESTART` line of an in-scope section, and every
service a restart of those cascades into through `DEPENDS` (e.g. `amavis` from `antivirus`), has a
`restart` hook and, when the watchdog is on (`zmconfigd_watchdog`), that every watchdog service has a
`status` hook. It exits with status 1 listing what is missing (`restart:<svc>`, `status:<svc>`)
instead of retrying forever. A missing `start`/`stop` hook fails only when that action is attempted.

## CLI behavior (legacy `LocalConfigCLI` parity)

Source: `internal/cli/localconfig` (flag mapping in `cmd/configd/localconfig.go`). Reference: carbonio-mailbox `v4.38.16`
`LocalConfigCLI.java`, `LocalConfig.java`, `ConfigWriter.java`.

- Dispatch order, like legacy: `-i`, `--all` (no config needed), then the config file is loaded,
  then `-e`, `-u`, `-p`, `-l`, and finally printing.
- A missing or unreadable config file only warns
  (``Warning: local config file `<path>' is not readable``) and counts as empty, so `-e` creates
  it; a malformed one is fatal (`Warning: Problem parsing local config file '<path>'`,
  `Error: failed when reading config file`, then the parse error on a third line). A failed save
  prints `Error: save to <path> failed` and the cause on the next line.
- Legacy long option names (`--config`, `--path`, `--default`, `--changed`, `--show`, `--format`, ...)
  are Kong aliases of the flags; the names Kong derived earlier (`--config-path`, `--mode`, ...) still work.
  `-k`/`--key <key>` (repeatable) is an extra way to name a key besides the positional arguments.
- Option compatibility, like `checkCompatibleOptions`: besides its main option, a mode accepts only
  the listed options, otherwise the first offender in command-line order fails with
  `option 'X' can not be used with option 'Y'`:

  | Main option | Also allowed |
  |---|---|
  | `-e` | `-q -f -r -c` |
  | `-u` | `-q -f -c` |
  | `-p` | `-q -c` |
  | `-i`, `--all` | `-q` |
  | `-n`, `-d`, or plain print | `-q -s -c -m -x` |
  | `-l` | nothing is checked; the node's own `localconfig.xml` is used |
- `-u <key>` drops the entry from the file (a known key then falls back to its default; `""` is
  never written). A known key that is not in the file is a successful no-op; an unknown unset key
  is `key <key> is not set`.
- `-n` (legacy `printChanged`) lists the keys configured in the file (or the keys given), sorted,
  and drops a known key whose *expanded configured* value equals its *raw* compiled-in default; a
  default containing `${...}` therefore always counts as changed. A listed key with no value
  warns `null valued key '<key>'`; `-d` warns `not a known key '<key>'` and plain print warns
  `null valued key '<key>'`.
- `-e`/`-u` refuse a key set by `CONFIGD_LC_<key>` (`key <key> is set by environment
  CONFIGD_LC_<key>`), and reads show the effective value. Beyond that, `-e`/`-u` validate every argument first and write once (`SetKeys`/`RemoveKeys`): a
  protected key without `-f`, a malformed `key=value` or an unset key leaves the file untouched.
- Values are raw (`${key}` left as is) unless `-x`; the `shell` and `export` modes always expand.
  Writes never freeze references: `-e` stores exactly what it is given, and
  `v=$(zmlocalconfig -m nokey k); zmlocalconfig -e "k=$v -Dx"` keeps the `${...}` of `k`.
- Passwords: only the plain, xml and nokey modes hide values, and only for keys containing the
  case-sensitive substring `password` (printed as `*`); `-s` shows them. `shell` and `export`
  never mask (`eval "$(zmlocalconfig -q -m shell ...)"` must see the real password). Names like
  `api_secret` or `ldap_pass` are not masked.
- Keys named on the command line print in the order given (repeats included), in every mode; no
  keys: sorted. A key with no value (null default, or a `<key>` without `<value>`) is skipped,
  with `Warning: null valued key` when named; an empty `<value/>` is the empty string.
- The whole file is validated while loading, like legacy `expandAll`: a `${}` cycle or a
  reference to a key without a value fails every invocation, even `-e`
  (`Error: failed with error in config file`, exit 1).
- A command line parse error (unknown flag, missing option argument) exits 1, like the legacy
  tool, not Kong's 80.
- Errors go to stderr as `Error: <message>` (legacy `Logging.error`), exit status 1; `-q`
  silences the message, not the status. Messages: `can not edit key <key>`,
  `argument '<arg>' not in key=value form`, `insufficient arguments`, `key <key> is not set`,
  `failed to create writer <mode>` (checked by the print branch, before the compatibility check),
  `option 'X' can not be used with option 'Y'`, `invalid option 'X'`.
- `-i`/`--all` print `internal/localconfig/lcdocs.go`: the `ZsMsg.properties` text of every
  `KnownKey` (80-column wrap, `* Changes are in effect after server restart.` unless
  `@Reloadable`). `-i` hides keys without LC.java `@Supported`. `lcdocs.go` and `lcdefaults.go` (every `KnownKey` default plus the `.protect()` set behind
  `-e` without `-f`) are generated: run `make generate` (= `go generate ./internal/localconfig/`,
  `scripts/gen-lc.py`, reads `LC.java`, `Constants.java` and `store/conf/msgs/ZsMsg.properties` from a
  carbonio-mailbox clone, default `~/zextras/carbonio-mailbox`) after bumping the tag pinned in
  `scripts/lc-mailbox-tag`. `make generate-check` regenerates into a temp dir and diffs against the
  committed files.
- `-l` authenticates with `zimbra_ldap_user`/`zimbra_ldap_password` and sends
  `ReloadLocalConfigRequest` to `https://${zimbra_zmprov_default_soap_server}:${zimbra_admin_service_port}`
  (read from the default `localconfig.xml`, not from `-c`). A refused connection prints
  `'mailbox' service is not running`.

## Variable interpolation

Source: `internal/localconfig/resolve.go` (`Interpolate`)

Values in `localconfig.xml` and the defaults registry may contain `${key}` references:

```xml
<key name="zimbra_log_directory">
  <value>${zimbra_home}/log</value>
</key>
```

Resolution rules:

- Pattern: `\$\{([^}]+)\}` — matches any `${name}` token.
- Up to **10 passes** are performed to resolve transitive references
  (e.g. `${zimbra_home}` inside `${zimbra_log_directory}` inside another key).
- The merged map is expanded once, after every layer (defaults, `localconfig.xml`, the
  `CONFIGD_LC_*` environment, flag overrides) is in; a key set in a higher layer always takes
  precedence over a lower one (see [Effective localconfig layers](#effective-localconfig-layers)).
- Self-references are detected per-pass and left unresolved to avoid infinite loops.
- After 10 passes, any remaining `${...}` tokens are left as-is in the output.

Example chain resolved in two passes:

```
Pass 1: zimbra_log_directory = "${zimbra_home}/log"
        → "/opt/zextras/log"  (zimbra_home resolved)

Pass 2: no remaining references → stop
```

## Regression history

Before the LC.java-mirror port, `defaults.go` hand-maintained only ~24 keys of those declared in
`LC.java`. That approach caused a production regression and is why the registry is now a
complete mirror instead.

With only ~24 defaults, running `carbonio-bootstrap -c /opt/product.config` produced in
`/tmp/zmsetup.log`:

- `Error: assertion -r /db.sql failed` from `/opt/zextras/libexec/zmmyinit`, because
  `zimbra_db_directory` resolved to `""`, so the store MySQL database was never
  initialised.
- `Can't call method "write" on an undefined value at /opt/zextras/bin/zmldappasswd line
  150` (and similarly at lines 224/274/324), because `zimbra_tmp_directory` resolved to
  `""`, so the temporary LDIF path became `/carbonio.ldif.$$` — unwritable by the
  `zextras` user — and `/opt/zextras/conf/carbonio.ldif` never received the new
  replication/postfix/amavis/nginx password hashes.

A key missing from both `lcDefaults` and `localconfig.xml` still resolves to the empty
string; the only signal is a stderr `Warning: key "..." not found` line, so omissions
fail silently unless that stderr is monitored. This is why completeness of the mirror,
not curation, is the safety property to preserve.

### Random password charset

Source: `internal/localconfig/password.go` (`passwordCharset`, `GeneratePassword`)

`configd localconfig -e -r <key>` replaces the Java `zmlocalconfig -r`, which drew from
`com.zimbra.common.util.RandomPassword.ALPHABET` —
`0123456789a-zA-Z_.`, 64 entries, no shell metacharacters. configd originally used a
wider charset including `!@#$%^&*()[]{}|;:,.<>?`, which broke every caller that
interpolates the generated value into a shell command. `/opt/zextras/libexec/zmmyinit`
runs `su - zextras -c "... zmmypasswd $sql_root_pw"`, so a password containing `(` or
`&` aborted the run with `bash: -c: line 1: syntax error near unexpected token '('`
right after the MySQL schema load, leaving both MySQL passwords unset.

`passwordCharset` therefore MUST stay restricted to the legacy alphabet.
`TestGeneratePassword_ShellSafe` enforces it.
