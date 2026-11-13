// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"time"
)

// The launch specs below are the data half of the declarative service
// registry: a ServiceDef says WHAT a service is (units, identity, how it is
// started, stopped and proven ready); the generic executors in launch*.go and
// collectors.go do the work. Genuinely service-specific code stays behind
// named hooks (ServiceDef.PreStart/PostStart/PreStop, a Step's Hook,
// ServiceDef.CustomStart/CustomStop, the Args builders), each a small function
// with one job; docs/service-registry.md lists every one that remains.

// ReadyKind names the probe that proves a freshly launched service is serving,
// run by the generic launcher before the start returns (ServiceDef.Ready).
type ReadyKind string

const (
	// ReadyNone starts and returns: a service that forks away (or whose
	// readiness is signalled with sd_notify, ServiceDef.UseSDNotify) needs no
	// further wait.
	ReadyNone ReadyKind = ""
	// ReadyLDAP polls the bind URL until an anonymous rootDSE search answers,
	// failing fast when the child exits first. slapd is built without
	// libsystemd, so it never sends READY=1.
	ReadyLDAP ReadyKind = "ldap"
	// ReadyConfigd polls the daemon's STATUS until it answers SUCCESS ACTIVE
	// (legacy zmconfigdctl start).
	ReadyConfigd ReadyKind = "configd"
	// ReadyMailboxPort waits until zimbraMailPort accepts connections (the
	// tail of legacy zmmailboxdctl start).
	ReadyMailboxPort ReadyKind = "mailbox-port"
)

// JavaSpec launches a JVM service (mailbox, milter): the java binary of
// localconfig, the Args the service needs, a detached session logging to
// LogFile.
type JavaSpec struct {
	// Label names the service in log lines and errors.
	Label string
	// LogFile receives the JVM's stdout and stderr.
	LogFile string
	// Args builds the complete java argument list from localconfig.
	Args func(lc map[string]string) []string
	// Mkdirs are directories created (best effort, 0755) before the launch.
	Mkdirs []string
	// Touch are files pre-created (0640) when missing, so the JVM, which only
	// needs write access, finds them with the right owner.
	Touch []string
}

// MysqldSpec launches a MariaDB through mysqld_safe and stops it safely: the
// shutdown is a SIGTERM and a bounded wait, never a SIGKILL (a mariadbd killed
// mid-flush needs InnoDB crash recovery). The pidfile is ServiceDef.PidFile.
type MysqldSpec struct {
	// DefaultsKey and ErrlogKey are the localconfig keys of the server's
	// --defaults-file and --log-error.
	DefaultsKey string
	ErrlogKey   string
	// OptionalDefaults makes an unset DefaultsKey a quiet skip instead of an
	// error: hosts where the database is not configured.
	OptionalDefaults bool
	// Active, when set, gates the start and the stop: the database runs on
	// this host only while it reports true (the antispam database lives on
	// another node unless configured local). Nil means always.
	Active func(ctx context.Context) bool
}

// CommandSpec controls a service through one binary (ServiceDef.BinaryPath)
// and its verbs, as postfix does: `postfix start|stop|status|reload`. With
// ServiceDef.NeedsRoot the binary runs through sudo. Self-daemonizing: Start
// returns once the master has forked.
type CommandSpec struct {
	// Start, Stop, Status and Reload are the arguments of each verb; nil means
	// the service has no such verb. Status exits 0 when the service is running.
	Start, Stop, Status, Reload []string
	// Prepare are the steps run, in order, before Start when Status says the
	// service is not running already (main.cf bootstrap, table rendering,
	// aliases rebuild).
	Prepare []Step
	// ReloadPrepare are the steps run, in order, before every Reload verb
	// (the MTA's ldap-*.cf rendering, which legacy postfix reload redid).
	ReloadPrepare []Step
	// RestartAsReload turns `restart` into a configuration rewrite followed by
	// a reload: the service is never stopped for a restart (postfix).
	RestartAsReload bool
	// ReloadRestartsDependencies makes a reload restart the enabled
	// Dependencies first (legacy zmmtactl reload runs reload on each of its
	// START_SCRIPTS, and milter/saslauthd reload is stop+start).
	ReloadRestartsDependencies bool
}

// CollectorSpec supervises the collectors of stats ("setup stat run
// <collector>..."): all of them run in one process, started from the libexec
// directory with a LogName log, which keeps a pidfile per collector in the
// controller's stats pid directory (all naming that process). Stop kills the
// process group of each pidfile's process, with sudo for a root-owned one,
// then by identity (ServiceDef.ProcessName) for survivors; rotate sends SIGHUP
// (the collectors reopen their logs) to the process of the pidfiles, once.
type CollectorSpec struct {
	// Binary is the executable under libexec that runs the collectors, Args
	// the arguments before them ("stat", "run"), and LogName the file of the
	// log directory the process writes to.
	Binary  string
	Args    []string
	LogName string
	// Always are the collectors every host runs, as the name and its options
	// ("io -x"); each is one argument of the process.
	Always []string
	// Optional are the collectors enabled by a localconfig key set to TRUE.
	Optional []OptionalCollector
}

// OptionalCollector is a collector run only when localconfig key LCKey is TRUE.
type OptionalCollector struct {
	// Cmd is the collector and its options, as in CollectorSpec.Always.
	Cmd   string
	LCKey string
}

// Step is one action of an ordered launch sequence (ServiceDef.Setup,
// CommandSpec.Prepare, DirectSpec.Prepare). Exactly one of Mkdir, RemoveStale,
// Run and Hook is set. A failing step stops the sequence, except a best-effort
// Run, which is only logged.
type Step struct {
	// Mkdir creates the directories (and their parents, 0755).
	Mkdir []string
	// RemoveStale clears leftovers of a previous run.
	RemoveStale *StaleDirs
	// Run runs one command.
	Run *RunStep
	// Hook is the escape hatch for a step no field expresses; each one is
	// listed, with its reason, in docs/service-registry.md.
	Hook func(ctx context.Context) error
	// UnlessExists skips the step when the path exists: a one-time
	// initialization (the cbpolicyd database).
	UnlessExists string
}

// StaleDirs names the directories directly under Dir whose name starts with
// Prefix; they are removed with their content, a failure being logged only.
type StaleDirs struct {
	Dir    string
	Prefix string
}

// RunStep is a command run by a Step: Path with Args, through `sudo -n` when
// Sudo.
type RunStep struct {
	Path string
	Args []string
	Sudo bool
	// IfExists skips the command, quietly, when this path is absent: an
	// optional helper script, or the input file of a rebuild.
	IfExists string
	// SkipOnNoRewrite skips the command for a start that must not regenerate
	// configuration (`norewrite`).
	SkipOnNoRewrite bool
	// BestEffort logs a failure, with what the command printed, instead of
	// failing the sequence.
	BestEffort bool
}

// Companion is a helper daemon a service cannot do without: legacy
// amavisdctl status needs amavis-mc next to amavisd and its start launches
// both. Identity is its process identity (see ServiceDef.ProcessName); Binary,
// when set, is started through `sudo -n` before the main process unless a
// process of that identity runs already (DirectSpec.Wait then bounds the wait
// for it to appear). A stop terminates the main process, then each companion.
type Companion struct {
	Identity string
	Binary   string
}

// ProcessWait bounds how long a launch waits for a process to show up in the
// process table: Attempts probes, Interval apart. The zero value does not wait.
type ProcessWait struct {
	Attempts int
	Interval time.Duration
}

// DirectSpec refines a BinaryPath launch (ServiceDef.Direct) with what a
// daemon needs around its spawn; the sequence is the one of legacy amavisdctl.
type DirectSpec struct {
	// OptionalBinary makes an absent BinaryPath a quiet no-op: the legacy
	// wrapper exits 0 on hosts without the daemon installed.
	OptionalBinary bool
	// SkipIfRunning leaves the service alone when a process of ProcessName
	// runs already. Companions are still brought up first.
	SkipIfRunning bool
	// Prepare are the steps run after the companions and before the spawn.
	Prepare []Step
	// Wait bounds the wait for the companions and, after the spawn, for
	// ProcessName to appear.
	Wait ProcessWait
}
