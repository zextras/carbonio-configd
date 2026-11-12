// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/config"
)

const (
	syslogConf        = "/etc/rsyslog.conf"
	syslogDropInDir   = "/etc/rsyslog.d"
	syslogDefaultConf = "/etc/rsyslog.d/50-default.conf"
	syslogCarbonio    = "/etc/rsyslog.d/60-carbonio.conf"
	syslogZimbraOld   = "/etc/rsyslog.d/60-zimbra.conf"
	syslogLogrotate   = "/etc/logrotate.d/carbonio"
	syslogLogFile     = "/var/log/carbonio.log"
	syslogLogTarget   = "-" + syslogLogFile
	syslogUnit        = "/usr/lib/systemd/system/rsyslog.service"
	syslogLogMode     = fs.FileMode(0o640)
	syslogUbuntu      = "ubuntu"
)

// syslog rewrite rules of zmsyslogsetup, applied to a line at a time. The
// patterns are the Perl ones.
var (
	syslogOwnLine    = regexp.MustCompile(`^local[01]\.\*`)
	syslogNoneLine   = regexp.MustCompile(`^\tlocal0,local1.none;\\`)
	syslogAuthLine   = regexp.MustCompile(`^auth\.(\*|info) `)
	syslogNoneTags   = regexp.MustCompile(`;(local0|local1|auth|mail).none`)
	syslogInfoRule   = regexp.MustCompile(`^\*\.info`)
	syslogAllRule    = regexp.MustCompile(`^\*\.\*;auth,authpriv.none`)
	syslogDebugRule  = regexp.MustCompile(`^\*\.=debug;\\`)
	syslogNoticeRule = regexp.MustCompile(`^\*\.=info;\*\.=notice;\*\.=warn;\\`)
)

// syslogTarget is where the facility rules of this node send the logs.
type syslogTarget struct {
	// loghost is zimbraLogHostname ("" when unset).
	loghost string
	// remote is set when loghost is another host: the rules then also
	// forward over UDP.
	remote bool
}

// syslogTargetOf decides between local and remote logging the way
// zmsyslogsetup did: remote only when zimbraLogHostname is set and is not
// this host.
func syslogTargetOf(loghost, hostname string) syslogTarget {
	loghost = strings.TrimSpace(loghost)

	return syslogTarget{loghost: loghost, remote: loghost != "" && !strings.EqualFold(loghost, hostname)}
}

// rules are the lines this node appends: local0 and auth (and, remote, local1)
// and mail go to carbonio.log, and to the log host when remote.
func (t syslogTarget) rules() string {
	var b strings.Builder

	if t.remote {
		b.WriteString("local0.*                @" + t.loghost + "\n")
		b.WriteString("local1.*                @" + t.loghost + "\n")
		b.WriteString("auth.info               @" + t.loghost + "\n")
	}

	b.WriteString("local0.*                " + syslogLogTarget + "\n")
	b.WriteString("auth.info               " + syslogLogTarget + "\n")

	if t.remote {
		b.WriteString("mail.*                @" + t.loghost + "\n")
	}

	b.WriteString("mail.*                " + syslogLogTarget + "\n")

	return b.String()
}

// ownMail reports whether line is a mail rule this tool wrote: it names the
// log host, zimbra (an old install) or carbonio.log. zmsyslogsetup tested only
// the first two, so with the log host on this node its mail rule piled up on
// every run; an unset log host matched every line that starts with "mail".
func (t syslogTarget) ownMail(line string) bool {
	rest, ok := strings.CutPrefix(line, "mail")
	if !ok {
		return false
	}

	return t.loghost == "" || strings.Contains(rest, t.loghost) ||
		strings.Contains(rest, "zimbra") || strings.Contains(rest, syslogLogFile)
}

// syslogStrip drops the rules of an earlier run from the lines of a syslog
// configuration and keeps the facilities of this tool out of the catch-all
// rules (so a message is not logged twice). infoRule is set for the
// "*.info" rule: non-Ubuntu rsyslog.conf only.
// t is nil when no mail rule is dropped (the rsyslog.d file of Ubuntu).
func syslogStrip(content string, t *syslogTarget, infoRule bool) string {
	var b strings.Builder

	for line := range strings.SplitAfterSeq(content, "\n") {
		if line == "" || syslogOwnLine.MatchString(line) || syslogNoneLine.MatchString(line) ||
			syslogAuthLine.MatchString(line) || (t != nil && t.ownMail(line)) {
			continue
		}

		b.WriteString(syslogRewriteLine(line, infoRule))
	}

	return b.String()
}

// syslogRewriteLine adds local0, local1 and mail to the "none" lists of one
// catch-all rule.
func syslogRewriteLine(line string, infoRule bool) string {
	line = syslogNoneTags.ReplaceAllString(line, "")

	if infoRule {
		line = syslogInfoRule.ReplaceAllString(line, "*.info;local0.none;local1.none;mail.none;auth.none")
	}

	line = syslogAllRule.ReplaceAllString(line, "*.*;auth,authpriv.none;local0.none;local1.none;mail.none")
	line = syslogDebugRule.ReplaceAllString(line, "*.=debug;\\\n\tlocal0,local1.none;\\")
	line = syslogNoticeRule.ReplaceAllString(line, "*.=info;*.=notice;*.=warn;\\\n\tlocal0,local1.none;\\")

	return line
}

// syslogRotate fills in the user and group of the carbonio logrotate file
// (USER and GROUP; the first of a line, as the Perl did) and, on Ubuntu's
// rsyslog.d layout, enables its "su" line.
func syslogRotate(content, user, group string, enableSu bool) string {
	var b strings.Builder

	for line := range strings.SplitAfterSeq(content, "\n") {
		line = strings.Replace(line, "USER", user, 1)
		line = strings.Replace(line, "GROUP", group, 1)

		if enableSu {
			line = strings.Replace(line, "#su zextras zextras", "su zextras zextras", 1)
		}

		b.WriteString(line)
	}

	return b.String()
}

// syslogSetup is one run of the step.
type syslogSetup struct {
	e      *Env
	target syslogTarget
	// changed is set once a file changed.
	changed bool
}

// write replaces name atomically with data, unless data is old.
func (s *syslogSetup) write(name string, old, data []byte, mode fs.FileMode) error {
	if bytes.Equal(old, data) {
		return nil
	}

	if err := s.e.Sys.FS.WriteFileAtomic(name, data, mode); err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}

	s.changed = true

	return nil
}

// update rewrites an existing syslog configuration: the previous content is
// kept in name+".bak" (as zmsyslogsetup did), the file loses the rules of an
// earlier run and gets extra appended.
func (s *syslogSetup) update(fn, name string, rewrite func(string) string, extra string) error {
	e := s.e

	old, err := e.Sys.FS.ReadFile(name)
	if err != nil {
		return fmt.Errorf("reading %s: %w", name, err)
	}

	info, err := e.Sys.FS.Stat(name)
	if err != nil {
		return fmt.Errorf("stat %s: %w", name, err)
	}

	next := rewrite(string(old))
	if extra != "" {
		if next != "" && !strings.HasSuffix(next, "\n") {
			next += "\n"
		}

		next += extra
	}

	if next == string(old) {
		e.Log.Info("%s: %s is up to date.", fn, name)

		return nil
	}

	if err := s.write(name+".bak", nil, old, info.Mode().Perm()); err != nil {
		return err
	}

	if err := s.write(name, old, []byte(next), info.Mode().Perm()); err != nil {
		return err
	}

	e.Log.Info("%s: Updating %s...done.", fn, name)

	return nil
}

// updateRsyslogConf is updateSyslog of the script: the rules go to the end of
// /etc/rsyslog.conf.
func (s *syslogSetup) updateRsyslogConf(ubuntu bool) error {
	return s.update("updateSyslog", syslogConf, func(c string) string {
		return syslogStrip(c, &s.target, !ubuntu)
	}, s.target.rules())
}

// updateRsyslogD is updateRsyslogd of the script (Ubuntu): the rules are in
// their own file, 60-carbonio.conf.
func (s *syslogSetup) updateRsyslogD() error {
	if _, err := s.e.Sys.FS.Stat(syslogDefaultConf); err != nil {
		return fmt.Errorf("no default rsyslog configuration: %w", err)
	}

	if err := s.update("updateRsyslogd", syslogDefaultConf, func(c string) string {
		return syslogStrip(c, nil, false)
	}, ""); err != nil {
		return err
	}

	if err := s.e.Sys.FS.Remove(syslogZimbraOld); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", syslogZimbraOld, err)
	}

	cur, err := s.e.Sys.FS.ReadFile(syslogCarbonio)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("reading %s: %w", syslogCarbonio, err)
	}

	return s.write(syslogCarbonio, cur, []byte(s.target.rules()), 0o644)
}

// syslogIDs are the uid and gid of the owner of carbonio.log: syslog:adm on
// Ubuntu's rsyslog.d layout (rsyslog runs as syslog and drops to it), else
// zextras.
func syslogIDs(e *Env, ubuntuD bool) (uid, gid int, err error) {
	if !ubuntuD {
		return zextrasIDs(e)
	}

	out, err := e.Output(&Cmd{Path: "id", Args: []string{"-u", "syslog"}})
	if err != nil {
		return 0, 0, fmt.Errorf("id -u syslog: %w", err)
	}

	if uid, err = strconv.Atoi(strings.TrimSpace(string(out))); err != nil {
		return 0, 0, fmt.Errorf("id -u syslog: %q is not a number", strings.TrimSpace(string(out)))
	}

	out, err = e.Output(&Cmd{Path: "getent", Args: []string{"group", "adm"}})
	if err != nil {
		return 0, 0, fmt.Errorf("getent group adm: %w", err)
	}

	fields := strings.Split(strings.TrimSpace(string(out)), ":")
	if len(fields) < 3 {
		return 0, 0, fmt.Errorf("getent group adm: unexpected output %q", strings.TrimSpace(string(out)))
	}

	if gid, err = strconv.Atoi(fields[2]); err != nil {
		return 0, 0, fmt.Errorf("getent group adm: %q is not a gid", fields[2])
	}

	return uid, gid, nil
}

// prepareLog creates carbonio.log when it is missing and gives it to the
// owner rsyslog writes it as.
func (s *syslogSetup) prepareLog(uid, gid int) error {
	f := s.e.Sys.FS

	if _, err := f.Stat(syslogLogFile); errors.Is(err, fs.ErrNotExist) {
		w, err := f.OpenFile(syslogLogFile, os.O_WRONLY|os.O_CREATE, syslogLogMode)
		if err != nil {
			return fmt.Errorf("creating %s: %w", syslogLogFile, err)
		}

		if err := w.Close(); err != nil {
			return fmt.Errorf("creating %s: %w", syslogLogFile, err)
		}
	}

	if err := f.Chown(syslogLogFile, uid, gid); err != nil {
		return fmt.Errorf("chown %s: %w", syslogLogFile, err)
	}

	return nil
}

// rotate sets the user and group of the logrotate file, when installed.
func (s *syslogSetup) rotate(user, group string, ubuntuD bool) error {
	cur, err := s.e.Sys.FS.ReadFile(syslogLogrotate)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("reading %s: %w", syslogLogrotate, err)
	}

	info, err := s.e.Sys.FS.Stat(syslogLogrotate)
	if err != nil {
		return fmt.Errorf("stat %s: %w", syslogLogrotate, err)
	}

	return s.write(syslogLogrotate, cur, []byte(syslogRotate(string(cur), user, group, ubuntuD)), info.Mode().Perm())
}

// syslogLogHost is zimbraLogHostname of the global config.
func syslogLogHost(e *Env) (string, error) {
	dir, err := svcOpenDir(e)
	if err != nil {
		return "", err
	}

	defer func() { _ = dir.Close() }()

	global, err := dir.Global()
	if err != nil {
		return "", err
	}

	return global["zimbraLogHostname"], nil
}

// syslogHostname is the name of this node: the bootstrap host name, else
// localconfig zimbra_server_hostname, else the host name of the machine.
func syslogHostname(e *Env) string {
	if e.Cfg != nil && e.Cfg.Host.Hostname != "" {
		return e.Cfg.Host.Hostname
	}

	if v, err := e.LCValue("zimbra_server_hostname"); err == nil && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}

	h, _ := os.Hostname()

	return h
}

// syslogFileExists reports whether name exists.
func syslogFileExists(e *Env, name string) bool {
	_, err := e.Sys.FS.Stat(name)

	return err == nil
}

// ConfigureSyslog sets rsyslog up for Carbonio, in process: the whole of what
// libexec/zmsyslogsetup (Perl) did, which is now a wrapper for
// "setup syslog". The step and the command both run it; see StepSetupSyslog
// for the differences. e.Cfg may be nil (the command runs without a bootstrap
// configuration).
func ConfigureSyslog(e *Env) error {
	if !syslogFileExists(e, syslogConf) {
		return errors.New("no syslog configuration to edit: /etc/rsyslog.conf does not exist")
	}

	loghost, err := syslogLogHost(e)
	if err != nil {
		return fmt.Errorf("reading zimbraLogHostname: %w", err)
	}

	s := &syslogSetup{e: e, target: syslogTargetOf(loghost, syslogHostname(e))}
	ubuntu := osReleaseID(e) == syslogUbuntu
	ubuntuD := ubuntu && syslogFileExists(e, syslogDropInDir)

	if ubuntuD {
		err = s.updateRsyslogD()
	} else {
		err = s.updateRsyslogConf(ubuntu)
	}

	if err != nil {
		return err
	}

	uid, gid, err := syslogIDs(e, ubuntuD)
	if err != nil {
		return err
	}

	if err := s.prepareLog(uid, gid); err != nil {
		return err
	}

	user, group := config.ZextrasUser, config.ZextrasUser
	if ubuntuD {
		user, group = "syslog", "adm"
	}

	if err := s.rotate(user, group, ubuntuD); err != nil {
		return err
	}

	return s.restart()
}

// restart restarts rsyslog when a file changed, as the script did (it
// ignored a failure of the restart too).
func (s *syslogSetup) restart() error {
	if !s.changed {
		return nil
	}

	if !syslogFileExists(s.e, syslogUnit) {
		return errors.New("unable to restart rsyslog: " + syslogUnit + " does not exist; do it manually")
	}

	if err := s.e.Root("systemctl", "restart", "rsyslog.service"); err != nil {
		s.e.Log.Warn("restarting rsyslog failed: %v", err)
	}

	return nil
}
