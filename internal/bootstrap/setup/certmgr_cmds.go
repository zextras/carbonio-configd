// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"crypto"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
)

// Names that the commands use more than once.
const (
	certLCHostname   = "zimbra_server_hostname"
	certMsgReqFailed = "openssl req failed(1):\n%s\n"
	certIndexAttr    = ".attr"
)

// altNames is the subject alternative names of the certificates the command
// makes: the host name (unless -noDefaultSubjectAltName) and the names of
// -subjectAltNames, once each.
func (m *certMgr) altNames() []string {
	var names []string

	if !m.o.NoDefaultSAN {
		names = append(names, m.lc[certLCHostname])
	}

	return certUniqueNames(append(names, m.sans...))
}

// caNames is the subject alternative names of the CA certificate: the host
// name and -subjectAltNames.
func (m *certMgr) caNames() []string {
	return certUniqueNames(append([]string{m.lc[certLCHostname]}, m.sans...))
}

// cmdAddCACert is `addcacert <certfile>`.
func (m *certMgr) cmdAddCACert(args []string) (bool, error) {
	return m.addcacert(args[0], "")
}

// createca is createca: the CA of the node, made or fetched from LDAP.
func (m *certMgr) createca() (bool, error) {
	m.debugf("createca\n")

	// -new makes a CA on the LDAP master only; while a new install
	// bootstraps ldap_is_master is false, so the host name decides.
	if m.isNew && strings.EqualFold(m.lc["ldap_is_master"], "false") &&
		!strings.EqualFold(m.lc[certLCHostname], m.lc["ldap_host"]) {
		return m.fail("'-new' is only allowed on an ldap master\n"), nil
	}

	if !m.initSSLDirs() {
		return false, nil
	}

	if m.isNew || !m.isFile(m.p.confCAFile) {
		m.createConf(m.p.confCAFile, m.caNames()...)
	}

	if !m.o.Newkey {
		m.fetchCAFiles()
	}

	switch {
	case m.o.Newkey:
		return m.docreateca(true)
	case m.isNew:
		return m.docreateca(!m.hasData(m.p.caKey))
	default:
		return m.isPrivateKey(m.p.caKey), nil
	}
}

// certCAFetch is a file createca takes from LDAP when it is missing.
type certCAFetch struct {
	file, desc, key, loc string
	perm                 fs.FileMode
	concat               bool
}

// fetchCAFiles reuses the CA of LDAP: the files that are empty are
// retrieved, so a new CA keeps the key (and the trust) of the old one.
func (m *certMgr) fetchCAFiles() {
	files := []certCAFetch{
		{m.p.caCrt, "CA cert", certAttrCACert, certScopeGlobal, certModeFile, false},
		{m.p.caKey, "CA private key", certAttrCAKey, certScopeGlobal, certModeKey, false},
		{m.p.commCACrt, "Commercial CA cert", certAttrSSLCert, m.lc["ldap_host"], certModeFile, true},
	}

	for i, f := range files {
		if i != 1 && m.isNew {
			m.debugf("createca skipping %s\n", f.desc)

			continue
		}

		if m.hasData(f.file) {
			m.say("** Using %s in '%s'\n", f.desc, f.file)

			continue
		}

		m.retrieveCAFile(&f)
	}
}

func (m *certMgr) retrieveCAFile(f *certCAFetch) {
	m.say("** Retrieving %s from LDAP... ", f.desc)

	val, ok := m.getConfKey(f.key, f.loc)
	if ok {
		m.say("ok\n")
	} else {
		m.say("failed\n")
	}

	if val == "" {
		return
	}

	if f.concat {
		// The first certificate is the server's own.
		certs := certConcat(val)
		val = strings.Join(certs[min(1, len(certs)):], "")

		if val == "" {
			return
		}
	}

	m.say("** Creating '%s'\n", f.file)

	if err := m.writeFile(f.file, []byte(val), f.perm); err != nil {
		m.fail("createca: open '%s' failed: %s\n", f.file, certErrText(err))
	}
}

// isPrivateKey is _is_private_key.
func (m *certMgr) isPrivateKey(file string) bool {
	if !m.hasData(file) {
		return m.fail("createca: empty key '%s'\n", file)
	}

	data, err := m.slurp(file)
	if err != nil {
		return m.fail("createca: read '%s' failed: %s\n", file, certErrText(err))
	}

	return strings.Contains(string(data), "PRIVATE KEY")
}

// docreateca is docreateca: the self-signed CA certificate, with a new key
// or the one of ca.key.
func (m *certMgr) docreateca(newKey bool) (bool, error) {
	m.debugf("docreateca\n")

	if !m.isFile(m.p.confCAFile) {
		m.createConf(m.p.confCAFile, m.caNames()...)
	}

	if ok, err := m.createCASerial(); !ok || err != nil {
		return false, err
	}

	kind := "existing "
	if newKey {
		kind = "new "
	}

	m.say("** Creating CA with %sprivate key %s\n", kind, m.p.caKey)

	subject, err := certParseSubject(m.subject)
	if err != nil {
		return m.fail(certMsgReqFailed, err), nil
	}

	key, err := m.caKeyFor(newKey)
	if err != nil {
		return m.fail(certMsgReqFailed, err), nil
	}

	issued, err := certSelfSign(key, subject, m.caNames(), m.days, m.digest, m.e.Sys.Clock.Now())
	if err == nil {
		err = m.writeFile(m.p.caCrt, certPEMBlock("CERTIFICATE", issued.DER), certModeFile)
	}

	if err != nil {
		return m.fail(certMsgReqFailed, certErrMessage(err)), nil
	}

	return true, nil
}

// caKeyFor generates the CA key into ca.key, or reads the existing one.
func (m *certMgr) caKeyFor(newKey bool) (crypto.Signer, error) {
	if !newKey {
		data, err := m.slurp(m.p.caKey)
		if err != nil {
			return nil, err
		}

		return certParseKey(data)
	}

	return m.newKeyFile(m.p.caKey)
}

// newKeyFile generates an RSA key of the keysize and writes it, PKCS#8 and
// readable by its owner only, as openssl -newkey does.
func (m *certMgr) newKeyFile(file string) (crypto.Signer, error) {
	key, err := m.keyGen(m.keysize)
	if err != nil {
		return nil, err
	}

	data, err := certKeyPEM(key)
	if err != nil {
		return nil, err
	}

	if err := m.writeFile(file, data, certModeKey); err != nil {
		return nil, err
	}

	return key, nil
}

// createcrt is createcrt: the self-signed server certificate, made with the
// CA of the node unless a valid one is there or LDAP has one.
func (m *certMgr) createcrt() (bool, error) {
	m.debugf("createcrt\n")

	if !m.backupSSLDirs() || !m.initSSLDirs() {
		return false, nil
	}

	if ok, err := m.createCASerial(); !ok || err != nil {
		return false, err
	}

	m.createConf(m.p.confCert, m.altNames()...)

	if done, ok := m.reuseSelfCert(); done {
		return ok, nil
	}

	if m.o.Allservers {
		m.isNew, m.sans = true, m.allServers()
	}

	if !m.hasData(m.p.self.csr) || m.isNew {
		if _, err := m.createcsr(certTypeSelf); err != nil {
			return false, err
		}
	}

	return m.signSelfCert()
}

// signSelfCert is the end of createcrt: the request of the server is signed
// with the CA, on a database that is not marked as unique-subject.
func (m *certMgr) signSelfCert() (bool, error) {
	if m.isFile(m.p.index + certIndexAttr) {
		if err := m.e.Sys.FS.Remove(m.p.index + certIndexAttr); err != nil {
			m.debugf("removing '%s': %v\n", m.p.index+certIndexAttr, err)
		}
	}

	m.say("** Signing cert request %s\n", m.p.self.csr)

	if m.hasData(m.p.index) || !m.exists(m.p.index) {
		if err := m.writeFile(m.p.index, nil, certModeFile); err != nil {
			return false, fmt.Errorf("createcrt: create empty '%s' failed: %s", m.p.index, certErrText(err))
		}
	}

	if msg := m.signSelf(m.p.self.csr, m.p.self.crt, m.altNames()); msg != "" {
		return m.fail("openssl ca failed(1):\n%s\n", msg), nil
	}

	return true, nil
}

// reuseSelfCert is the start of createcrt: a valid server certificate that is
// there, or in LDAP, is kept. done says createcrt is over, with ok.
func (m *certMgr) reuseSelfCert() (done, ok bool) {
	crtf := m.p.self.crt

	if m.hasData(crtf) && !m.isNew && m.verifycrt(certTypeSelf, "", "", "") {
		m.say("** %s already exists.\n", crtf)

		return true, true
	}

	if m.isNew {
		return false, false
	}

	got, err := m.getcrt(certTypeSelf)
	if err != nil {
		return true, false
	}

	if !got {
		// LDAP could not be read: the local files are kept and, as the
		// script did, a new certificate is made.
		return false, false
	}

	if m.hasData(crtf) && m.verifycrt(certTypeSelf, "", "", "") {
		m.say("** %s downloaded from ldap.\n", crtf)

		return true, true
	}

	return false, false
}

// cmdCreateCSR is `createcsr <self|comm>`.
func (m *certMgr) cmdCreateCSR(args []string) (bool, error) { return m.createcsr(args[0]) }

// createcsr is createcsr: a key and a certificate request for download.
func (m *certMgr) createcsr(typ string) (bool, error) {
	m.debugf("createcsr(%s)\n", typ)
	m.say("** Generating a server CSR of type '%s' for download\n", typ)

	files := m.p.typed(typ)
	if m.isFile(files.csr) && !m.isNew {
		return m.fail("Certificate Signing Request already exists: %s\n", files.csr), nil
	}

	m.createConf(m.p.confCert, m.altNames()...)

	if !m.backupSSLDirs() || !m.initSSLDirs() {
		return false, nil
	}

	// A new CA is not forced here, and a CA that cannot be had (LDAP down)
	// does not stop the request.
	was := m.isNew
	m.isNew = false
	_, err := m.createca()
	m.isNew = was

	if err != nil {
		return false, err
	}

	m.createServerCertReq(typ)

	return true, nil
}

// createServerCertReq is createServerCertReq: the key and the request, and
// the key in LDAP.
func (m *certMgr) createServerCertReq(typ string) bool {
	files := m.p.typed(typ)

	m.createConf(m.p.confCert, m.altNames()...)
	m.say("** Creating server cert request %s with keysize=%d digest=%s\n", files.csr, m.keysize, m.digest)

	subject, err := certParseSubject(m.subject)
	if err != nil {
		return m.fail(certMsgReqFailed, err)
	}

	key, err := m.newKeyFile(files.key)
	if err == nil {
		var csr []byte

		if csr, err = certNewCSR(key, subject, m.altNames(), m.digest); err == nil {
			err = m.writeFile(files.csr, certPEMBlock("CERTIFICATE REQUEST", csr), certModeFile)
		}
	}

	if err != nil {
		return m.fail(certMsgReqFailed, certErrMessage(err))
	}

	return m.saveConfKey(certAttrSSLKey, certScopeServer, files.key)
}

// deployca is deployca: the CA goes to LDAP (on the master), to the
// truststore and to conf/ca.
func (m *certMgr) deployca() (bool, error) {
	m.debugf("deployca\n")

	if !m.initSSLDirs() {
		return false, nil
	}

	if !m.o.Localonly && m.isMaster() {
		if !m.saveConfKey(certAttrCACert, certScopeGlobal, m.p.caCrt) ||
			!m.saveConfKey(certAttrCAKey, certScopeGlobal, m.p.caKey) {
			return false, nil
		}
	}

	// LDAP's TLS negotiation fails if the CA is added to the truststore first.
	if ok, err := m.addcacert(m.p.caCrt, "my_ca"); !ok || err != nil {
		return false, err
	}

	return m.installCA()
}

// errCertFileNotAllowed is the die of deploycrt for files given with "self"
// (certmgr.pl 1030). The command line takes none or both files, so the other
// dies of the script (1032, 1048: one file only) cannot happen.
var errCertFileNotAllowed = errors.New("certfile argument is not allowed for type 'self'")

// deploycrt is deploycrt: the certificate of a type is installed for the
// services (a commercial one is first checked and completed with its chain).
func (m *certMgr) deploycrt(args []string) (bool, error) {
	typ := args[0]
	files := m.p.typed(typ)
	crtf, caPEM, given := certDeployFiles(files.crt, args)

	if typ == certTypeSelf && given {
		return false, errCertFileNotAllowed
	}

	if !m.initSSLDirs() {
		return false, nil
	}

	crtf, ok, err := m.prepareCert(typ, crtf, caPEM, given)
	if !ok || err != nil {
		return false, err
	}

	// The errors of saving the certificate to LDAP do not stop the deploy.
	failed := !m.o.Localonly && m.isMaster() && !m.savecrt(typ)

	if !m.installServices(typ, files.key, crtf) {
		return false, nil
	}

	if ok, err := m.installCA(); !ok || err != nil {
		return false, err
	}

	if m.o.Allservers && !m.deployRemote(typ) {
		failed = true
	}

	return !failed, nil
}

// certDeployFiles are the certificate and the chain file of the command line
// of deploycrt (the arguments after the type); def is the certificate of the
// type, which is used when none is given.
func certDeployFiles(def string, args []string) (crtf, caPEM string, given bool) {
	crtf, given = def, len(args) > 1

	if given {
		crtf = args[1]
	}

	if len(args) > 2 {
		caPEM = args[2]
	}

	return crtf, caPEM, given
}

// prepareCert makes sure the certificate to deploy is there: a self-signed
// one is created when missing; a commercial one is taken from the files given
// or from LDAP, verified, and gets its chain appended. It returns the
// certificate file to deploy.
func (m *certMgr) prepareCert(typ, crtf, caPEM string, given bool) (out string, ok bool, err error) {
	files := m.p.typed(typ)

	if typ == certTypeSelf {
		if m.hasData(crtf) {
			return crtf, true, nil
		}

		ok, err := m.createcrt()

		return crtf, ok, err
	}

	if given {
		if !m.fixNewline(crtf, caPEM) || !m.keepFirstCert(crtf) {
			return crtf, false, nil
		}
	} else {
		if got, err := m.getcrt(typ); err != nil || !got {
			return crtf, false, err
		}

		caPEM = files.ca
	}

	if !m.checkFiles(files.key, crtf, caPEM) || !m.verifycrt(typ, files.key, crtf, caPEM) {
		return crtf, false, nil
	}

	return m.completeComm(files, crtf, caPEM, given)
}

// completeComm copies the commercial files to their place, appends the chain
// to the certificate and adds it to the truststore.
func (m *certMgr) completeComm(files certTyped, crtf, caPEM string, given bool) (out string, ok bool, err error) {
	if given && crtf != files.crt {
		if !m.copyGiven(crtf, files.crt, certModeFile) {
			return crtf, false, nil
		}

		crtf = files.crt
	}

	if given && caPEM != files.ca && !m.copyGiven(caPEM, files.ca, certModeFile) {
		return crtf, false, nil
	}

	if !m.appendChain(caPEM, crtf) {
		return crtf, false, nil
	}

	ok, err = m.addcacert(files.ca, "")

	return crtf, ok, err
}

// deployNames are the services -deploy names, each once, sorted.
func (m *certMgr) deployNames() []string {
	set := map[string]bool{}

	for _, d := range m.deploy {
		for _, s := range m.services(d) {
			set[s] = true
		}
	}

	names := make([]string, 0, len(set))
	for s := range set {
		names = append(names, s)
	}

	slices.Sort(names)

	return names
}

// installServices copies the certificate and the key to the services, or
// builds the keystore of mailboxd.
func (m *certMgr) installServices(typ, keyf, crtf string) bool {
	for _, name := range m.deployNames() {
		if name == certSvcMailboxd {
			if m.lc["mailboxd_server"] == "" {
				m.debugf("deploycrt: skip non %s server\n", name)

				continue
			}

			if !m.createkeystore(typ) {
				return false
			}

			continue
		}

		dcrt, dkey := m.p.svcCrt(name), m.p.svcKey(name)
		m.say("** Installing %s certificate '%s' and key '%s'\n", name, dcrt, dkey)

		if !m.copyFile(crtf, dcrt, certModeFile) || !m.copyFile(keyf, dkey, certModeFile) {
			return false
		}
	}

	m.say("** NOTE: restart services to use the new certificates.\n")

	return true
}

// deployRemote runs getcrt and deploycrt on the other servers, through zmrc;
// a server that fails does not stop the others.
func (m *certMgr) deployRemote(typ string) bool {
	ok := true
	me := m.lc[certLCHostname]

	for _, svr := range m.allServers() {
		if svr = strings.ToLower(svr); svr == me {
			continue
		}

		if !m.remote(svr, "zmcertmgr getcrt "+typ+" -allservers") {
			ok = false

			continue
		}

		if !m.remote(svr, "zmcertmgr deploycrt "+typ) {
			ok = false
		}
	}

	return ok
}

// cmdGetCrt is `getcrt <self|comm>`.
func (m *certMgr) cmdGetCrt(args []string) (bool, error) { return m.getcrt(args[0]) }

// scope is the LDAP place the certificate of -allservers or of this server is
// kept in.
func (m *certMgr) scope() string {
	if m.o.Allservers {
		return certScopeGlobal
	}

	return certScopeServer
}

// getcrt is getcrt: the certificate and key (and for a commercial
// certificate its chain) of LDAP are written to their files; a file whose
// value LDAP does not have is removed.
func (m *certMgr) getcrt(typ string) (bool, error) {
	m.debugf("getcrt(%s)\n", typ)

	files := m.p.typed(typ)
	data := map[string]string{}

	// An unreadable directory is not an empty value: the files that are
	// there stay, whatever the directory would have said.
	for _, kv := range []struct{ file, attr string }{{files.crt, certAttrSSLCert}, {files.key, certAttrSSLKey}} {
		val, ok := m.getConfKey(kv.attr, m.scope())
		if !ok {
			return m.fail("getcrt: cannot read '%s' from LDAP, keeping the local files\n", kv.attr), nil
		}

		data[kv.file] = val
	}

	if typ == certTypeComm && data[files.crt] != "" {
		certs := certConcat(data[files.crt])
		data[files.ca] = strings.Join(certs[min(1, len(certs)):], "")
	}

	for _, file := range slices.Sorted(maps.Keys(data)) {
		if m.isFile(file) {
			m.debugf("removing '%s'\n", file)

			_ = m.e.Sys.FS.Remove(file)
		}

		if data[file] == "" {
			continue
		}

		m.say("** Creating %s\n", file)

		if err := m.writeFile(file, []byte(data[file]), certModeFile); err != nil {
			return false, fmt.Errorf("getcrt: open '%s' failed: %s", file, certErrText(err))
		}
	}

	return true, nil
}

// cmdSaveCrt is `savecrt <self|comm>`.
func (m *certMgr) cmdSaveCrt(args []string) (bool, error) { return m.savecrt(args[0]), nil }

// savecrt is savecrt: the certificate and the key go to LDAP.
func (m *certMgr) savecrt(typ string) bool {
	m.debugf("savecrt(%s)\n", typ)

	files := m.p.typed(typ)

	return m.saveConfKey(certAttrSSLCert, m.scope(), files.crt) &&
		m.saveConfKey(certAttrSSLKey, m.scope(), files.key)
}
