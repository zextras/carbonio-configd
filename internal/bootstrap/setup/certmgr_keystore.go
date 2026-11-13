// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/pavlo-v-chernykh/keystore-go/v4"
)

// certX509 is the certificate type of a Java keystore entry.
const certX509 = "X509"

// certJKS reads a Java keystore; a missing file is an empty store.
func (m *certMgr) certJKS(file string, pass []byte) (keystore.KeyStore, error) {
	ks := keystore.New(keystore.WithOrderedAliases())

	data, err := m.slurp(file)
	if errors.Is(err, os.ErrNotExist) {
		return ks, nil
	}

	if err != nil {
		return ks, err
	}

	if err := ks.Load(bytes.NewReader(data), pass); err != nil {
		return ks, errors.New(certKeytoolTampered)
	}

	return ks, nil
}

// storeJKS writes a Java keystore over file (atomically: it keeps its mode
// and owner; a new one is 0640, owned by zextras).
func (m *certMgr) storeJKS(ks keystore.KeyStore, file string, pass []byte) error {
	var buf bytes.Buffer

	if err := ks.Store(&buf, pass); err != nil {
		return err
	}

	return m.replaceFile(file, buf.Bytes(), certModeFile)
}

// createkeystore is createkeystore: the key and the certificates of a type
// go into the mailboxd keystore (JKS) under the alias of the mailboxd server,
// replacing what the alias held. The script made a PKCS12 file with openssl
// and let a Java class merge it into the keystore; here the entry is built
// directly, and no ssl/carbonio/jetty.pkcs12 is left behind.
func (m *certMgr) createkeystore(typ string) bool {
	m.debugf("createkeystore(%s)\n", typ)

	files := m.p.typed(typ)
	if !m.checkFiles(files.key, files.crt) {
		return false
	}

	server := m.lcGet("mailboxd_server")
	if server == "" {
		server = m.lcGet("zimbra_server_hostname")
	}

	server = strings.ToLower(server)
	pass := []byte(m.lcGet("mailboxd_keystore_password"))

	entry, err := m.keystoreEntry(files)
	if err != nil {
		return m.fail("openssl pkcs12 export of '%s' failed(1):\n%s\n", files.crt, err)
	}

	m.say("** Creating keystore '%s'\n", m.p.keystore)

	ks, err := m.certJKS(m.p.keystore, pass)
	if err == nil {
		ks.DeleteEntry(server)
		err = ks.SetPrivateKeyEntry(server, entry, pass)
	}

	if err == nil {
		err = m.storeJKS(ks, m.p.keystore, pass)
	}

	if err != nil {
		return m.fail("com.zimbra.cert.MyPKCS12Import to '%s' returned non-zero(1):\n%s\n",
			m.p.keystore, certErrMessage(err))
	}

	return true
}

// certErrMessage is the text of an error without the "open <file>:" prefix of
// a path error.
func certErrMessage(err error) string {
	if _, ok := errors.AsType[*os.PathError](err); ok {
		return certErrText(err)
	}

	return err.Error()
}

// keystoreEntry is the private key entry of a key and a certificate file: the
// key as PKCS#8 and the chain, the certificate of the key first and then the
// others of the file.
func (m *certMgr) keystoreEntry(files certTyped) (keystore.PrivateKeyEntry, error) {
	var entry keystore.PrivateKeyEntry

	keyPEM, err := m.slurp(files.key)
	if err != nil {
		return entry, err
	}

	key, err := certParseKey(keyPEM)
	if err != nil {
		return entry, err
	}

	crtPEM, err := m.slurp(files.crt)
	if err != nil {
		return entry, err
	}

	certs, err := certParseCerts(crtPEM)
	if err != nil {
		return entry, err
	}

	chain, err := certChainFor(key, certs)
	if err != nil {
		return entry, err
	}

	if entry.PrivateKey, err = x509.MarshalPKCS8PrivateKey(key); err != nil {
		return entry, err
	}

	entry.CreationTime = m.e.Sys.Clock.Now()

	for _, c := range chain {
		entry.CertificateChain = append(entry.CertificateChain, keystore.Certificate{Type: certX509, Content: c.Raw})
	}

	return entry, nil
}

// certChainFor puts the certificate of key first and the others after it.
func certChainFor(key crypto.Signer, certs []*x509.Certificate) ([]*x509.Certificate, error) {
	type equaler interface{ Equal(crypto.PublicKey) bool }

	pub, ok := key.Public().(equaler)
	if !ok {
		return nil, errors.New("unsupported key type")
	}

	for i, c := range certs {
		if pub.Equal(c.PublicKey) {
			chain := []*x509.Certificate{c}
			chain = append(chain, certs[:i]...)

			return append(chain, certs[i+1:]...), nil
		}
	}

	return nil, errors.New("no certificate matches the private key")
}

// keystoreToPEM is _keystoreToPEM: the certificate of the mailboxd alias of
// the keystore (what keytool -export wrote) goes to mailboxd.der, and as PEM
// to mailboxd.pem.
func (m *certMgr) keystoreToPEM() bool {
	alias := m.lcGet("mailboxd_server")

	der, err := m.exportCert(m.p.keystore, m.lcGet("mailboxd_keystore_password"), alias)
	if err == nil {
		err = m.writeFile(m.p.mailboxdDER, der, certModeFile)
	}

	if err != nil {
		return m.fail("mailboxd: export to '%s' failed(1):\n%s\n", m.p.mailboxdDER, certErrMessage(err))
	}

	if _, err = x509.ParseCertificate(der); err == nil {
		err = m.writeFile(m.p.mailboxdPEM, certPEMBlock("CERTIFICATE", der), certModeFile)
	}

	if err != nil {
		return m.fail("mailboxd: convert to '%s' failed(1):\n%s\n", m.p.mailboxdPEM, certErrMessage(err))
	}

	return true
}

// exportCert is keytool -export: the certificate of an alias of a keystore.
func (m *certMgr) exportCert(file, pass, alias string) ([]byte, error) {
	ts, err := m.openTrustStore(file, pass, false)
	if err != nil {
		return nil, err
	}

	der, ok := ts.get(alias)
	if !ok {
		return nil, errors.New(certKeytoolNoAlias(alias))
	}

	return der, nil
}

// certKeytoolNoAlias is the message of keytool for an alias a keystore does
// not have.
func certKeytoolNoAlias(alias string) string {
	return "keytool error: java.lang.Exception: Alias <" + alias + "> does not exist"
}

// addcacert is addcacert: the certificate of the file is imported into the
// truststore (cacerts) under the alias, replacing the alias, as keytool
// -delete and -import -noprompt did.
func (m *certMgr) addcacert(crtf, alias string) (bool, error) {
	if crtf == "" {
		return false, errors.New("addcacert: no certfile argument specified")
	}

	m.debugf("addcacert(%s %s)\n", crtf, alias)

	if !m.checkFiles(crtf) {
		return false, nil
	}

	if alias == "" {
		alias = "zcs-user-" + strings.TrimSuffix(filepath.Base(crtf), filepath.Ext(crtf))
	}

	m.say("** Importing cert '%s' as '%s' into cacerts '%s'\n", crtf, alias, m.p.cacerts)

	pass := m.lcGet("mailboxd_truststore_password")

	ts, err := m.openTrustStore(m.p.cacerts, pass, true)
	if err == nil {
		err = m.importCert(ts, crtf, alias, pass)
	}

	if err != nil {
		return m.fail(certImportFailed, crtf, alias, m.p.cacerts, certErrMessage(err)), nil
	}

	m.say("** NOTE: restart mailboxd to use the imported certificate.\n")

	return true, nil
}

// certImportFailed is the error of an import into the truststore: the file,
// the alias, the truststore and why.
const certImportFailed = "importing '%s' as '%s' into cacerts '%s' failed: %s\n"

// importCert is keytool -delete and -import -noprompt: the certificate of
// the file replaces the alias, and the store is written.
func (m *certMgr) importCert(ts trustStore, crtf, alias, pass string) error {
	data, err := m.slurp(crtf)
	if err != nil {
		return err
	}

	cert, err := certFromFile(data)
	if err != nil {
		return err
	}

	ts.remove(alias)
	ts.set(alias, cert)

	return m.saveTrustStore(ts, m.p.cacerts, pass)
}
