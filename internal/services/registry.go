// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"
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
	// Setup are the declarative steps run before starting the service, in
	// every start path and ahead of the PreStart hooks: directories to
	// create, one-time initializations, helper commands.
	Setup []Step
	// PreStart hooks run before starting the service, after Setup
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
	// The escape hatch for a launch no spec below expresses; no registry
	// entry needs it today. Prefer a spec.
	CustomStart func(ctx context.Context, def *ServiceDef) error
	// CustomStop mirrors CustomStart for the stop side.
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

	// Members makes the service a composite: its lifecycle is the lifecycle of
	// the listed registry services, started in this order (legacy zmstorectl
	// START_ORDER, zmantivirusctl SCRIPTS). A composite has no process of its
	// own: it is running when every applicable member is, and its
	// SystemdUnits are the union of the members' units (for observers only;
	// start/stop go through the members).
	Members []string
	// StopMembersForward stops the members in start order, as the legacy
	// zmantivirusctl/zmantispamctl do, instead of in reverse (zmstorectl:
	// the JVM before the database).
	StopMembersForward bool
	// ForceMembersNoRewrite starts the members with norewrite: the composite
	// regenerates its own ConfigRewrite once up front (legacy zmstorectl and
	// zmantivirusctl start their children `norewrite`).
	ForceMembersNoRewrite bool
	// MemberApplies reports whether a member takes part in this host's
	// lifecycle (zmantivirusctl skips amavisd when it is not installed). Nil
	// means every member applies.
	MemberApplies func(ctx context.Context, member string) bool
	// DetailMember is the member whose process the composite's status detail
	// (pid, since) shows.
	DetailMember string
	// Wants lists registry services that must be running before this one
	// starts. Unlike Dependencies they are never stopped with it, and a
	// failure to start one is logged, not fatal (systemd Wants=): mailbox
	// wants appserver-db, yet stopping the JVM must leave the database up.
	Wants []string
	// Component marks a service that is only a part of a composite (mailbox
	// and appserver-db of store, clamd of antivirus). It stays addressable
	// through the CLI but is omitted from listings and from `control`'s
	// start/stop/status walk, which orchestrate the composite.
	Component bool
	// IdleWhenDisabled makes every lifecycle verb a successful no-op while
	// EnableCheck is false, and status report "nothing to check" (legacy
	// zmantispamctl: the whole wrapper is guarded by the service being enabled
	// for the host).
	IdleWhenDisabled bool
	// Companions are further processes that must be running for the service
	// to count as running in legacy mode (amavisd and amavis-mc: legacy
	// amavisdctl status needs both). The ProcessName process stays the one
	// whose pid is reported.
	Companions []Companion
	// RefreshSignal is the signal `refresh` delivers to the service's main
	// process, signaled directly (as the zextras user) in every mode. Zero
	// means the service has no refresh (only milter: SIGCONT).
	RefreshSignal syscall.Signal
	// ArgsFromConfig computes the arguments of a BinaryPath launch from
	// localconfig, replacing BinaryArgs (slapd's -h bind URL).
	ArgsFromConfig func(lc map[string]string) []string
	// Ready is the probe the launcher runs after starting the service, before
	// the start is reported done (see ReadyKind).
	Ready ReadyKind
	// Java, Mysqld, Commands and Collectors are the launch strategies of
	// services that BinaryPath alone cannot describe (see spec.go). A service
	// declares at most one, and none together with CustomStart. Direct
	// refines the BinaryPath launch and is no strategy of its own.
	Java       *JavaSpec
	Mysqld     *MysqldSpec
	Commands   *CommandSpec
	Collectors *CollectorSpec
	Direct     *DirectSpec
	// Heavy marks a service whose restart is costly (the mailbox JVM, its
	// database, the directory server): on a systemd host a configuration
	// change flags it restart-pending instead of restarting it. internal/plan
	// (HeavyService) lists the same names, which services cannot import back;
	// a test keeps both in step.
	Heavy bool
}

// SignalReport is the outcome of signaling a service's processes: the pids a
// signal was delivered to and those that were already gone.
type SignalReport struct {
	Signaled   []int
	NotRunning []int
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
	svcMailboxd:          svcMailbox,
	svcMysql:             svcAppserverDB,
	svcMariadb:           svcAppserverDB,
	svcService:           svcStore,
	svcZmconfigd:         svcConfigd,
	groupDirectoryServer: svcLdap,
	"directory":          svcLdap,
	"config-service":     svcConfigd,
}

// ldapServiceNames maps zimbraServiceEnabled values whose registry service is
// not the same-named one, and which are NOT CLI aliases: the "mailbox" LDAP
// service is legacy zmstorectl (database + JVM), i.e. the store composite,
// whereas the registry's `mailbox` is the JVM alone.
var ldapServiceNames = map[string]string{
	svcMailbox: svcStore,
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
			// Legacy cbpolicyd start: create the sqlite database once.
			Setup: []Step{
				{UnlessExists: c.p.cbpolicydDB, Mkdir: []string{filepath.Dir(c.p.cbpolicydDB)}},
				{UnlessExists: c.p.cbpolicydDB, Run: &RunStep{Path: c.p.cbpolicydInitBin}},
			},
		},
		svcStats: {
			Name:         svcStats,
			DisplayName:  svcStats,
			SystemdUnits: []string{"carbonio-stats.service"},
			ProcessName:  c.p.statsProc,
			Collectors:   statsCollectors(),
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
			Java: &JavaSpec{
				Label:   "milter",
				LogFile: c.p.log + "/milter.out",
				Args:    c.milterJavaArgs,
			},
			// zmmilterctl refresh: SIGCONT makes MilterServer re-read its config.
			RefreshSignal: syscall.SIGCONT,
		},
		svcAmavis: {
			Name:         svcAmavis,
			DisplayName:  svcAmavis,
			SystemdUnits: []string{unitMailthreat},
			BinaryPath:   c.p.amavisd,
			BinaryArgs:   []string{"-X", "no_conf_file_writable_check", "-c", c.p.conf + "/amavisd.conf"},
			PidFile:      c.p.run + "/amavisd.pid",
			ProcessName:  "amavisd",
			// amavisd is only useful with its amavis-mc companion: legacy
			// amavisdctl status needs both, start launches both.
			Companions:    []Companion{{Identity: amavisMCIdentity, Binary: c.p.amavisMC}},
			ConfigRewrite: []string{svcAmavis, svcAntispam},
			// The pieces of legacy amavisdctl around its `configrewrite amavis
			// antispam`: the data directory and, unless the start is
			// `norewrite`, the altermime configuration (a failing
			// zmaltermimeconfig is ignored, as the wrapper discards its output).
			Setup: []Step{
				{Mkdir: []string{c.p.amavisDir + "/.spamassassin"}},
				{Run: &RunStep{Path: c.p.altermime, IfExists: c.p.altermime, SkipOnNoRewrite: true, BestEffort: true}},
			},
			// Legacy amavisdctl start: amavis-mc, then (unless amavisd runs)
			// quarantine/tmp/var with the stale tmp/amavis-* cleared, then
			// amavisd, each given 10 x 1s to appear.
			Direct: &DirectSpec{
				OptionalBinary: true,
				SkipIfRunning:  true,
				Prepare: []Step{
					{RemoveStale: &StaleDirs{Dir: c.p.amavisDir + "/tmp", Prefix: "amavis-"}},
					{Mkdir: []string{c.p.amavisDir + "/quarantine", c.p.amavisDir + "/tmp", c.p.amavisDir + "/var"}},
				},
				Wait: ProcessWait{Attempts: amavisReadyAttempts, Interval: amavisReadyInterval},
			},
			Component: true,
		},
		svcClamd: {
			Name:          svcClamd,
			DisplayName:   svcClamd,
			SystemdUnits:  []string{unitAntivirus},
			BinaryPath:    c.p.common + "/sbin/clamd",
			BinaryArgs:    []string{"--config-file=" + c.p.conf + "/clamd.conf"},
			Detached:      true,
			PidFile:       c.p.run + "/clamd.pid",
			ProcessName:   c.p.common + "/sbin/clamd",
			UseSDNotify:   true,
			ConfigRewrite: []string{svcAntivirus},
			Setup:         []Step{{Mkdir: []string{c.p.clamdDir}}},
			Component:     true,
		},
		// antivirus is legacy zmantivirusctl: amavisd (+amavis-mc), clamd and
		// freshclam, started in that order and stopped in the same order.
		// `clamd` alone is the separate clamd-only service (zmclamdctl).
		svcAntivirus: {
			Name:                  svcAntivirus,
			DisplayName:           svcAntivirus,
			SystemdUnits:          []string{unitMailthreat, unitAntivirus, "carbonio-freshclam.service"},
			Members:               []string{svcAmavis, svcClamd, svcFreshclam},
			StopMembersForward:    true,
			ForceMembersNoRewrite: true,
			MemberApplies:         c.amavisMemberApplies,
			DetailMember:          svcClamd,
			ConfigRewrite:         []string{svcAntivirus},
		},
		svcAntispamDB: {
			Name:         svcAntispamDB,
			DisplayName:  svcAntispamDB,
			SystemdUnits: []string{"carbonio-antispam.service"},
			PidFile:      c.p.antispamDBPidFile,
			ProcessName:  c.p.common + "/sbin/m*d* --defaults-file=" + c.p.conf + "/antispam-my.cnf",
			Mysqld: &MysqldSpec{
				DefaultsKey:      "antispam_mysql_mycnf",
				ErrlogKey:        "antispam_mysql_errlogfile",
				OptionalDefaults: true,
				Active:           c.antispamDBEnabled,
			},
			Component: true,
		},
		// antispam is legacy zmantispamctl: the amavisd mariadb (when local),
		// then amavisd, all of it only on hosts running the antispam service.
		svcAntispam: {
			Name:               svcAntispam,
			DisplayName:        svcAntispam,
			SystemdUnits:       []string{"carbonio-antispam.service", unitMailthreat},
			Members:            []string{svcAntispamDB, svcAmavis},
			StopMembersForward: true,
			MemberApplies:      c.antispamMemberApplies,
			DetailMember:       svcAmavis,
			EnableCheck:        c.antispamEnabled,
			IdleWhenDisabled:   true,
		},
		serviceMTA: {
			Name:         serviceMTA,
			DisplayName:  serviceMTA,
			SystemdUnits: []string{unitPostfix},
			BinaryPath:   c.p.postfixBin,
			NeedsRoot:    true,
			PidFile:      c.p.data + "/postfix/spool/pid/master.pid",
			ProcessName:  c.p.common + "/libexec/master",
			Commands: &CommandSpec{
				Start:  []string{actionStart},
				Stop:   []string{"stop"},
				Status: []string{"status"},
				Reload: []string{actionReload},
				Prepare: []Step{
					{Hook: c.prepareMainCf},
					{Hook: c.prepareLDAPTables},
					{Run: &RunStep{
						Path: c.p.postaliasBin, Args: []string{c.p.aliases}, Sudo: true,
						IfExists: c.p.aliases, BestEffort: true,
					}},
				},
				ReloadPrepare:              []Step{{Hook: c.prepareLDAPTables}},
				RestartAsReload:            true,
				ReloadRestartsDependencies: true,
			},
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
		// mailbox is the mailboxd JVM alone (legacy zmmailboxdctl). The
		// database is appserver-db and the pair together is the store
		// composite, which stops the JVM first and the database last.
		svcMailbox: {
			Name:          svcMailbox,
			DisplayName:   svcMailbox,
			SystemdUnits:  []string{unitAppserver},
			ProcessName:   mailboxMainClass,
			ConfigRewrite: mailboxConfigRewrite,
			// carbonio-appserver.service has Wants=carbonio-appserver-db.service:
			// starting the JVM brings the database up, stopping it leaves it up.
			Wants: []string{svcAppserverDB},
			Java: &JavaSpec{
				Label:   svcMailbox,
				LogFile: c.p.log + "/zmmailboxd.out",
				Args:    c.mailboxJavaArgs,
				Mkdirs: []string{
					c.p.mailboxd + "/work/service/jsp", c.p.mailboxd + "/work", c.p.appserverState(),
				},
				Touch: []string{c.p.log + "/gc.log"},
				// Flat class path and AOT cache (JDK 25+), as
				// carbonio-appserver.service does; a failed preparation starts
				// the JVM with the previous command line.
				Prepare:      c.prepareMailboxAOT,
				FallbackArgs: c.mailboxJavaArgsFallback,
			},
			// Start returns once the mail port accepts connections (legacy
			// zmmailboxdctl start).
			Ready:     ReadyMailboxPort,
			PostStart: []Hook{c.MailboxAdvancedStatusHook},
			Component: true,
			Heavy:     true,
		},
		svcAppserverDB: {
			Name:         svcAppserverDB,
			DisplayName:  svcMysql,
			SystemdUnits: []string{unitAppserverDB},
			PidFile:      c.p.appserverDBPidFile,
			// mariadbd (or mysqld) of the mailbox defaults file; the antispam
			// database runs the same binary with its own defaults file.
			ProcessName: c.p.common + "/sbin/m*d* --defaults-file=" + c.p.conf + "/my.cnf",
			Mysqld: &MysqldSpec{
				DefaultsKey: "mysql_mycnf",
				ErrlogKey:   "mysql_errlogfile",
			},
			// The InnoDB flush must precede the stop in both orchestration modes.
			PreStop:   []Hook{c.appserverDBFlushHook},
			Component: true,
			Heavy:     true,
		},
		// store is legacy zmstorectl: appserver-db then mailbox; stopped in
		// reverse so the JVM is gone before the database shuts down.
		svcStore: {
			Name:                  svcStore,
			DisplayName:           svcMailbox,
			SystemdUnits:          []string{unitAppserverDB, unitAppserver},
			Members:               []string{svcAppserverDB, svcMailbox},
			ForceMembersNoRewrite: true,
			DetailMember:          svcMailbox,
			ConfigRewrite:         mailboxConfigRewrite,
			Heavy:                 true,
		},
		svcLdap: {
			Name:         svcLdap,
			DisplayName:  "directory server",
			SystemdUnits: []string{unitOpenldap},
			PidFile:      c.p.run + "/slapd.pid",
			ProcessName:  c.p.common + "/libexec/slapd",
			BinaryPath:   c.p.common + "/libexec/slapd",
			Detached:     true,
			LogFile:      c.p.log + "/slapd.out",
			// slapd listens where localconfig says (bind URL plus ldapi).
			ArgsFromConfig: c.slapdArgs,
			// slapd is built without libsystemd and never emits sd_notify
			// (READY=1 / STOPPING=1). Readiness is determined by an active LDAP
			// probe instead; leave UseSDNotify false so the stop path does not
			// wait on a STOPPING datagram that never arrives.
			UseSDNotify: false,
			Ready:       ReadyLDAP,
			// Declarative stop: see ldapStopTimeout.
			GracefulStop: ldapStopTimeout,
			Heavy:        true,
		},
		svcConfigd: {
			Name:         svcConfigd,
			DisplayName:  "config service",
			SystemdUnits: []string{"carbonio-configd.service"},
			BinaryPath:   c.p.bin + "/configd",
			Detached:     true,
			LogFile:      c.p.log + "/configd.out",
			ProcessName:  c.p.bin + "/configd",
			// Start returns once the daemon answers STATUS (legacy
			// zmconfigdctl start): control start's later steps rely on it.
			Ready: ReadyConfigd,
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

// mailboxConfigRewrite is the zmtlsctl configuration set of the mailbox
// (legacy zmstorectl/zmmailboxdctl run `zmtlsctl` unless `norewrite`): the
// files mailboxd reads at boot. Only the names that are zmconfigd.cf sections:
// webxml, service, zextras, zextrasAdmin and zimlet have none and were never
// rewritten.
var mailboxConfigRewrite = []string{svcSasl, svcMailbox}

// amavisMemberApplies skips amavis on hosts without amavisd installed
// (zmantivirusctl lists it among its scripts, which are no-ops there).
func (c *Controller) amavisMemberApplies(_ context.Context, member string) bool {
	return member != svcAmavis || c.binaryInstalled(c.p.amavisd)
}

// antispamMemberApplies runs the antispam database only when it lives on this
// host (antispamDBEnabled), and amavis only when it is installed.
func (c *Controller) antispamMemberApplies(ctx context.Context, member string) bool {
	if member == svcAntispamDB {
		return c.antispamDBEnabled(ctx)
	}

	return c.amavisMemberApplies(ctx, member)
}

// antispamEnabled reports whether the antispam service is enabled for this
// host in LDAP (or its cache). A host whose services cannot be discovered
// counts as not running antispam, as for legacy zmantispamctl.
func (c *Controller) antispamEnabled(ctx context.Context) bool {
	enabled, err := c.DiscoverEnabledServices(ctx)
	if err != nil {
		return false
	}

	return slices.Contains(enabled, svcAntispam)
}

// ControlServiceNames returns the services `control` orchestrates, sorted by
// start order: every registered service except the Components of a composite
// (their composite starts, stops and reports them).
func (c *Controller) ControlServiceNames() []string {
	names := make([]string, 0, len(c.registry))

	for _, name := range c.AllServiceNames() {
		if !c.registry[name].Component {
			names = append(names, name)
		}
	}

	return names
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
