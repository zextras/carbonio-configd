<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Carbonio Configd documentation

Start with the [project README](../README.md) for what configd is and a quick
tour. The pages below are the reference.

| Page | Read it to |
|---|---|
| [cli.md](cli.md) | find a command, flag, exit code or service verb; map a legacy `zm*` tool to its `configd` command; see the service names and aliases |
| [bootstrap.md](bootstrap.md) | write a node's bootstrap configuration (YAML, `CARBONIO_BOOTSTRAP_*`, wizard) or migrate a legacy `setup.pl` config file |
| [configuration.md](configuration.md) | know which localconfig keys and environment variables the daemon honors, and how polling and reload work |
| [systemd.md](systemd.md) | understand packaging per distribution, the systemd unit, readiness (`READY=1`), synchronous `REWRITE`, the polkit rule and the container image |
| [architecture.md](architecture.md) | follow the startup sequence, main loop, network protocol, cycle model (load, snapshot, plan, execute), observability, shadow mode and the package map |
| [restart-behavior.md](restart-behavior.md) | learn when a configuration change restarts a service, the hybrid policy on systemd hosts, the watchdog and dependency cascading |
| [service-registry.md](service-registry.md) | declare or change a service: the registry fields, the launch strategies and readiness probes, the lifecycle traces that guard them |
| [localconfig.md](localconfig.md) | see how the effective localconfig is built, the `zmlocalconfig` parity rules and the service hooks format |
| [proxy-templates.md](proxy-templates.md) | write or debug nginx proxy templates and use `configd proxy gen` / `proxy conf` |
| [performance.md](performance.md) | tune LDAP retries and parallel rewrites, and profile configd |

Other documents in the repository:

- [CONTRIBUTING.md](../CONTRIBUTING.md): development setup and hooks.
- [test/README.md](../test/README.md): integration test helpers (LDAP containers, Kubernetes).
