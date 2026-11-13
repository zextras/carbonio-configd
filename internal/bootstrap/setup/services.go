// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bufio"
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/provision"
	"github.com/zextras/carbonio-configd/internal/zxadmin"
)

// Stage names of setup.pl (the values written to .install_history).
const (
	svcStageInitMta     = "configInitMta"
	svcStageCreateDom   = "configCreateDomain"
	svcStageInitSQL     = "configInitSql"
	svcStageSyslog      = "setupSyslog"
	svcStageGALSync     = "configCreateDefaultDomainGALSyncAcct"
	svcStageCrontab     = "setupCrontab"
	svcDefaultMySQLPct  = 30
	svcDefaultLDAPPort  = 389
	svcPostfixOwner     = "postfix"
	svcPostfixGroup     = "postdrop"
	svcCrontabStart     = "# ZEXTRAS-START"
	svcCrontabEnd       = "# ZEXTRAS-END"
	svcCrontabEndLine   = svcCrontabEnd + " -- DO NOT EDIT ANYTHING BETWEEN THIS LINE AND ZEXTRAS-START"
	svcConfigdUnit      = "carbonio-configd.service"
	svcConfigdPattern   = "^/opt/zextras/bin/configd$"
	svcStrayWaitSeconds = 10
	svcLocalConfigHost  = "zimbra_server_hostname"
	svcTrueValue        = "TRUE"
	svcPkgDirectory     = "carbonio-directory-server"
	svcAttrDescription  = "description"
	svcCrontabBin       = "crontab"
	svcGALDataSource    = "InternalGAL"
)

// svcSystemdTargets are the role targets of Zextras::Util::Systemd, in the
// order it starts and stops them.
var svcSystemdTargets = []string{
	"carbonio-directory-server.target", "carbonio-appserver.target",
	"carbonio-proxy.target", "carbonio-mta.target",
}

// svcPortOwners is the port to package table of setup.pl checkPortConflicts.
var svcPortOwners = map[int]string{
	25: PkgMTA, 80: PkgAppserver, 110: PkgAppserver, 143: PkgAppserver,
	389: svcPkgDirectory, 443: PkgAppserver, 636: svcPkgDirectory,
	993: PkgAppserver, 995: PkgAppserver, 7025: PkgAppserver, 7071: PkgAppserver, 7072: PkgAppserver,
	7306: PkgAppserver, 7307: PkgAppserver, 8465: PkgMTA,
	10024: PkgMTA, 10025: PkgMTA, 10026: PkgMTA, 10027: PkgMTA, 10028: PkgMTA, 10029: PkgMTA, 10030: PkgMTA,
}

// svcDir reads the directory the way the service steps need to (an
// interface so tests do not need an LDAP server).
type svcDir interface {
	// Exists reports whether an entry matches the LDAP filter anywhere.
	Exists(filter string) (bool, error)
	// Global returns the global config attributes.
	Global() (map[string]string, error)
	// MTAServers is the number of servers with the mta service enabled.
	MTAServers() (int, error)
	// ServerAttribute is attr of every server, by server name; "" for a
	// server that does not set it.
	ServerAttribute(attr string) (map[string]string, error)
	// SetServerAttribute replaces attr on the entry of server host.
	SetServerAttribute(host, attr, value string) error
	Close() error
}

// svcOpenDir opens the directory of the node (replaced by tests).
var svcOpenDir = func(e *Env) (svcDir, error) {
	c, err := e.LDAP()
	if err != nil {
		return nil, err
	}

	return svcLDAP{c: c}, nil
}

// svcLDAPClient is the part of the native LDAP client svcLDAP uses.
type svcLDAPClient interface {
	Search(baseDN, filter string, attributes []string, scope int) (*ldap.SearchResult, error)
	GetGlobalConfig() (map[string]string, error)
	GetAllServersWithAttributes() (map[string]map[string]string, error)
	ModifyAttribute(dn, attribute, value string) error
	BaseDN() string
	Close() error
}

type svcLDAP struct{ c svcLDAPClient }

func (d svcLDAP) Exists(filter string) (bool, error) {
	res, err := d.c.Search("", filter, []string{"zimbraId"}, ldap.ScopeWholeSubtree)
	if err != nil {
		return false, err
	}

	return len(res.Entries) > 0, nil
}

func (d svcLDAP) Global() (map[string]string, error) { return d.c.GetGlobalConfig() }

func (d svcLDAP) MTAServers() (int, error) {
	all, err := d.c.GetAllServersWithAttributes()
	if err != nil {
		return 0, err
	}

	n := 0

	for _, a := range all {
		if slices.Contains(splitValues(a["zimbraServiceEnabled"]), "mta") {
			n++
		}
	}

	return n, nil
}

func (d svcLDAP) ServerAttribute(attr string) (map[string]string, error) {
	all, err := d.c.GetAllServersWithAttributes()
	if err != nil {
		return nil, err
	}

	out := make(map[string]string, len(all))

	for host, attrs := range all {
		out[host] = ""

		for name, v := range attrs {
			if strings.EqualFold(name, attr) {
				out[host] = v
			}
		}
	}

	return out, nil
}

func (d svcLDAP) SetServerAttribute(host, attr, value string) error {
	return d.c.ModifyAttribute("cn="+ldap.EscapeDN(host)+",cn=servers,"+d.c.BaseDN(), attr, value)
}

func (d svcLDAP) Close() error { return d.c.Close() }

// StepCheckPortConflicts ports setup.pl checkPortConflicts (L314-360), the
// first thing main does after the defaults: it reports the listening TCP
// ports that a package enabled on this node needs. Deviations: the ports come
// from /proc/net/tcp{,6} instead of netstat | egrep | awk | sed, and there is
// no "press Enter" prompt (bootstrap is never interactive); the step only
// warns.
func StepCheckPortConflicts() Step {
	return Step{
		Title:    "Check port conflicts",
		Optional: true,
		Apply: func(e *Env) error {
			ports, err := svcListeningPorts(e)
			if err != nil {
				return err
			}

			for _, port := range ports {
				pkg, ok := svcPortOwners[port]
				if !ok || !e.Facts.IsEnabled(pkg) {
					continue
				}

				if pkg == svcPkgDirectory && !e.Facts.NewInstall {
					continue
				}

				e.Log.Info("Port conflict detected: %d (%s)", port, pkg)
			}

			return nil
		},
	}
}

// svcListeningPorts returns the sorted TCP ports in LISTEN state.
func svcListeningPorts(e *Env) ([]int, error) {
	seen := map[int]bool{}

	for _, file := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := e.Sys.FS.ReadFile(file)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return nil, err
		}

		svcParseListening(data, seen)
	}

	ports := make([]int, 0, len(seen))
	for p := range seen {
		ports = append(ports, p)
	}

	sort.Ints(ports)

	return ports, nil
}

// svcParseListening records in seen the ports of the LISTEN entries of a
// /proc/net/tcp table.
func svcParseListening(data []byte, seen map[int]bool) {
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		// sl local_address rem_address st ...; st 0A is LISTEN.
		if len(fields) < 4 || fields[3] != "0A" {
			continue
		}

		_, hexPort, ok := strings.Cut(fields[1], ":")
		if !ok {
			continue
		}

		if port, err := strconv.ParseInt(hexPort, 16, 32); err == nil {
			seen[int(port)] = true
		}
	}
}

// ErrCoreNotEnabled ends the run when carbonio-core is not enabled.
var ErrCoreNotEnabled = errors.New("carbonio-core must be enabled")

// StepCheckCoreEnabled ports setup.pl main L171-174: exit 1 unless
// isEnabled("carbonio-core").
func StepCheckCoreEnabled() Step {
	return Step{
		Title: "Check that carbonio-core is enabled",
		When:  func(e *Env) bool { return !e.Facts.IsEnabled(PkgCore) },
		Apply: func(*Env) error { return ErrCoreNotEnabled },
	}
}

// StepConfigInitMta ports setup.pl configInitMta (L5667-5706), When
// isEnabled(carbonio-mta). It sets postfix_mail_owner and postfix_setgid_group
// (the localconfig value or postfix / postdrop), runs zmmtainit with the LDAP
// host and port, and sets the server's zimbraMtaMyNetworks.
//
// Deviations: "configd init mta" is not used: it needs the daemon running and
// starts the MTA, which setup.pl did not do here. zmmtainit is run when it is
// installed; the MTA start hook of configd renders the same ldap-*.cf. The
// zimbraServiceInstalled/Enabled lists setup.pl built here for isZCS are the
// job of the enabled-services step. A failing zmmtainit ends the run (setup.pl
// ignored its exit status).
func StepConfigInitMta() Step {
	return Step{
		Name:  svcStageInitMta,
		Title: "Initialize MTA config",
		When:  func(e *Env) bool { return e.Facts.IsEnabled(PkgMTA) },
		Apply: svcInitMta,
	}
}

func svcInitMta(e *Env) error {
	lc, err := e.LC()
	if err != nil {
		return err
	}

	pairs := map[string]string{
		"postfix_mail_owner":   svcOr(lc["postfix_mail_owner"], svcPostfixOwner),
		"postfix_setgid_group": svcOr(lc["postfix_setgid_group"], svcPostfixGroup),
	}

	if err := e.SetLC(pairs); err != nil {
		return err
	}

	tool := e.Paths.Path("libexec", "zmmtainit")
	if _, err := e.Sys.FS.Stat(tool); err == nil {
		port := svcDefaultLDAPPort
		if e.Cfg.LDAP.Port > 0 {
			port = e.Cfg.LDAP.Port
		}

		host := svcOr(e.Cfg.LDAP.Host, e.Cfg.Host.Hostname)
		if err := e.Zextras(tool, host, strconv.Itoa(port)); err != nil {
			return err
		}
	} else {
		e.Log.Detail("%s not installed: the LDAP tables are written when the MTA starts", tool)
	}

	if len(e.Cfg.MTA.MyNetworks) > 0 {
		return e.Prov("ms", e.Cfg.Host.Hostname, "zimbraMtaMyNetworks", strings.Join(e.Cfg.MTA.MyNetworks, " "))
	}

	return nil
}

func svcOr(v, def string) string {
	if v != "" {
		return v
	}

	return def
}

// StepSetTrainSACleanupHost ports the setup.pl applyConfig block (L5946-5952)
// that sets zmtrainsa_cleanup_host: "false" when more than one MTA server
// exists, "true" otherwise. When isEnabled(carbonio-mta) && newinstall.
func StepSetTrainSACleanupHost() Step {
	return Step{
		Title: "Set the spam training cleanup host",
		When:  func(e *Env) bool { return e.Facts.IsEnabled(PkgMTA) && e.Facts.NewInstall },
		Apply: func(e *Env) error {
			dir, err := svcOpenDir(e)
			if err != nil {
				return err
			}
			defer func() { _ = dir.Close() }()

			n, err := dir.MTAServers()
			if err != nil {
				return err
			}

			return e.SetLC(map[string]string{"zmtrainsa_cleanup_host": strconv.FormatBool(n <= 1)})
		},
	}
}

// StepConfigCreateDomain ports setup.pl configCreateDomain (L5528-5640), When
// the node has the directory server or the application server enabled.
//
// Directory server && domain.create: the domain, zimbraDefaultDomainName and
// zimbraMtaMyOrigin. Application server: when admin.create the admin domain,
// the admin account (the password goes through the zmprov batch on stdin, not
// argv), the root and postmaster aliases (service node only), the
// notification sender and recipients; when store.spam_training the spam, ham
// and virus quarantine accounts with random passwords and the global
// attributes naming them. Everything already in the directory is skipped, so
// Done is true once nothing is left to create.
//
// Deviations: random passwords come from crypto/rand (setup.pl used
// RandomPassword through zmjava); setup.pl printed "failed." per command and
// went on, the step runs all the commands in one batch and warns with all the
// errors (Optional).
func StepConfigCreateDomain() Step {
	return Step{
		Name:     svcStageCreateDom,
		Title:    "Create domain and accounts",
		Optional: true,
		When: func(e *Env) bool {
			return e.Facts.IsEnabled(svcPkgDirectory) || e.Facts.IsEnabled(PkgAppserver)
		},
		Done: func(e *Env) (bool, error) {
			p, err := svcDomainPlan(e)
			if err != nil {
				return false, err
			}

			return p.Len() == 0, nil
		},
		Apply: func(e *Env) error {
			p, err := svcDomainPlan(e)
			if err != nil {
				return err
			}

			return e.RunProv(p)
		},
	}
}

// svcPlanner builds the zmprov batch of configCreateDomain.
type svcPlanner struct {
	dir     svcDir
	global  map[string]string
	prov    *Prov
	domains map[string]bool
	// globals are the global config changes; they follow the creations, so the
	// batch runs the creations in one zmprov process and the changes to LDAP
	// (RunProv keeps the order of a batch). None of them depends on a creation.
	globals [][]string
	err     error
}

func svcDomainPlan(e *Env) (*Prov, error) {
	dir, err := svcOpenDir(e)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()

	global, err := dir.Global()
	if err != nil {
		return nil, err
	}

	pl := &svcPlanner{dir: dir, global: global, prov: &Prov{}, domains: map[string]bool{}}
	cfg := e.Cfg

	if e.Facts.IsEnabled(svcPkgDirectory) && cfg.Domain.Create {
		pl.domain(cfg.Domain.Name)
		pl.global1("zimbraDefaultDomainName", cfg.Domain.Name)
		pl.global1("zimbraMtaMyOrigin", cfg.Domain.Name)
	}

	if e.Facts.IsEnabled(PkgAppserver) {
		if cfg.Admin.Create {
			pl.admin(cfg, e.Facts.ServiceWebapp)
		}

		if cfg.Store.SpamTraining {
			pl.training(cfg)
		}
	}

	for _, g := range pl.globals {
		pl.prov.Add(g...)
	}

	return pl.prov, pl.err
}

func (pl *svcPlanner) exists(filter string) bool {
	if pl.err != nil {
		return true
	}

	ok, err := pl.dir.Exists(filter)
	if err != nil {
		pl.err = err
	}

	return ok
}

func (pl *svcPlanner) domain(name string) {
	key := strings.ToLower(name)
	if pl.domains[key] {
		return
	}

	filter := "(&(objectClass=zimbraDomain)(zimbraDomainName=" + ldap.EscapeFilter(name) + "))"
	if !pl.exists(filter) {
		pl.prov.Add("cd", name)
	}

	pl.domains[key] = true
}

func (pl *svcPlanner) accountExists(addr string) bool {
	return pl.exists("(&(objectClass=zimbraAccount)(zimbraMailDeliveryAddress=" + ldap.EscapeFilter(addr) + "))")
}

// global1 adds one mcf unless the global config already has the value.
func (pl *svcPlanner) global1(kv ...string) {
	var args []string

	for i := 0; i+1 < len(kv); i += 2 {
		if pl.global[kv[i]] != kv[i+1] {
			args = append(args, kv[i], kv[i+1])
		}
	}

	if len(args) > 0 {
		pl.globals = append(pl.globals, append([]string{provision.VerbModifyConfig}, args...))
	}
}

func (pl *svcPlanner) admin(cfg *bootstrap.Config, serviceNode bool) {
	addr := strings.ToLower(cfg.Admin.Account)
	_, domain, _ := strings.Cut(addr, "@")

	pl.domain(domain)

	if !pl.accountExists(addr) {
		pl.prov.Add("ca", addr, cfg.Admin.Password,
			"zimbraAdminConsoleUIComponents", "cartBlancheUI",
			svcAttrDescription, "Administrative Account",
			"displayName", "Carbonio Admin",
			"zimbraIsAdminAccount", svcTrueValue)
	}

	if serviceNode {
		for _, alias := range []string{"root", "postmaster"} {
			full := alias + "@" + cfg.Domain.Name

			filter := "(&(objectClass=zimbraAccount)(zimbraMailAlias=" + ldap.EscapeFilter(full) + "))"
			if !pl.exists(filter) {
				pl.prov.Add("aaa", addr, full)
			}
		}
	}

	pl.global1("carbonioNotificationFrom", addr, "carbonioNotificationRecipients", addr)
}

func (pl *svcPlanner) training(cfg *bootstrap.Config) {
	spam := strings.ToLower(cfg.Store.SpamAccount)
	ham := strings.ToLower(cfg.Store.HamAccount)
	virus := strings.ToLower(cfg.Store.VirusQuarantineAccount)

	common := []string{
		"amavisBypassSpamChecks", svcTrueValue, "zimbraAttachmentsIndexingEnabled", "FALSE",
		"zimbraIsSystemResource", svcTrueValue, "zimbraIsSystemAccount", svcTrueValue, "zimbraHideInGal", svcTrueValue,
		"zimbraMailQuota", "0",
	}

	accounts := []struct {
		addr  string
		extra []string
	}{
		{spam, []string{svcAttrDescription, "System account for spam training."}},
		{ham, []string{svcAttrDescription, "System account for Non-Spam (Ham) training."}},
		{virus, []string{"zimbraMailMessageLifetime", "30d",
			svcAttrDescription, "System account for Anti-virus quarantine."}},
	}

	for _, a := range accounts {
		if pl.accountExists(a.addr) {
			continue
		}

		pass, err := svcRandomPassword(svcPassLen, svcAlnum)
		if err != nil {
			pl.err = err

			return
		}

		args := append([]string{"ca", a.addr, pass}, common...)
		pl.prov.Add(append(args, a.extra...)...)
	}

	pl.global1("zimbraSpamIsSpamAccount", spam, "zimbraSpamIsNotSpamAccount", ham,
		"zimbraAmavisQuarantineAccount", virus)
}

const (
	svcAlnum     = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	svcLowerNum  = "abcdefghijklmnopqrstuvwxyz0123456789"
	svcPassLen   = 16
	svcGALSuffix = 10
)

// svcRandomPassword returns n random characters of the alphabet.
func svcRandomPassword(n int, alphabet string) (string, error) {
	out := make([]byte, n)
	limit := big.NewInt(int64(len(alphabet)))

	for i := range out {
		v, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", fmt.Errorf("random password: %w", err)
		}

		out[i] = alphabet[v.Int64()]
	}

	return string(out), nil
}

// StepConfigInitSQL ports setup.pl configInitSql (L5643-5666), When
// isEnabled(carbonio-appserver): zmmyinit --mysql_memory_percent as root. Done
// when the mailbox database directory exists (what getSystemStatus calls
// sqlConfigured). Deviation: a failing zmmyinit ends the run (setup.pl
// ignored its status and printed done).
func StepConfigInitSQL() Step {
	return Step{
		Name:  svcStageInitSQL,
		Title: "Initialize the store SQL database",
		When:  func(e *Env) bool { return e.Facts.IsEnabled(PkgAppserver) },
		Done: func(e *Env) (bool, error) {
			_, err := e.Sys.FS.Stat(e.Paths.Path("db", "data", "zimbra"))
			if errors.Is(err, fs.ErrNotExist) {
				return false, nil
			}

			return err == nil, err
		},
		Apply: func(e *Env) error {
			pct := e.Cfg.Store.MySQLMemoryPercent
			if pct <= 0 {
				pct = svcDefaultMySQLPct
			}

			return e.Root(e.Paths.Path("libexec", "zmmyinit"), "--mysql_memory_percent", strconv.Itoa(pct))
		},
	}
}

// StepSetupSyslog ports setup.pl setupSyslog (L6054-6069), which ran
// libexec/zmsyslogsetup (Perl) as root. It is done in process now (syslog.go):
// the local0, auth and mail rules of Carbonio go to /var/log/carbonio.log (and
// to zimbraLogHostname over UDP when that is another host), and local0, local1,
// mail and auth are kept out of the catch-all rules. Files are replaced
// atomically, a re-run changes nothing, and rsyslog is restarted only when a
// file changed.
//
// Deviations: only rsyslog is handled (/etc/rsyslog.conf, plus Ubuntu's
// rsyslog.d layout); the syslog-ng and /etc/syslog.conf branches of the script
// are not ported, as no supported platform uses them. A mail rule of an earlier
// run is recognised by carbonio.log as well, so a log host on this node does
// not pile one up on every run. The "no rsyslog.service" case still fails the
// step (a warning), but only when there is something to restart. A failing
// restart is logged as a warning, as the script ignored it. The failure to
// read zimbraLogHostname is an error here; the script treated it as unset.
func StepSetupSyslog() Step {
	return Step{
		Name:     svcStageSyslog,
		Title:    "Set up syslog.conf",
		Optional: true,
		Apply:    ConfigureSyslog,
	}
}

// StepStartServers ports the "Starting servers" block of setup.pl applyConfig
// (L5972-5995), When start_services. Deviation: setup.pl ignored the exit
// status of "zmcontrol start" and went on, so the GAL sync account step ran
// against a mailbox that was down and the run still said "Configuration
// complete". A service that does not start now fails the run, naming the
// service; a re-run starts the servers again and goes on.
//
// On Ubuntu, on an existing install with the application server, redo.log is
// chowned to zextras first. With systemd targets enabled the enabled role
// targets are stopped, a configd daemon outside its unit is killed
// (stopStrayConfigd) and the targets are started; otherwise zmcontrol (which
// is "configd control" run as zextras) stops, starts and reports. configd
// control itself is not called in process: it requires to run as zextras.
func StepStartServers() Step {
	return Step{
		Title: "Start servers",
		When:  func(e *Env) bool { return e.Cfg.StartServices },
		Apply: svcStartServers,
	}
}

func svcStartServers(e *Env) error {
	if e.Facts.IsEnabled(PkgAppserver) && strings.Contains(e.Facts.Platform, "ubuntu") && !e.Facts.NewInstall {
		if err := e.Root("chown", "zextras:zextras", e.Paths.Path("redolog", "redo.log")); err != nil {
			e.Log.Detail("redo.log: %v", err)
		}
	}

	if e.Facts.Systemd {
		return svcRestartTargets(e)
	}

	bin := e.Paths.Path("bin", "zmcontrol")
	if err := e.Zextras(bin, "stop"); err != nil {
		e.Log.Detail("zmcontrol stop: %v", err)
	}

	if err := e.Zextras(bin, "start"); err != nil {
		return err
	}

	if err := e.Zextras(bin, "status"); err != nil {
		e.Log.Detail("zmcontrol status: %v", err)
	}

	return nil
}

func svcRestartTargets(e *Env) error {
	enabled := svcEnabledTargets(e)

	for _, t := range enabled {
		if err := e.Root("systemctl", "stop", t); err != nil {
			e.Log.Detail("stopping %s: %v", t, err)
		}
	}

	if err := svcStopStrayConfigd(e); err != nil {
		return err
	}

	var errs []error

	for _, t := range enabled {
		if err := e.Root("systemctl", "start", t); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// svcEnabledTargets are the role targets systemctl says are enabled.
func svcEnabledTargets(e *Env) []string {
	var enabled []string

	for _, t := range svcSystemdTargets {
		out, _ := e.Output(&Cmd{Path: "systemctl", Args: []string{"is-enabled", t}})
		if strings.TrimSpace(string(out)) == "enabled" {
			enabled = append(enabled, t)
		}
	}

	return enabled
}

// svcStopStrayConfigd ports setup.pl stopStrayConfigd (L4087-4099): a configd
// daemon started outside carbonio-configd.service keeps its port, so after
// the targets are stopped it is terminated (KILL after 10 s).
func svcStopStrayConfigd(e *Env) error {
	if err := e.Root("systemctl", "stop", svcConfigdUnit); err != nil {
		e.Log.Detail("stopping %s: %v", svcConfigdUnit, err)
	}

	pkill := func(sig string) error {
		return e.Run(&Cmd{Path: "pkill", Args: []string{sig, "-u", config.ZextrasUser, "-f", svcConfigdPattern}})
	}
	running := func() bool { return pkill("-0") == nil }

	if !running() {
		return nil
	}

	e.Log.Detail("Stopping configd daemon running outside %s", svcConfigdUnit)

	_ = pkill("-TERM")

	for range svcStrayWaitSeconds {
		if !running() {
			return nil
		}

		if err := e.Sys.Clock.Sleep(e.Ctx, time.Second); err != nil {
			return err
		}
	}

	_ = pkill("-KILL")

	return nil
}

// StepConfigCreateDefaultDomainGALSyncAcct ports setup.pl
// configCreateDefaultDomainGALSyncAcct (L5726-5742), run after the servers
// start: When start_services && newinstall && isStoreServiceNode() &&
// isEnabled(carbonio-appserver). It creates galsync.<random>@<domain> for the
// default domain (domain.name) as zmgsautil createAccount does, but through
// the admin SOAP API (zxadmin.CreateGalSyncAccount), not by starting the
// tool's JVM. Done when such an account exists. Optional ("failed." and carry
// on). The history stage is written on success only, as setup.pl did.
func StepConfigCreateDefaultDomainGALSyncAcct() Step {
	return Step{
		Name:     svcStageGALSync,
		Title:    "Create GAL sync account",
		Optional: true,
		When: func(e *Env) bool {
			return e.Cfg.StartServices && e.Facts.NewInstall && e.Facts.ServiceWebapp &&
				e.Facts.IsEnabled(PkgAppserver)
		},
		Done: func(e *Env) (bool, error) {
			dir, err := svcOpenDir(e)
			if err != nil {
				return false, err
			}
			defer func() { _ = dir.Close() }()

			return dir.Exists("(&(objectClass=zimbraAccount)(zimbraMailDeliveryAddress=galsync.*@" +
				ldap.EscapeFilter(e.Cfg.Domain.Name) + "))")
		},
		Apply: func(e *Env) error {
			server, err := e.LCValue(svcLocalConfigHost)
			if err != nil {
				return err
			}

			_, err = CreateGalSyncAccount(e, e.Cfg.Domain.Name, svcOr(server, e.Cfg.Host.Hostname))

			return err
		},
	}
}

// CreateGalSyncAccount creates galsync.<random>@domain on the mailbox server
// (the work of "zmgsautil createAccount -n InternalGAL -t zimbra -f
// _InternalGAL -p 1d") and returns the account. It needs only the node's
// localconfig, not a bootstrap configuration, so the setup.pl commands share
// it with the step.
func CreateGalSyncAccount(e *Env, domain, server string) (zxadmin.GalSyncAccount, error) {
	suffix, err := svcRandomPassword(svcGALSuffix, svcLowerNum)
	if err != nil {
		return zxadmin.GalSyncAccount{}, err
	}

	admin, err := svcOpenGAL(e)
	if err != nil {
		return zxadmin.GalSyncAccount{}, err
	}

	acct, err := admin.CreateGalSyncAccount(e.Ctx, &zxadmin.GalSyncAccountRequest{
		Account: "galsync." + suffix + "@" + domain, Name: svcGALDataSource, Domain: domain,
		Type: "zimbra", Folder: "_InternalGAL", Server: server, PollingInterval: "1d",
	})
	if err != nil {
		return zxadmin.GalSyncAccount{}, fmt.Errorf("creating the GAL sync account: %w", err)
	}

	e.Log.Detail("Created GAL sync account %s (%s)", acct.Name, acct.ID)

	return acct, nil
}

// svcGALAdmin is the admin API call the GAL sync step makes (an interface so
// tests need no mailbox).
type svcGALAdmin interface {
	CreateGalSyncAccount(ctx context.Context, r *zxadmin.GalSyncAccountRequest) (zxadmin.GalSyncAccount, error)
}

// svcOpenGAL opens the admin API of the node (replaced by tests).
var svcOpenGAL = func(e *Env) (svcGALAdmin, error) {
	lc, err := e.LC()
	if err != nil {
		return nil, err
	}

	return zxadmin.NewWithBaseURL(svcAdminURL(lc), lc), nil
}

// svcAdminURL is the admin URL of the server zmgsautil talked to:
// zimbra_zmprov_default_soap_server on zimbra_admin_service_port.
func svcAdminURL(lc map[string]string) string {
	host := cmp.Or(lc["zimbra_zmprov_default_soap_server"], "localhost")
	port := cmp.Or(lc["zimbra_admin_service_port"], strconv.Itoa(zxadmin.AdminPort))

	return "https://" + net.JoinHostPort(host, port)
}

// StepGALSyncSkipNotice warns, as setup.pl did (L5996-6010), when the default
// domain GAL sync account is not created because the servers are not started.
// The other reasons it is not created (not a service node, existing install)
// are not worth the operator's attention and the step does not apply.
func StepGALSyncSkipNotice() Step {
	return Step{
		Title: "Create GAL sync account",
		When: func(e *Env) bool {
			return e.Facts.IsEnabled(PkgAppserver) && !e.Cfg.StartServices
		},
		Apply: func(e *Env) error {
			e.Log.Warn("skipped: services not started")

			return nil
		},
	}
}

// StepSetupCrontab ports setup.pl setupCrontab (L6071-6112): the ZEXTRAS
// block of the zextras crontab is replaced by conf/crontabs/crontab plus the
// directory-server, application-server and mta files of the enabled packages.
// As in setup.pl a crontab without both markers is dropped. The crontab is
// read and installed through crontab(1) and stdin, no /tmp files. Done when
// the crontab already is what would be installed.
func StepSetupCrontab() Step {
	return Step{
		Name:  svcStageCrontab,
		Title: "Set up the zextras crontab",
		Done: func(e *Env) (bool, error) {
			want, cur, err := svcCrontab(e)
			if err != nil {
				return false, err
			}

			return want == cur, nil
		},
		Apply: func(e *Env) error {
			want, _, err := svcCrontab(e)
			if err != nil {
				return err
			}

			return e.Run(&Cmd{
				Path:  svcCrontabBin,
				Args:  []string{"-u", config.ZextrasUser, "-"},
				Stdin: strings.NewReader(want),
			})
		},
	}
}

// svcCrontab returns the crontab to install and the current one.
func svcCrontab(e *Env) (want, cur string, err error) {
	out, _ := e.Output(&Cmd{Path: svcCrontabBin, Args: []string{"-u", config.ZextrasUser, "-l"}, Quiet: true})
	cur = string(out)

	rest := ""
	if strings.Contains(cur, "ZEXTRAS-START") && strings.Contains(cur, "ZEXTRAS-END") {
		rest = svcStripBlock(cur)
	}

	var b strings.Builder

	dir := e.Paths.Path("conf", "crontabs")

	files := []struct {
		name     string
		when     bool
		required bool
	}{
		{"crontab", true, true},
		{"crontab.ldap", e.Facts.IsEnabled(svcPkgDirectory), false},
		{"crontab.store", e.Facts.IsEnabled(PkgAppserver), false},
		{"crontab.mta", e.Facts.IsEnabled(PkgMTA), false},
	}

	for _, f := range files {
		if !f.when {
			continue
		}

		data, rerr := e.Sys.FS.ReadFile(dir + "/" + f.name)
		if rerr != nil {
			if f.required {
				return "", "", rerr
			}

			e.Log.Detail("crontab: %v", rerr)

			continue
		}

		b.Write(data)

		if len(data) > 0 && data[len(data)-1] != '\n' {
			b.WriteByte('\n')
		}
	}

	b.WriteString(svcCrontabEndLine + "\n")
	b.WriteString(rest)

	return b.String(), cur, nil
}

// svcStripBlock is sed '/# ZEXTRAS-START/,/# ZEXTRAS-END/d'.
func svcStripBlock(s string) string {
	var b strings.Builder

	skipping := false

	for line := range strings.SplitSeq(strings.TrimSuffix(s, "\n"), "\n") {
		if !skipping && strings.Contains(line, svcCrontabStart) {
			skipping = true
		}

		if skipping {
			if strings.Contains(line, svcCrontabEnd) {
				skipping = false
			}

			continue
		}

		b.WriteString(line + "\n")
	}

	return b.String()
}
