// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"crypto"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
)

// certDieStatus is the exit status of a Perl die.
const certDieStatus = 255

// The defaults of zmcertmgr.
const (
	certDefaultDays    = 1825
	certDefaultDaysExp = 30
	certDefaultKeysize = 2048
	certDefaultDigest  = "sha256"
	certMinKeysize     = 2048
)

// certDigestRE is validDigest of the script. It is not anchored at the end,
// as in the script: "sha256xyz" passes it too.
var certDigestRE = regexp.MustCompile(`^(?:ripemd160|sha(?:|1|224|256|384|512))`)

// The services zmcertmgr deploys to or looks at.
var certServices = []string{certSvcLDAP, certSvcMailboxd, certSvcMTA, certSvcProxy}

// Commands and services that the code refers to by name.
const (
	certCmdCheckExpiry = "checkcrtexpiration"
	certCmdCreateCA    = "createca"
	certCmdCreateCRT   = "createcrt"
	certCmdVerifyCRT   = "verifycrt"
	certCmdViewStaged  = "viewstagedcrt"

	certSvcAll      = "all"
	certSvcLDAP     = "ldap"
	certSvcMailboxd = "mailboxd"
	certSvcMTA      = "mta"
	certSvcProxy    = "proxy"
)

// certMgr is one run of zmcertmgr (the script's CertMgr object).
type certMgr struct {
	e    *Env
	o    *certOpts
	out  io.Writer
	errw io.Writer
	lc   map[string]string
	p    certPaths

	days, keysize int
	digest        string
	subject       string
	isNew         bool
	sans          []string
	deploy        []string
	dir           certDirectory
	dirErr        error
	dirOpened     bool
	initDone      bool
	backupDone    bool
	confMemo      string
	confMemoed    bool
	zuid, zgid    int
	idsLoaded     bool
	idsErr        error
	keyGen        func(bits int) (crypto.Signer, error)
}

// certCmdFunc is a command: ok is the script's return value (false: exit 1),
// err a die (exit 255).
type certCmdFunc func(m *certMgr, args []string) (ok bool, err error)

// certCmdFuncs are the methods of the commands.
var certCmdFuncs = map[string]certCmdFunc{
	"addcacert":        (*certMgr).cmdAddCACert,
	certCmdCheckExpiry: (*certMgr).checkcrtexpiration,
	certCmdCreateCA:    func(m *certMgr, _ []string) (bool, error) { return m.createca() },
	certCmdCreateCRT:   func(m *certMgr, _ []string) (bool, error) { return m.createcrt() },
	"createcsr":        (*certMgr).cmdCreateCSR,
	"deployca":         func(m *certMgr, _ []string) (bool, error) { return m.deployca() },
	"deploycrt":        (*certMgr).deploycrt,
	"getcrt":           (*certMgr).cmdGetCrt,
	"migrate":          func(m *certMgr, _ []string) (bool, error) { return m.migrate() },
	"savecrt":          (*certMgr).cmdSaveCrt,
	certCmdVerifyCRT:   (*certMgr).cmdVerifyCrt,
	"verifycrtchain":   (*certMgr).cmdVerifyCrtChain,
	"verifycrtkey":     (*certMgr).cmdVerifyCrtKey,
	"viewcsr":          (*certMgr).viewcsr,
	"viewdeployedcrt":  (*certMgr).viewdeployedcrt,
	certCmdViewStaged:  (*certMgr).viewstagedcrt,
}

// certUsage is what the script does with a command line that asks for help or
// is refused (pod2usage), before anything runs; done is false when the
// command is to run.
func certUsage(p *certParse, stdout io.Writer) (status int, done bool) {
	switch {
	case p.opts.Help:
		_, _ = io.WriteString(stdout, certSynopsis+certOptionsText)

		return 0, true
	case p.opts.Man:
		_, _ = io.WriteString(stdout, certManText)

		return 0, true
	case !p.optsOK:
		_, _ = io.WriteString(stdout, certSynopsis)

		return 1, true
	case len(p.errs) > 0:
		// pod2usage(-verbose => 1, -message => ...): the exit status is 1 and
		// the message and the usage go to stdout.
		for _, msg := range p.errs {
			_, _ = fmt.Fprintf(stdout, "%s: %s\n", certProg, msg)
		}

		_, _ = io.WriteString(stdout, certSynopsis+certOptionsText)

		return 1, true
	}

	return 0, false
}

// RunCertMgr is zmcertmgr: the certificate manager of the node. args are what
// follows the command name. It returns the exit status of the script: 0 when
// the command did its work, 1 when it did not or for a bad command line, and
// 255 for a die; the messages go to stdout and stderr where the script put
// them.
func RunCertMgr(e *Env, args []string, stdout, stderr io.Writer) int {
	p := certParseArgs(args, stderr)
	o := p.opts

	if status, done := certUsage(p, stdout); done {
		return status
	}

	if o.Debug > 0 && e.Log != nil && e.Log.file == nil {
		// -debug: the command lines run (the log of an Env without a log
		// file) go to stderr.
		e.Log.file = stderr
	}

	m := &certMgr{e: e, o: o, out: stdout, errw: stderr, keyGen: certNewRSAKey}
	if e.certKeyGen != nil {
		m.keyGen = e.certKeyGen
	}

	defer m.close()

	ok, err := m.configure()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)

		return certDieStatus
	}

	if !ok {
		_, _ = io.WriteString(stdout, certSynopsis)

		return 1
	}

	if o.HasDays && o.Days != 0 && o.Command != certCmdCheckExpiry {
		m.say("validation days: %d\n", m.days)
	}

	cmdArgs := o.Args
	if o.Type != "" {
		cmdArgs = append([]string{o.Type}, cmdArgs...)
	}

	ok, err = certCmdFuncs[o.Command](m, cmdArgs)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: ERROR %s(%s) failed:\n %s\n",
			certProg, o.Command, strings.Join(cmdArgs, " "), err)

		return certDieStatus
	}

	if !ok {
		return 1
	}

	return 0
}

// configure applies the options to the run as CertMgr->new does and reads the
// defaults from localconfig. ok is false when an option was refused (the
// message is printed); err is a die.
func (m *certMgr) configure() (ok bool, err error) {
	o := m.o
	m.isNew = o.New
	m.sans = o.SubjectAltNames
	m.keysize = certDefaultKeysize
	m.deploy = []string{certSvcAll}

	if m.lc, err = m.e.LC(); err != nil {
		return false, fmt.Errorf("%s: new: %w", certProg, err)
	}

	if len(m.lc) == 0 {
		return false, fmt.Errorf("%s: new: unable to get localconfig", certProg)
	}

	m.p = newCertPaths(m)

	if !m.setDays() || !m.setDigest() || !m.setKeysize() || !m.setDeploy() {
		return false, nil
	}

	m.subject = certDefaultSubject(m)
	if o.HasSubject {
		m.subject = o.Subject
	}

	return true, nil
}

// setDays validates -days and the default of the command.
func (m *certMgr) setDays() bool {
	o := m.o

	switch {
	case o.HasDays:
		if o.Days <= 0 {
			return m.fail("invalid days '%d': must be > 0\n", o.Days)
		}

		m.days = o.Days
	case o.Command == certCmdCheckExpiry:
		m.days = certDefaultDaysExp
	default:
		m.days = certDefaultDays
	}

	return true
}

// setDigest validates -digest, or takes ssl_default_digest from localconfig.
func (m *certMgr) setDigest() bool {
	dig := m.o.Digest

	if !m.o.HasDigest {
		dig = m.lc["ssl_default_digest"]
		if dig == "" {
			dig = certDefaultDigest
		}
	}

	if !certDigestRE.MatchString(dig) {
		return m.fail("unknown digest method '%s'\n", dig)
	}

	m.digest = dig

	return true
}

// setKeysize validates -keysize.
func (m *certMgr) setKeysize() bool {
	if !m.o.HasKeysize {
		return true
	}

	if m.o.Keysize < certMinKeysize {
		return m.fail("invalid keysize '%d': must be >= %d\n", m.o.Keysize, certMinKeysize)
	}

	m.keysize = m.o.Keysize

	return true
}

// setDeploy validates -deploy.
func (m *certMgr) setDeploy() bool {
	if len(m.o.Deploy) == 0 {
		return true
	}

	for _, s := range m.o.Deploy {
		if !certValidService(s) {
			return m.fail("deploy: unknown service '%s'\n", s)
		}
	}

	m.deploy = m.o.Deploy

	return true
}

// certDefaultSubject is _defaultSubject: the CA has an organisation.
func certDefaultSubject(m *certMgr) string {
	org := ""
	if m.o.Command == certCmdCreateCA {
		org = "/O=CA"
	}

	return org + "/OU=Zextras Carbonio/CN=" + m.lc["zimbra_server_hostname"]
}

// certValidService is _isValidService.
func certValidService(name string) bool {
	return name == certSvcAll || slices.Contains(certServices, name)
}

// services is _getServiceInfo: the services of sname ("" or all: every one),
// sorted.
func (m *certMgr) services(sname string) []string {
	if sname == "" || sname == certSvcAll {
		return certServices
	}

	return []string{sname}
}

// say prints to stdout.
func (m *certMgr) say(format string, args ...any) {
	_, _ = fmt.Fprintf(m.out, format, args...)
}

// fail is CertMgr::error: it prints "ERROR: " and the message on stdout and
// returns false, the script's undef.
func (m *certMgr) fail(format string, args ...any) bool {
	m.say("ERROR: "+format, args...)

	return false
}

// debugf prints a DEBUG line on stderr when -debug is on.
func (m *certMgr) debugf(format string, args ...any) {
	if m.o.Debug > 0 {
		_, _ = fmt.Fprintf(m.errw, "DEBUG: "+format, args...)
	}
}

// close ends the directory connection.
func (m *certMgr) close() {
	if m.dir != nil {
		_ = m.dir.Close()
	}
}

// lcGet is Localconfig->get.
func (m *certMgr) lcGet(key string) string { return m.lc[key] }

// isMaster is lc(ldap_is_master) eq "true".
func (m *certMgr) isMaster() bool { return strings.EqualFold(m.lc["ldap_is_master"], "true") }
