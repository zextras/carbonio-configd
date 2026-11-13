// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// The certificate types of Flags.CertType.
const (
	certTypeSelf = "self"
	certTypeComm = "comm"
)

// certNewFlag is the zmcertmgr option that regenerates (setup.pl
// $needNewCert = "-new").
const certNewFlag = "-new"

// Stage names of setup.pl the certificate steps record.
const (
	certStageCASetup     = "configCASetup"
	certStageSaveCA      = "configSaveCA"
	certStageCreateCert  = "configCreateCert"
	certStageSaveCert    = "configSaveCert"
	certStageInstallStor = "configInstallCertStore"
	certStageInstallMTA  = "configInstallCertMTA"
	certStageInstallLDAP = "configInstallCertLDAP"
	certStageInstallPrxy = "configInstallCertProxy"
)

// The packages that have no Pkg constant in facts.go.
const (
	certPkgDirectory = "carbonio-directory-server"
	certPkgProxy     = "carbonio-proxy"
)

// certDHParamAttr is the global config attribute that names the DH parameter
// file.
const certDHParamAttr = "zimbraSSLDHParam"

// File names under conf/: the CA certificate (in conf/ca/) and the DH
// parameters.
const (
	certCAFile      = "ca.pem"
	certDHParamFile = "dhparam.pem.crb"
	// certDHParamMode is what openssl, run as zextras with its 027 umask, gave
	// the file in setup.pl.
	certDHParamMode = 0o640
)

// certRestartLDAP restarts the local LDAP server so it reads the new
// certificate (setup.pl stopLdap + startLdap). Tests replace it.
var certRestartLDAP = func(e *Env) error {
	if err := ldapNativeKit().stop(e); err != nil {
		return err
	}

	if err := ldapControl(e, e.Facts.Systemd, ldapStart, e.Ctl.ServiceStart); err != nil {
		return fmt.Errorf("starting ldap: %w", err)
	}

	return nil
}

// certGlobalConfig reads the global config natively. Tests replace it.
var certGlobalConfig = func(e *Env) (map[string]string, error) {
	c, err := e.LDAP()
	if err != nil {
		return nil, err
	}

	defer func() { _ = c.Close() }()

	return c.GetGlobalConfig()
}

func certFile(e *Env, elem ...string) string { return e.Paths.Path(elem...) }

func certIsFile(e *Env, name string) bool {
	fi, err := e.Sys.FS.Stat(name)

	return err == nil && !fi.IsDir()
}

func certIsDir(e *Env, name string) bool {
	fi, err := e.Sys.FS.Stat(name)

	return err == nil && fi.IsDir()
}

// certZmcertmgr runs `zmcertmgr args` in process (RunCertMgr, the code of
// `setup certmgr`; no script and no openssl or keytool process runs). It
// narrates every file it copies and every lookup it tries, so its output
// reaches the log only when it fails or the run is verbose.
func certZmcertmgr(e *Env, args ...string) error {
	status, out := certRunMgr(e, args)
	shown := "zmcertmgr " + shownArgs(args)

	e.Log.Detail("*** Running in process: %s", shown)

	if status != 0 {
		e.Log.Detail("%s", strings.TrimRight(out, "\n"))

		return &CmdError{Cmd: shown, Code: status, Output: out}
	}

	if e.Verbose {
		e.Log.Detail("%s", strings.TrimRight(out, "\n"))

		if len(args) > 0 && (args[0] == certCmdCreateCA || args[0] == certCmdCreateCRT) {
			e.Log.Detail("Note: the \"failed\" lookups above (CA key or commercial CA in LDAP, saving " +
				"zimbraSSLPrivateKey before the server entry exists) are expected when nothing is stored yet; " +
				"zmcertmgr goes on and a later step saves the certificate in LDAP.")
		}
	}

	return nil
}

// certRunMgr runs zmcertmgr and returns its exit status and its output (both
// streams, as the log had them).
func certRunMgr(e *Env, args []string) (status int, output string) {
	if e.certRun != nil {
		return e.certRun(args)
	}

	var buf bytes.Buffer

	status = RunCertMgr(e, args, &buf, &buf)

	return status, buf.String()
}

// certType is the certificate type in effect ("self" until a step finds a
// commercial certificate).
func certType(e *Env) string {
	if e.Flags.CertType == "" {
		return certTypeSelf
	}

	return e.Flags.CertType
}

func certNeedFlag(e *Env) string {
	if e.Flags.NeedNewCert {
		return certNewFlag
	}

	return ""
}

// certArgs is "createcrt [-new]" style: the verb plus the -new option when
// set.
func certArgs(verb, opt string) []string {
	if opt == "" {
		return []string{verb}
	}

	return []string{verb, opt}
}

func certStageDone(e *Env, stage string) bool { return e.Facts != nil && e.Facts.Configured[stage] }

// StepAddCACert ports the pre-step of setup.pl main (lines 165-176): on an
// existing install with a CA at conf/ca/ca.pem, `zmcertmgr addcacert` adds it
// to the trust store. A failure only prints "failed." in setup.pl, so the step
// is optional.
func StepAddCACert() Step {
	return Step{
		Title:    "Add the CA to the trust store",
		Optional: true,
		When: func(e *Env) bool {
			return !e.Facts.NewInstall && certIsFile(e, certFile(e, "conf", "ca", certCAFile))
		},
		Apply: func(e *Env) error {
			return certZmcertmgr(e, "addcacert", certFile(e, "conf", "ca", certCAFile))
		},
	}
}

// StepConfigCASetup ports configCASetup (setup.pl 4487-4540).
//
// Deviations: the expiry of the CA is read from its certificate (setup.pl
// piped only stdout of openssl verify, where OpenSSL 1.1+ prints nothing);
// zmcertmgr is called in process (RunCertMgr); the configured ssl_default_digest is written to localconfig here
// too, because zmcertmgr reads it from there (setup.pl wrote it in configLC).
func StepConfigCASetup() Step {
	return Step{
		Name:  certStageCASetup,
		Title: "Set up the CA",
		Done: func(e *Env) (bool, error) {
			return certStageDone(e, certStageCASetup) && certIsDir(e, certFile(e, "ssl", "carbonio", "ca")), nil
		},
		Apply: certApplyCASetup,
	}
}

func certApplyCASetup(e *Env) error {
	caDir := certFile(e, "ssl", "carbonio", "ca")
	pairs := map[string]string{}

	if e.Cfg.LDAP.Host != e.Cfg.Host.Hostname {
		// The LDAP master is another node: this one needs its credentials.
		pairs["ldap_root_password"] = e.Cfg.LDAP.RootPassword
		pairs["zimbra_ldap_password"] = e.Cfg.LDAP.AdminPassword
	}

	if e.Cfg.Host.SSLDigest != "" {
		pairs["ssl_default_digest"] = e.Cfg.Host.SSLDigest
	}

	if len(pairs) > 0 {
		if err := e.SetLC(pairs); err != nil {
			return fmt.Errorf("writing localconfig: %w", err)
		}
	}

	caPEM := certFile(e, "conf", "ca", certCAFile)
	if !e.Facts.NewInstall && certIsFile(e, caPEM) && certCAExpired(e, caPEM) {
		e.Flags.NeedNewCert = true
	}

	var newCA string

	if e.Cfg.LDAP.Host == e.Cfg.Host.Hostname && (!certIsDir(e, caDir) || e.Flags.NeedNewCert) {
		newCA = certNewFlag
	}

	// A CA that is downloaded or created needs a new self-signed certificate.
	if !certIsDir(e, caDir) {
		e.Flags.NeedNewCert = true
	}

	if err := certZmcertmgr(e, certArgs(certCmdCreateCA, newCA)...); err != nil {
		return fmt.Errorf("creating the CA: %w", err)
	}

	if err := certZmcertmgr(e, "deployca", "-localonly"); err != nil {
		return fmt.Errorf("deploying the CA to %s: %w", certFile(e, "conf", "ca"), err)
	}

	return nil
}

// certCAExpired reports whether the CA is expired (the "error 10" of openssl
// verify of the CA against itself).
func certCAExpired(e *Env, caPEM string) bool {
	data, err := e.Sys.FS.ReadFile(caPEM)
	if err != nil {
		return false
	}

	certs, err := certParseCerts(data)

	return err == nil && len(certs) > 0 && certs[0].NotAfter.Before(e.Sys.Clock.Now())
}

// StepConfigSaveCA ports configSaveCA (setup.pl 4854-4870): store the CA in
// LDAP.
func StepConfigSaveCA() Step {
	return Step{
		Name:  certStageSaveCA,
		Title: "Save the CA in LDAP",
		Done:  func(e *Env) (bool, error) { return certStageDone(e, certStageSaveCA), nil },
		Apply: func(e *Env) error { return certZmcertmgr(e, "deployca") },
	}
}

// certTarget is a package whose certificate configCreateCert creates.
type certTarget struct {
	pkg   string
	title string
	// have lists files whose presence means the package has a certificate.
	have func(e *Env) string
}

func certTargets() []certTarget {
	return []certTarget{
		{PkgAppserver, PkgAppserver, certMailboxdKeystore},
		{certPkgDirectory, certPkgDirectory, func(e *Env) string {
			return certFile(e, "conf", "slapd.crt")
		}},
		{PkgMTA, "carbonio-mta", func(e *Env) string { return certFile(e, "conf", "smtpd.crt") }},
		{certPkgProxy, certPkgProxy, func(e *Env) string { return certFile(e, "conf", "nginx.crt") }},
	}
}

func certMailboxdDir(e *Env) string {
	if v, _ := e.LCValue("mailboxd_directory"); v != "" {
		return v
	}

	return e.Paths.Path("mailboxd")
}

func certMailboxdKeystore(e *Env) string {
	if v, _ := e.LCValue("mailboxd_keystore"); v != "" {
		return v
	}

	if certIsDir(e, e.Paths.Path("mailboxd")) {
		return certMailboxdDir(e) + "/etc/keystore"
	}

	return certFile(e, "conf", "keystore")
}

// StepConfigCreateCert ports configCreateCert (setup.pl 4872-5004).
//
// On an existing install it decides Flags.CertType and Flags.NeedNewCert from
// zmcertmgr verifycrt. Deviation: once `createcrt -new` ran in this step it is
// not run again for the next package; the later packages share the same
// certificate, so setup.pl regenerated it to no effect.
func StepConfigCreateCert() Step {
	return Step{
		Name:  certStageCreateCert,
		Title: "Create SSL certificates",
		Done: func(e *Env) (bool, error) {
			return certStageDone(e, certStageCreateCert) && certIsDir(e, certFile(e, "ssl", "carbonio", "server")), nil
		},
		Apply: certApplyCreateCert,
	}
}

func certApplyCreateCert(e *Env) error {
	if !e.Facts.NewInstall {
		if err := certDetectType(e); err != nil {
			return err
		}
	}

	serverCrt := certFile(e, "ssl", "carbonio", "server", "server.crt")
	ranNew := false

	for _, t := range certTargets() {
		if !e.Facts.IsInstalled(t.pkg) {
			continue
		}

		opt, create, err := certCreateOpt(e, t, serverCrt, ranNew)
		if err != nil {
			return err
		}

		if !create {
			continue
		}

		if err := certZmcertmgr(e, certArgs(certCmdCreateCRT, opt)...); err != nil {
			return fmt.Errorf("creating the %s certificate: %w", t.title, err)
		}

		ranNew = ranNew || opt == certNewFlag
	}

	return nil
}

// certCreateOpt decides whether zmcertmgr createcrt runs for t and with which
// flag: the need flag when the package has no certificate, -new to replace
// a self-signed one (once per run, ranNew), nothing otherwise.
func certCreateOpt(e *Env, t certTarget, serverCrt string, ranNew bool) (opt string, create bool, err error) {
	switch {
	case !certIsFile(e, t.have(e)) && !certIsFile(e, serverCrt):
		if t.pkg == PkgAppserver {
			if err := certPrepareMailboxd(e); err != nil {
				return "", false, err
			}
		}

		e.Log.Info("Creating SSL %s certificate", t.title)

		return certNeedFlag(e), true, nil
	case e.Flags.NeedNewCert && certType(e) == certTypeSelf:
		if ranNew {
			return "", false, nil
		}

		e.Log.Info("Creating new %s SSL certificate", t.title)

		return certNewFlag, true, nil
	default:
		return "", false, nil
	}
}

// certDetectType sets Flags.CertType and Flags.NeedNewCert from the installed
// certificates (setup.pl 4879-4895).
func certDetectType(e *Env) error {
	commErr := certVerifyCrt(e, certTypeComm)
	if commErr == nil {
		e.Flags.CertType = certTypeComm
		e.Flags.NeedNewCert = false

		return nil
	}

	if _, ok := errors.AsType[*CmdError](commErr); !ok {
		return commErr
	}

	selfErr := certVerifyCrt(e, certTypeSelf)
	if selfErr == nil {
		return nil
	}

	if _, ok := errors.AsType[*CmdError](selfErr); !ok {
		return selfErr
	}

	e.Log.Info("No valid SSL certificates were found.")
	e.Log.Info("New self-signed certificates will be generated and installed.")

	e.Flags.NeedNewCert = true
	e.Flags.CertType = certTypeSelf

	return nil
}

// certVerifyCrt is `zmcertmgr verifycrt <kind>`, quietly: an error that is a
// *CmdError says the certificate does not verify.
func certVerifyCrt(e *Env, kind string) error {
	args := []string{certCmdVerifyCRT, kind}

	status, _ := certRunMgr(e, args)
	if status != 0 {
		return &CmdError{Cmd: "zmcertmgr verifycrt " + kind, Code: status, quiet: true}
	}

	return nil
}

// certPrepareMailboxd creates the mailboxd directory the keystore lives in
// when there is none (setup.pl 4901-4903).
func certPrepareMailboxd(e *Env) error {
	dir := certMailboxdDir(e)
	if certIsDir(e, dir) {
		return nil
	}

	if err := e.Root("mkdir", "-p", dir+"/etc"); err != nil {
		return err
	}

	if err := e.Root("chown", "-R", "zextras:zextras", dir); err != nil {
		return err
	}

	return e.Root("chmod", "744", dir+"/etc")
}

// StepConfigSaveCert ports configSaveCert (setup.pl 5006-5024): store the
// server certificate in LDAP. It does nothing when there is no certificate.
func StepConfigSaveCert() Step {
	return Step{
		Name:  certStageSaveCert,
		Title: "Save the SSL certificate in LDAP",
		When: func(e *Env) bool {
			return certIsFile(e, certFile(e, "ssl", "carbonio", "server", "server.crt"))
		},
		Done: func(e *Env) (bool, error) { return certStageDone(e, certStageSaveCert), nil },
		Apply: func(e *Env) error {
			return certZmcertmgr(e, "savecrt", certType(e))
		},
	}
}

// certInstall describes one of the four deploy stages of configInstallCert.
type certInstall struct {
	stage string
	pkg   string
	title string
	// have is true when the package already has its certificate files.
	have func(e *Env) bool
	// restartLDAP restarts the local LDAP after the deploy.
	restartLDAP bool
}

func (c *certInstall) step() Step {
	return Step{
		Name:  c.stage,
		Title: c.title,
		When:  func(e *Env) bool { return e.Facts.IsInstalled(c.pkg) },
		Done: func(e *Env) (bool, error) {
			if e.Flags.NeedNewCert {
				return false, nil
			}

			return certStageDone(e, c.stage) || c.have(e), nil
		},
		Apply: func(e *Env) error {
			if err := certZmcertmgr(e, "deploycrt", certType(e)); err != nil {
				return fmt.Errorf("installing the certificate: %w", err)
			}

			if c.restartLDAP && certLDAPConfigured(e) {
				return certRestartLDAP(e)
			}

			return nil
		},
	}
}

// certLDAPConfigured is setup.pl $ldapConfigured: the LDAP database exists.
func certLDAPConfigured(e *Env) bool {
	return certIsFile(e, e.Paths.Path("data", "ldap", "mdb", "db", "data.mdb"))
}

func certAnyFile(e *Env, names ...string) bool {
	for _, n := range names {
		if certIsFile(e, certFile(e, "conf", n)) {
			return true
		}
	}

	return false
}

// StepConfigInstallCertStore ports the carbonio-appserver part of
// configInstallCert (setup.pl 5028-5051): deploy the mailboxd keystore.
func StepConfigInstallCertStore() Step {
	c := certInstall{
		stage: certStageInstallStor, pkg: PkgAppserver, title: "Install mailboxd SSL certificates",
		have: func(e *Env) bool { return certIsFile(e, certMailboxdKeystore(e)) },
	}

	return c.step()
}

// StepConfigInstallCertMTA ports the carbonio-mta part of configInstallCert
// (setup.pl 5053-5075).
func StepConfigInstallCertMTA() Step {
	c := certInstall{
		stage: certStageInstallMTA, pkg: PkgMTA, title: "Install MTA SSL certificates",
		have: func(e *Env) bool { return certAnyFile(e, "smtpd.key", "smtpd.crt") },
	}

	return c.step()
}

// StepConfigInstallCertLDAP ports the carbonio-directory-server part of
// configInstallCert (setup.pl 5077-5100). After a deploy it restarts a
// configured local LDAP server.
func StepConfigInstallCertLDAP() Step {
	c := certInstall{
		stage: certStageInstallLDAP, pkg: certPkgDirectory, title: "Install LDAP SSL certificate",
		have:        func(e *Env) bool { return certAnyFile(e, "slapd.key", "slapd.crt") },
		restartLDAP: true,
	}

	return c.step()
}

// StepConfigInstallCertProxy ports the carbonio-proxy part of
// configInstallCert (setup.pl 5102-5123).
func StepConfigInstallCertProxy() Step {
	c := certInstall{
		stage: certStageInstallPrxy, pkg: certPkgProxy, title: "Install proxy SSL certificate",
		have: func(e *Env) bool { return certAnyFile(e, "nginx.key", "nginx.crt") },
	}

	return c.step()
}

// StepGenerateDHParam ports the dhparam block of applyConfig (setup.pl
// 5884-5900): on a new install write conf/dhparam.pem.crb and record it in
// zimbraSSLDHParam (nginx reads it from there). Deviations: the file holds the
// fixed RFC 7919 ffdhe2048 group (the one Mozilla's TLS guidelines recommend)
// written in process, instead of a group `openssl dhparam 2048` generated in
// 15-30 seconds, and no openssl process runs; a failure fails the run
// (setup.pl called a bare exit(), status 0); the file is replaced atomically,
// so an interrupted run leaves no truncated key.
func StepGenerateDHParam() Step {
	return Step{
		Title: "Generate dhparam key",
		When:  func(e *Env) bool { return e.Facts.NewInstall },
		Done: func(e *Env) (bool, error) {
			if !certIsFile(e, certFile(e, "conf", certDHParamFile)) {
				return false, nil
			}

			gc, err := certGlobalConfig(e)
			if err != nil {
				return false, fmt.Errorf("reading the global config: %w", err)
			}

			// zimbraSSLDHParam is binary: "mcf zimbraSSLDHParam <path>"
			// stores the file's content, not its path.
			data, err := e.Sys.FS.ReadFile(certFile(e, "conf", certDHParamFile))
			if err != nil {
				return false, fmt.Errorf("reading the dhparam key: %w", err)
			}

			return gc[certDHParamAttr] != "" && gc[certDHParamAttr] == string(data), nil
		},
		Apply: certApplyDHParam,
	}
}

// InstallDHParam writes conf/dhparam.pem.crb (ffdhe2048) unless present and
// records it in zimbraSSLDHParam: the step's work, shared with the setup.pl
// command.
func InstallDHParam(e *Env) error { return certApplyDHParam(e) }

func certApplyDHParam(e *Env) error {
	dest := certFile(e, "conf", certDHParamFile)

	if !certIsFile(e, dest) {
		data, err := dhGroupPEM(dhDefaultBits)
		if err != nil {
			return fmt.Errorf("failed to generate dhparam key: %w", err)
		}

		if err := e.Sys.FS.WriteFileAtomic(dest, data, certDHParamMode); err != nil {
			return fmt.Errorf("installing the dhparam key: %w", err)
		}

		if err := e.Root("chown", "zextras:zextras", dest); err != nil {
			return fmt.Errorf("installing the dhparam key: %w", err)
		}
	}

	return e.Prov("mcf", certDHParamAttr, dest)
}
