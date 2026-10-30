// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// Hook is a function that runs before/after service actions.
type Hook func(ctx context.Context, sm *ServiceManager) error

// EnableCheckFunc determines if a service/dependency should be started.
type EnableCheckFunc func(ctx context.Context) bool

// ServiceDef defines a service with its systemd units, dependencies, and hooks.
type ServiceDef struct {
	// Name is the internal service name (e.g., "mta")
	Name string
	// DisplayName is the human-readable name (e.g., "MTA")
	DisplayName string
	// SystemdUnits are the systemd unit names for this service
	SystemdUnits []string
	// BinaryPath is the direct binary for non-systemd fallback (e.g., "/opt/zextras/common/sbin/postfix")
	BinaryPath string
	// BinaryArgs are args passed when starting via BinaryPath
	BinaryArgs []string
	// Detached marks long-running daemons that don't fork themselves and must be
	// spawned in the background by configd (Setsid + don't Wait). For services
	// that already self-daemonize (postfix, slapd, opendkim, …), leave false:
	// startDirect's Wait() will return as soon as the launcher returns.
	Detached bool
	// LogFile is where stdout+stderr are redirected for detached services.
	// Defaults to /opt/zextras/log/<name>.out when empty and Detached=true.
	LogFile string
	// NeedsRoot marks services that require root privileges to start (e.g. postfix).
	// When true and the current user is not root, startDirect prefixes with sudo.
	NeedsRoot bool
	// PidFile is the path to the service's PID file (e.g., /run/carbonio/nginx.pid).
	// Legacy-mode status reads the PID from this file first and believes it only
	// when that process is alive AND is of the ProcessName identity (a stale
	// file whose PID was recycled by another binary is ignored). When the file is
	// unreadable or stale, detection falls through to the ProcessName scan.
	PidFile string
	// ProcessName is the service's process identity in legacy mode, used for
	// status, pid lookup and stop (procident.go). It is an exact name, never a
	// substring: an absolute path (compared with /proc/<pid>/exe and with every
	// argv token — the script path of a perl service, the retitled
	// "nginx: master process <path>"), a glob over a whole path
	// ("/opt/zextras/libexec/zmstat-*"), or a slash-less name such as a Java
	// main class or "amavisd" (compared with each token and its basename). For
	// a binary shared with unrelated processes the program may be followed by
	// required arguments, all of which must be argv tokens:
	// "/usr/bin/consul agent /etc/zextras/service-discover". A
	// process that merely mentions the service — "tail -f .../clamd.log",
	// "consul connect envoy -sidecar-for carbonio-memcached" — is not matched.
	ProcessName string
	// GracefulStop, when positive, makes the legacy-mode stop SIGTERM the
	// PidFile process (validated against ProcessName), wait up to this long and
	// only then SIGKILL — instead of the default immediate SIGTERM-all of
	// ProcessName with the kill grace. For daemons that must flush a backing
	// store (slapd/LMDB) before exiting. Requires PidFile; CustomStop wins.
	GracefulStop time.Duration
	// Dependencies are services that must be started before this one
	Dependencies []string
	// EnableCheck is called to determine if this service should be started (for conditional deps)
	EnableCheck EnableCheckFunc
	// PreStart hooks run before starting the service
	PreStart []Hook
	// PostStart hooks run after starting the service
	PostStart []Hook
	// PreStop hooks run before stopping the service
	PreStop []Hook
	// ConfigRewrite lists config names to regenerate before start
	ConfigRewrite []string
	// UseSDNotify marks services whose binary is compiled with sd_notify support
	// and sends READY=1 when fully initialized. When true, the start path creates
	// a temporary NOTIFY_SOCKET and waits for READY=1 before returning, giving
	// event-driven readiness detection instead of polling.
	UseSDNotify bool
	// CustomStart, when set, takes over the non-systemd start path entirely.
	// Used for services whose launch is too dynamic for a static BinaryPath —
	// e.g., mailbox (Java command built from localconfig + heap sizing) and
	// stats (orchestrator for ~11 zmstat-* collectors with a pidfile bundle).
	CustomStart func(ctx context.Context, def *ServiceDef) error
	// CustomStop mirrors CustomStart for the stop side. Used by stats to
	// kill every collector recorded in its aggregate pidfile.
	CustomStop func(ctx context.Context, def *ServiceDef) error
	// UseSystemdForStatus, when true, forces status detection via systemctl
	// is-active even in non-systemd (legacy) mode. Use for services like
	// service-discover that are always managed by their systemd unit regardless
	// of the Carbonio orchestration mode.
	UseSystemdForStatus bool
	// ExternallyManaged marks services whose lifecycle configd only observes.
	// Start, stop, restart and reload are no-ops in every mode; status still
	// reports the service. service-discover is the only such service: it owns
	// its own systemd unit (service-discover.service runs
	// /usr/bin/service-discoverd as the service-discover user, enabled at
	// boot), and legacy zmcontrol never touched it either — control.pl skips
	// it in doStartup, omits it from %allservices/%stoporder, and only ever
	// runs `systemctl is-active service-discover.service` for status.
	ExternallyManaged bool
}

// ServiceAliases is the single canonical table of alternative service names
// — both legacy CLI aliases ("clamd", "mailboxd", "directory",
// "config-service") and LDAP zimbraServiceEnabled values ("directory-server",
// "service", "zmconfigd") — mapped to their canonical Registry key. Resolved
// by LookupService (start/stop/restart/status/reload, network protocol,
// watchdog) and, via MapLDAPServiceToRegistry, by discovery.go's LDAP
// service list. These names are accepted as input but do NOT appear in
// Registry or AllServiceNames — they are resolution aliases only.
var ServiceAliases = map[string]string{
	svcClamd:             svcAntivirus,
	svcMailboxd:          svcMailbox,
	svcService:           svcMailbox,
	svcZmconfigd:         svcConfigd,
	groupDirectoryServer: svcLdap,
	"directory":          svcLdap,
	"config-service":     svcConfigd,
}

// cbpolicydInitDB initializes the cbpolicyd sqlite database if it doesn't exist.
// Mirrors legacy cbpolicydctl.sh pre-start logic.
func (c *Controller) cbpolicydInitDB(_ context.Context, _ *ServiceManager) error {
	if _, err := c.env.FS.Stat(c.p.cbpolicydDB); err == nil {
		return nil
	}

	if err := c.env.FS.MkdirAll(filepath.Dir(c.p.cbpolicydDB), 0o755); err != nil {
		return fmt.Errorf("failed to create cbpolicyd DB directory: %w", err)
	}

	if _, err := c.env.FS.Stat(c.p.cbpolicydInitBin); err != nil {
		return fmt.Errorf("cbpolicyd DB missing and init binary not found: %w", err)
	}

	if output, err := sys.CombinedOutput(context.Background(), c.env.Exec, c.p.cbpolicydInitBin); err != nil {
		return fmt.Errorf("cbpolicyd DB init failed: %s: %w", strings.TrimSpace(string(output)), err)
	}

	return nil
}

// clamdDirInit creates the clamav database directory if missing. Mirrors the
// `mkdir -p /opt/zextras/data/clamav/db` from legacy clamdctl.sh; clamd refuses
// to start if the directory does not exist.
func (c *Controller) clamdDirInit(_ context.Context, _ *ServiceManager) error {
	if err := c.env.FS.MkdirAll(c.p.clamdDir, 0o755); err != nil {
		return fmt.Errorf("failed to create clamav DB directory: %w", err)
	}

	return nil
}

// milterEnabled checks if the milter service is enabled via mta_milter_options file.
func (c *Controller) milterEnabled(_ context.Context) bool {
	data, err := c.env.FS.ReadFile(c.p.milterOptions)
	if err != nil {
		return false
	}

	return strings.Contains(string(data), "zimbraMilterServerEnabled=TRUE")
}

// defaultRegistry builds the production service registry for this Controller:
// paths come from its layout and the hooks are bound to it, so every launcher
// acts through the Controller's sys.System.
func (c *Controller) defaultRegistry() map[string]*ServiceDef {
	return map[string]*ServiceDef{
		svcMemcached: {
			Name:         svcMemcached,
			DisplayName:  svcMemcached,
			SystemdUnits: []string{"carbonio-memcached.service"},
			BinaryPath:   c.p.common + "/bin/memcached",
			BinaryArgs:   []string{"-d", "-U", "0", "-l", "127.0.1.1,127.0.0.1", "-p", "11211"},
			ProcessName:  c.p.common + "/bin/memcached",
		},
		svcCbpolicyd: {
			Name:          svcCbpolicyd,
			DisplayName:   svcCbpolicyd,
			SystemdUnits:  []string{unitPolicyd},
			BinaryPath:    c.p.common + "/bin/cbpolicyd",
			BinaryArgs:    []string{"--config", c.p.conf + "/cbpolicyd.conf"},
			PidFile:       c.p.run + "/cbpolicyd.pid",
			ProcessName:   c.p.common + "/bin/cbpolicyd",
			ConfigRewrite: []string{svcCbpolicyd},
			PreStart:      []Hook{c.cbpolicydInitDB},
		},
		svcStats: {
			Name:         svcStats,
			DisplayName:  svcStats,
			SystemdUnits: []string{"carbonio-stats.service"},
			ProcessName:  c.p.statsGlob,
			CustomStart:  c.statsCustomStart,
			CustomStop:   c.statsCustomStop,
		},
		svcOpendkim: {
			Name:         svcOpendkim,
			DisplayName:  svcOpendkim,
			SystemdUnits: []string{unitOpendkim},
			BinaryPath:   c.p.common + "/sbin/opendkim",
			BinaryArgs:   []string{"-f", "-x", c.p.conf + "/opendkim.conf", "-u", userZextras},
			PidFile:      c.p.run + "/opendkim.pid",
			ProcessName:  c.p.common + "/sbin/opendkim",
			UseSDNotify:  true,
		},
		svcFreshclam: {
			Name:         svcFreshclam,
			DisplayName:  svcFreshclam,
			SystemdUnits: []string{"carbonio-freshclam.service"},
			BinaryPath:   c.p.common + "/bin/freshclam",
			BinaryArgs: []string{
				"--config-file=" + c.p.conf + "/freshclam.conf",
				"--quiet", "-d", "--checks=12", "--foreground=true",
			},
			Detached:    true,
			UseSDNotify: true,
			PidFile:     c.p.run + "/freshclam.pid",
			ProcessName: c.p.common + "/bin/freshclam",
		},
		svcSaslauthd: {
			Name:          svcSaslauthd,
			DisplayName:   svcSaslauthd,
			SystemdUnits:  []string{"carbonio-saslauthd.service"},
			BinaryPath:    c.p.common + "/sbin/saslauthd",
			BinaryArgs:    []string{"-r", "-a", saslMechZimbra},
			PidFile:       c.p.run + "/saslauthd.pid",
			ProcessName:   c.p.common + "/sbin/saslauthd",
			ConfigRewrite: []string{svcSasl},
		},
		svcMilter: {
			Name:         svcMilter,
			DisplayName:  svcMilter,
			SystemdUnits: []string{"carbonio-milter.service"},
			ProcessName:  milterMainClass,
			EnableCheck:  c.milterEnabled,
			CustomStart:  c.milterCustomStart,
		},
		svcAmavis: {
			Name:          svcAmavis,
			DisplayName:   svcAmavis,
			SystemdUnits:  []string{unitMailthreat},
			BinaryPath:    c.p.common + "/sbin/amavisd",
			BinaryArgs:    []string{"-X", "no_conf_file_writable_check", "-c", c.p.conf + "/amavisd.conf"},
			PidFile:       c.p.run + "/amavisd.pid",
			ProcessName:   "amavisd",
			ConfigRewrite: []string{svcAmavis, svcAntispam},
		},
		svcAntivirus: {
			Name:          svcAntivirus,
			DisplayName:   svcAntivirus,
			SystemdUnits:  []string{unitAntivirus},
			BinaryPath:    c.p.common + "/sbin/clamd",
			BinaryArgs:    []string{"--config-file=" + c.p.conf + "/clamd.conf"},
			Detached:      true,
			PidFile:       c.p.run + "/clamd.pid",
			ProcessName:   c.p.common + "/sbin/clamd",
			UseSDNotify:   true,
			ConfigRewrite: []string{svcAntivirus},
			PreStart:      []Hook{c.clamdDirInit},
			Dependencies:  []string{svcFreshclam},
		},
		svcAntispam: {
			Name:         svcAntispam,
			DisplayName:  svcAntispam,
			SystemdUnits: []string{"carbonio-antispam.service"},
			ProcessName:  "amavisd",
			CustomStart:  c.antispamCustomStart,
			CustomStop:   c.antispamCustomStop,
		},
		serviceMTA: {
			Name:          serviceMTA,
			DisplayName:   serviceMTA,
			SystemdUnits:  []string{unitPostfix},
			BinaryPath:    c.p.postfixBin,
			BinaryArgs:    []string{actionStart},
			NeedsRoot:     true,
			PidFile:       c.p.data + "/postfix/spool/pid/master.pid",
			ProcessName:   c.p.common + "/libexec/master",
			CustomStart:   c.mtaCustomStart,
			CustomStop:    c.mtaCustomStop,
			Dependencies:  []string{svcSaslauthd, svcMilter},
			ConfigRewrite: []string{svcAntispam, svcAntivirus, svcOpendkim, serviceMTA, svcSasl},
		},
		svcProxy: {
			Name:         svcProxy,
			DisplayName:  svcProxy,
			SystemdUnits: []string{unitNginx},
			BinaryPath:   c.p.common + "/sbin/nginx",
			BinaryArgs:   []string{"-c", c.p.conf + "/nginx.conf"},
			PidFile:      c.p.run + "/nginx.pid",
			// Full binary path: nginx retitles itself "nginx: master process
			// <path> -c ...", and /usr/bin/carbonio-prometheus-nginx-exporter
			// must not be mistaken for it.
			ProcessName:   c.p.common + "/sbin/nginx",
			UseSDNotify:   true,
			ConfigRewrite: []string{svcProxy},
		},
		svcMailbox: {
			Name:        svcMailbox,
			DisplayName: svcMailbox,
			// Units are listed in start order (DB first, JVM second). startService
			// iterates forward; stopService iterates in reverse so the JVM is
			// terminated before mariadb is shut down. Matches legacy zmstorectl's
			// START_ORDER="mysql.server zmmailboxdctl" /
			// STOP_ORDER="zmmailboxdctl mysql.server". carbonio-appserver.service
			// already Wants=carbonio-appserver-db.service, so starting the JVM
			// unit alone would pull in the DB — but Wants= is a start-time link
			// only; without listing the DB here the stop path would leave
			// mariadb running after `zmcontrol stop`.
			SystemdUnits:  []string{"carbonio-appserver-db.service", unitAppserver},
			ProcessName:   mailboxMainClass,
			ConfigRewrite: []string{svcMailbox},
			CustomStart:   c.mailboxCustomStart,
			CustomStop:    c.mailboxCustomStop,
			PostStart:     []Hook{c.MailboxAdvancedStatusHook},
		},
		svcLdap: {
			Name:         svcLdap,
			DisplayName:  "directory server",
			SystemdUnits: []string{unitOpenldap},
			PidFile:      c.p.run + "/slapd.pid",
			ProcessName:  c.p.common + "/libexec/slapd",
			// slapd is built without libsystemd and never emits sd_notify
			// (READY=1 / STOPPING=1). Readiness is determined by an active LDAP
			// probe in ldapCustomStart instead; leave UseSDNotify false so the
			// stop path does not wait on a STOPPING datagram that never arrives.
			UseSDNotify: false,
			CustomStart: c.ldapCustomStart,
			// Declarative stop: see ldapStopTimeout.
			GracefulStop: ldapStopTimeout,
		},
		svcConfigd: {
			Name:         svcConfigd,
			DisplayName:  "config service",
			SystemdUnits: []string{"carbonio-configd.service"},
			BinaryPath:   c.p.bin + "/configd",
			Detached:     true,
			LogFile:      c.p.log + "/configd.out",
			ProcessName:  c.p.bin + "/configd",
		},
		svcServiceDiscover: {
			Name:                svcServiceDiscover,
			DisplayName:         "service discover",
			SystemdUnits:        []string{"service-discover.service"},
			ProcessName:         serviceDiscoverIdentity,
			ExternallyManaged:   true,
			UseSystemdForStatus: true,
		},
	}
}

// LookupService returns the ServiceDef for a service name, or nil if not found.
// Resolves ServiceAliases (e.g. "clamd" → "antivirus") before the registry lookup,
// so every lifecycle call site (start/stop/restart/status/reload, network protocol,
// watchdog) transparently accepts alias names.
func (c *Controller) LookupService(name string) *ServiceDef {
	if def, ok := c.registry[name]; ok {
		return def
	}

	if canonical, ok := ServiceAliases[name]; ok {
		return c.registry[canonical]
	}

	return nil
}

// AllServiceNames returns all registered service names sorted by start order.
func (c *Controller) AllServiceNames() []string {
	order := getDefaultStartOrder()
	names := make([]string, 0, len(c.registry))

	for name := range c.registry {
		names = append(names, name)
	}

	sortByOrder(names, order)

	return names
}

func sortByOrder(names []string, order map[string]int) {
	sort.Slice(names, func(i, j int) bool {
		oi := orderOf(names[i], order)
		oj := orderOf(names[j], order)

		if oi != oj {
			return oi < oj
		}

		return names[i] < names[j]
	})
}

func orderOf(name string, order map[string]int) int {
	if o, ok := order[name]; ok {
		return o
	}

	return 1000
}

// IsCustomEnabled returns true if the service has an EnableCheck and it passes.
// Used by controlStart to include services not registered in LDAP (e.g. milter).
func (c *Controller) IsCustomEnabled(ctx context.Context, name string) bool {
	def := c.LookupService(name)
	if def == nil || def.EnableCheck == nil {
		return false
	}

	return def.EnableCheck(ctx)
}
