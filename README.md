<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Carbonio Configd

![Contributors](https://img.shields.io/github/contributors/zextras/carbonio-configd "Contributors") ![Activity](https://img.shields.io/github/commit-activity/m/zextras/carbonio-configd "Activity") ![License](https://img.shields.io/badge/license-AGPL--3.0-green
"License") ![Project](https://img.shields.io/badge/project-carbonio-informational
"Project") [![Twitter](https://img.shields.io/twitter/url/https/twitter.com/zextras.svg?style=social&label=Follow%20%40zextras)](https://twitter.com/zextras)

Carbonio Configd is the Go configuration and service-management daemon of
Carbonio. It replaces the legacy Jython `zmconfigd` and the
`zm*ctl`/`zmcontrol`/`zmlocalconfig`/`zmproxyconfgen` implementations with a
single static binary, `configd`, with no JVM: it watches LDAP and localconfig,
renders configuration files from templates, applies Postfix settings and
restarts services when their configuration changes.

- [What it does](#what-it-does)
- [Installation](#installation)
- [Usage](#usage)
- [Legacy tools](#legacy-tools)
- [Documentation](#documentation)
- [Development](#development)
- [License](#license)

## What it does

- **Daemon**: polls LDAP and localconfig every `zmconfigd_interval` seconds (default 60), rewrites the files whose configuration changed and restarts the services that need it; `systemd` readiness, watchdog and reload supported.
- **Rewrite on demand**: `configd rewrite <section>` asks the running daemon and returns when the files are written.
- **Dry run**: `configd plan` shows what a rewrite would change and why; `configd shadow` runs the daemon cycle next to a real configd without any side effect.
- **Service control**: `configd service` and `configd control` start, stop, restart and report every Carbonio service (systemd or legacy launchers).
- **Built-in tools**: native `localconfig` reader/editor, nginx `proxy` generator, `tls` mail-mode switch, and `ldap` access for the few `zmprov` verbs configd needs.
- **Containers**: runs in Kubernetes from environment variables, with an image published by the pipeline.

## Installation

configd is the `carbonio-configd` package, a dependency of `carbonio-core`; it
installs `/opt/zextras/bin/configd`. Where Carbonio is orchestrated by systemd
(ubuntu-noble, rocky-9) the package also installs, enables and starts
`carbonio-configd.service`. On ubuntu-jammy and rocky-8 no unit is shipped: the
daemon runs in legacy mode, started by `zmcontrol`. Details in
[docs/systemd.md](docs/systemd.md).

## Usage

Run as the `zextras` user (the daemon and everything that changes the host refuse other users).

```bash
systemctl status carbonio-configd.service        # the daemon (systemd hosts)
configd rewrite proxy mta                        # force a rewrite of two sections
configd plan mta                                 # what would change, and why (exit 1 = changes)
configd service restart proxy                    # one service
configd control status                           # every enabled service
configd localconfig -k ldap_port                 # read localconfig (zmlocalconfig)
configd ldap gcf zimbraMtaRestriction            # read the global config (zmprov gcf)
configd proxy gen -n                             # dry-run the nginx config generator
configd --help                                   # all commands
```

Every command, flag and exit code is documented in [docs/cli.md](docs/cli.md).

## Legacy tools

`carbonio-core` keeps the legacy command names as thin wrappers around
`configd`, so existing scripts keep working:

| Legacy | Now |
|---|---|
| `zmcontrol start\|stop\|restart\|status` | `configd control …` |
| `zm<name>ctl <verb>` (`zmmtactl`, `zmproxyctl`, `zmstorectl`, …) | `configd service <verb> <name>` |
| `zmconfigdctl` | `configd service <verb> configd` |
| `zmlocalconfig` | `configd localconfig` |
| `zmproxyconfgen` / `zmproxyconf` | `configd proxy gen` / `configd proxy conf` |
| `zmtlsctl` | `configd tls` |
| `configrewrite` | `configd rewrite` |

The full table and the service names are in [docs/cli.md](docs/cli.md#legacy-wrappers).

## Documentation

See the [documentation index](docs/README.md):

- [docs/cli.md](docs/cli.md): commands, flags, exit codes, services, legacy wrappers
- [docs/configuration.md](docs/configuration.md): localconfig keys, environment, polling
- [docs/systemd.md](docs/systemd.md): packaging, systemd unit, readiness, container image
- [docs/architecture.md](docs/architecture.md): how the daemon works, package map
- [docs/restart-behavior.md](docs/restart-behavior.md): when and how services restart
- [docs/localconfig.md](docs/localconfig.md), [docs/proxy-templates.md](docs/proxy-templates.md), [docs/performance.md](docs/performance.md)

## Development

Requires Go 1.27 (see `go.mod`) and `make`.

```bash
make build              # static binary in ./bin/configd
make test               # unit tests
make lint               # golangci-lint
make check              # lint + race tests + deadcode + duplication
make reuse              # license headers (REUSE)
make test-integration-ldap   # OpenLDAP container suite (docker or podman)
```

Build tags `profiling` and `tracing` add flags to the binary
(`make build-profiling`, `make build-tracing`, `make build-full`). See
[CONTRIBUTING.md](CONTRIBUTING.md) for the pre-commit hooks and
[test/README.md](test/README.md) for the integration tests.

Layout: `cmd/configd/` (entry point and Kong command structs), `internal/`
(one package per concern: `cli/` subcommands, `configmgr`, `plan`, `restart`,
`services`, `ldap`, `localconfig`, `proxy`, `network`, …; see the
[package map](docs/architecture.md#package-map)), `build/configd/` (PKGBUILD,
systemd unit, polkit rule), `docker/configd/` (image).

## License

Released under the **GNU Affero General Public License v3.0**, see [COPYING](COPYING).

Copyright 2026 Zextras <https://www.zextras.com>
