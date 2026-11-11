<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Bootstrap parity harness

Compares the node state left by the legacy bootstrap (`carbonio-bootstrap` →
`/opt/zextras/libexec/setup.pl`, as shipped in `carbonio-core`) with the state left
by `configd bootstrap run`, starting from the **same image** and the **same
`-c` configuration file**.

```
Containerfile        Ubuntu 24.04 + systemd (PID 1) + Carbonio CE packages, NOT bootstrapped
config/*.config      setup.pl -c files (KEY="VALUE"), used verbatim by both paths (single-node, store-node)
build.sh             build the image
run-legacy.sh        fresh container -> carbonio-bootstrap -c -> snapshot   (-> out/legacy)
run-configd.sh BIN   fresh container -> configd bootstrap run -> snapshot   (-> out/configd)
run-2node.sh MODE    2-node variant (legacy | configd BIN): node 1 = single-node, node 2 = second store
snapshot.sh          runs inside the container, writes the normalised state
normalise-ldif.pl    LDIF normaliser used by snapshot.sh
snapshot-exclude.txt files under /opt/zextras/conf whose content is volatile (keys, certs, ...)
diff.sh              diff two snapshot directories (default: golden/single-node vs out/configd)
lib.sh               shared helpers (sourced)
golden/single-node/  reference snapshot of the legacy path (local data, not committed; license in REUSE.toml)
```

## Requirements

`podman` (rootless is fine, tested with 6.2), network access to `docker.io`,
`repo.zextras.io` and `keyserver.ubuntu.com`, ~6 GiB of disk, a few GiB of RAM
(MariaDB gets 30 % of the memory, see `MYSQLMEMORYPERCENT`).

## Run

```sh
cd tests/bootstrap-parity
./build.sh                         # ~3 min, image localhost/carbonio-parity:noble
./run-legacy.sh                    # ~4 min -> out/legacy/ (+ out/legacy.bootstrap.log, out/legacy.timing)
make -C ../.. build                # any linux/amd64 configd binary
./run-configd.sh ../../bin/configd # -> out/configd/
./diff.sh                          # golden/single-node vs out/configd; exit 1 on any difference
./diff.sh out/legacy out/configd   # compare two fresh runs instead
```

Options of both run scripts: `--out DIR`, `--name CONTAINER`, `--keep` (keep the
container for inspection: `podman exec -it NAME bash`), `--config FILE`
(default `config/single-node.config`). Environment: `PARITY_IMAGE`,
`PARITY_FQDN` (default `mail.parity.test`, must match `HOSTNAME` of the config),
`BOOT_TIMEOUT`.

To refresh the reference after an intentional change of the legacy side (new
package versions, new image):

```sh
./run-legacy.sh --out golden/single-node   # then move the .bootstrap.log/.timing next to it
```

## What the container looks like

* `podman run -d --systemd=always --no-hosts --hostname mail.parity.test --cap-add=SYS_ADMIN --tmpfs /tmp`.
  * `--cap-add=SYS_ADMIN`: the `carbonio-*.service` units use systemd sandboxing
    (`ProtectKernelModules`, `PrivateDevices`, …); in a rootless container they fail
    with `217/USER` ("Failed to keep CAP_SYS_ADMIN") without it. The capability is
    confined to the user namespace.
* `/etc/hosts` is written by `lib.sh` (`127.0.0.1 localhost` + `<container ip> FQDN short`,
  as in the CE docs, "Setting Hostname") and `hostnamectl set-hostname FQDN`.
* **DNS**: none is needed. The legacy DNS checks (A/MX lookup of the host and domain,
  `setHostName`, `setPublicServiceHostname`, `setCreateDomain`, `setDefaults` L1541-1636) only
  run interactively / on a new install *without* `-c`; with `-c` they are skipped.
  The FQDN only has to resolve through `/etc/hosts`.
* The `carbonio-*.target` units must be **enabled** for setup.pl to be in
  "systemd mode" (`Zextras::Util::Systemd::isSystemd`: any of
  `carbonio-{directory-server,appserver,proxy,mta}.target` enabled). The package
  maintainer scripts only enable them when `/run/systemd/system` exists, so the
  Containerfile creates that directory during `apt-get install` and sets
  `SYSTEMD_OFFLINE=1` (no systemd runs at build time). Without it setup.pl silently
  takes the **non-systemd** path (`ldap start`, `zmcontrol`).
* Container workarounds shared with the Carbonio CE image pipeline
  (`infra-carbonio-ce-images`), applied identically before both paths by
  `prepare_container`: `ldap_db_maxsize` / `ldap_accesslog_maxsize` in localconfig and the
  `olcDbMaxsize` fix in the LDAP config template (the default 80 GiB mdb map does not fit).
  Both end up in the snapshot (`localconfig.txt`) and are therefore identical on both sides.
* `/opt/product.config` (0600) is `config/single-node.config`: derived from the vanilla
  single-server config of `infra-carbonio-ce-images`; host `mail.parity.test`, domain
  `parity.test`, `STARTSERVERS="no"` (bootstrap configures, nothing but LDAP/MariaDB that the
  bootstrap itself starts is left running), all passwords are throw-away test values.
  Do not put comments into a `-c` file: `setup.pl` `loadConfig` turns every line into a key.

## Image content

`CARBONIO_PACKAGES` (build arg): `carbonio-directory-server carbonio-mta carbonio-appserver
carbonio-proxy carbonio-memcached carbonio-clamav` (+ dependencies, among them
`carbonio-core` and `service-discover-server`/`-base`). This is the single-server set
of the CE manual installation, minus the UI/files/chats/docs apps, which do not take
part in bootstrap. The exact versions installed are in the image at
`/opt/zextras/.parity-package-versions` and in
`golden/single-node.package-versions`.
Packages come from `https://repo.zextras.io/release/ubuntu noble main`
(`ZEXTRAS_REPO_CHANNEL=release`, key `0x5dc7680b…` from `keyserver.ubuntu.com`, the procedure of
the CE docs "Repository Configuration"). The repository moves, so rebuild the image *and* the
golden snapshot together.

A gap of the packages themselves is compensated in the image (it would also bite a
real minimal Ubuntu install):

* `libncurses6` is not a dependency of `carbonio-mariadb`, but `mysql` links `libncurses.so.6`;
  without it `zmmyinit` loops on "Failed to connect to mysql…retrying" (`mysql -u root -p=`
  fails with `error while loading shared libraries`).

## Snapshot (`snapshot.sh`, files of a snapshot directory)

| file | content |
|---|---|
| `ldap-config.ldif` | `slapcat -n 0` (cn=config, schema, olc*) |
| `ldap-main.ldif` | `slapcat -b ""` (cn=zimbra tree, domain, accounts, servers, COS, global config) |
| `localconfig.txt` | sorted `key = value`; keys matching pass/secret/_pw/token/_key/credential → `<secret>`/`<empty>` |
| `crontab.txt` | `crontab -u zextras -l` |
| `conf-files.txt` | `path⇥type⇥mode⇥owner:group⇥sha256` for `/opt/zextras/conf`, `/etc/{rsyslog.d,logrotate.d,cron.d,sudoers.d}`; volatile files (`snapshot-exclude.txt`) → `<volatile>` |
| `systemd-units.txt` | enabled unit files; active / failed `.service/.timer/.socket/.target` |
| `install-history.txt` | `/opt/zextras/.install_history` without the epoch column (stage order) |
| `listeners.txt` | listening TCP ports |

LDIF normalisation (`normalise-ldif.pl`): drops `entryCSN entryUUID createTimestamp modifyTimestamp
modifiersName creatorsName contextCSN structuralObjectClass zimbraCreateTimestamp …`; every UUID
becomes `<id:DN of the entry that owns it as zimbraId>` (cross references stay checkable, random
values go away); `*password`, `olcRootPW`, keys, certificates, tokens, DKIM selectors → `<secret>`;
`credentials=` of syncrepl → `<secret>`; generalized-time values → `<time>`; entries sorted by DN,
attribute lines sorted.

## Results of the legacy run (single node)

Image `localhost/carbonio-parity:noble` (1.92 GB), packages from the release channel on 2026-11-11
(`carbonio-core 4.6.7`, `carbonio-appserver 4.5.3`, `carbonio-directory-server 5.5.1`,
`carbonio-mta 4.2.8`, `carbonio-proxy 4.14.11`, `carbonio-memcached 1.6.45`, `carbonio-clamav 1.5.4`; full list in
`golden/single-node.package-versions`).

| step | duration |
|---|---|
| `./build.sh` (cold cache, includes `ubuntu:24.04` pull and ~220 packages) | 3 – 4 min |
| container start until systemd is `running`/`degraded` | 3 – 8 s |
| `carbonio-bootstrap -c` (exit 0) | 163 – 279 s (194 s alone; two runs side by side 163 s / 199 s); the spread comes from `dhparam` generation and the ~25 JVM starts of `zmprov` |
| `snapshot.sh` + copy | 2 s |

Two independent legacy runs from the same image produce **byte-identical snapshots**
(`./diff.sh out/legacy out/legacy2` is empty), so every difference `diff.sh` reports against
`golden/single-node` is a real difference, not noise.

`golden/single-node/` also has, next to it (not compared by `diff.sh`):
`single-node.bootstrap.log` (stdout of `carbonio-bootstrap`), `single-node.zmsetup.log`
(`/opt/zextras/log/zmsetup.*.log`, the detailed command log; timestamps stripped, test passwords
replaced by `<test-secret>`), `single-node.timing`, `single-node.package-versions`.

Stage order of the legacy run (`install-history.txt`):
`BEGIN configLCValues configInitCore configCASetup configCreateCert configInstallCertStore
configInstallCertMTA configInstallCertLDAP configInstallCertProxy configSetupLdap
configCreateServerEntry configSaveCA configSaveCert configSetServicePorts zimbraPrefTimeZoneId
configInitMta configSetEnabledServices configCreateDomain configInitSql setupSyslog setupCrontab END`.
End state: slapd (389) and MariaDB (7306) running under systemd
(`carbonio-openldap.service`, `carbonio-appserver-db.service`), everything else enabled but not
started (`STARTSERVERS="no"`).

### Problems met while getting the legacy path to complete (all handled by the harness)

1. **`zmmyinit` hangs** ("Failed to connect to mysql...retrying", then exits 1): `libncurses6`
   is missing on a minimal Ubuntu 24.04 (package dependency gap of `carbonio-mariadb`), fixed in the Containerfile.
2. **Silently the non-systemd path**: the carbonio targets are only enabled by the maintainer scripts
   if `/run/systemd/system` exists at install time; otherwise `isSystemd()` is false and setup.pl
   starts slapd with `ldap start` outside systemd. Fixed with `SYSTEMD_OFFLINE=1` + marker directory at build time.
3. **Enabled targets start every unconfigured service at container boot** (boot takes minutes, `starting` forever).
   The image records the enabled units and disables them again; `lib.sh` re-enables them right before the
   bootstrap, as the postinst of a live host does (enable, no start).
4. **`carbonio-openldap.service` fails with `217/USER`** in a rootless container (`Failed to keep CAP_SYS_ADMIN`)
   → `--cap-add=SYS_ADMIN`.
5. **Mailbox/LDAP map size**: `ldap_db_maxsize`/`olcDbMaxsize` workaround of the CE image pipeline (see above).
6. `repo.zextras.io/` itself answers 403; only `/release/ubuntu/dists/noble/...` is public (that is all apt needs).
7. Legacy behaviours worth knowing when comparing (all reproduced by golden): `Creating domain parity.test...already
   exists.` is printed on the second `cd` of `configCreateDomain`; "galsync account creation skipped" warning with
   `STARTSERVERS="no"`; `mailboxd_keystore_password` is written in clear text to the zmsetup log.
8. Unrelated to the harness: the legacy bootstrap needs no DNS with `-c` (see above).

## 2-node variant

## 2-node variant

Implemented and exercised for the legacy path: node 1 is the single-node scenario (LDAP master, MTA, proxy,
store), node 2 (`mail2.parity.test`) is a second store (`carbonio-appserver`, `carbonio-memcached`) joining the
LDAP of node 1 (`config/store-node.config`, `LDAPHOST=mail.parity.test`).

```
./build.sh                    # node 1 image
./build.sh --store            # node 2 image: localhost/carbonio-parity:noble-store (~2 min)
./run-2node.sh legacy         # out/2node-legacy/{node1,node2}, out/2node-legacy.node{1,2}.bootstrap.log
./run-2node.sh configd ../../bin/configd
```

Both containers join the podman network `parity-net` (10.89.77.0/24, fixed IPs .10 and .11), and resolve each
other through `/etc/hosts` (no DNS). Node 1 is bootstrapped first, then node 2; the node 1 snapshot is taken
**after** node 2 joined, so it contains node 2's `cn=mail2.parity.test,cn=servers` entry. Measured: node 1
161 s, node 2 117 s, 294 s in total including container start and snapshots. Snapshots of node 2 contain
`install-history.txt` ending with `CONFIG SESSION COMPLETE` and one listener (MariaDB 7306).

No golden is stored for the 2-node case (single-node is the required one); create it the same way:
`cp -r out/2node-legacy golden/2node` and compare with `./diff.sh golden/2node/node1 out/2node-configd/node1`
(and `node2`). A **replica LDAP** node (`LDAPREPLICATIONTYPE=replica`, needs `zmldapenablereplica` on the master
first) is not covered; it would be a third container with `carbonio-directory-server` installed.

Gotcha found while implementing: a duplicated own line in `/etc/hosts` makes slapd bind the same address twice
(`Address already in use`); `prepare_container` therefore writes exactly one line for the node itself.
