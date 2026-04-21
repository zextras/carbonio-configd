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
}

var (
	milterOptionsPath = confPath + "/mta_milter_options"
	cbpolicydDBPath   = dataPath + "/cbpolicyd/db/cbpolicyd.sqlitedb"
	cbpolicydInitBin  = basePath + "/libexec/zmcbpolicydinit"
)

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
	"memcached": {
		Name:         "memcached",
		DisplayName:  "memcached",
		SystemdUnits: []string{"carbonio-memcached.service"},
		BinaryPath:   commonPath + "/bin/memcached",
		BinaryArgs:   []string{"-d", "-U", "0", "-l", "127.0.1.1,127.0.0.1", "-p", "11211"},
		ProcessName:  "memcached",
	},
	"cbpolicyd": {
		Name:          "cbpolicyd",
		DisplayName:   "cbpolicyd",
		SystemdUnits:  []string{"carbonio-policyd.service"},
		BinaryPath:    commonPath + "/bin/cbpolicyd",
		BinaryArgs:    []string{"--config", confPath + "/cbpolicyd.conf"},
		PidFile:       pidDir + "/cbpolicyd.pid",
		ProcessName:   "cbpolicyd",
		ConfigRewrite: []string{"cbpolicyd"},
		PreStart:      []Hook{cbpolicydInitDB},
	},
	"stats": {
		Name:         "stats",
		DisplayName:  "stats",
		SystemdUnits: []string{"carbonio-stats.service"},
		ProcessName:  "zmstat-",
		CustomStart:  statsCustomStart,
		CustomStop:   statsCustomStop,
	},
	"opendkim": {
		Name:         "opendkim",
		DisplayName:  "opendkim",
		SystemdUnits: []string{"carbonio-opendkim.service"},
		BinaryPath:   commonPath + "/sbin/opendkim",
		BinaryArgs:   []string{"-f", "-x", confPath + "/opendkim.conf", "-u", "zextras"},
		PidFile:      pidDir + "/opendkim.pid",
		ProcessName:  "opendkim",
		UseSDNotify:  true,
	},
	"freshclam": {
		Name:         "freshclam",
		DisplayName:  "freshclam",
		SystemdUnits: []string{"carbonio-freshclam.service"},
		BinaryPath:   commonPath + "/bin/freshclam",
		BinaryArgs: []string{
			"--config-file=" + confPath + "/freshclam.conf",
			"--quiet", "-d", "--checks=12", "--foreground=true",
		},
		Detached:    true,
		UseSDNotify: true,
		PidFile:     pidDir + "/freshclam.pid",
		ProcessName: "freshclam",
	},
	"clamd": {
		Name:         "clamd",
		DisplayName:  "clamd",
		SystemdUnits: []string{"carbonio-antivirus.service"},
		BinaryPath:   commonPath + "/sbin/clamd",
		BinaryArgs:   []string{"--config-file=" + confPath + "/clamd.conf"},
		Detached:     true,
		UseSDNotify:  true,
		PidFile:      pidDir + "/clamd.pid",
		ProcessName:  "clamd",
	},
	"saslauthd": {
		Name:          "saslauthd",
		DisplayName:   "saslauthd",
		SystemdUnits:  []string{"carbonio-saslauthd.service"},
		BinaryPath:    commonPath + "/sbin/saslauthd",
		BinaryArgs:    []string{"-r", "-a", "zimbra"},
		PidFile:       pidDir + "/saslauthd.pid",
		ProcessName:   "saslauthd",
		ConfigRewrite: []string{"sasl"},
	},
	"milter": {
		Name:         "milter",
		DisplayName:  "milter",
		SystemdUnits: []string{"carbonio-milter.service"},
		ProcessName:  "milter.MilterServer",
		EnableCheck:  milterEnabled,
		CustomStart:  milterCustomStart,
	},
	"amavis": {
		Name:          "amavis",
		DisplayName:   "amavis",
		SystemdUnits:  []string{"carbonio-mailthreat.service"},
		BinaryPath:    commonPath + "/sbin/amavisd",
		BinaryArgs:    []string{"-X", "no_conf_file_writable_check", "-c", confPath + "/amavisd.conf"},
		PidFile:       pidDir + "/amavisd.pid",
		ProcessName:   "amavisd",
		ConfigRewrite: []string{"amavis", "antispam"},
	},
	"antivirus": {
		Name:         "antivirus",
		DisplayName:  "antivirus",
		SystemdUnits: []string{"carbonio-antivirus.service"},
		ProcessName:  "clamd",
		Dependencies: []string{"clamd", "freshclam"},
	},
	"antispam": {
		Name:         "antispam",
		DisplayName:  "antispam",
		SystemdUnits: []string{"carbonio-antispam.service"},
		ProcessName:  "amavisd",
		CustomStart:  antispamCustomStart,
		CustomStop:   antispamCustomStop,
	},
	"mta": {
		Name:          "mta",
		DisplayName:   "mta",
		SystemdUnits:  []string{"carbonio-postfix.service"},
		BinaryPath:    postfixBin,
		BinaryArgs:    []string{"start"},
		NeedsRoot:     true,
		PidFile:       dataPath + "/postfix/spool/pid/master.pid",
		ProcessName:   "common/libexec/master",
		CustomStart:   mtaCustomStart,
		CustomStop:    mtaCustomStop,
		Dependencies:  []string{"saslauthd", "milter"},
		ConfigRewrite: []string{"antispam", "antivirus", "opendkim", "mta", "sasl"},
	},
	"proxy": {
		Name:          "proxy",
		DisplayName:   "proxy",
		SystemdUnits:  []string{"carbonio-nginx.service"},
		BinaryPath:    commonPath + "/sbin/nginx",
		BinaryArgs:    []string{"-c", confPath + "/nginx.conf"},
		PidFile:       pidDir + "/nginx.pid",
		ProcessName:   "nginx",
		UseSDNotify:   true,
		ConfigRewrite: []string{"proxy"},
	},
	"mailbox": {
		Name:          "mailbox",
		DisplayName:   "mailbox",
		SystemdUnits:  []string{"carbonio-appserver.service"},
		ProcessName:   "com.zextras.mailbox.Mailbox",
		ConfigRewrite: []string{"mailbox"},
		CustomStart:   mailboxCustomStart,
		CustomStop:    mailboxCustomStop,
		PostStart:     []Hook{MailboxAdvancedStatusHook},
	},
	"ldap": {
		Name:         "ldap",
		DisplayName:  "directory server",
		SystemdUnits: []string{"carbonio-openldap.service"},
		PidFile:      pidDir + "/slapd.pid",
		ProcessName:  "slapd",
		UseSDNotify:  true,
		CustomStart:  ldapCustomStart,
		CustomStop:   ldapCustomStop,
	},
	"configd": {
		Name:         "configd",
		DisplayName:  "config service",
		SystemdUnits: []string{"carbonio-configd.service"},
		BinaryPath:   binPath + "/configd",
		Detached:     true,
		LogFile:      logPath + "/configd.out",
		ProcessName:  binPath + "/configd",
	},
	"service-discover": {
		Name:         "service-discover",
		DisplayName:  "service discover",
		SystemdUnits: []string{"service-discover.service"},
		BinaryPath:   "/usr/bin/service-discovered",
		Detached:     true,
		ProcessName:  "service-discovered",
		CustomStart:  serviceDiscoverCustomStart,
	},
}

// LookupService returns the ServiceDef for a service name, or nil if not found.
func LookupService(name string) *ServiceDef {
	return Registry[name]
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
