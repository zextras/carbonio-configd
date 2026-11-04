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
- `TimeoutStartSec=300`, `TimeoutStopSec=10`, `KillSignal=SIGTERM`;
- `Restart=on-failure`, `RestartSec=3`, 5 starts per 300 s;
- `KillMode=process`: stopping or restarting configd signals only the daemon. The services it starts in legacy (non-systemd) mode live in their own units (see [Service run units](#service-run-units)), so they are not in this cgroup. `KillMode=process` still matters for the ones a configd older than this unit started, which are in this cgroup, and for a host where the run units cannot be used: `mixed`/`control-group` would `SIGKILL` them on `systemctl restart carbonio-configd` (a package upgrade does that), which the legacy `zmconfigd` never did;
- resource limits `MemoryHigh=384M`, `MemoryMax=512M`, `CPUQuota=80%`, `TasksMax=256`. They bound configd and the short-lived helpers it runs (`zmprov`, `zmlocalconfig`, `sudo`, `systemctl`), not the services, which are in the run units. On a host where the run units cannot be used the services are in this cgroup again (a one-time `WARN` says so: `Cannot start services as systemd units`) and these limits must be lifted with a drop-in (`MemoryHigh=infinity`, `MemoryMax=infinity`, `CPUQuota=`, `TasksMax=infinity`): clamd alone holds ~1.5 GB of signatures. `OOMScoreAdjust=-500` and `GOMAXPROCS=4` stay;
- hardening, limited to what keeps configd's own helpers working: `ProtectSystem=strict` with write access to `/opt/zextras` and `/run/carbonio`, `ProtectHome`, `PrivateTmp`, `ProtectControlGroups`, `ProcSubset=pid`, `RemoveIPC`, `UMask=0027`, and `CapabilityBoundingSet=` as a deny list (`CAP_SYS_BOOT`, `CAP_SYS_MODULE`, `CAP_SYS_TIME`, `CAP_SYSLOG`, `CAP_SYS_RAWIO`, `CAP_MKNOD`, `CAP_BPF`, `CAP_PERFMON`, `CAP_MAC_*`, `CAP_AUDIT_CONTROL`/`READ`, ...). configd has no capabilities and is granted no ambient ones.

### What the unit does not restrict, and why

Everything in the unit is inherited by every process configd execs. Since the service
processes moved to their own units, what is left to inherit is `sudo` (setuid root; postfix
status/stop/reload, `postalias`/`postmap` and the other prepare steps, the `kill` fallback for
root-owned processes: see the `zextras` sudoers rules), the java tools (`zmprov`, `zmlocalconfig`)
and `systemctl`. These are therefore **not** set, because `sudo` stops working with them:

- `NoNewPrivileges`: the kernel then ignores setuid bits and file capabilities on exec. `sudo` fails with `The "no new privileges" flag is set`. (Before the run units, nginx, with its file capability `cap_net_bind_service=ep`, could not bind ports 80/443 either.)
- An empty `CapabilityBoundingSet=`: `sudo` needs `CAP_SETUID`, `CAP_SETGID` and `CAP_AUDIT_WRITE`, and a binary whose file capabilities are not all in the bounding set fails to exec with `EPERM` (`fork/exec /opt/zextras/common/sbin/nginx: operation not permitted` was the restart of proxy that failed).
- Every directive that systemd implements with a seccomp filter: with `User=zextras`, systemd sets NoNewPrivileges on the process for any of them, whatever `NoNewPrivileges=` says (measured per directive on systemd 252 with `systemd-run -p User=zextras`): `PrivateDevices`, `ProtectKernelModules`, `ProtectKernelTunables`, `ProtectKernelLogs`, `ProtectClock`, `ProtectHostname`, `RestrictRealtime`, `RestrictSUIDSGID`, `LockPersonality`, `RestrictNamespaces`, `RestrictAddressFamilies`, `SystemCallArchitectures`, `SystemCallFilter` (and `MemoryDenyWriteExecute`, which also breaks Java). `NoNewPrivileges=no` is written out in the unit so that this stays explicit.
- `ProtectProc=invisible`: `/proc` hides the processes of other users, so root-owned services (postfix master, `zmstat-fd` started through sudo) would look stopped to the daemon and `zmcontrol status` and be restarted over and over.

Moving the `sudo` calls out of configd (to units of their own) would make `NoNewPrivileges` and
the seccomp directives possible; it was not done because those calls are short, frequent and
synchronous (status polling) and a unit per call is slower and accumulates state, and because
configd would then have no `sudo` fallback when systemd or polkit refuse. It is the next step if
the sandbox is to be tightened.

In systemd mode configd controls the other units with `systemctl` and the polkit rule below and
does not use `sudo`; the unit is the same in both modes, because the mode is chosen at run time
(a `carbonio-*` target is enabled or not).

## Service run units

In legacy (non-systemd) mode configd, and `zmcontrol start`/`configd service start`, start the
services themselves. If they were children of `carbonio-configd.service` they would live in its
cgroup and inherit its sandbox and limits, and a restart of the unit would stop them. Each is
therefore started as the instance of a template unit, under `carbonio.slice`:

| unit | `Type=` | runs | used for |
| --- | --- | --- | --- |
| `carbonio-run-exec@<service>.service` | `exec` | a foreground daemon, which stays the main process | everything started with a plain start: nginx, opendkim, clamd, freshclam, slapd, mailboxd, milter, `mysqld_safe`, the stats collectors |
| `carbonio-run-forking@<service>.service` | `forking` | a launcher that runs to completion and leaves daemons behind in the cgroup | `memcached -d`, saslauthd, amavisd, `postfix start` and the amavis-mc companion (both through `sudo`), cbpolicyd, the `zm*ctl` start scripts |

Both have `User=zextras`, `UMask=0027`, `Restart=no` (configd's watchdog is the restarter, as in
legacy), `KillMode=control-group`, `TimeoutStopSec=120` and the environment of the old daemon
(`EnvironmentFile=-/opt/zextras/data/systemd.env`, `TMPDIR`). They carry **no sandbox**:
the processes they run are the ones the legacy `zmconfigd` ran with no sandbox, and need `sudo`
and file capabilities (nginx). `systemd-cgls` shows each service under
`carbonio.slice/carbonio-run-*@<service>.service`, `systemctl status carbonio-run-exec@nginx` its
state and `journalctl -u carbonio-run-exec@nginx` what it prints to stdout/stderr when no log file
is configured. `systemctl set-property carbonio.slice MemoryMax=...` bounds all of them together;
no limit is shipped because the services have never had one.

How a start works (`internal/rununit`):

1. configd writes the command line to `/run/carbonio/units/<kind>-<service>.json` (a directory
   owned by `zextras`, mode 0700).
2. It runs `systemctl start carbonio-run-<kind>@<service>.service`. The unit's `ExecStart` is the
   hidden command `configd run-unit <kind> <service>`. It checks the owner and mode of the
   directory and of the spec, that the program path is absolute and clean, and that the environment
   holds nothing but `NOTIFY_SOCKET` (no `LD_*`, no `PATH`), then, for `exec`, execs the program in
   place (it becomes the main process, and its `stdout`/`stderr` are the log file of the spec), and,
   for `forking`, runs it to completion, writes its exit status and output next to the spec and exits 0
   (configd reads them back, so the `zm*ctl` exit status and output reach the caller as before).
3. The Process configd keeps is the unit's main process: its pid comes from `MainPID`, and waiting
   for it uses a `pidfd`; the unit's `Result`/`ExecMainStatus` give the exit status.

A start finds what an earlier one left under the same unit name: a *failed* unit is reset and a
*stopping* one waited for, so the name can be reused; an *active* (or still activating) unit is
**joined**, not restarted: the start waits for it (`systemctl start` on an active unit is a no-op)
and takes its main pid. This is what the daemon's watchdog and `zmcontrol start` (or a service
and the service that `Wants` it) find when they start the same service at the same time; the
legacy second start would just fail to bind, and replacing the unit would kill the service that
the first one is starting.

nginx forks into the background by default, which would end its unit with the parent, so in a unit it
runs with `-g "daemon off;"` (`ServiceDef.UnitArgs`); `ps` shows that in its command line. As a
plain child (the fallback) it keeps its usual arguments.

Services that `sudo` starts as root (postfix `master`, `amavis-mc`) are not under `carbonio.slice`
even though their launcher ran in a run unit: `sudo`'s PAM session (`pam_systemd`) moves the process
it starts into a `session-N.scope` of `user-0.slice`, as it always did. They are not children of
configd either.

The run units are used only when `/run/systemd/system` exists (systemd is init) and the caller is
not root. Anything that does not fit (stdin, a relative program, an environment other than
`NOTIFY_SOCKET`, an output that is not a file) is started as a plain child, as before. If
`systemctl` cannot start a unit (polkit denial, a template that is not installed because the package
is older, no bus), configd logs one `WARN` and starts every service as a child from then on, until
it restarts: a host never ends with a service it could not start. The configd daemon itself is not
started this way. Tests and `--once` runs that do not opt in with `services.WithUnits()` start plain
children.

### Upgrade path

The package ships the templates and the slice with the new unit and rule. `postinst` runs
`daemon-reload` and restarts configd (`KillMode=process` leaves the running services alone).
Services started by the old configd are in `carbonio-configd.service`'s cgroup (or in the session
that ran `zmcontrol start`); the new configd adopts them by pidfile or identity as it always did, and
they move to a run unit at their next restart, whether the daemon restarts them for a config change
or `zmcontrol restart` does. Nothing is restarted by the upgrade. Until then those processes keep
the old cgroup, which now carries the resource limits above: a heavy one (clamd) may be throttled
until it is restarted, so restart the antivirus after the upgrade.

## Readiness, reload and shutdown

- **`READY=1` is sent after the first config pass** (load, plan, rewrites, restarts) has completed. The TCP listener (`zmconfigd_listen_port`, default 7171, loopback) starts at the same point, so a `REWRITE` is never accepted before the daemon has configured the host. Use a `TimeoutStartSec` that covers a slow first pass (the shipped value is 300 s).
- **`REWRITE` is synchronous.** `configd rewrite` (and `init`, `tls`, `proxy`) wait until the main loop has written the sections: reply `SUCCESS REWRITES COMPLETE`, or `ERROR REWRITE TIMEOUT` after 60 s (the request stays queued). Callers can safely chain `configrewrite X && start X`. See [cli.md](cli.md#rewrite). One exception: while a cycle applies its restarts, a `REWRITE` for sections that same cycle has just rewritten is answered at once (and not queued). In systemd mode the daemon restarts a service with `systemctl try-reload-or-restart`, and the unit's `ExecStartPre` (`echo REWRITE proxy | netcat ... 7171`) asks for the rewrite of its own section; the daemon's loop is blocked on that very `systemctl`, so waiting for the next cycle would stall every such restart for the 60 s timeout.
- **Reload**: `systemctl reload` sends `SIGHUP` (`SIGUSR2` and `SIGALRM` do the same). configd sends `RELOADING=1`, runs a full cycle and sends `READY=1` again when it ends, whether or not the cycle succeeded.
- **Shutdown**: `SIGTERM`/`SIGINT` send `STOPPING=1` and cancel the main loop.

## Polkit rule

`50-carbonio-configd.rules` also lets `zextras` run `start`, `stop` and `reset-failed` on the
instances of `carbonio-run-exec@` and `carbonio-run-forking@` (`^carbonio-run-(exec|forking)@[a-z0-9][a-z0-9_.-]*\.service$`)
and denies every other verb on them. The units are static templates with a fixed `User=zextras`
rather than transient units (`systemd-run`) because polkit can only match the unit name and the
verb, not the properties: a `zextras` right to start transient units would let any process of that
user (the mailbox JVM, nginx workers) start one as root. With the templates the worst a compromised
`zextras` can do is run a command as `zextras` in a cgroup, which it can do anyway; the
spec directory is `zextras`-only, and nothing in a template runs as root.

The rule also lets the `zextras` user run `start`, `stop`,
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
