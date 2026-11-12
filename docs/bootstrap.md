<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Bootstrap configuration

`carbonio-bootstrap` configures a Carbonio node. It is a binary of its own
(`/opt/zextras/libexec/setup`, from the carbonio-configd package, run by
carbonio-core's `/usr/bin/carbonio-bootstrap`) that replaces `setup.pl`; it is not part
of `configd`, so the terminal interface is not linked into the daemon.
`carbonio-bootstrap config` builds the node configuration, validates it, and
prints it or saves it; `carbonio-bootstrap run` builds it the same way and
configures the node (see [Running](#running)). Without a command it runs
`run`.

The options of the old script keep working: `-c FILE` is
`--legacy-config FILE --non-interactive`, `-d` is accepted and ignored (the
run always logs to `/opt/zextras/log/bootstrap.<date>.log`), and `-h` prints
the help.

```bash
carbonio-bootstrap config                                    # wizard on a terminal, otherwise print
carbonio-bootstrap config --config /etc/carbonio/bootstrap.yaml --non-interactive
carbonio-bootstrap config --legacy-config /opt/zextras/config.12345 --output bootstrap.yaml
CARBONIO_BOOTSTRAP_ADMIN_PASSWORD_FILE=/run/secrets/admin carbonio-bootstrap config --set ldap.port=636
```

| Flag | Meaning |
|---|---|
| `--config FILE` | YAML configuration file |
| `--legacy-config FILE` | `setup.pl` configuration file (`KEY=VALUE`, the `-c` of the old script); not together with `--config` |
| `--set key=value` | set one key (repeatable), e.g. `--set ldap.port=636` or `--set ldap.root_password_file=/run/secrets/root` |
| `--non-interactive` | never run the wizard; a missing key is an error |
| `--wizard missing\|all` | which questions the wizard asks (default `missing`) |
| `--plain` | line-based wizard and output instead of the full-screen interface |
| `--verbose` | list every legacy key that is not imported (otherwise only their number), and log the full output of `zmcertmgr` |
| `--output FILE` | save the configuration there (mode `0600`, no passwords) instead of printing it |

Exit status: `0` when the configuration is valid, `1` otherwise. Every
problem is printed on stderr, one per line.

## Layers

Lowest precedence first. Each key records the layer that set it, and the
printed configuration shows it as a comment (`# env`, `# detected`, ...).

1. **Defaults**: built-in values, some read from the node (`detected`) and
   some drawn at random (`generated`).
2. **Existing install** (`carbonio-bootstrap run` only): on a node already
   bootstrapped, the values in localconfig and LDAP (`existing`).
3. **File**: the YAML file, or the legacy file (`legacy`).
4. **Environment**: `CARBONIO_BOOTSTRAP_*`.
5. **Flags**: `--set`.
6. **Wizard**: the answers.

Defaults are computed after the other layers, in key order, so a default
follows the keys set before it: `domain.name` follows `host.hostname`, and
`admin.account` follows `domain.name`.

## Keys

Key path = YAML path. Environment variable = `CARBONIO_BOOTSTRAP_` plus the
path upper-cased, with `.` replaced by `_` (`ldap.root_password` becomes
`CARBONIO_BOOTSTRAP_LDAP_ROOT_PASSWORD`). Lists are YAML sequences, or
comma-separated in the environment and in `--set`. Booleans accept
`true/false`, `yes/no`, `1/0`.

| Key | Legacy key | Default | Required |
|---|---|---|---|
| `roles` | (`INSTALL_PACKAGES`) | installed `carbonio-*` packages: `directory-server`, `mta`, `appserver`, `proxy`, `clamav`, `memcached` | always |
| `host.hostname` | `HOSTNAME` | `hostname --fqdn` | always |
| `host.public_hostname` | `PUBLICSERVICEHOSTNAME` | `host.hostname` | always |
| `host.ip_mode` | `zimbraIPMode` | `ipv6` on an IPv6-only node, else `ipv4` | |
| `host.timezone` | `zimbraPrefTimeZoneId` | `/etc/localtime`, else `America/Los_Angeles` | |
| `host.ssl_digest` | `ssl_default_digest` | `sha256` (`ripemd160`, `sha`, `sha1`, `sha224`, `sha256`, `sha384`, `sha512`) | |
| `host.require_interprocess_security` | `ZIMBRA_REQ_SECURITY`, `zimbra_require_interprocess_security` | `false` | |
| `ldap.host` | `LDAPHOST` | `host.hostname` on the LDAP master | always |
| `ldap.port` | `LDAPPORT` | `389` | |
| `ldap.root_password` (secret) | `LDAPROOTPASS` | generated on the LDAP master | directory-server |
| `ldap.admin_password` (secret) | `LDAPADMINPASS` | generated on the LDAP master | always |
| `ldap.postfix_password` (secret) | `LDAPPOSTPASS` | generated on the LDAP master | directory-server, mta |
| `ldap.amavis_password` (secret) | `LDAPAMAVISPASS` | generated on the LDAP master | directory-server, mta |
| `ldap.nginx_password` (secret) | `ldap_nginx_password` | generated on the LDAP master | directory-server, proxy |
| `ldap.base_dn` | `ldap_dit_base_dn_config` | `cn=zimbra` | |
| `ldap.replication_type` | `LDAPREPLICATIONTYPE` | `master` when `ldap.host` is this node, else `replica` (`master`, `replica`, `mmr`) | directory-server |
| `ldap.server_id` | `LDAPSERVERID` | `2` | when `mmr` |
| `ldap.replication_password` (secret) | `LDAPREPPASS` | generated on the LDAP master | directory-server |
| `ldap.force_replication` | `FORCEREPLICATION` | `false` | |
| `domain.create` | `DOCREATEDOMAIN` | `true` on the LDAP master | |
| `domain.name` | `CREATEDOMAIN` | `host.hostname` minus its first label | when `domain.create` |
| `admin.create` | `DOCREATEADMIN` | `true` on an application server | |
| `admin.account` | `CREATEADMIN` | `zextras@<domain.name>` | when `admin.create` |
| `admin.password` (secret, 6+ characters) | `CREATEADMINPASS` | none | when `admin.create` |
| `notifications.smtp_source` | `SMTPSOURCE` | `admin.account` | |
| `notifications.smtp_destination` | `SMTPDEST` | `admin.account` | |
| `notifications.av_notify_user` | `AVUSER` | `admin.account` | |
| `mta.my_networks` | `zimbraMtaMyNetworks` | `127.0.0.0/8`, `[::1]/128` and the node's networks | mta |
| `mta.antispam` | `RUNSA` | `true` | |
| `mta.antivirus` | `RUNAV` | `true` when `clamav` is installed | |
| `mta.opendkim` | `RUNDKIM` | `true` | |
| `mta.cbpolicyd` | `RUNCBPOLICYD` | `false` | |
| `store.smtp_host` | `SMTPHOST` | `host.hostname` when the node runs the MTA | appserver |
| `store.mode` | `MODE` | `https` (`http`, `https`, `both`, `mixed`, `redirect`) | |
| `store.mail_proxy`, `store.web_proxy` | `zimbraMailProxy`, `zimbraWebProxy` | `true` | |
| `store.spam_training` | `DOTRAINSA` | `true` | |
| `store.spam_account`, `store.ham_account` | `TRAINSASPAM`, `TRAINSAHAM` | `spam.<random>@<domain>`, `ham.<random>@<domain>` | appserver, with training |
| `store.keyboard_shortcuts` | `USEKBSHORTCUTS` | `true` | |
| `store.mailboxd_memory_mb` | `MAILBOXDMEMORY` | a quarter of the memory, a fifth above 16 GB | |
| `store.mysql_memory_percent` | `MYSQLMEMORYPERCENT` | `30` | |
| `store.virus_quarantine_account` | `VIRUSQUARANTINE` | `virus-quarantine.<random>@<domain>` | appserver |
| `store.ports.{imap,imaps,pop3,pop3s,http,https}` | `IMAPPORT` ... `HTTPSPORT` | `7143 7993 7110 7995 8080 8443` (mailboxd runs as zextras and cannot bind ports below 1024) | |
| `proxy.mode` | `PROXYMODE` | `https` (`https`, `redirect`) | |
| `proxy.mail_enabled`, `proxy.web_enabled` | `MAILPROXY`, `HTTPPROXY` | `true` on a proxy | |
| `proxy.strict_server_name` | `STRICTSERVERNAMEENABLED` (only `yes` is true, as in `setup.pl`) | `false` | |
| `proxy.ports.{imap,imaps,pop3,pop3s,http,https}` | `IMAPPROXYPORT` ... `HTTPSPROXYPORT` | `143 993 110 995 80 443` (the proxy, on this node or another) | |
| `start_services` | `STARTSERVERS` | `true` | |

Passwords are generated only on the LDAP master. Every other node, replicas
included, must be given the passwords of the master.

A `replica` or `mmr` directory server replicates from `ldap.host`, which must
then be another node (unless `ldap.force_replication`); a `master` must name
itself.

Validation also rejects names that are not fully qualified, malformed
addresses and networks, ports outside 1-65535, and a proxy and a mailbox on
the same node using the same port.

### Secrets

A secret can be given directly, or read from a file: `<key>_file` in YAML and
`--set`, `<VAR>_FILE` in the environment. One trailing newline is dropped.
Setting both `<VAR>` and `<VAR>_FILE` is an error. Secrets are never printed
(`********`) and never saved: `--output` writes the `_file` path the secret
came from, or an empty `<key>_file:` with a comment.

An unknown key (YAML, `--set`) or an unknown `CARBONIO_BOOTSTRAP_*` variable
is an error, reported with its line for YAML, so a typo cannot fall back to
a default without notice.

### Example

```yaml
roles: [directory-server, mta, appserver, proxy]
host:
  hostname: mail.example.com
  public_hostname: webmail.example.com
ldap:
  root_password_file: /run/secrets/ldap-root
admin:
  account: admin@example.com
  password_file: /run/secrets/admin
mta:
  my_networks: [127.0.0.0/8, "[::1]/128", 10.0.0.0/24]
```

## Wizard

On a terminal (stdin and stdout), and unless `--non-interactive` is given,
the wizard runs when a required key is missing, when no file, variable or
flag set any key, or with `--wizard all`. It asks only about the keys that
apply to the node's roles, and checks each answer as it is given.

There are two interfaces. The default on a capable terminal is the
full-screen one below. The line wizard is used instead with `--plain`,
`--non-interactive` (which never asks), `TERM=dumb`, or when stdin or stdout
is not a terminal; it behaves as it always did.

### Full-screen interface

Configure screen:

- The header shows the breadcrumb and the node (hostname, OS, new or existing
  install). Under it, the **roles** of the node (`directory-server`, `mta`,
  `appserver`, `proxy`, `clamav`, `memcached`) are shown enabled (`●`) or
  disabled (`○`), with where they come from (installed packages, a file, the
  environment). Toggling a role recomputes the defaults and shows or hides
  the sections and keys that apply to it.
- The left pane lists the sections (Node, Directory (LDAP), Domain & admin,
  Mail transport, Mailbox store, Proxy, Notifications, Advanced) with
  `✓` (complete), `● N` required values missing, or `○` optional and
  untouched. Every key of [Keys](#keys) is in a section; the rarely changed
  ones (SSL digest, interprocess security, memory, ports, base DN, ...) are
  under Advanced. **Review & apply** stays locked while a required value is
  missing.
- The right pane is a table of SETTING, VALUE and SOURCE (`default`,
  `detected`, `generated`, `existing`, `legacy`, `file`, `env`, `flag`,
  `wizard`). A value set by the environment or a `--set` flag is locked
  (`🔒`): it is shown, with why, but not editable here. Passwords are shown
  as `••••` (or `required`) and typed in a masked input, twice; they appear
  nowhere else on the screen.
- Under the focused key an inline help box shows the live validation message
  while you type (names, e-mail addresses, networks, passwords, numbers,
  enums as a list, booleans as a toggle), the help text, and the YAML key
  and the environment variable (`CARBONIO_BOOTSTRAP_*`, with `_FILE` for
  secrets) that set it.
- A DNS line checks, in the background, that the hostname resolves and the
  domain has an MX record. It never blocks.
- The status bar counts the required values still missing and the keys by
  source.

| Key | Action |
|---|---|
| `↑` `↓` | move (in a pane, or in a list) |
| `Enter` | edit the focused key; toggle a role; open a section |
| `Tab` / `Shift+Tab` | next / previous pane (roles, sections, keys) |
| `n` | jump to the next required value that is missing |
| `/` | search the keys by name |
| `u` | reset the key to its default |
| `s` | save the YAML (no passwords) |
| `r` | review the configuration |
| `?` | key help |
| `q` | quit (asks first if you changed something) |

The review page shows the YAML that would be used (`config` prints the same,
with the passwords masked) and offers Apply (in `run`) or Done (in `config`),
Save yaml and Back.

With `run`, a first page shows the licence notice of the installed edition
(Carbonio Bootstrap, from `/opt/zextras/.mini_alue_ce`); it is informational,
Enter continues. After Apply, the apply screen takes over (see
[Running](#running)). The screen needs 80x24; a smaller terminal shows a
message instead. Colors follow the terminal background and `NO_COLOR`.

### Line wizard

- `--wizard missing` skips the keys set by a file, the environment or a
  flag. Defaults and detected values are asked, pre-filled.
- `--wizard all` asks everything, pre-filled with the current value.
- Enter keeps the shown value. For a generated password, Enter keeps the
  generated one.
- Passwords are read without echo, twice.
- The hostname is checked in DNS (it must resolve, and not only to loopback),
  and so is the domain MX. A failed check is a warning only, because DNS may
  be set up after the node.
- At the end the wizard shows the configuration with its sources. You can
  accept it, go back with `edit` (every question, pre-filled), or abort.
  It then offers to save the answers to a file, without passwords.

Without a terminal, missing keys are an error that names the variable to set,
so a pipeline never waits on a prompt.

## Migrating a legacy configuration

`--legacy-config` reads the format `setup.pl` loads with `-c` and writes with
`saveConfig` (`/opt/zextras/config.<pid>`). That is `KEY=VALUE` lines, split
on the first `=`, with double quotes removed. Combined with `--output`, it
converts the file to YAML:

```bash
carbonio-bootstrap config --legacy-config /opt/zextras/config.12345 --output /etc/carbonio/bootstrap.yaml
```

- Keys with a YAML equivalent are imported (see the table). An empty value,
  and `PUBLICSERVICEHOSTNAME=UNSET`, leave the key to its default.
- Keys that are computed, fixed, obsolete or read from LDAP are not imported.
  `config` prints their number on stderr (`Note: 27 legacy keys not imported
  (--verbose lists them)`); with `--verbose` each one is listed with the
  reason (`Note: legacy key EXPANDMENU not imported: obsolete`). `run` shows
  the number on its `Config:` line and always writes the list to the log
  file; `--verbose` also prints it under that line.
- A key neither mapped nor listed above (for example the extra keys some
  container images add) is ignored and listed on stderr as
  `unknown key, ignored`, as `setup.pl` ignored it (counted with the others).
- Files saved by `setup.pl` hold no passwords, because `saveConfig` skips
  them. A hand-written file may hold some. They are used for the run but not
  written by `--output`; give them again through `*_FILE`, a `_file` key or
  the wizard.

## Running

`carbonio-bootstrap run` (as root) takes the flags of `config`, plus
`--dry-run`. It reads the node first: whether this is a new install
(`.install_history`, as `setup.pl`), the installed and enabled packages, and
on an existing install the values in localconfig and LDAP. A new node whose
`ldap.host` is another, reachable node also reads the master's values. Then
it runs the steps of `setup.pl` in its order:

1. LDAP configuration and schema, the CA of an existing install, port
   conflicts, `zmldapupdateldif`.
2. localconfig values, CA and certificates, server entry, LDAP setup
   (master, `replica` or `mmr`), service ports and store, timezone and proxy
   preferences, MTA, enabled services, host pool.
3. Default domain and accounts, SQL, syslog, service start (systemd targets,
   or `zmcontrol`), GAL sync account, crontab, ssh keys.

The syslog step runs in process (`setup syslog`, which is also what
`libexec/zmsyslogsetup` calls): it adds the local0, auth and mail rules to
`/etc/rsyslog.conf` (Ubuntu with `/etc/rsyslog.d`: to `60-carbonio.conf`, and
the catch-all rules of `50-default.conf` lose those facilities), forwards to
`zimbraLogHostname` over UDP when that is another host, creates
`/var/log/carbonio.log`, sets the owner in `/etc/logrotate.d/carbonio` and
restarts rsyslog only if a file changed. Only rsyslog is handled; syslog-ng and
`/etc/syslog.conf` are not.

Before the steps, `run` prints "Starting Carbonio Bootstrap..." and the
licence notice, wrapped at 80 columns between two rules (also with
`--dry-run`; `config` does not print it). On a terminal with the full-screen
interface the notice is the first page instead.

The first step, "Prepare system users and directories", is the one the old
shell script did before `setup.pl`: it runs `systemd-sysusers` for each
`/usr/lib/sysusers.d/carbonio-*.conf`, then gives `/opt/zextras/conf` to
`root:root`, runs `systemd-tmpfiles --create` for each
`/usr/lib/tmpfiles.d/carbonio-*.conf`, and gives the directory back to
`zextras:zextras`. It has no `.install_history` stage and always runs, so
the users and directories exist on every run. A failure of one of those
commands is logged, not fatal; a missing `systemd-sysusers` or
`systemd-tmpfiles` is skipped. `--dry-run` lists it as `would apply`. The
tools' output goes to the log only. Like the rest of `run`, it needs root.

Each step first checks whether the node already holds what it produces and
skips it then, so after a failure running the command again resumes. Steps
that `setup.pl` let fail with a warning still only warn, except "Start
servers": a service that does not start fails the run (setup.pl went on and
the GAL sync account step then failed against a stopped mailbox). Each
finished step is recorded in `.install_history` as `setup.pl` did
(`CONFIGURED <stage>`).

The run logs to `/opt/zextras/log/bootstrap.<date>.log` (mode `0600`):

- a header: the `carbonio-bootstrap` build, the Carbonio version, the inputs
  (legacy file, YAML file, environment keys, `--set` values, wizard), the
  node (hostname, new or existing install, platform, systemd or zmcontrol),
  the enabled roles, and the resolved configuration with each key's source
  and the passwords masked;
- for each step `<title>: running`, the commands run (`*** Running as
  <user>: <argv>`, secrets masked, arguments with spaces or newlines quoted)
  with their output a line at a time, then `<title>: done in 1.2s` (or
  `failed after ...`, `warning: ...`, `already done`);
- at the end every warning with its step, `Summary: N applied, N already
  done, N with warnings, in 54s`, and the result (`Configuration complete.`,
  `Result: failed: ...` or `Result: stopped by the operator`).

`zmcertmgr`, which narrates every file it copies and every LDAP lookup it
tries, has its output logged only when it fails; `--verbose` logs it all,
with a note on the lookups that are expected to fail on a first install.

On a terminal, the apply screen shows the run. On the left, the preflight
checks (root, the hostname resolves, the domain MX, the needed ports are
free, memory against what the roles recommend; they only warn) and the plan
(steps, already done, where the YAML was saved). On the right, the steps with
a progress bar and the elapsed time: `✓` done (with its duration), `↷`
already done, a spinner on the running one, `·` pending, `⋮ N more` when the
list does not fit, and `✗` on the one that failed, with its error and the
reminder that running the command again resumes. At the bottom, the last log
lines, already redacted; `l` opens the whole log. `Ctrl+C` stops after the
current step: it never kills one in the middle. The colours are those of the
Carbonio Design System (brand blue `#2b73d2` for the title, accents and the
progress bar; green, amber and red for done, warning and failure; grey for
the rest), with darker shades of green and amber on a light terminal.

### Plain output

Without a terminal, with `--plain` or with `--non-interactive` (so with
`-c FILE`), `run` prints the notice, then a header, one line per step and a
summary. A step that does not apply to the node is not listed and not
counted; one already done is listed as `skip`. A step's own messages follow
its line, indented. The details of the commands go to the log file only.

```text
Carbonio Bootstrap 26.12.0 · mail.example.com · new install
Config:  /opt/product.config (legacy) · 27 keys not imported, see log
Log:     /opt/zextras/log/bootstrap.20261112-005601.log

[ 1/46] Prepare system users and directories   ok      0.4s
[ 4/46] Check port conflicts                   skip    already done
[10/46] Create SSL certificates                ok      3.1s
        · carbonio-appserver certificate created
[20/46] Generate dhparam key                   ok      0.0s
[44/46] Create GAL sync account                warn    skipped: services not started

Done in 3:12 · 41 applied · 4 already done · 1 warning
Warnings:
  · Create GAL sync account: skipped: services not started
Services not started (start_services: no). Start them with: systemctl start carbonio-directory-server.target carbonio-appserver.target
Admin console: https://mail.example.com:6071   Webmail: https://mail.example.com
```

Durations under a minute are `0.4s` or `12s`, then `m:ss`. On failure the
step shows `FAIL` with its error, and stderr gets `Failed at step n/N:
<title>`, the error, and the reminder that running the command again resumes
(exit status `1`). The services line shows the command that starts them
(`systemctl start` the role targets on a systemd node, else
`su - zextras -c 'zmcontrol start'`); the URLs are shown for a node that
runs the proxy web service.

On a terminal that is not `dumb`, the running step has a live line (a
spinner and the elapsed time) that its final line replaces, and the statuses
are coloured (`ok` green, `skip` grey, `warn` amber, `FAIL` red); `NO_COLOR`
turns the colours off. Piped output has no colour and no carriage returns:
one line per step, clean in a CI log.

The step titles are in one style: an imperative verb, sentence case, upper
case acronyms, no final punctuation.

`--dry-run` prints each step with `skipped`, `done` or `would apply` (a table, not the plain output above) and
writes nothing. On an existing install it may start the local LDAP server to
read the node.

Passwords are never passed on a command line or written to the log:
`zmprov` reads its commands on stdin, localconfig is written directly, and
the LDAP passwords are set natively (no `zmldappasswd`).

Three steps that `setup.pl` ran as shell scripts or JVM tools are done in
Go, without starting a process:

- "Create GAL sync account" (`zmgsautil createAccount`) authenticates to the
  admin SOAP API with the LDAP master credentials of localconfig and sends
  `CreateGalSyncAccountRequest` to
  `zimbra_zmprov_default_soap_server:zimbra_admin_service_port` (as the
  tool did), then logs the name and id of the account. The server certificate
  is always verified, against the system roots and the Carbonio CA files; the
  request is sent once (it is not retried) and may take up to 5 minutes.
- "Generate zextras SSH key" (`zmsshkeygen`) writes a new 2048-bit RSA key
  pair to `/opt/zextras/.ssh/zimbra_identity` (PEM `RSA PRIVATE KEY`, mode
  `0600`) and `zimbra_identity.pub` (`ssh-rsa <key> <server host name>`,
  mode `0644`), owned by `zextras`, and stores the public key in
  `zimbraSshPublicKey` of the server entry. A key that is already there is
  replaced.
- "Update authorized SSH keys" (`zmupdateauthkeys`) rewrites
  `/opt/zextras/.ssh/authorized_keys`: for each server with a
  `zimbraSshPublicKey` the lines of that host are replaced by
  `command="/opt/zextras/libexec/zmrcd" <key>`, in the format the script
  wrote, so the other nodes can keep running the script. A server without a
  key is logged as `Key for <server> NOT FOUND`. The remote mode
  (`zmupdateauthkeys -a`) is not part of the bootstrap.

The tools stay in carbonio-core; the bootstrap no longer calls them. The
`zextras` user and group ids are read from `zimbra_uid` and `zimbra_gid` of
localconfig (stored by the "Set local config values" step), so these steps
fail when they are not set.

Differences from `setup.pl`:

- A failure of `zmmtainit`, `zmmyinit` or the dhparam key ends the run
  (`setup.pl` went on, or exited `0`).
- `conf/dhparam.pem.crb` holds the fixed RFC 7919 `ffdhe2048` group (the one
  Mozilla's TLS guidelines recommend), written in process, instead of a group
  freshly generated by `openssl dhparam 2048`: it takes no time instead of
  15-30 seconds, and every node shares the same group. It is still recorded in
  `zimbraSSLDHParam`, which nginx reads.
- No `/opt/zextras/config.<pid>` file is written; use `--output`.
- A replica or multi-master directory server is set up from the
  configuration (`ldap.replication_type`), not only from the interactive
  menu. Its server entry is created before replication is enabled: from a
  `-c` file `setup.pl` created it after, so enabling the directory-server
  service on the new replica failed (`NO_SUCH_SERVER`), and it left the
  replica's slapd stopped. `carbonio-bootstrap run` leaves it running.
- With the mail proxy off, the POP3S proxy port, not the mailbox one, moves
  to 995.
- `authorized_keys` is written without the empty first line that
  `zmupdateauthkeys` leaves when the file did not exist; a file that has one
  keeps it.

## Commands for setup.pl

While carbonio-core still runs `setup.pl`, it calls these commands of the
same binary (`/opt/zextras/libexec/setup`) instead of the scripts and JVM
tools. They do exactly what the three steps above do, without a bootstrap
configuration: the host name, the `zextras` ids, the LDAP and the admin
credentials come from localconfig. They must run as root (otherwise they
print an error and exit `1`), print what the legacy tools printed to stdout
and the details to stderr, and exit `1` on failure with `Error: <reason>` on
stderr; a wrong command line exits `2`.

| Command | Replaces | |
| --- | --- | --- |
| `setup gal-sync-account --domain D --server S` | `zmgsautil createAccount -a galsync.<random>@D -n InternalGAL --domain D -s S -t zimbra -f _InternalGAL -p 1d` | Creates the account and prints `<name>` and `<id>` separated by a tab. It fails when the account cannot be created; it does not check for an existing one. |
| `setup ssh-keygen` | `zmsshkeygen` | Generates the key pair and stores the public key in the server entry. |
| `setup update-authkeys` | `zmupdateauthkeys` | Rewrites `authorized_keys`. There is no `-a` (remote) mode. |
