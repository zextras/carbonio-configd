// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"crypto"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

// certDateFormat is how openssl prints a date.
const certDateFormat = "Jan _2 15:04:05 2006 GMT"

// cmdVerifyCrt is `verifycrt <self|comm> [[[priv_key] [certfile]] [ca_chain_file]]`.
func (m *certMgr) cmdVerifyCrt(args []string) (bool, error) {
	var keyf, crtf, caf string

	rest := args[1:]
	for i, dst := range []*string{&keyf, &crtf, &caf} {
		if i < len(rest) {
			*dst = rest[i]
		}
	}

	return m.verifycrt(args[0], keyf, crtf, caf), nil
}

// verifycrt is verifycrt: the key matches the certificate and the
// certificate is valid against the CA file.
func (m *certMgr) verifycrt(typ, keyf, crtf, caf string) bool {
	m.debugf("verifycrt(%s %s %s %s)\n", typ, keyf, crtf, caf)

	files := m.p.typed(typ)

	for _, f := range []struct {
		dst *string
		def string
	}{{&keyf, files.key}, {&crtf, files.crt}, {&caf, files.ca}} {
		if *f.dst == "" {
			*f.dst = f.def
		}
	}

	return m.verifycrtkey(keyf, crtf) && m.verifycrtchain(caf, crtf)
}

// cmdVerifyCrtKey is `verifycrtkey <priv_key> <certfile>`.
func (m *certMgr) cmdVerifyCrtKey(args []string) (bool, error) {
	return m.verifycrtkey(args[0], args[1]), nil
}

// verifycrtkey is verifycrtkey: the public key of the private key is the one
// of the certificate.
func (m *certMgr) verifycrtkey(keyf, crtf string) bool {
	m.say("** Verifying '%s' against '%s'\n", crtf, keyf)

	if !m.checkFiles(keyf, crtf) {
		return false
	}

	info := fmt.Sprintf("Certificate '%s' and private key '%s'", crtf, keyf)

	if !m.keyMatches(keyf, crtf) {
		return m.fail("%s do not match.\n", info)
	}

	m.say("%s match.\n", info)

	return true
}

// keyMatches compares the public key of a private key file and of the first
// certificate of a file; a file that cannot be read does not match.
func (m *certMgr) keyMatches(keyf, crtf string) bool {
	keyPEM, err := m.slurp(keyf)
	if err != nil {
		return false
	}

	key, err := certParseKey(keyPEM)
	if err != nil {
		return false
	}

	crtPEM, err := m.slurp(crtf)
	if err != nil {
		return false
	}

	certs, err := certParseCerts(crtPEM)
	if err != nil || len(certs) == 0 {
		return false
	}

	pub, ok := key.Public().(interface{ Equal(crypto.PublicKey) bool })

	return ok && pub.Equal(certs[0].PublicKey)
}

// cmdVerifyCrtChain is `verifycrtchain <ca_chain_file> <certfile>`.
func (m *certMgr) cmdVerifyCrtChain(args []string) (bool, error) {
	return m.verifycrtchain(args[0], args[1]), nil
}

// verifycrtchain is verifycrtchain: `openssl verify -purpose sslserver
// -CAfile <ca> <crt>`.
func (m *certMgr) verifycrtchain(cafile, crtf string) bool {
	m.debugf("verifycrtchain(%s %s)\n", cafile, crtf)
	m.say("** Verifying '%s' against '%s'\n", crtf, cafile)

	if !m.checkFiles(cafile, crtf) {
		return false
	}

	problem := m.chainProblem(cafile, crtf)
	if problem != "" {
		return m.fail("Unable to validate certificate chain: %s\n", problem)
	}

	m.say("Valid certificate chain: %s: OK\n", crtf)

	return true
}

// chainProblem is what `openssl verify` prints when the certificate does not
// verify, "" when it does.
func (m *certMgr) chainProblem(cafile, crtf string) string {
	crtPEM, err := m.slurp(crtf)
	if err != nil {
		return fmt.Sprintf("error %s: %s\nerror %s: verification failed", crtf, certErrText(err), crtf)
	}

	leafs, err := certParseCerts(crtPEM)
	if err != nil || len(leafs) == 0 {
		return fmt.Sprintf("Could not read certificate from %s\nerror %s: verification failed", crtf, crtf)
	}

	caPEM, err := m.slurp(cafile)
	if err != nil {
		return fmt.Sprintf("Error loading file %s\nerror %s: verification failed", cafile, crtf)
	}

	cas, err := certParseCerts(caPEM)
	if err != nil {
		return fmt.Sprintf("Error loading file %s\nerror %s: verification failed", cafile, crtf)
	}

	p := certVerify(leafs[0], cas, m.e.Sys.Clock.Now())
	if p == nil {
		return ""
	}

	subject := p.Cert.Subject.String()
	if dn, err := certParseDN(p.Cert.RawSubject); err == nil {
		subject = dn.oneline()
	}

	return fmt.Sprintf("%s\nerror %d at %d depth lookup: %s\nerror %s: verification failed",
		subject, p.Code, p.Depth, p.Text, crtf)
}

// viewcsr is `viewcsr <self|comm> [csr_file]`.
func (m *certMgr) viewcsr(args []string) (bool, error) {
	file := m.p.typed(args[0]).csr
	if len(args) > 1 {
		file = args[1]
	}

	return m.viewCsrOrCrt(false, file, ""), nil
}

// viewstagedcrt is `viewstagedcrt <self|comm> [certfile]`.
func (m *certMgr) viewstagedcrt(args []string) (bool, error) {
	file := m.p.typed(args[0]).crt
	if len(args) > 1 {
		file = args[1]
	}

	return m.viewCsrOrCrt(true, file, ""), nil
}

// viewCsrOrCrt is viewCsrOrCrt: dates, subject and issuer of a certificate
// (the subject of a request) as `openssl x509`/`req` print them, then its
// subject alternative names.
func (m *certMgr) viewCsrOrCrt(isCrt bool, file, info string) bool {
	if !m.checkFiles(file) {
		return false
	}

	cmd := "req"
	if isCrt {
		cmd = "x509"
	}

	lines, san, err := m.describe(isCrt, file)
	if err != nil {
		m.fail("openssl %s on '%s' failed(1)\n", cmd, file)
		m.say("- %s%s\n", info, file)

		return false
	}

	m.say("- %s%s\n%s", info, file, lines)
	m.say("SubjectAltName=%s\n", san)

	return true
}

// describe returns the lines openssl prints for a file and its subject
// alternative names.
func (m *certMgr) describe(isCrt bool, file string) (lines, san string, err error) {
	data, err := m.slurp(file)
	if err != nil {
		return "", "", err
	}

	if !isCrt {
		csr, err := certParseCSR(data)
		if err != nil {
			return "", "", err
		}

		return "subject=" + certSubjectText(csr.RawSubject) + "\n", certNames(csr.Extensions), nil
	}

	certs, err := certParseCerts(data)
	if err != nil || len(certs) == 0 {
		return "", "", fmt.Errorf("no certificate: %w", err)
	}

	c := certs[0]
	lines = "notBefore=" + c.NotBefore.UTC().Format(certDateFormat) + "\n" +
		"notAfter=" + c.NotAfter.UTC().Format(certDateFormat) + "\n" +
		"subject=" + certSubjectText(c.RawSubject) + "\n" +
		"issuer=" + certSubjectText(c.RawIssuer) + "\n"

	return lines, certNames(c.Extensions), nil
}

// certSubjectText is a name in the one-line form of OpenSSL 3.
func certSubjectText(raw []byte) string {
	dn, err := certParseDN(raw)
	if err != nil {
		return ""
	}

	return dn.oneline()
}

// certNames is the subject alternative names with the "DNS:" labels dropped.
func certNames(exts []pkix.Extension) string {
	return strings.ReplaceAll(certSANLine(exts), "DNS:", "")
}

// serviceCert prepares the certificate file of a service for viewing or the
// expiry check: mailboxd's is exported from the keystore. skip is true when
// there is nothing to look at.
func (m *certMgr) serviceCert(name string) (crtf string, skip, failed bool) {
	crtf = m.p.svcCrt(name)

	if name == certSvcMailboxd {
		switch {
		case m.lc["mailboxd_server"] == "":
			if m.isFile(m.p.keystore) {
				_, _ = fmt.Fprintf(m.errw, "NOTE: possibly stale keystore: %s\n", m.p.keystore)
			}

			if !m.isFile(crtf) {
				return crtf, true, false
			}
		case m.isFile(m.p.keystore):
			if !m.keystoreToPEM() {
				return crtf, true, true
			}
		}
	}

	if !m.isFile(crtf) {
		m.say("NOTE: %s: file '%s' does not exist.\n", name, crtf)

		return crtf, true, false
	}

	return crtf, false, false
}

// checkService is the service argument of viewdeployedcrt and
// checkcrtexpiration.
func checkService(args []string, usage string) (string, error) {
	if len(args) == 0 || args[0] == "" {
		return "", nil
	}

	if !certValidService(args[0]) {
		return "", fmt.Errorf("%s: unknown service '%s'", usage, args[0])
	}

	return args[0], nil
}

// viewdeployedcrt is viewdeployedcrt: the certificates the services use.
func (m *certMgr) viewdeployedcrt(args []string) (bool, error) {
	sname, err := checkService(args, "viewdeployedcrt "+certServicesRef)
	if err != nil {
		return false, err
	}

	ok := true

	for _, name := range m.services(sname) {
		crtf, skip, failed := m.serviceCert(name)
		if failed || (!skip && !m.viewCsrOrCrt(true, crtf, name+": ")) {
			ok = false
		}
	}

	return ok, nil
}

// stdinIsTerminal tells whether the standard input is a terminal (-t
// STDIN); tests replace it for their Env.
func (m *certMgr) stdinIsTerminal() bool {
	if m.e.certTTY != nil {
		return m.e.certTTY()
	}

	return term.IsTerminal(int(os.Stdin.Fd()))
}

// checkcrtexpiration is checkcrtexpiration: the certificates that are
// expired, or expire within -days (30), are reported (all of them on a
// terminal).
func (m *certMgr) checkcrtexpiration(args []string) (bool, error) {
	sname, err := checkService(args, "checkcrtexpiration "+certServicesRef)
	if err != nil {
		return false, err
	}

	names := m.services(sname)
	tagLen := 0

	for _, n := range names {
		tagLen = max(tagLen, len(n)+1)
	}

	ok := true

	for _, name := range names {
		crtf, skip, failed := m.serviceCert(name)
		if failed {
			ok = false
		}

		if skip {
			continue
		}

		if !m.expiryLine(name+":", tagLen, crtf) {
			ok = false
		}
	}

	return ok, nil
}

// expiryLine prints the state of one certificate; it returns false when the
// certificate is expired, expires within the days or cannot be checked.
func (m *certMgr) expiryLine(tag string, tagLen int, crtf string) bool {
	end, expired, soon, err := m.expiry(crtf)

	var msg string

	switch {
	case err != nil:
		m.say("%-*s '%s' CANNOT BE CHECKED: %v\n", tagLen, tag, crtf, err)

		return false
	case expired:
		msg = "EXPIRED!"
	case soon:
		msg = fmt.Sprintf("EXPIRES within %d days!", m.days)
	default:
		msg = fmt.Sprintf("expires outside of %d days (OK)", m.days)
	}

	if expired || soon || m.stdinIsTerminal() {
		m.say("%-*s %s '%s' %s\n", tagLen, tag, end, crtf, msg)
	}

	return !expired && !soon
}

// errNoCertificate is a file that has no certificate in it.
var errNoCertificate = errors.New("no certificate found")

// expiry is `openssl x509 -enddate` and `-checkend 0` / `-checkend <days>`:
// the end date line and whether the first certificate of the file has expired
// or expires within the days. A file that cannot be read or has no
// certificate Go can parse is an error, not an expired certificate: nothing
// is known about its end.
func (m *certMgr) expiry(crtf string) (end string, expired, soon bool, err error) {
	data, err := m.slurp(crtf)
	if err != nil {
		return "", false, false, err
	}

	certs, err := certParseCerts(data)
	if err != nil {
		return "", false, false, err
	}

	if len(certs) == 0 {
		return "", false, false, errNoCertificate
	}

	now := m.e.Sys.Clock.Now()
	end, expired, soon = expiryOf(certs[0], now, now.UTC().AddDate(0, 0, m.days))

	return end, expired, soon, nil
}

func expiryOf(c *x509.Certificate, now, deadline time.Time) (end string, expired, soon bool) {
	end = "notAfter=" + c.NotAfter.UTC().Format(certDateFormat)

	return end, c.NotAfter.Before(now), c.NotAfter.Before(deadline)
}
