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
- `internal/ldap/client_container_test.go` - LDAP client integration tests
- `internal/ldap/ldap_container_test.go` - LDAP manager integration tests
- `internal/tls/mode_container_test.go` - TLS mode integration tests

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
