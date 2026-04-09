<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Proxy Templates

## Template discovery

Source: `internal/proxy/generator.go`, `internal/proxy/config.go`

The proxy generator scans two directories for templates:

| Directory | Purpose |
|---|---|
| `conf/nginx/templates/` | Standard templates shipped with the package |
| `conf/nginx/templates_custom/` | Site-local overrides (optional, not created by default) |

Only files with a `.template` suffix are processed. The output filename is the template
name with the `.template` suffix stripped, written to `conf/nginx/includes/`.

**Precedence**: when a file in `templates_custom/` has the same basename as a file in
`templates/`, the custom file is used and the standard file is ignored. This allows
operators to override individual templates without modifying package-owned files.

Example:

```
templates/nginx.conf.template         ← standard
templates_custom/nginx.conf.template  ← overrides the standard one
```

Output in both cases: `conf/nginx/includes/nginx.conf`

## Variable syntax

Source: `internal/proxy/template.go` (`interpolateLine`, `processEnablerLine`)

Three syntax forms are used in template files.

### 1. Simple substitution: `${VAR}`

Any occurrence of `${VAR}` in a line is replaced with the resolved variable value. If the
variable does not exist, the token is replaced with an empty string.

```nginx
# Template
server_name ${vhn};
proxy_pass  http://127.0.0.1:${mail.imap.port};

# Output (vhn = "mail.example.com", mail.imap.port = "7143")
server_name mail.example.com;
proxy_pass  http://127.0.0.1:7143;
```

### 2. Enabler variables: `${VAR}` at line start

When `${VAR}` appears at the very start of a line (optionally preceded by whitespace), the
token acts as a conditional switch for the rest of the line:

- If VAR resolves to a **truthy** value (non-empty string, `true`, non-zero int): the
  `${VAR}` token is removed and the remainder of the line is kept, with any nested
  `${...}` tokens also substituted.
- If VAR resolves to a **falsy** value (empty string, `false`, zero): the line is
  commented out with `#` prepended after the leading whitespace.

The variable must be registered with `ValueTypeEnabler` in the variable registry for this
logic to apply. A `${VAR}` at line start that references a non-enabler variable falls
through to simple substitution.

```nginx
# Template
    ${mail.imap.enabled}include mail.imap.conf;
    ${mail.pop3.enabled}include mail.pop3.conf;

# Output when imap enabled, pop3 disabled
    include mail.imap.conf;
    #include mail.pop3.conf;
```

### 3. Explode directive: `!{explode ...}`

When the **first line** of a template file is an explode directive, the rest of the
template is rendered once per domain or server returned from LDAP. The directive line
itself is not written to the output.

See [Explode directive](#explode-directive) below.

## Enabler variables

Source: `internal/proxy/template.go` (`processEnablerLine`), regex `^(\s*)\$\{([^}]+)\}(.+)$`

The enabler pattern requires at least one character after the closing `}`. A `${VAR}` on
a line by itself is not an enabler — it is a simple substitution.

After the enabler token is stripped, the rest of the line is passed back through
`interpolateLine` recursively, so nested `${VAR}` tokens are fully substituted regardless
of whether the line is kept or commented out.

Empty-directive cleanup: after all substitution, if a line matches the pattern
`whitespace + word + whitespace + ;` (a directive with no argument), it is automatically
commented out regardless of the enabler logic.

## Explode directive

Source: `internal/proxy/template.go` (`processExplode`, `processExplodeDomain`,
`processExplodeServer`)

### `!{explode domain(vhn)}`

Generates one block per LDAP domain that has a non-empty virtual hostname. Domains are
sorted by name for deterministic output. Domains with an empty virtual hostname are
skipped.

Per-iteration variables set in the template scope:

| Variable | Value |
|---|---|
| `${vhn}` | Domain's virtual hostname (e.g. `mail.example.com`) |
| `${vip}` | Domain's virtual IP address |
| `${ssl.crt}` | Domain-specific SSL certificate path, or global default if unset |
| `${ssl.key}` | Domain-specific SSL private key path, or global default if unset |

Blocks are separated by a blank line. SSL variables are restored to their global defaults
after each domain so they do not leak between iterations.

### `!{explode domain(vhn, sso)}`

Same as `domain(vhn)` but further filters to domains where `zimbraClientCertMode` is not
empty and not `"off"`. Used for templates that require client-certificate authentication.

### `!{explode server(serviceName)}`

Generates one block per LDAP server where `zimbraServiceEnabled` includes `serviceName`.
Servers are sorted by hostname then by ID. Comment lines (lines starting with `#`) are
skipped during iteration.

Per-iteration variables:

| Variable | Value |
|---|---|
| `${server_id}` | Server's `zimbraId` |
| `${server_hostname}` | Server's `zimbraServiceHostname` |

Example:

```nginx
!{explode server(mailbox)}
# skipped comment
    server ${server_hostname}:7071;

# Output (2 mailbox servers)
    server mailbox1.example.com:7071;
    server mailbox2.example.com:7071;
```
