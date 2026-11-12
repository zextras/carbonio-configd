// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"crypto"
	"crypto/x509"
	"fmt"
	"math/big"
	"path/filepath"
	"strconv"
	"strings"
)

// certIndexTime is the date format of index.txt (UTCTime).
const certIndexTime = "060102150405Z"

// certOSSLError is a message of openssl ca, kept as openssl words it.
type certOSSLError string

func (e certOSSLError) Error() string { return string(e) }

// createCASerial is createCASerial: ca.srl gets the time as its next serial
// number (the digits of the time, which openssl reads as hex).
func (m *certMgr) createCASerial() (bool, error) {
	m.debugf("createCASerial\n")

	if !m.initSSLDirs() {
		return false, nil
	}

	srl := strconv.FormatInt(m.e.Sys.Clock.Now().Unix(), 10) + "\n"
	if err := m.writeFile(m.p.caSrl, []byte(srl), certModeFile); err != nil {
		return false, fmt.Errorf("createCASerial: open '%s' failed: %s", m.p.caSrl, certErrText(err))
	}

	return true, nil
}

// certSerialText is how openssl writes a serial number: upper case hex with
// an even number of digits.
func certSerialText(n *big.Int) string {
	s := strings.ToUpper(n.Text(16))
	if len(s)%2 == 1 {
		s = "0" + s
	}

	return s
}

// signSelf is `openssl ca -batch -notext -policy policy_anything -extensions
// v3_req` with the CA of the node: the request of the server is signed with
// the CA key and the certificate is written to crtf; the CA database
// (ca.srl, index.txt, newcerts/) is updated as openssl does. It returns the
// text of the error openssl would have printed, "" on success.
func (m *certMgr) signSelf(csrf, crtf string, names []string) string {
	ca, key, err := m.loadCA()
	if err != nil {
		return err.Error()
	}

	csrPEM, err := m.slurp(csrf)
	if err != nil {
		return "Error opening certificate request " + csrf + ": " + certErrText(err)
	}

	csr, err := certParseCSR(csrPEM)
	if err != nil {
		return "Error reading certificate request in " + csrf
	}

	subject, err := certParseDN(csr.RawSubject)
	if err != nil {
		return err.Error()
	}

	if !subject.has("CN") {
		return "The commonName field needed to be supplied and was missing"
	}

	serial, oldSrl, err := m.nextSerial()
	if err != nil {
		return err.Error()
	}

	issued, err := certSignCSR(&certSignReq{
		CSR: csr, SANs: names, CA: ca, CAKey: key, Serial: serial,
		Days: m.days, Digest: m.digest, Now: m.e.Sys.Clock.Now(),
	})
	if err != nil {
		return err.Error()
	}

	if err := m.recordIssued(issued, serial, oldSrl, crtf); err != nil {
		return certErrText(err)
	}

	return ""
}

// loadCA reads the CA certificate and key of the node.
func (m *certMgr) loadCA() (*x509.Certificate, crypto.Signer, error) {
	crtPEM, err := m.slurp(m.p.caCrt)
	if err != nil {
		return nil, nil, certOSSLError("Error opening CA certificate " + m.p.caCrt + ": " + certErrText(err))
	}

	certs, err := certParseCerts(crtPEM)
	if err != nil || len(certs) == 0 {
		return nil, nil, certOSSLError("Error reading CA certificate in " + m.p.caCrt)
	}

	keyPEM, err := m.slurp(m.p.caKey)
	if err != nil {
		return nil, nil, certOSSLError("Error opening CA private key " + m.p.caKey + ": " + certErrText(err))
	}

	key, err := certParseKey(keyPEM)
	if err != nil {
		return nil, nil, certOSSLError("Error reading CA private key in " + m.p.caKey + ": " + err.Error())
	}

	return certs[0], key, nil
}

// nextSerial reads the serial number of ca.srl (hex).
func (m *certMgr) nextSerial() (n *big.Int, raw []byte, err error) {
	raw, err = m.slurp(m.p.caSrl)
	if err != nil {
		return nil, nil, certOSSLError("Error while loading serial number: " + m.p.caSrl)
	}

	n, ok := new(big.Int).SetString(strings.TrimSpace(string(raw)), 16)
	if !ok {
		return nil, nil, certOSSLError("Error while loading serial number: " + m.p.caSrl)
	}

	return n, raw, nil
}

// recordIssued writes the issued certificate and what openssl ca records of
// it: the next serial (the old one in ca.srl.old), the database line, and
// newcerts/<serial>.pem.
func (m *certMgr) recordIssued(issued *certIssued, serial *big.Int, oldSrl []byte, crtf string) error {
	pemData := certPEMBlock("CERTIFICATE", issued.DER)
	text := certSerialText(serial)

	subject, err := certParseDN(issued.Cert.RawSubject)
	if err != nil {
		return err
	}

	line := "V\t" + issued.Cert.NotAfter.UTC().Format(certIndexTime) + "\t\t" + text + "\tunknown\t" +
		subject.slash() + "\n"

	steps := []struct {
		name string
		data []byte
	}{
		{filepath.Join(m.p.newcerts, text+".pem"), pemData},
		{m.p.index + ".old", nil},
		{m.p.index, []byte(line)},
		{m.p.index + ".attr", []byte("unique_subject = yes\n")},
		{m.p.caSrl + ".old", oldSrl},
		{m.p.caSrl, []byte(certSerialText(new(big.Int).Add(serial, big.NewInt(1))) + "\n")},
		{crtf, pemData},
	}

	for _, s := range steps {
		if err := m.writeFile(s.name, s.data, certModeFile); err != nil {
			return err
		}
	}

	return nil
}
