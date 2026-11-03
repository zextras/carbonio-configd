//go:build integration

// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	openLDAPPort  = "1389/tcp"
	openLDAPSPort = "1636/tcp"
	fixturesDir   = "/fixtures"
	tlsDir        = fixturesDir + "/tls"
)

// openLDAPEntrypoint installs slapd, builds slapd-config from the fixtures,
// loads the schema and the seed data offline, then runs slapd in the
// foreground on port 1389 (and ldaps on 1636 when listeners names it).
func openLDAPEntrypoint(withTLS bool) string {
	listeners := "ldap://0.0.0.0:1389/"
	if withTLS {
		listeners += " ldaps://0.0.0.0:1636/"
	}

	return `set -eu
apk add --no-cache openldap openldap-back-mdb >/dev/null
mkdir -p /etc/openldap/slapd.d /var/lib/openldap/openldap-data /run/openldap
slapadd -n 0 -F /etc/openldap/slapd.d -l ` + fixturesDir + `/slapd.ldif
slapadd -n 0 -F /etc/openldap/slapd.d -l ` + fixturesDir + `/zimbra-schema.ldif
slapadd -n 1 -F /etc/openldap/slapd.d -l ` + fixturesDir + `/seed.ldif
exec slapd -d stats -F /etc/openldap/slapd.d -h "` + listeners + `"
`
}

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
	// TLS, when non-nil, makes slapd serve StartTLS on ldap:// and an ldaps://
	// listener with this certificate.
	TLS *OpenLDAPTLS
}

// OpenLDAPTLS is the server certificate material of an OpenLDAP container.
type OpenLDAPTLS struct {
	// CAPEM, CertPEM and KeyPEM are PEM encoded. The certificate must be valid
	// for the host the container is reached on (localhost / 127.0.0.1).
	CAPEM, CertPEM, KeyPEM []byte
}

// OpenLDAP is a running self-contained OpenLDAP container.
type OpenLDAP struct {
	// URL is the ldap:// URL of the mapped port on the host.
	URL string
	// LDAPSURL is the ldaps:// URL of the mapped TLS port; empty without
	// OpenLDAPFixtures.TLS.
	LDAPSURL string

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

	files, err := fixtureFiles(fixtures)
	if err != nil {
		t.Fatalf("OpenLDAP fixtures: %v", err)
	}

	ports := []string{openLDAPPort}
	waits := []wait.Strategy{wait.ForLog("slapd starting"), wait.ForListeningPort(openLDAPPort)}

	if fixtures.TLS != nil {
		ports = append(ports, openLDAPSPort)
		waits = append(waits, wait.ForListeningPort(openLDAPSPort))
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        OpenLDAPImage,
			Cmd:          []string{"sh", "-c", openLDAPEntrypoint(fixtures.TLS != nil)},
			Files:        files,
			ExposedPorts: ports,
			WaitingFor:   wait.ForAll(waits...),
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

	if fixtures.TLS != nil {
		sport, err := c.MappedPort(ctx, openLDAPSPort)
		if err != nil {
			t.Fatalf("OpenLDAP container ldaps port: %v", err)
		}

		o.LDAPSURL = fmt.Sprintf("ldaps://%s:%s", host, sport.Port())
	}

	return o
}

// fixtureFiles lists the files copied into the container. With TLS the
// slapd-config LDIF is rewritten to point cn=config at the certificate files.
func fixtureFiles(fixtures OpenLDAPFixtures) ([]testcontainers.ContainerFile, error) {
	files := make([]testcontainers.ContainerFile, 0, 6)

	for name, host := range map[string]string{
		"slapd.ldif":         fixtures.Slapd,
		"zimbra-schema.ldif": fixtures.Schema,
		"seed.ldif":          fixtures.Seed,
	} {
		abs, err := filepath.Abs(host)
		if err != nil {
			return nil, fmt.Errorf("fixture %s: %w", host, err)
		}

		f := testcontainers.ContainerFile{HostFilePath: abs, ContainerFilePath: fixturesDir + "/" + name, FileMode: 0o644}

		if name == "slapd.ldif" && fixtures.TLS != nil {
			ldif, err := os.ReadFile(abs) //nolint:gosec // G304: fixture path chosen by the test
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", abs, err)
			}

			patched, err := withTLSSettings(string(ldif))
			if err != nil {
				return nil, err
			}

			f = testcontainers.ContainerFile{
				Reader:            strings.NewReader(patched),
				ContainerFilePath: f.ContainerFilePath,
				FileMode:          f.FileMode,
			}
		}

		files = append(files, f)
	}

	if fixtures.TLS != nil {
		for name, content := range map[string][]byte{
			"ca.pem": fixtures.TLS.CAPEM, "server.crt": fixtures.TLS.CertPEM, "server.key": fixtures.TLS.KeyPEM,
		} {
			files = append(files, testcontainers.ContainerFile{
				Reader:            bytes.NewReader(content),
				ContainerFilePath: tlsDir + "/" + name,
				FileMode:          0o600,
			})
		}
	}

	return files, nil
}

// withTLSSettings appends the olcTLS* attributes to the cn=config entry (the
// first entry of the slapd-config LDIF) and requires TLS (olcSecurity tls=1)
// for every operation, so a client that skips StartTLS is refused with
// "confidentiality required" instead of silently working in clear text.
func withTLSSettings(ldif string) (string, error) {
	const marker = "dn: cn=config\n"

	start := strings.Index(ldif, marker)
	if start < 0 {
		return "", fmt.Errorf("slapd.ldif has no %q entry", strings.TrimSpace(marker))
	}

	end := strings.Index(ldif[start:], "\n\n")
	if end < 0 {
		return "", fmt.Errorf("slapd.ldif: cn=config entry is not terminated by a blank line")
	}

	end += start

	return ldif[:end] + "\nolcTLSCACertificateFile: " + tlsDir + "/ca.pem" +
		"\nolcTLSCertificateFile: " + tlsDir + "/server.crt" +
		"\nolcTLSCertificateKeyFile: " + tlsDir + "/server.key" +
		"\nolcSecurity: tls=1" + ldif[end:], nil
}
