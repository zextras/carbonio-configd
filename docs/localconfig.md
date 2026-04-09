<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Local Configuration

## Defaults registry

Source: `internal/localconfig/defaults.go`

The Go implementation ships approximately 24 hardcoded defaults. This is intentionally
smaller than the ~626 keys in the Java `LC.java`. Only keys that meet at least one of
these criteria are included:

- Consumed directly by configd (e.g. `zmconfigd_listen_port`, `zimbra_configrewrite_timeout`)
- Required by systemd environment scripts (`systemd-envscript.sh`)
- Used by shell scripts that invoke configd subcommands
- Critical for LDAP or service connectivity (e.g. `ldap_port`, `zimbra_server_hostname`)
- Required for JVM startup of mailboxd (`mailboxd_java_options`, `zimbra_zmjava_options`)

To add a new default, add the key/value to the `Defaults` map in
`internal/localconfig/defaults.go`. Follow the same inclusion criteria — do not add keys
just because they exist in the Java implementation.

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

## Compatibility gap with Java zmlocalconfig

The Go implementation only covers the ~24 keys in `defaults.go`. Scripts or tools that
read keys outside this set — and those keys are not set in `localconfig.xml` — will
receive an **empty string** without an error. The Java `zmlocalconfig` would have returned
the Java-defined default for the same key.

**Symptom**: a script that relied on a Java-only default silently gets an empty value and
may fail in unexpected ways.

**Remediation options**:

1. Add the missing key to `internal/localconfig/defaults.go` with the value from the Java
   `LC.java` source. This is the preferred fix for keys that configd or its scripts
   actually need.
2. Set the key explicitly in `localconfig.xml` on the affected installation. This is a
   per-node workaround that does not require a code change.
