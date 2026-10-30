//go:build integration

// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// OpenLDAPImage is the public base image the self-contained OpenLDAP
	// container is assembled from (slapd is installed at start-up).
	OpenLDAPImage = "docker.io/library/alpine:3.24"

	// OpenLDAPDataBindDN is the rootDN of the data database of the container,
	// as fixed by its slapd.ldif.
	OpenLDAPDataBindDN = "uid=zimbra,cn=admins,cn=zimbra"
	// OpenLDAPDataPassword is the throwaway password of OpenLDAPDataBindDN.
	OpenLDAPDataPassword = "password"

	// OpenLDAPConfigBindDN is the rootDN of the slapd-config (cn=config) database.
	OpenLDAPConfigBindDN = "cn=config"
	// OpenLDAPConfigPassword is the throwaway password of OpenLDAPConfigBindDN.
	OpenLDAPConfigPassword = "configpass"

	openLDAPPort = "1389/tcp"
	fixturesDir  = "/fixtures"
)

// openLDAPEntrypoint installs slapd, builds slapd-config from the fixtures,
// loads the schema and the seed data offline, then runs slapd in the
// foreground on port 1389.
const openLDAPEntrypoint = `set -eu
apk add --no-cache openldap openldap-back-mdb >/dev/null
mkdir -p /etc/openldap/slapd.d /var/lib/openldap/openldap-data /run/openldap
slapadd -n 0 -F /etc/openldap/slapd.d -l ` + fixturesDir + `/slapd.ldif
slapadd -n 0 -F /etc/openldap/slapd.d -l ` + fixturesDir + `/zimbra-schema.ldif
slapadd -n 1 -F /etc/openldap/slapd.d -l ` + fixturesDir + `/seed.ldif
exec slapd -d stats -F /etc/openldap/slapd.d -h ldap://0.0.0.0:1389/
`

// OpenLDAPFixtures names the LDIF files an OpenLDAP container is built from.
// Each is a path on the host.
type OpenLDAPFixtures struct {
	// Slapd is the slapd-config LDIF: global config, schemas, config and data
	// databases.
	Slapd string
	// Schema is the LDIF added under cn=schema,cn=config.
	Schema string
	// Seed is the LDIF loaded into the data database (slapadd -n 1).
	Seed string
}

// OpenLDAP is a running self-contained OpenLDAP container.
type OpenLDAP struct {
	// URL is the ldap:// URL of the mapped port on the host.
	URL string

	container testcontainers.Container
}

// Stop terminates the container. It is safe to call from t.Cleanup.
func (o *OpenLDAP) Stop(ctx context.Context) error {
	return o.container.Terminate(ctx)
}

// StartOpenLDAP starts slapd in a container assembled from fixtures and
// returns once it accepts connections. It skips the test when no container
// runtime is reachable (Docker, or Podman through its Docker-compatible
// socket, e.g. DOCKER_HOST=unix:///run/user/$UID/podman/podman.sock) and
// registers the container's termination with t.Cleanup.
func StartOpenLDAP(ctx context.Context, t *testing.T, fixtures OpenLDAPFixtures) *OpenLDAP {
	t.Helper()

	SkipIfNoDocker(t)

	files := make([]testcontainers.ContainerFile, 0, 3)

	for name, host := range map[string]string{
		"slapd.ldif":         fixtures.Slapd,
		"zimbra-schema.ldif": fixtures.Schema,
		"seed.ldif":          fixtures.Seed,
	} {
		abs, err := filepath.Abs(host)
		if err != nil {
			t.Fatalf("fixture %s: %v", host, err)
		}

		files = append(files, testcontainers.ContainerFile{
			HostFilePath:      abs,
			ContainerFilePath: fixturesDir + "/" + name,
			FileMode:          0o644,
		})
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        OpenLDAPImage,
			Cmd:          []string{"sh", "-c", openLDAPEntrypoint},
			Files:        files,
			ExposedPorts: []string{openLDAPPort},
			WaitingFor: wait.ForAll(
				wait.ForLog("slapd starting"),
				wait.ForListeningPort(openLDAPPort),
			),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start OpenLDAP container: %v", err)
	}

	o := &OpenLDAP{container: c}

	t.Cleanup(func() {
		if err := o.Stop(context.WithoutCancel(ctx)); err != nil {
			t.Logf("stop OpenLDAP container: %v", err)
		}
	})

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("OpenLDAP container host: %v", err)
	}

	port, err := c.MappedPort(ctx, openLDAPPort)
	if err != nil {
		t.Fatalf("OpenLDAP container port: %v", err)
	}

	o.URL = fmt.Sprintf("ldap://%s:%s", host, port.Port())

	return o
}
