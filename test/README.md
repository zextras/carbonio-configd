<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Integration Tests

This package contains reusable test helpers for spinning up Carbonio service containers (LDAP) via Docker, Podman, or Kubernetes.

## Running Integration Tests

Integration tests require a container runtime (Docker, Podman, or Kubernetes) and are excluded from default builds to keep the main module's dependency surface minimal.

### Prerequisites

- **Docker** or **Podman**: Required for local testing
- **Kubernetes cluster**: Required for K8s-based testing (auto-detected via `KUBERNETES_SERVICE_HOST`)

### Running Tests

To run integration tests, use the `integration` build tag:

```bash
go test -tags=integration ./...
```

Or for a specific package:

```bash
go test -tags=integration ./internal/ldap
go test -tags=integration ./internal/tls
```

### Default Behavior

Without the `integration` tag, integration test files are excluded from compilation:

```bash
go build ./...      # Excludes integration tests
go test ./...       # Excludes integration tests
go vet ./...        # Excludes integration tests
```

## Gated Files

The following files are gated with `//go:build integration` and only compile when the integration tag is specified:

- `test/k8s.go` - Kubernetes pod management
- `test/testcontainers.go` - Docker/Podman container management via testcontainers-go
- `test/openldap.go` - self-contained OpenLDAP container (`StartOpenLDAP`) built from LDIF fixtures
- `internal/configmgr/openldap_integration_test.go` - configd loaders, plan and Execute against that container
- `internal/ldap/client_container_test.go` - LDAP client integration tests
- `internal/ldap/ldap_container_test.go` - LDAP manager integration tests
- `internal/tls/mode_container_test.go` - TLS mode integration tests

## OpenLDAP Suite (`make test-integration-ldap`)

`TestOpenLDAP_ConfigdCycle` (in `internal/configmgr`) needs no private image or live environment. It starts
slapd in a container from the public `docker.io/library/alpine:3.24` image (`apk add openldap` at start-up, about 5 s
once the image is cached), builds slapd-config and the data database from the LDIF fixtures in
`internal/configmgr/testdata/openldap/` and runs configd against it:

- `slapd.ldif`: slapd-config; `cn=config` writable by its rootDN, empty data suffix as in Carbonio
- `zimbra-schema.ldif`: reduced Carbonio schema (real attribute names and OIDs, only what the suite reads)
- `seed.ldif`: sanitized directory: global config, two servers (one overrides inherited attributes), two domains

The host side is fake: `postconf` and every other command go to a recording `systest.Exec`, files to an
in-memory `systest.FS`. The suite asserts the loaded snapshot (global, server with `serverInherited` attributes,
services), parity of the snapshot with the LDAP client's `gacf`/`gs` view, the first cycle's rewrite, postconf and
`cn=config` writes, the plan reasons after a modify (global attribute and localconfig), and that an unchanged
directory plans nothing.

```bash
make test-integration-ldap
```

Podman needs no extra setup: when `DOCKER_HOST` is unset the target uses the rootless socket
`$XDG_RUNTIME_DIR/podman/podman.sock` (enable it with `systemctl --user enable --now podman.socket`). Set
`DOCKER_HOST=unix:///run/user/$UID/podman/podman.sock` explicitly for `go test -tags integration` runs. Without a
reachable runtime the test skips.

Nightly job: run `make test-integration-ldap` on an agent with docker or podman and outbound access to Docker Hub and
the Alpine mirrors (the container installs slapd with `apk` at start-up). A failure to start the container fails the
test; only a missing runtime skips it. The target uses `-count=1`, so results are never cached.

## Dependencies

Integration tests depend on the following packages (only required when building with `-tags=integration`):

- `k8s.io/api`, `k8s.io/apimachinery`, `k8s.io/client-go` - Kubernetes client libraries
- `github.com/testcontainers/testcontainers-go` - Container orchestration
- `github.com/moby/moby` - Docker/Moby API
- `github.com/shirou/gopsutil` - System utilities (indirect dependency)

These dependencies are NOT included in the default module build and do not affect `go mod tidy` or `govulncheck` for standard builds.

## Container Runtime Detection

The test helpers automatically detect and use the available container runtime:

1. **Docker**: Preferred if available
2. **Podman**: Used if Docker is unavailable; automatically configures `DOCKER_HOST` and `TESTCONTAINERS_RYUK_DISABLED`
3. **Kubernetes**: Used when running inside a Kubernetes pod (detected via `KUBERNETES_SERVICE_HOST`)

Tests will skip gracefully if no container runtime is available.

## Example Usage

```go
package ldap

import (
	"testing"
	"github.com/zextras/carbonio-configd/test"
)

func TestWithContainer(t *testing.T) {
	// Skip if no container runtime is available
	test.SkipIfNoDocker(t)

	// Start LDAP container
	container, ctx := test.SpinUpCarbonioLdap(t, test.PublicImageAddress, test.LatestRelease)
	defer container.Stop()

	// Use container.URL() to connect
	url := container.URL()
	// ... test code ...
}
```
