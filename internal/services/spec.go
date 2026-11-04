// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
)

// The launch specs below are the data half of the declarative service
// registry: a ServiceDef says WHAT a service is (units, identity, how it is
// started, stopped and proven ready); the generic executors in launch*.go and
// collectors.go do the work. Genuinely service-specific code stays behind
// named hooks (ServiceDef.PreStart/PostStart/PreStop, CommandSpec.Prepare,
// ServiceDef.CustomStart/CustomStop, the Args builders), each a small function
// with one job.

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
	// Prepare are the named steps run, in order, before Start when Status says
	// the service is not running already (main.cf bootstrap, table rendering).
	Prepare []func(ctx context.Context) error
	// RestartAsReload turns `restart` into a configuration rewrite followed by
	// a reload: the service is never stopped for a restart (postfix).
	RestartAsReload bool
	// ReloadRestartsDependencies makes a reload restart the enabled
	// Dependencies first (legacy zmmtactl reload runs reload on each of its
	// START_SCRIPTS, and milter/saslauthd reload is stop+start).
	ReloadRestartsDependencies bool
}

// CollectorSpec supervises a set of helper processes that each write a pidfile
// (the zmstat-* collectors of stats): started one by one from the libexec
// directory with a <name>.out log, stopped through the pidfiles of the
// controller's stats pid directory — killing each process group, with sudo for
// root-owned collectors — then by identity (ServiceDef.ProcessName) for
// survivors, and rotated by sending SIGHUP (they reopen their logs) to every
// pidfile's process.
type CollectorSpec struct {
	// Always are the collectors every host runs, as command lines ("zmstat-io
	// -x"): the executable and its arguments.
	Always []string
	// Optional are the collectors enabled by a localconfig key set to TRUE.
	Optional []OptionalCollector
}

// OptionalCollector is a collector run only when localconfig key LCKey is TRUE.
type OptionalCollector struct {
	Cmd   string
	LCKey string
}
