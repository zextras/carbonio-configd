// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
)

// factsServices is the part of the service controller the facts use.
// *services.Controller implements it; the tests use a fake.
type factsServices interface {
	IsSystemdMode() bool
	ServiceStatus(ctx context.Context, name string) (bool, error)
	ServiceStart(ctx context.Context, name string) error
}

// factsLDAPReader is the part of the LDAP client the facts use.
type factsLDAPReader interface {
	GetServerConfig(hostname string) (map[string]string, error)
	Close() error
}

// factsServicesOf and factsOpenLDAP are the seams of the tests.
var (
	factsServicesOf = func(e *Env) factsServices { return e.Ctl }
	factsOpenLDAP   = func(e *Env) (factsLDAPReader, error) {
		c, err := e.LDAP()
		if err != nil {
			return nil, err
		}

		return c, nil
	}
)

// Packages are the carbonio packages setup.pl knew (@packageList), in its
// order.
var Packages = []string{
	PkgCore,
	"carbonio-clamav",
	"carbonio-directory-server",
	PkgMTA,
	PkgAppserver,
	"carbonio-memcached",
	"carbonio-proxy",
}

// The packages the steps name most.
const (
	PkgCore      = "carbonio-core"
	PkgMTA       = "carbonio-mta"
	PkgAppserver = "carbonio-appserver"
)

// serviceToPackage maps a zimbraServiceInstalled/Enabled value to its
// package (setup.pl %packageServiceMap).
var serviceToPackage = map[string]string{
	"amavis":           PkgMTA,
	"antivirus":        "carbonio-clamav",
	"antispam":         PkgMTA,
	"opendkim":         PkgMTA,
	"cbpolicyd":        PkgMTA,
	"mta":              PkgMTA,
	"mailbox":          PkgAppserver,
	"directory-server": "carbonio-directory-server",
	"service-discover": PkgCore,
	"stats":            PkgCore,
	"memcached":        "carbonio-memcached",
	"proxy":            "carbonio-proxy",
	"service":          PkgAppserver,
}

// Facts are what the bootstrap reads from the node before it resolves the
// configuration.
type Facts struct {
	// Platform is the os-release ID (ubuntu, rhel, rocky, ...).
	Platform string
	// NewInstall is the setup.pl bootstrap mode: true until a configuration
	// session reached END after carbonio-core was installed.
	NewInstall bool
	// Configured are the stages .install_history marks CONFIGURED in the
	// current session.
	Configured map[string]bool
	// Installed are the installed carbonio packages.
	Installed map[string]bool
	// PrevInstalled are the packages LDAP lists as installed on this server
	// (zimbraServiceInstalled), on an existing install.
	PrevInstalled map[string]bool
	// Enabled are the packages whose services run on this node: on a new
	// install every installed package; on an existing one the packages LDAP
	// lists in zimbraServiceEnabled plus the newly installed ones.
	Enabled map[string]bool
	// EnabledServices are the zimbraServiceEnabled values of the server
	// ("imapproxy" read as "proxy"), on an existing install.
	EnabledServices map[string]bool
	// ServiceWebapp is the "service" web application (setup.pl
	// installedWebapps{service}).
	ServiceWebapp bool
	// Systemd is true when the services run under systemd.
	Systemd bool
}

// IsInstalled reports whether pkg is installed.
func (f *Facts) IsInstalled(pkg string) bool { return f.Installed[pkg] }

// IsEnabled reports whether pkg is enabled on the node.
func (f *Facts) IsEnabled(pkg string) bool { return f.Enabled[pkg] }

// IsServiceEnabled reports whether LDAP lists service as enabled.
func (f *Facts) IsServiceEnabled(service string) bool { return f.EnabledServices[service] }

// EnabledRoles are the bootstrap roles of the enabled packages.
func (f *Facts) EnabledRoles() []string {
	var roles []string

	for _, r := range bootstrap.Roles {
		if f.Enabled[bootstrap.RolePackage(r)] {
			roles = append(roles, r)
		}
	}

	return roles
}

// ReadHistory sets NewInstall and Configured from .install_history, as
// setup.pl getInstallStatus.
func (f *Facts) ReadHistory(data []byte) {
	installed := false
	f.Configured = map[string]bool{}

	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()

		switch {
		case strings.Contains(line, "CONFIG SESSION COMPLETE"), strings.Contains(line, "INSTALL SESSION COMPLETE"):
			continue
		case strings.Contains(line, "CONFIG SESSION START"):
			f.Configured = map[string]bool{}

			continue
		case strings.Contains(line, "INSTALL SESSION START"):
			installed = false
			f.Configured = map[string]bool{}

			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		switch op, stage := fields[1], fields[2]; {
		case op == "INSTALLED" && stage == PkgCore:
			installed = true
		case op == "CONFIGURED":
			f.Configured[stage] = true
		}
	}

	f.NewInstall = installed && !f.Configured["END"]
}

// DetectFacts reads the facts of the node. On an existing install it reads
// the server entry from LDAP, starting the local LDAP server first when this
// node is the one ldap_url names (setup.pl isEnabled).
func DetectFacts(e *Env) (*Facts, error) {
	f := &Facts{
		Installed:       map[string]bool{},
		PrevInstalled:   map[string]bool{},
		Enabled:         map[string]bool{},
		EnabledServices: map[string]bool{},
		Configured:      map[string]bool{},
		NewInstall:      true,
		Systemd:         factsServicesOf(e).IsSystemdMode(),
	}

	f.Platform = osReleaseID(e)

	switch data, err := e.Sys.FS.ReadFile(e.Paths.History); {
	case err == nil:
		f.ReadHistory(data)
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}

	installed, err := installedPackages(e, f.Platform)
	if err != nil {
		return nil, err
	}

	for _, p := range Packages {
		if slices.Contains(installed, p) {
			f.Installed[p] = true
		}
	}

	if f.NewInstall {
		for p := range f.Installed {
			f.Enabled[p] = true
		}

		f.ServiceWebapp = f.Enabled[PkgAppserver]

		return f, nil
	}

	if err := f.readServerEntry(e); err != nil {
		return nil, err
	}

	// setup.pl: an existing install always enables the service webapp.
	f.ServiceWebapp = true

	return f, nil
}

func (f *Facts) readServerEntry(e *Env) error {
	host, err := e.LCValue("zimbra_server_hostname")
	if err != nil {
		return err
	}

	url, err := e.LCValue("ldap_url")
	if err != nil {
		return err
	}

	if host != "" && strings.Contains(url, "/"+host) {
		if err := e.EnsureLDAPRunning(); err != nil {
			return err
		}
	}

	client, err := factsOpenLDAP(e)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	entry, err := client.GetServerConfig(host)
	if err != nil {
		return fmt.Errorf("reading server %s: %w", host, err)
	}

	f.applyServerEntry(e, entry)

	return nil
}

// applyServerEntry is setup.pl isEnabled on an existing install.
func (f *Facts) applyServerEntry(e *Env, entry map[string]string) {
	f.Enabled[PkgCore] = f.Installed[PkgCore]

	f.applyInstalledServices(e, entry["zimbraServiceInstalled"])
	f.applyEnabledServices(e, entry["zimbraServiceEnabled"])

	for _, p := range Packages {
		if f.Installed[p] && !f.PrevInstalled[p] {
			e.Log.Detail("Marking %s as installed. Services for %s will be enabled.", p, p)
			f.Enabled[p] = true
		}
	}

	for p := range f.Enabled {
		if !f.Installed[p] {
			delete(f.Enabled, p)
		}
	}
}

func (f *Facts) applyInstalledServices(e *Env, value string) {
	for _, svc := range splitValues(value) {
		p, ok := serviceToPackage[svc]
		if !ok {
			e.Log.Warn("unknown package installed for %s", svc)

			continue
		}

		f.PrevInstalled[p] = true
	}
}

func (f *Facts) applyEnabledServices(e *Env, value string) {
	for _, svc := range splitValues(value) {
		if svc == "imapproxy" {
			svc = "proxy"
		}

		p, ok := serviceToPackage[svc]
		if !ok {
			e.Log.Warn("unknown package installed for %s", svc)

			continue
		}

		f.Enabled[p] = true
		f.EnabledServices[svc] = true
	}
}

// EnsureLDAPRunning starts the local LDAP server when it is not running.
func (e *Env) EnsureLDAPRunning() error {
	running, err := factsServicesOf(e).ServiceStatus(e.Ctx, "ldap")
	if err == nil && running {
		return nil
	}

	e.Log.Detail("Starting the local LDAP server")

	if err := factsServicesOf(e).ServiceStart(e.Ctx, "ldap"); err != nil {
		return fmt.Errorf("starting ldap: %w", err)
	}

	e.Flags.LDAPStarted = true

	return nil
}

// splitValues splits a multi-valued LDAP attribute as the client returns it
// (one value per line).
func splitValues(v string) []string {
	return strings.FieldsFunc(v, func(r rune) bool { return r == '\n' })
}

// installedPackages lists the installed carbonio packages: dpkg counts only
// "install ok installed", as setup.pl rejected "not-installed".
func installedPackages(e *Env, platform string) ([]string, error) {
	var (
		out []byte
		err error
	)

	switch platform {
	case "ubuntu", "debian":
		out, err = e.Output(&Cmd{Path: "dpkg-query", Args: []string{"-W", "-f", "${Package} ${Status}\n", "carbonio-*"}})
	default:
		out, err = e.Output(&Cmd{Path: "rpm", Args: []string{"-qa", "--qf", "%{NAME} install ok installed\n", "carbonio-*"}})
	}

	var cmdErr *CmdError
	if err != nil && !errors.As(err, &cmdErr) {
		return nil, err
	}

	var names []string

	for line := range strings.SplitSeq(string(out), "\n") {
		name, status, ok := strings.Cut(line, " ")
		if ok && status == "install ok installed" {
			names = append(names, name)
		}
	}

	return names, nil
}

func osReleaseID(e *Env) string {
	data, _ := e.Sys.FS.ReadFile("/etc/os-release")

	for line := range strings.SplitSeq(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "ID="); ok {
			return strings.Trim(v, `"`)
		}
	}

	return ""
}
