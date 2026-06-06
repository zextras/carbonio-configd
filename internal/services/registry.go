// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
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
	// When set, status detection reads the PID from this file and checks /proc/<pid>.
	// This is the preferred method — faster and more reliable than /proc cmdline scan.
	PidFile string
	// ProcessName is the fallback for status detection when PidFile is not set.
	// Used for /proc cmdline/comm scanning.
	ProcessName string
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
}

var (
	milterOptionsPath = confPath + "/mta_milter_options"
	cbpolicydDBPath   = dataPath + "/cbpolicyd/db/cbpolicyd.sqlitedb"
	cbpolicydInitBin  = basePath + "/libexec/zmcbpolicydinit"
	clamdDirPath      = dataPath + "/clamav/db"
)

// ServiceAliases maps alternative service names to their canonical Registry key.
// These names are accepted by LookupService but do NOT appear in Registry or
// AllServiceNames — they are resolution aliases only.
var ServiceAliases = map[string]string{
	svcClamd:             svcAntivirus,
	svcMailboxd:          svcMailbox,
	svcService:           svcMailbox,
	groupDirectoryServer: svcLdap,
	"directory":          svcLdap,
	"config-service":     svcConfigd,
}

// serviceDiscoverCustomStart starts service-discovered in the correct role:
// "server" on LDAP nodes, "agent" on non-LDAP nodes. Mirrors the two separate
// systemd units (build/server/ vs build/agent/) from the service-discover repo.
func serviceDiscoverCustomStart(ctx context.Context, def *ServiceDef) error {
	role := "agent"
	if IsLDAPLocal() {
		role = "server"
	}

	logger.InfoContext(ctx, "Starting service-discover", "role", role)

	// service-discovered (consul) manages its own logging internally.
	// Redirect stdout/stderr to /dev/null — no configd-managed log file needed.
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("failed to open /dev/null: %w", err)
	}

	defer func() { _ = devNull.Close() }()

	cmd := exec.CommandContext(ctx, def.BinaryPath, role) //nolint:gosec // fixed internal path
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	cmd.SysProcAttr = detachedSysProcAttr()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start service-discover (%s): %w", role, err)
	}

	if err := cmd.Process.Release(); err != nil {
		logger.WarnContext(ctx, "Failed to release service-discover handle", "error", err)
	}

	return nil
}

// cbpolicydInitDB initializes the cbpolicyd sqlite database if it doesn't exist.
// Mirrors legacy cbpolicydctl.sh pre-start logic.
func cbpolicydInitDB(_ context.Context, _ *ServiceManager) error {
	if _, err := os.Stat(cbpolicydDBPath); err == nil {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(cbpolicydDBPath), 0o755); err != nil {
		return fmt.Errorf("failed to create cbpolicyd DB directory: %w", err)
	}

	if _, err := os.Stat(cbpolicydInitBin); err != nil {
		return fmt.Errorf("cbpolicyd DB missing and init binary not found: %w", err)
	}

	cmd := exec.CommandContext(context.Background(), cbpolicydInitBin) //nolint:gosec // fixed internal path
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cbpolicyd DB init failed: %s: %w", strings.TrimSpace(string(output)), err)
	}

	return nil
}

// clamdDirInit creates the clamav database directory if missing. Mirrors the
// `mkdir -p /opt/zextras/data/clamav/db` from legacy clamdctl.sh; clamd refuses
// to start if the directory does not exist.
func clamdDirInit(_ context.Context, _ *ServiceManager) error {
	if err := os.MkdirAll(clamdDirPath, 0o755); err != nil {
		return fmt.Errorf("failed to create clamav DB directory: %w", err)
	}

	return nil
}

// milterEnabled checks if the milter service is enabled via mta_milter_options file.
func milterEnabled(_ context.Context) bool {
	data, err := os.ReadFile(milterOptionsPath)
	if err != nil {
		return false
	}

	return strings.Contains(string(data), "zimbraMilterServerEnabled=TRUE")
}

// Registry maps service names to their definitions.
var Registry = map[string]*ServiceDef{
	svcMemcached: {
		Name:         svcMemcached,
		DisplayName:  svcMemcached,
		SystemdUnits: []string{"carbonio-memcached.service"},
		BinaryPath:   commonPath + "/bin/memcached",
		BinaryArgs:   []string{"-d", "-U", "0", "-l", "127.0.1.1,127.0.0.1", "-p", "11211"},
		ProcessName:  svcMemcached,
	},
	svcCbpolicyd: {
		Name:          svcCbpolicyd,
		DisplayName:   svcCbpolicyd,
		SystemdUnits:  []string{unitPolicyd},
		BinaryPath:    commonPath + "/bin/cbpolicyd",
		BinaryArgs:    []string{"--config", confPath + "/cbpolicyd.conf"},
		PidFile:       pidDir + "/cbpolicyd.pid",
		ProcessName:   svcCbpolicyd,
		ConfigRewrite: []string{svcCbpolicyd},
		PreStart:      []Hook{cbpolicydInitDB},
	},
	svcStats: {
		Name:         svcStats,
		DisplayName:  svcStats,
		SystemdUnits: []string{"carbonio-stats.service"},
		ProcessName:  "zmstat-",
		CustomStart:  statsCustomStart,
		CustomStop:   statsCustomStop,
	},
	svcOpendkim: {
		Name:         svcOpendkim,
		DisplayName:  svcOpendkim,
		SystemdUnits: []string{unitOpendkim},
		BinaryPath:   commonPath + "/sbin/opendkim",
		BinaryArgs:   []string{"-f", "-x", confPath + "/opendkim.conf", "-u", userZimbra},
		PidFile:      pidDir + "/opendkim.pid",
		ProcessName:  svcOpendkim,
		UseSDNotify:  true,
	},
	svcFreshclam: {
		Name:         svcFreshclam,
		DisplayName:  svcFreshclam,
		SystemdUnits: []string{"carbonio-freshclam.service"},
		BinaryPath:   commonPath + "/bin/freshclam",
		BinaryArgs: []string{
			"--config-file=" + confPath + "/freshclam.conf",
			"--quiet", "-d", "--checks=12", "--foreground=true",
		},
		Detached:    true,
		UseSDNotify: true,
		PidFile:     pidDir + "/freshclam.pid",
		ProcessName: svcFreshclam,
	},
	svcSaslauthd: {
		Name:          svcSaslauthd,
		DisplayName:   svcSaslauthd,
		SystemdUnits:  []string{"carbonio-saslauthd.service"},
		BinaryPath:    commonPath + "/sbin/saslauthd",
		BinaryArgs:    []string{"-r", "-a", userZimbra},
		PidFile:       pidDir + "/saslauthd.pid",
		ProcessName:   svcSaslauthd,
		ConfigRewrite: []string{svcSasl},
	},
	svcMilter: {
		Name:         svcMilter,
		DisplayName:  svcMilter,
		SystemdUnits: []string{"carbonio-milter.service"},
		ProcessName:  "milter.MilterServer",
		EnableCheck:  milterEnabled,
		CustomStart:  milterCustomStart,
	},
	svcAmavis: {
		Name:          svcAmavis,
		DisplayName:   svcAmavis,
		SystemdUnits:  []string{unitMailthreat},
		BinaryPath:    commonPath + "/sbin/amavisd",
		BinaryArgs:    []string{"-X", "no_conf_file_writable_check", "-c", confPath + "/amavisd.conf"},
		PidFile:       pidDir + "/amavisd.pid",
		ProcessName:   "amavisd",
		ConfigRewrite: []string{svcAmavis, svcAntispam},
	},
	svcAntivirus: {
		Name:          svcAntivirus,
		DisplayName:   svcAntivirus,
		SystemdUnits:  []string{unitAntivirus},
		BinaryPath:    commonPath + "/sbin/clamd",
		BinaryArgs:    []string{"--config-file=" + confPath + "/clamd.conf"},
		Detached:      true,
		PidFile:       pidDir + "/clamd.pid",
		ProcessName:   "clamd",
		UseSDNotify:   true,
		ConfigRewrite: []string{svcAntivirus},
		PreStart:      []Hook{clamdDirInit},
		Dependencies:  []string{svcFreshclam},
	},
	svcAntispam: {
		Name:         svcAntispam,
		DisplayName:  svcAntispam,
		SystemdUnits: []string{"carbonio-antispam.service"},
		ProcessName:  "amavisd",
		CustomStart:  antispamCustomStart,
		CustomStop:   antispamCustomStop,
	},
	serviceMTA: {
		Name:          serviceMTA,
		DisplayName:   serviceMTA,
		SystemdUnits:  []string{unitPostfix},
		BinaryPath:    postfixBin,
		BinaryArgs:    []string{actionStart},
		NeedsRoot:     true,
		PidFile:       dataPath + "/postfix/spool/pid/master.pid",
		ProcessName:   "common/libexec/master",
		CustomStart:   mtaCustomStart,
		CustomStop:    mtaCustomStop,
		Dependencies:  []string{svcSaslauthd, svcMilter},
		ConfigRewrite: []string{svcAntispam, svcAntivirus, svcOpendkim, serviceMTA, svcSasl},
	},
	svcProxy: {
		Name:         svcProxy,
		DisplayName:  svcProxy,
		SystemdUnits: []string{unitNginx},
		BinaryPath:   commonPath + "/sbin/nginx",
		BinaryArgs:   []string{"-c", confPath + "/nginx.conf"},
		PidFile:      pidDir + "/nginx.pid",
		// Path fragment, not the bare token "nginx": avoids matching
		// /usr/bin/carbonio-prometheus-nginx-exporter, whose argv
		// contains "nginx" multiple times.
		ProcessName:   "/sbin/nginx",
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
		ProcessName:   "com.zextras.mailbox.Mailbox",
		ConfigRewrite: []string{svcMailbox},
		CustomStart:   mailboxCustomStart,
		CustomStop:    mailboxCustomStop,
		PostStart:     []Hook{MailboxAdvancedStatusHook},
	},
	svcLdap: {
		Name:         svcLdap,
		DisplayName:  "directory server",
		SystemdUnits: []string{unitOpenldap},
		PidFile:      pidDir + "/slapd.pid",
		ProcessName:  "slapd",
		// slapd is built without libsystemd and never emits sd_notify
		// (READY=1 / STOPPING=1). Readiness is determined by an active LDAP
		// probe in ldapCustomStart instead; leave UseSDNotify false so the
		// stop path does not wait on a STOPPING datagram that never arrives.
		UseSDNotify: false,
		CustomStart: ldapCustomStart,
		CustomStop:  ldapCustomStop,
	},
	svcConfigd: {
		Name:         svcConfigd,
		DisplayName:  "config service",
		SystemdUnits: []string{"carbonio-configd.service"},
		BinaryPath:   binPath + "/configd",
		Detached:     true,
		LogFile:      logPath + "/configd.out",
		ProcessName:  binPath + "/configd",
	},
	svcServiceDiscover: {
		Name:                svcServiceDiscover,
		DisplayName:         "service discover",
		SystemdUnits:        []string{"service-discover.service"},
		BinaryPath:          "/usr/bin/service-discover",
		Detached:            true,
		ProcessName:         svcServiceDiscover,
		CustomStart:         serviceDiscoverCustomStart,
		UseSystemdForStatus: true,
	},
}

// LookupService returns the ServiceDef for a service name, or nil if not found.
// Resolves ServiceAliases (e.g. "clamd" → "antivirus") before the registry lookup,
// so every lifecycle call site (start/stop/restart/status/reload, network protocol,
// watchdog) transparently accepts alias names.
func LookupService(name string) *ServiceDef {
	if def, ok := Registry[name]; ok {
		return def
	}

	if canonical, ok := ServiceAliases[name]; ok {
		return Registry[canonical]
	}

	return nil
}

// AllServiceNames returns all registered service names sorted by start order.
func AllServiceNames() []string {
	order := getDefaultStartOrder()
	names := make([]string, 0, len(Registry))

	for name := range Registry {
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
func IsCustomEnabled(ctx context.Context, name string) bool {
	def := LookupService(name)
	if def == nil || def.EnableCheck == nil {
		return false
	}

	return def.EnableCheck(ctx)
}
