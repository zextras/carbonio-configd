// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package test provides reusable test helpers for spinning up Carbonio
// service containers (LDAP) via Docker, Podman, or Kubernetes.
package test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-units"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// LatestRelease is the default image tag.
	LatestRelease = "latest"
	// PublicImageAddress is the format string for the Carbonio OpenLDAP image.
	PublicImageAddress = "registry.dev.zextras.com/dev/carbonio-openldap:%s"
	// DefaultLdapUserDN is the bind DN used by tests.
	DefaultLdapUserDN = "uid=zimbra,cn=admins,cn=zimbra"
)

// LdapContainer holds closures for interacting with a running LDAP container.
type LdapContainer struct {
	Stop func()
	URL  func() string
	IP   func() string
	Port func() string
}

// SkipIfNoDocker calls t.Skip when neither Docker nor Podman is reachable.
// When Podman is found but Docker is not, DOCKER_HOST and
// TESTCONTAINERS_RYUK_DISABLED are set so testcontainers-go uses the
// Podman socket transparently.
func SkipIfNoDocker(t *testing.T) {
	t.Helper()

	if dockerAvailable() {
		return
	}

	if podmanSocket := podmanAvailable(); podmanSocket != "" {
		t.Logf("Docker not available, using Podman via %s", podmanSocket)
		t.Setenv("DOCKER_HOST", "unix://"+podmanSocket)
		t.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

		return
	}

	t.Skip("No container runtime available: neither Docker nor Podman reachable")
}

func dockerAvailable() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}

	ctx := context.Background()

	return exec.CommandContext(ctx, "docker", "info").Run() == nil
}

func podmanAvailable() string {
	if _, err := exec.LookPath("podman"); err != nil {
		return ""
	}

	ctx := context.Background()

	out, err := exec.CommandContext(ctx, "podman", "info", "--format", "{{.Host.RemoteSocket.Path}}").Output()
	if err != nil {
		return ""
	}

	socket := strings.TrimSpace(string(out))
	if socket == "" {
		return ""
	}

	return socket
}

// ContainerRuntimeAvailable reports whether any supported container runtime
// (Docker, Podman, or Kubernetes) can be reached. When Podman is detected,
// DOCKER_HOST and TESTCONTAINERS_RYUK_DISABLED are configured for
// testcontainers-go compatibility.
func ContainerRuntimeAvailable() bool {
	if dockerAvailable() {
		return true
	}

	if socket := podmanAvailable(); socket != "" {
		_ = os.Setenv("DOCKER_HOST", "unix://"+socket)
		_ = os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

		return true
	}

	return IsRunningInKubernetes()
}

// StartLdapContainer is a TestMain-friendly variant that returns an error
// instead of calling t.Fatal. Use this in TestMain where *testing.T is
// unavailable. The caller must defer container.Stop().
func StartLdapContainer() (LdapContainer, error) {
	if IsRunningInKubernetes() {
		return startLdapContainerK8s()
	}

	return startLdapContainerDocker()
}

func startLdapContainerDocker() (LdapContainer, error) {
	ctx := context.Background()

	ulimits := []*units.Ulimit{{Name: "nofile", Soft: 32678, Hard: 32678}}
	req := testcontainers.ContainerRequest{
		Image:        fmt.Sprintf(PublicImageAddress, LatestRelease),
		ExposedPorts: []string{"1389/tcp"},
		WaitingFor: wait.ForAll(
			wait.ForLog(`modifying entry "uid=zimbra,cn=admins,cn=zimbra"`),
			wait.ForListeningPort("1389/tcp"),
		),
		HostConfigModifier: func(config *container.HostConfig) {
			config.AutoRemove = true
			config.Ulimits = ulimits
		},
		ShmSize: 8 * 1024 * 1024 * 1024,
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return LdapContainer{}, fmt.Errorf("start container: %w", err)
	}

	port, _ := c.MappedPort(ctx, "1389")

	lc := LdapContainer{
		Stop: func() { _ = c.Terminate(ctx) },
		IP:   func() string { return "localhost" },
		Port: func() string { return port.Port() },
	}
	lc.URL = func() string { return "ldap://localhost:" + lc.Port() }

	return lc, nil
}

// SpinUpCarbonioLdap launches a Carbonio LDAP instance with the desired
// version. It returns the LDAP instance context and the container itself.
// Note it is necessary to defer the container stop otherwise the instance
// will be hanging forever `defer ldapContainer.Stop()`!
//
// When running inside a Kubernetes cluster (detected via KUBERNETES_SERVICE_HOST),
// the container is launched as a Kubernetes Pod. Otherwise, Docker via
// testcontainers is used.
//
//nolint:thelper // not a helper, it's a setup function
func SpinUpCarbonioLdap(t *testing.T, address, version string) (LdapContainer, context.Context) {
	if IsRunningInKubernetes() {
		t.Log("Kubernetes environment detected, using K8s pod for LDAP container")

		return SpinUpCarbonioLdapK8s(t, address, version)
	}

	t.Log("Using Docker via testcontainers for LDAP container")

	ctx := context.Background()

	t.Log("Networks that are going to be attached to the container")

	ulimits := []*units.Ulimit{{Name: "nofile", Soft: 32678, Hard: 32678}}
	req := testcontainers.ContainerRequest{
		Image:        fmt.Sprintf(address, version),
		ExposedPorts: []string{"1389/tcp"},
		WaitingFor: wait.ForAll(
			wait.ForLog("modifying entry \"uid=zimbra,cn=admins,cn=zimbra\""),
			wait.ForListeningPort("1389/tcp"),
		),
		HostConfigModifier: func(config *container.HostConfig) {
			config.AutoRemove = true
			config.Ulimits = ulimits
		},
		ShmSize: 8 * 1024 * 1024 * 1024,
	}

	ldapContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatal(err)
	}

	cip, _ := ldapContainer.ContainerIP(ctx)
	t.Log("Container ip: " + cip)

	ports, _ := ldapContainer.Ports(ctx)

	for port, bindings := range ports {
		for _, binding := range bindings {
			t.Log("Port: " + port.Port() + " host bind: " + binding.HostPort + " ip bind: " + binding.HostIP)
		}
	}

	containerWithPort := LdapContainer{
		Stop: func() {
			err := ldapContainer.Terminate(ctx)
			if err != nil {
				t.Log(err)
			}
		},
		IP: func() string {
			return "localhost"
		},
		Port: func() string {
			port, _ := ldapContainer.MappedPort(ctx, "1389")

			return port.Port()
		},
	}
	containerWithPort.URL = func() string {
		return "ldap://localhost:" + containerWithPort.Port()
	}

	return containerWithPort, ctx
}
