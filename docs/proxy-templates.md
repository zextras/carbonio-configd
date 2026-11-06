<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Proxy Templates

## Template discovery

Source: `internal/proxy/generator.go` (`DiscoverTemplates`), `internal/proxy/config.go`

The proxy generator scans two directories for templates (`<workdir>/conf` is the conf dir,
`/opt/zextras/conf` unless `configd proxy gen -w` or `CARBONIO_BASE_DIR` (daemon) says otherwise; see
[the command line](#command-line-configd-proxy-gen-and-configd-proxy-conf)):

| Directory | Purpose |
|---|---|
| `conf/nginx/templates/` (or `-t`/`--templatedir`) | Standard templates shipped with the package |
| `conf/nginx/templates_custom/` | Site-local overrides (optional, not created by default) |

Directories are walked recursively and only files with a `.template` suffix are processed;
`-P`/`--template-prefix` further limits the set. The output filename is the template
name with the `.template` suffix stripped, written to `conf/nginx/includes/` (`-i`/`--include-dir`; `nginx.conf` itself goes to the conf dir).

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

Source: `internal/proxy/tmpl` (`RenderLine`, the variable and enabler regexes), driven by
`internal/proxy/template_interpolate.go` (`interpolateLine`)

Three syntax forms are used in template files.

### 1. Simple substitution: `${VAR}`

Any occurrence of `${VAR}` in a line is replaced with the resolved variable value. Names match
`[a-zA-Z0-9._:]+`. If the variable does not exist (or cannot be expanded), the token is replaced
with an empty string.

```nginx
# Template
server_name ${vhn};
proxy_pass  http://127.0.0.1:${mail.imap.port};

# Output (vhn = "mail.example.com", mail.imap.port = "7143")
server_name mail.example.com;
proxy_pass  http://127.0.0.1:7143;
```

**Multi-line values.** When a value contains newlines (for example `${web.add.headers.default}`
or an upstream `:servers` list), every non-empty continuation line is prefixed with the
whitespace that precedes the placeholder on its template line (or, if the placeholder follows
other text, with the line's leading indentation). Empty lines stay empty. A value therefore
lines up with the directive it replaces, at any nesting depth:

```nginx
# Template
    location /static/
    {
        ${web.add.headers.default}
    }

# Output
    location /static/
    {
        add_header X-Frame-Options "sameorigin";
        add_header X-Robots-Tag "noindex, nofollow";
    }
```

If the line starts with an enabler that renders `#`, every line of the value is commented, not
only the first.

> **Intentional divergence from the legacy Java generator.** `ProxyConfGen` leaves the
> continuation lines to the value, which indents them with a fixed number of spaces, so the
> second and later `add_header` lines of a nested block come out misaligned (4 spaces inside an
> 8-space block). configd indents them to the placeholder's column instead. This is the only
> intended output difference: `diff -w` between the Java and Go output is empty. Resolvers
> therefore emit continuation lines without indentation (except indentation that is relative
> structure of the value, such as the MIME types under `gzip_types`). `${listen.:addresses}` is
> the one exception: it is placed at column 0 in the templates, so each line carries its own
> `listen` indentation and strict-servername prefix.

### 2. Enabler variables: `${VAR}` at line start

When `${VAR}` appears at the very start of a line (optionally preceded by whitespace), the
token acts as a conditional switch for the rest of the line:

- If VAR resolves to a **truthy** value (non-empty string, boolean `true`, non-zero int): the
  `${VAR}` token is removed and the remainder of the line is kept, with any nested
  `${...}` tokens also substituted.
- If VAR resolves to a **falsy** value (empty string, `false`, zero): the line is
  commented out, with `#` placed after the leading whitespace (and the enabler token removed).
  When the expanded value spans several lines (e.g. `server host:port;` per upstream), every
  non-blank line is commented.

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

Source: `internal/proxy/tmpl` (`RenderLine`), regex `tmpl.EnablerPattern`:
`^(\s*)\$\{([^}]+)\}(.+)$`

The enabler pattern requires at least one character after the closing `}`. A `${VAR}` on
a line by itself is not an enabler — it is a simple substitution.

After the enabler token is stripped, the rest of the line is passed back through
`interpolateLine` recursively, so nested `${VAR}` tokens are fully substituted regardless
of whether the line is kept or commented out.

Empty-directive cleanup: after all substitution, if a line matches the pattern
`whitespace + word + whitespace + ;` (a directive with no argument, regex
`^\s+[a-z0-9_]+\s+;`), it is automatically commented out (rewritten as `    # <directive>;`)
regardless of the enabler logic.

## Explode directive

Source: `internal/proxy/template_explode.go` (`processExplode`, `processExplodeDomain`,
`processExplodeServer`); directive regex `^!\{explode\s+(\w+)\(([^)]*)\)\}`. An unknown explode
type is an error.

### `!{explode domain(vhn)}`

Generates one block per LDAP domain that has a non-empty virtual hostname. Domains are
sorted by name for deterministic output. Domains with an empty virtual hostname are
skipped. No domains are produced when `zimbraReverseProxyGenConfigPerVirtualHostname` is
`FALSE` (server value, then global; unset counts as `TRUE`), and a domain whose virtual
hostname, IP or SSL paths could not be rendered safely is dropped with an error log
(`internal/proxy/sanitize.go`).

Per-iteration variables set in the template scope:

| Variable | Value |
|---|---|
| `${vhn}` | Domain's virtual hostname (e.g. `mail.example.com`) |
| `${vip}` | Domain's virtual IP address |
| `${ssl.crt}` | Domain-specific SSL certificate path, or global default if unset |
| `${ssl.key}` | Domain-specific SSL private key path, or global default if unset |

Blocks are separated by a blank line (none after the last domain). SSL variables are restored to their global defaults
after each domain so they do not leak between iterations.

### `!{explode domain(vhn, sso)}`

Same as `domain(vhn)` but further filters to domains where `zimbraClientCertMode` is not
empty and not `"off"`. Used for templates that require client-certificate authentication.

### `!{explode server(serviceName)}`

Generates one block per LDAP server where `zimbraServiceEnabled` includes `serviceName`.
Servers are sorted by hostname then by ID. Comment lines (lines starting with `#`) are
skipped during iteration; no blank line is written between server blocks.

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

## Template tools: `configd proxy template`

Source: `internal/proxy/tmpl` (the pure template language: lexer, `RenderLine`, `Format`, `Lint`,
`Diff`; it knows no generator and no LDAP), `internal/proxy/registry.go` (the variable registry
the tools use), `internal/proxy/offline.go` (offline rendering), `internal/cli/proxy/template.go`.

The generator and the tools share one implementation of the language, so they cannot disagree
about what a line means. The tools need no LDAP, no localconfig and not the `zextras` user.

### `fmt [--check | --write] <file|dir>...`

Re-indents each template by brace depth (4 spaces; braces in quotes, in comments and in
`${...}` do not count) and aligns the values of consecutive directives. Enabler prefixes are
re-attached to their directive, after the indent: `${core.ipboth.enabled}listen ...`. The
`!{explode ...}` line is never touched. It never adds or removes a line, and it is idempotent.
A directory is searched for `*.template` files.

- no flag: write the result of a single file to stdout;
- `--check`: write nothing, print a unified diff per file that would change, list those files
  on stderr, exit 1 if there are any (for CI);
- `--write`: rewrite the files that change and list them.

Rules that keep the rendered output the same (up to whitespace):

- only prefixes that are enabler variables are separated from their text; any other
  `${var}` at the start of a line is text, and left alone;
- a directive whose values are all variables (`imap_id ${mail.imapid};`) is commented out
  by the generator when it renders empty, but only if it is indented, so its indent is kept
  as it is;
- a quote that spans lines, and the lines it covers, are left verbatim;
- already-aligned directives keep their column; otherwise the most common column wins.

### `lint <file|dir>...`

Prints `file:line:col: message [rule]` and exits 1 when there is any finding.

| Rule | Finds |
|---|---|
| `unknown-variable` | a `${var}` that is neither registered nor defined by the explode directive of the file (`vhn`, `vip`, `ssl.crt`... under `domain`, `server_id`, `server_hostname` under `server`) |
| `enabler-as-value` | an enabler variable used in the middle of a line: it renders `""` or `#` |
| `value-as-enabler` | a number, duration or on/off variable at the start of a line, where only a `""`/`#` variable can work. String variables are not reported: `web.strict.servername` and the `*.disable` variables are such switches |
| `explode` | an explode directive that is not on the first line, or is malformed |
| `braces` | unbalanced `{`/`}`, per file and per enabler group (`${x}foo {` ... `${x}}`) |
| `whitespace` | trailing whitespace, tabs |

### `render --values <fixture.yaml> --templates <dir> --out <dir>`

Renders every template with the same code as `proxy gen`, with the configuration taken from a
fixture, and writes the files the generator writes: `<out>/nginx.conf` and
`<out>/nginx/includes/<name>`, that is `<out>` stands for the conf directory. Paths inside
the files (includes, certificates) are the fixture's `workdir` (default `/opt/zextras`).
Domains and servers for `explode` come from the fixture. Upstream server lists come from LDAP
in production, so a fixture sets them with `variables`.

```yaml
workdir: /opt/zextras
hostname: proxy1.example.com
global:                            # LDAP global config attributes (server: overrides them)
  zimbraIPMode: both               # ipv4, ipv6 or both
  zimbraReverseProxyStrictServerNameEnabled: "TRUE"
  zimbraReverseProxyMailEnabled: "TRUE"
variables:                         # template variables, by name, after resolution
  web.upstream.:servers: "    server 10.0.0.11:8080;"
domains:                           # for !{explode domain(...)}; vip must be an IP address
  - {name: example.com, vhn: mail.example.com, vip: 192.0.2.10}
servers:                           # for !{explode server(service)}
  mailbox:
    - {id: 6a1d1b1c-0000-4000-8000-000000000001, hostname: mbox1.example.com}
```

Unknown fields and unknown variable names are errors. Virtual IPs are never resolved through
the DNS. Examples: `internal/proxy/testdata/fixtures/`.

The safety property of `fmt` is a test: for every fixture, the files rendered from the
formatted templates equal those of the originals up to whitespace
(`TestFormatPreservesRender`). Set `CONFIGD_PROXY_TEMPLATES` to a template directory (for
example `carbonio-proxy/proxy/conf/nginx/templates`) to run it on real templates too.

## Command line: `configd proxy gen` and `configd proxy conf`

Source: `cmd/configd/proxycli.go`, `cmd/configd/proxygen.go`, `internal/cli/proxy/gen.go`,
`internal/cli/proxy/proxy.go`, `internal/cli/proxy/nginxconf.go`. The full command reference is
[cli.md](cli.md).

### `configd proxy gen` (legacy `zmproxyconfgen`)

`zmproxyconfgen` is `exec configd proxy gen "$@"`. The command loads the configuration
from LDAP and localconfig and runs the same generator as the daemon's `PROXYGEN` directive
**synchronously in the calling process**; the files are on disk when it returns. It does
not talk to the daemon. It takes the flags of the Java `ProxyConfGen`:

| Flag | Meaning |
|---|---|
| `-h`, `--help` | Show the usage |
| `-v`, `--verbose` | Debug logging (the default is INFO on stderr) |
| `-w`, `--workdir <dir>` | Working directory, default `/opt/zextras` (`core.workdir`, `<dir>/conf` is the conf dir) |
| `-t`, `--templatedir <dir>` | Template directory, default `<workdir>/conf/nginx/templates` |
| `-n`, `--dry-run` | Render everything, write nothing |
| `-d`, `--defaults` | Print the default variable map and exit; reads no LDAP |
| `-D`, `--definitions` | Load the configuration, apply `-c`, print the variable map and exit |
| `-p`, `--prefix <name>` | Prefix of the generated files, default `nginx.conf` |
| `-P`, `--template-prefix <name>` | Prefix of the templates, default the config prefix; only templates of that prefix are generated |
| `-i`, `--include-dir <dir>` | Directory of the included files relative to `<workdir>/conf`, default `nginx/includes` |
| `-s`, `--server <name>` | Generate from this server object instead of the local server |
| `-f`, `--force-dns-resolution` | Fail when the virtual hostname (or virtual IP) of a domain does not resolve |
| `-c`, `--config name=value` | Override the text a variable expands to. Unknown names and entries without `=` are logged and ignored. `-c a=b c=d` works as in commons-cli: every extra `name=value` argument is one more override |

Exit status: `0` on success, `-d`, `-D`, `-h` and when stray positional arguments only make
it print the usage; `1` on any error (configuration not loadable, unknown `-s` server,
template or write failure, `-f` failure, not run as `zextras` when files would be written;
`-n`, `-d` and `-D` do not require `zextras`). The `-h` help also lists the root flags
`-v`/`--version` and `--disable-restarts`, which the generator does not use.

`-v` is rewritten to `--verbose` before parsing, because the root command owns `-v`
(`--version`). Differences from the Java tool: `-f` checks the domains before rendering
and only with `-f` (Java also warns without it); `-d`/`-D` list the Go variable names
sorted by keyword.

### `configd proxy conf` (legacy `zmproxyconf`/`zmnginxconf`)

Flags: `-m`/`--markers`, `-i`/`--indent`, `-n`/`--nocomments` (alias `--no-comments`),
`-e`/`--noempty` (alias `--no-empty`), optional `NGINX-CONF-FILE`. Exit status `1` with
`Nginx not installed, exiting` on stdout when the nginx binary is missing, as `proxyconf.pl`.
