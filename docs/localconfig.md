<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Local Configuration

## Defaults registry

Source: `internal/localconfig/lcdefaults.go` (map `lcDefaults`), exposed via
`internal/localconfig/defaults.go` as `Defaults = buildDefaults()` (a defensive copy).

`lcDefaults` is a verbatim port of **all 619** `KnownKey` defaults declared in
carbonio-mailbox's `common/src/main/java/com/zimbra/common/localconfig/LC.java`
(branch `CO-3358`). configd is now the only local-config implementation present on a
node: the Java `LocalConfigCLI` has been retired, `/opt/zextras/bin/zmlocalconfig` is a
bash shim over `configd localconfig`, and `zmsetvars` (`bin/shutil.sh`) evals
`configd localconfig -q -s -m export`. There is no fallback path to a Java-backed
default anymore, so the registry MUST stay a complete mirror of `LC.java` — every key
it declares belongs in `lcDefaults`, not a curated subset. When `LC.java` gains a key,
changes a default, or removes one, mirror the change in `lcdefaults.go` in the same
change.

### Java-to-Go value conversion rules

The port applies these conversions to each `KnownKey` default:

- Java `null` -> `""`, matching `LC.get`'s `Strings.nullToEmpty` behavior.
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
- XML values are merged first; defaults fill in only missing keys. This means an XML value
  always takes precedence over the hardcoded default for the same key.
- Self-references are detected per-pass and left unresolved to avoid infinite loops.
- After 10 passes, any remaining `${...}` tokens are left as-is in the output.

Example chain resolved in two passes:

```
Pass 1: zimbra_log_directory = "${zimbra_home}/log"
        → "/opt/zextras/log"  (zimbra_home resolved)

Pass 2: no remaining references → stop
```

## Regression history

Before the LC.java-mirror port, `defaults.go` hand-maintained only ~24 keys out of the
619 declared in `LC.java`, selected by curated inclusion criteria. That approach caused
a production regression and is why the registry is now a complete mirror instead.

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

### Related cleanup

`internal/localconfig/constants.go` was removed: its consts (`defaultBaseDir`,
`defaultLogDir`, `boolTrueStr`, `lcKeyLDAPPort`, `lcKeyLDAPHost`, `localhostName`,
`lcKeyZimbraHome`, `lcKeyZimbraLogDirectory`) existed only to build the old hand-written
defaults map and have no counterpart in the `lcDefaults` port.

`mail_service_port` was dropped from the defaults registry: it is a configd-only key
absent from `LC.java` and referenced nowhere in this codebase. `systemd-envscript.sh`
derives the systemd `mail_service_port=` entry from `zimbra_mail_service_port`, which
remains in `lcDefaults`.
