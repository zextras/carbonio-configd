<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Bootstrap configuration

`configd bootstrap` replaces `carbonio-bootstrap` (`setup.pl` in
carbonio-core). `configd bootstrap config` builds the node configuration,
validates it, and prints it or saves it; `configd bootstrap run` builds it
the same way and configures the node (see [Running](#running)).

```bash
configd bootstrap config                                    # wizard on a terminal, otherwise print
configd bootstrap config --config /etc/carbonio/bootstrap.yaml --non-interactive
configd bootstrap config --legacy-config /opt/zextras/config.12345 --output bootstrap.yaml
CARBONIO_BOOTSTRAP_ADMIN_PASSWORD_FILE=/run/secrets/admin configd bootstrap config --set ldap.port=636
```

| Flag | Meaning |
|---|---|
| `--config FILE` | YAML configuration file |
| `--legacy-config FILE` | `setup.pl` configuration file (`KEY=VALUE`, the `-c` of `carbonio-bootstrap`); not together with `--config` |
| `--set key=value` | set one key (repeatable), e.g. `--set ldap.port=636` or `--set ldap.root_password_file=/run/secrets/root` |
| `--non-interactive` | never run the wizard; a missing key is an error |
| `--wizard missing\|all` | which questions the wizard asks (default `missing`) |
| `--output FILE` | save the configuration there (mode `0600`, no passwords) instead of printing it |

Exit status: `0` when the configuration is valid, `1` otherwise. Every
problem is printed on stderr, one per line.

## Layers

Lowest precedence first. Each key records the layer that set it, and the
printed configuration shows it as a comment (`# env`, `# detected`, ...).

1. **Defaults**: built-in values, some read from the node (`detected`) and
   some drawn at random (`generated`).
2. **Existing install** (`configd bootstrap run` only): on a node already
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
| `store.ports.{imap,imaps,pop3,pop3s,http,https}` | `IMAPPORT` ... `HTTPSPORT` | `7143 7993 7110 7995 80 443` with a proxy on the node, else `143 993 110 995 80 443` | |
| `proxy.mode` | `PROXYMODE` | `https` (`https`, `redirect`) | |
| `proxy.mail_enabled`, `proxy.web_enabled` | `MAILPROXY`, `HTTPPROXY` | `true` on a proxy | |
| `proxy.strict_server_name` | `STRICTSERVERNAMEENABLED` (only `yes` is true, as in `setup.pl`) | `false` | |
| `proxy.ports.{imap,imaps,pop3,pop3s,http,https}` | `IMAPPROXYPORT` ... `HTTPSPROXYPORT` | `143 993 110 995 8080 8443` on a proxy, else `7143 7993 7110 7995 8080 8443` | |
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
flag set any key, or with `--wizard all`. It asks only the questions that
apply to the node's roles (about a dozen), and checks each answer as it is
given.

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
configd bootstrap config --legacy-config /opt/zextras/config.12345 --output /etc/carbonio/bootstrap.yaml
```

- Keys with a YAML equivalent are imported (see the table). An empty value,
  and `PUBLICSERVICEHOSTNAME=UNSET`, leave the key to its default.
- Keys that are computed, fixed, obsolete or read from LDAP are not imported.
  Each one is listed on stderr with the reason (`Note: legacy key EXPANDMENU
  not imported: obsolete`).
- An unknown key is an error.
- Files saved by `setup.pl` hold no passwords, because `saveConfig` skips
  them. A hand-written file may hold some. They are used for the run but not
  written by `--output`; give them again through `*_FILE`, a `_file` key or
  the wizard.

## Running

`configd bootstrap run` (as root) takes the flags of `config`, plus
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

Each step first checks whether the node already holds what it produces and
skips it then, so after a failure running the command again resumes. Steps
that `setup.pl` let fail with a warning still only warn. Each finished step
is recorded in `.install_history` as `setup.pl` did (`CONFIGURED <stage>`).
The run logs to `/opt/zextras/log/bootstrap.<date>.log` (mode `0600`).

`--dry-run` prints each step with `skipped`, `done` or `would apply` and
writes nothing. On an existing install it may start the local LDAP server to
read the node.

Passwords are never passed on a command line or written to the log:
`zmprov` reads its commands on stdin, localconfig is written directly, and
the LDAP passwords are set natively (no `zmldappasswd`).

Differences from `setup.pl`:

- A failure of `zmmtainit`, `zmmyinit` or the dhparam key ends the run
  (`setup.pl` went on, or exited `0`).
- No `/opt/zextras/config.<pid>` file is written; use `--output`.
- A replica or multi-master directory server is set up from the
  configuration (`ldap.replication_type`), not only from the interactive
  menu.
- With the mail proxy off, the POP3S proxy port, not the mailbox one, moves
  to 995.
