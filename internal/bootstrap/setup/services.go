// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bufio"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
	"github.com/zextras/carbonio-configd/internal/config"
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
		pl.prov.Add(append([]string{"mcf"}, args...)...)
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

// StepSetupSyslog ports setup.pl setupSyslog (L6054-6069): zmsyslogsetup as
// root; "Failed" (here a warning) when it is missing or fails.
func StepSetupSyslog() Step {
	return Step{
		Name:     svcStageSyslog,
		Title:    "Set up syslog.conf",
		Optional: true,
		Apply: func(e *Env) error {
			tool := e.Paths.Path("libexec", "zmsyslogsetup")
			if _, err := e.Sys.FS.Stat(tool); err != nil {
				return fmt.Errorf("%s: %w", tool, err)
			}

			return e.Root(tool)
		},
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
		Apply:    svcStartServers,
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
// isEnabled(carbonio-appserver). zmgsautil creates galsync.<random>@<domain>
// for the default domain (domain.name). Done when such an account exists.
// Optional ("failed." and carry on). The history stage is written on success
// only, as setup.pl did.
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

			suffix, err := svcRandomPassword(svcGALSuffix, svcLowerNum)
			if err != nil {
				return err
			}

			domain := e.Cfg.Domain.Name

			return e.Zextras(e.Paths.Path("bin", "zmgsautil"), "createAccount",
				"-a", "galsync."+suffix+"@"+domain, "-n", "InternalGAL", "--domain", domain,
				"-s", svcOr(server, e.Cfg.Host.Hostname), "-t", "zimbra", "-f", "_InternalGAL", "-p", "1d")
		},
	}
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

// StepZmsshkeygen ports setup.pl applyConfig L6011-6013: zmsshkeygen as
// zextras on a new install. It regenerates the key on a re-run of an
// unfinished new install (cheap, so no Done). Optional: setup.pl ignored the
// status.
func StepZmsshkeygen() Step {
	return Step{
		Title:    "Generate zextras SSH key",
		Optional: true,
		When:     func(e *Env) bool { return e.Facts.NewInstall },
		Apply:    func(e *Env) error { return e.Zextras(e.Paths.Path("bin", "zmsshkeygen")) },
	}
}

// StepZmupdateauthkeys ports setup.pl applyConfig L6014-6018: zmupdateauthkeys
// as zextras on every run. Optional: setup.pl ignored the status.
func StepZmupdateauthkeys() Step {
	return Step{
		Title:    "Update authorized SSH keys",
		Optional: true,
		Apply:    func(e *Env) error { return e.Zextras(e.Paths.Path("bin", "zmupdateauthkeys")) },
	}
}
