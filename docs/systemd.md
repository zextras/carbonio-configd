<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Packaging, systemd and containers

Source: `build/configd/` (`PKGBUILD`, `carbonio-configd.service`,
`carbonio-configd.rules`, completions), `build/yap.json`, `Jenkinsfile`,
`docker/configd/Dockerfile`, `cmd/configd/mainloop.go`, `cmd/configd/signals.go`.

- [Package and distributions](#package-and-distributions)
- [The systemd unit](#the-systemd-unit)
- [Readiness, reload and shutdown](#readiness-reload-and-shutdown)
- [Polkit rule](#polkit-rule)
- [Systemd vs legacy orchestration](#systemd-vs-legacy-orchestration)
- [Container image](#container-image)

## Package and distributions

`carbonio-configd` ships one static binary (`/opt/zextras/bin/configd`, built
with `CGO_ENABLED=0`), the polkit rule
(`/usr/share/polkit-1/rules.d/50-carbonio-configd.rules`) and bash completions
for `configd` and for the legacy wrapper names. Packages are built with
[yap](https://github.com/M0Rf30/yap) (`build/yap.json`) by the Jenkins
`Build Packages` stage; `carbonio-core` depends on `carbonio-configd`
(unversioned). The legacy `zm*` wrapper scripts belong to `carbonio-core`, not
to this package ([cli.md](cli.md#legacy-wrappers)).

| Distribution | systemd unit | How the daemon runs |
|---|---|---|
| ubuntu-noble, rocky-9 | `carbonio-configd.service` installed, enabled and started by the package | under systemd |
| ubuntu-jammy, rocky-8 | **not installed** | legacy mode: started by `zmcontrol` / `zmconfigdctl` (`configd service start configd` detaches the daemon and waits until it answers `STATUS`) |

Maintainer scripts (systemd distributions):

- `postinst`: reload systemd, kill a still-running legacy Jython `zmconfigd` JVM (waits up to 10 s), then on a fresh install `enable --now --no-block` (only `enable` when the `zextras` user or `systemd-envscript` does not exist yet, as configd is configured before `carbonio-core` on a fresh install); on upgrade `restart --no-block`. Never blocks the package manager on LDAP.
- `prerm`: `disable --now` on removal, `stop` on upgrade.

On jammy and rocky-8 the `postinst` only kills the legacy `zmconfigd` JVM.

Runtime dependencies are `libsystemd0` and `polkitd` (apt), `systemd-libs` and
`polkit` (yum).

## The systemd unit

`carbonio-configd.service` (`Type=notify`, `NotifyAccess=main`, user
`zextras`):

- part of `carbonio-appserver`, `carbonio-directory-server`, `carbonio-mta` and `carbonio-proxy` targets (`PartOf=`/`WantedBy=`); `After=network-online.target carbonio-openldap.service`;
- `ExecStartPre=` runs `/opt/zextras/bin/systemd-envscript`, then runs `configd ldap wait --timeout 120s`: it waits up to 120 s for one of the `ldap_url` URLs (resolved from localconfig and with the same TLS rules as the daemon, space-separated lists fail over) to accept a connection, and fails the start after that. When no `ldap_url` is configured it prints a warning and succeeds at once, because the daemon itself starts without LDAP (its LDAP queries fail until it is configured);
- `ExecStart=/opt/zextras/bin/configd`, `ExecReload=/bin/kill -HUP $MAINPID`;
- `WatchdogSec=120`: configd pings systemd every 60 s once READY has been sent (the watchdog only counts after `READY=1`, so a slow first pass is not penalised);
- `TimeoutStartSec=300`, `TimeoutStopSec=10`, `KillMode=mixed` (in systemd mode the unit's cgroup holds only the daemon and its short-lived helpers, `zmprov`, `postconf`, `systemctl`: SIGTERM to the daemon, SIGKILL to a helper still around; `legacy-mode.conf` below sets `process`), `KillSignal=SIGTERM`;
- `Restart=on-failure`, `RestartSec=3`, 5 starts per 300 s;
- limits: `MemoryMax=512M`, `MemoryHigh=384M`, `CPUQuota=80%`, `TasksMax=256`, `OOMScoreAdjust=-500`, `GOMAXPROCS=4`;
- hardening: `ProtectSystem=strict` with write access only to `/opt/zextras/{conf,log,data,common/conf}` and `/run/carbonio`, `PrivateTmp`, `PrivateDevices`, `NoNewPrivileges`, empty `CapabilityBoundingSet`, `RestrictNamespaces`, `RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX`, `SystemCallFilter=@system-service`, kernel/proc/clock protections, `UMask=0027`. `MemoryDenyWriteExecute` is left off because child tools (`zmprov`) run Java.

Because the unit drops every capability and sets `NoNewPrivileges`, configd
never uses `sudo` there: it controls other units through `systemctl` and the
polkit rule below. In systemd mode every service start, stop, restart and
status goes through `systemctl` (`startService`, `stopService`, `IsSystemdMode`),
and the other execs of the daemon (`postconf`, `postmap`, `postalias` of the MTA
config, `zmprov`/`zmlocalconfig` helpers) need neither `sudo` nor file
capabilities, so the whole sandbox holds, including `ProtectProc=invisible`
(status in systemd mode asks systemd, not `/proc`) and the cgroup limits, which
size configd alone.

The unit is **not** meant for the other layout: see
[Legacy mode on a systemd host](#legacy-mode-on-a-systemd-host).

## Legacy mode on a systemd host

Supported layouts:

| Host | configd is started by | Services are | Unit |
|---|---|---|---|
| rocky-9, ubuntu-noble (carbonio targets enabled) | `carbonio-configd.service` (hardened) | units of their own, driven by `systemctl` | shipped as is |
| ubuntu-jammy, rocky-8 | the SysV script / `zmcontrol` (no unit is installed there) | children of the daemon (legacy mode) | none, no sandbox |
| containers, dev images | `zmcontrol` or a custom unit | children of the daemon | none |

The unsupported fourth case is a host that has the unit installed **and** no
Carbonio target enabled (for example a dev image built from the rocky-9 packages
without enabling the targets). configd then runs in legacy mode (it starts the
services itself with the zm*ctl scripts, nginx and `sudo`, as the legacy
`zmconfigd` did) but under the hardened unit, which breaks that:

- `NoNewPrivileges=yes` (and every seccomp-backed directive, which makes systemd
  set it for `User=zextras`) makes the kernel ignore setuid bits and file
  capabilities: `sudo` fails with `The "no new privileges" flag is set`, nginx
  (`cap_net_bind_service=ep`) cannot bind 443;
- the empty `CapabilityBoundingSet=` makes exec of a file-capability binary fail
  with `fork/exec /opt/zextras/common/sbin/nginx: operation not permitted`;
- `ProtectProc=invisible` hides root-owned services (postfix `master`) from
  `/proc` status checks;
- the cgroup limits and `KillMode=mixed` apply to the services in the cgroup:
  clamd (about 1.5 GB of signatures) is throttled by `MemoryHigh` until its start
  times out, and `systemctl restart carbonio-configd` (a package upgrade) would
  `SIGKILL` every service, which the legacy `zmconfigd` never did.

The daemon notices it: when it runs with `NoNewPrivs: 1` in `/proc/self/status` and
`IsSystemdMode()` is false it logs one `WARN` at startup that names the drop-in.
The right fix is to enable the targets. If that is not wanted, install the
opt-in drop-in the package ships (it is **not** installed automatically):

```
install -D -m 644 /usr/share/carbonio-configd/systemd/legacy-mode.conf \
    /etc/systemd/system/carbonio-configd.service.d/legacy-mode.conf
systemctl daemon-reload && systemctl restart carbonio-configd
```

It relaxes exactly what the legacy-mode execs need: `NoNewPrivileges=no` plus every
seccomp-backed directive (`PrivateDevices`, `ProtectKernel*`, `ProtectClock`,
`ProtectHostname`, `Restrict*`, `LockPersonality`, `SystemCall*`), the empty
bounding set (replaced by a deny list of capabilities nothing here uses:
`CAP_SYS_MODULE`, `CAP_SYS_TIME`, `CAP_SYSLOG`, `CAP_SYS_RAWIO`, `CAP_MKNOD`, ...),
`ProtectProc=default`, `ReadWritePaths=-/opt/zextras -/run/carbonio` (services keep
state under `/opt/zextras`), no cgroup limits and `KillMode=process`. Everything else
stays: `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, `ProtectControlGroups`,
`ProcSubset=pid`, `RemoveIPC`, `UMask`. configd never gets ambient capabilities.
Delete the file and reload to go back to the hardened unit. The drop-in is
only packaged for rocky-9 and ubuntu-noble, with the unit.

`systemctl restart carbonio-configd` with the drop-in leaves the services running
(systemd logs `remains running after unit stopped` for them, which is expected).

## Readiness, reload and shutdown

- **`READY=1` is sent after the first config pass** (load, plan, rewrites, restarts) has completed. The TCP listener (`zmconfigd_listen_port`, default 7171, loopback) starts at the same point, so a `REWRITE` is never accepted before the daemon has configured the host. Use a `TimeoutStartSec` that covers a slow first pass (the shipped value is 300 s).
- **`REWRITE` is synchronous.** `configd rewrite` (and `init`, `tls`, `proxy`) wait until the main loop has written the sections: reply `SUCCESS REWRITES COMPLETE`, or `ERROR REWRITE TIMEOUT` after 60 s (the request stays queued). Callers can safely chain `configrewrite X && start X`. See [cli.md](cli.md#rewrite).
- **Reload**: `systemctl reload` sends `SIGHUP` (`SIGUSR2` and `SIGALRM` do the same). configd sends `RELOADING=1`, runs a full cycle and sends `READY=1` again when it ends, whether or not the cycle succeeded.
- **Shutdown**: `SIGTERM`/`SIGINT` send `STOPPING=1` and cancel the main loop.

## Polkit rule

`50-carbonio-configd.rules` lets the `zextras` user run `start`, `stop`,
`restart`, `reload`, `try-restart` and `try-reload-or-restart` on an explicit
allowlist of units (`carbonio-antispam`, `-antivirus`, `-appserver-db`,
`-appserver`, `-configd`, `-freshclam`, `-mailthreat`, `-memcached`, `-milter`,
`-nginx`, `-opendkim`, `-openldap`, `-policyd`, `-postfix`, `-saslauthd`,
`-stats` and `service-discover`, all `.service`). Other verbs (`enable`,
`disable`, `mask`, ...) on those units are explicitly denied, and other units
are left to the system policy: no prefix match, so an attacker-created
`carbonio-*.service` gains nothing.

## Systemd vs legacy orchestration

configd decides whether Carbonio is orchestrated by systemd: at least
one of the targets `carbonio-directory-server`, `carbonio-appserver`,
`carbonio-proxy`, `carbonio-mta` or `service-discover` must be enabled (`systemctl is-enabled`, `internal/systemd`).
Otherwise (jammy, rocky-8, containers) services are managed by the
legacy launchers (binary, pid file, process identity), or by
[service hooks](localconfig.md#service-hooks-zmconfigd_service_hooks).

On a systemd host config-driven restarts follow the **hybrid policy**: light
services get `systemctl try-reload-or-restart`; the heavy ones (`mailbox`,
`appserver-db`, `store`, `ldap`) are never restarted automatically, the daemon
only logs `Restart pending` for them. Everything about it, including debounce,
retries and the watchdog, is in [restart-behavior.md](restart-behavior.md).

## Container image

`docker/configd/Dockerfile` builds the `carbonio-configd` image: `FROM scratch`,
a `zextras` user (uid/gid 999) and `USER 999`, the static
`/opt/zextras/bin/configd` as `ENTRYPOINT`, and the templates taken from the
`carbonio-core` and `carbonio-common-appserver-conf` Ubuntu jammy packages
under `/opt/zextras/share/configd/conf/` (`zmconfigd.cf`, `*.in`,
`zmconfigd/*.cf`, `attrs/`). The image sets `CARBONIO_BASE_DIR=/opt/zextras`
and `CONFIGD_LC_zmconfigd_template_dir=/opt/zextras/share/configd`, so the
daemon and `configd ldap gs` find their templates there. The Jenkinsfile
publishes it for `linux/amd64` and `linux/arm64`.

```bash
docker build -f docker/configd/Dockerfile -t carbonio-configd .
```

**OCI metadata.** The Dockerfile sets the labels that never change
(`org.opencontainers.image.licenses=AGPL-3.0-only`, `.documentation`). The Jenkinsfile
(`dockerStage` `ocLabels`) passes `title`, `vendor` (Zextras), `version` (the `pkgver` of
`build/configd/PKGBUILD`) and `description` (the text of `docker/configd/description.md`),
and the shared pipeline library adds `created`, `source`, `url`, `revision` and `ref.name`
from the git checkout. Inspect them with
`docker inspect --format '{{json .Config.Labels}}' carbonio-configd`. Keep
`description.md` free of double quotes, backticks and `$`: it is inlined into a shell
argument.

Use it as a container, or as a layer in an image that needs a co-located
binary:

```dockerfile
COPY --from=<registry>/carbonio-configd:main /opt/zextras/ /opt/zextras/
```

There is no systemd in a container: services are controlled through
`zmconfigd_service_hooks` and the keys come from `CONFIGD_LC_*` variables, see
[configuration.md](configuration.md#containers-and-kubernetes).
