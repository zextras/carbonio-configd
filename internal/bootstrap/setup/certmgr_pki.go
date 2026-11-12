// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // the key identifier of RFC 5280 4.2.1.2 (1) is SHA-1
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
)

// The PEM types and OIDs the certificates here are made of.
const (
	certPEMCert   = "CERTIFICATE"
	certPEMReq    = "CERTIFICATE REQUEST"
	certPEMPKCS8  = "PRIVATE KEY"
	certSerialLen = 159 // bits of the random serial of a self-signed certificate (OpenSSL)
)

var (
	certOIDBasic    = asn1.ObjectIdentifier{2, 5, 29, 19}
	certOIDKeyUsage = asn1.ObjectIdentifier{2, 5, 29, 15}
	certOIDSAN      = asn1.ObjectIdentifier{2, 5, 29, 17}
	certOIDSKI      = asn1.ObjectIdentifier{2, 5, 29, 14}
	certOIDAKI      = asn1.ObjectIdentifier{2, 5, 29, 35}
)

// certDigests are the digests of -digest that a signature can be made with
// (what crypto/x509 knows): by RSA, and by ECDSA.
var certDigests = map[string][2]x509.SignatureAlgorithm{
	"sha1":   {x509.SHA1WithRSA, x509.ECDSAWithSHA1},
	"sha256": {x509.SHA256WithRSA, x509.ECDSAWithSHA256},
	"sha384": {x509.SHA384WithRSA, x509.ECDSAWithSHA384},
	"sha512": {x509.SHA512WithRSA, x509.ECDSAWithSHA512},
}

// certNewRSAKey generates the key of -keysize.
func certNewRSAKey(bits int) (crypto.Signer, error) {
	return rsa.GenerateKey(rand.Reader, bits)
}

// certKeyPEM is the key as `openssl req -keyout` writes it (PKCS#8).
func certKeyPEM(k crypto.Signer) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, err
	}

	return pem.EncodeToMemory(&pem.Block{Type: certPEMPKCS8, Bytes: der}), nil
}

// certParseKey reads the first private key of PEM data (PKCS#8, PKCS#1 RSA or
// SEC1 EC), as `openssl -key` does without a pass phrase.
func certParseKey(data []byte) (crypto.Signer, error) {
	for rest := data; ; {
		var blk *pem.Block

		blk, rest = pem.Decode(rest)
		if blk == nil {
			return nil, errors.New("no private key found")
		}

		if !strings.HasSuffix(blk.Type, "PRIVATE KEY") {
			continue
		}

		return certParseKeyBlock(blk)
	}
}

// certParseKeyBlock decodes one private key block.
func certParseKeyBlock(blk *pem.Block) (crypto.Signer, error) {
	if blk.Type == "ENCRYPTED PRIVATE KEY" || blk.Headers["Proc-Type"] != "" {
		return nil, errors.New("encrypted private keys are not supported")
	}

	var (
		key any
		err error
	)

	switch blk.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(blk.Bytes)
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(blk.Bytes)
	default:
		key, err = x509.ParsePKCS8PrivateKey(blk.Bytes)
	}

	if err != nil {
		return nil, err
	}

	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, errors.New("unsupported private key type")
	}

	return signer, nil
}

// certSigAlg is the signature algorithm of a digest for a key; the digests
// that -digest accepts and crypto/x509 has no algorithm for are refused with
// the list of those that are signed.
func certSigAlg(pub crypto.PublicKey, digest string) (x509.SignatureAlgorithm, error) {
	if _, ok := pub.(ed25519.PublicKey); ok {
		return x509.PureEd25519, nil
	}

	algs, ok := certDigests[digest]
	if !ok {
		return 0, fmt.Errorf("unsupported digest '%s' (supported: sha1, sha256, sha384, sha512)", digest)
	}

	switch pub.(type) {
	case *rsa.PublicKey:
		return algs[0], nil
	case *ecdsa.PublicKey:
		return algs[1], nil
	}

	return 0, errors.New("unsupported key type")
}

// certSerial is a random positive serial of 159 bits, as OpenSSL makes them.
func certSerial() (*big.Int, error) {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), certSerialLen))
	if err != nil {
		return nil, err
	}

	return n.Add(n, big.NewInt(1)), nil
}

// certExt builds an extension with an ASN.1 value; never critical, as the
// zmssl.cnf sections are.
func certExt(oid asn1.ObjectIdentifier, v any) (pkix.Extension, error) {
	der, err := asn1.Marshal(v)
	if err != nil {
		return pkix.Extension{}, err
	}

	return pkix.Extension{Id: oid, Value: der}, nil
}

// certExtBasic is basicConstraints = CA:true|false.
func certExtBasic(ca bool) (pkix.Extension, error) {
	return certExt(certOIDBasic, struct {
		CA bool `asn1:"optional"`
	}{ca})
}

// certExtKeyUsage is keyUsage = nonRepudiation, digitalSignature,
// keyEncipherment.
func certExtKeyUsage() (pkix.Extension, error) {
	return certExt(certOIDKeyUsage, asn1.BitString{Bytes: []byte{0xE0}, BitLength: 3})
}

// certExtSAN is subjectAltName = DNS:<name>, ... (an IP address in the list
// is a DNS name too, as the "DNS:" prefix of zmssl.cnf makes it).
func certExtSAN(names []string) (pkix.Extension, error) {
	gns := make([]asn1.RawValue, len(names))
	for i, n := range names {
		gns[i] = asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 2, Bytes: []byte(n)}
	}

	return certExt(certOIDSAN, gns)
}

// certKeyID is the SHA-1 of the subjectPublicKey bits (subjectKeyIdentifier =
// hash).
func certKeyID(pub crypto.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}

	var spki struct {
		Algo pkix.AlgorithmIdentifier
		Key  asn1.BitString
	}

	if _, err := asn1.Unmarshal(der, &spki); err != nil {
		return nil, err
	}

	sum := sha1.Sum(spki.Key.RightAlign()) //nolint:gosec // see the import

	return sum[:], nil
}

// certExtSKI is subjectKeyIdentifier = hash.
func certExtSKI(keyID []byte) (pkix.Extension, error) {
	return certExt(certOIDSKI, keyID)
}

// certExtAKI is authorityKeyIdentifier = keyid[:always][,issuer:always]: the
// key identifier, and when issuerDER is not nil the issuer name and serial.
func certExtAKI(keyID, issuerDER []byte, serial *big.Int) (pkix.Extension, error) {
	var body []byte

	if keyID != nil {
		kid, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, Bytes: keyID})
		if err != nil {
			return pkix.Extension{}, err
		}

		body = append(body, kid...)
	}

	if issuerDER != nil {
		dirName, err := asn1.Marshal(asn1.RawValue{
			Class: asn1.ClassContextSpecific, Tag: 4, IsCompound: true, Bytes: issuerDER,
		})
		if err != nil {
			return pkix.Extension{}, err
		}

		issuer, err := asn1.Marshal(asn1.RawValue{
			Class: asn1.ClassContextSpecific, Tag: 1, IsCompound: true, Bytes: dirName,
		})
		if err != nil {
			return pkix.Extension{}, err
		}

		ser, err := certIntegerContent(serial)
		if err != nil {
			return pkix.Extension{}, err
		}

		serTLV, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 2, Bytes: ser})
		if err != nil {
			return pkix.Extension{}, err
		}

		body = append(append(body, issuer...), serTLV...)
	}

	return certExt(certOIDAKI, asn1.RawValue{
		Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: body,
	})
}

// certIntegerContent is the content octets of the DER INTEGER n.
func certIntegerContent(n *big.Int) ([]byte, error) {
	der, err := asn1.Marshal(n)
	if err != nil {
		return nil, err
	}

	var rv asn1.RawValue

	if _, err := asn1.Unmarshal(der, &rv); err != nil {
		return nil, err
	}

	return rv.Bytes, nil
}

// certIssued is what a signing leaves: the DER and the parsed certificate.
type certIssued struct {
	DER  []byte
	Cert *x509.Certificate
}

// certTemplate is the common part of the certificates made here.
func certTemplate(serial *big.Int, subject []byte, now time.Time, days int, alg x509.SignatureAlgorithm,
) *x509.Certificate {
	nb := now.UTC().Truncate(time.Second)

	return &x509.Certificate{
		SerialNumber:       serial,
		RawSubject:         subject,
		NotBefore:          nb,
		NotAfter:           nb.AddDate(0, 0, days),
		SignatureAlgorithm: alg,
	}
}

// certSelfSign is `openssl req -x509 -extensions v3_ca`: a self-signed CA
// with the key identifier, the issuer-and-serial authority identifier,
// basicConstraints CA:true and the subject alternative names.
func certSelfSign(key crypto.Signer, subject certDN, sans []string, days int, digest string, now time.Time,
) (*certIssued, error) {
	alg, err := certSigAlg(key.Public(), digest)
	if err != nil {
		return nil, err
	}

	subj, err := subject.marshal()
	if err != nil {
		return nil, err
	}

	serial, err := certSerial()
	if err != nil {
		return nil, err
	}

	kid, err := certKeyID(key.Public())
	if err != nil {
		return nil, err
	}

	tpl := certTemplate(serial, subj, now, days, alg)

	tpl.ExtraExtensions, err = certCAExtensions(kid, subj, serial, sans)
	if err != nil {
		return nil, err
	}

	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, key.Public(), key)

	return certParsed(der, err)
}

// certCAExtensions are the extensions of [v3_ca] of zmssl.cnf.
func certCAExtensions(kid, issuer []byte, serial *big.Int, sans []string) ([]pkix.Extension, error) {
	ski, err := certExtSKI(kid)
	if err != nil {
		return nil, err
	}

	aki, err := certExtAKI(kid, issuer, serial)
	if err != nil {
		return nil, err
	}

	basic, err := certExtBasic(true)
	if err != nil {
		return nil, err
	}

	exts := []pkix.Extension{ski, aki, basic}

	return certAppendSAN(exts, sans)
}

// certAppendSAN adds the subjectAltName extension when there are names.
func certAppendSAN(exts []pkix.Extension, sans []string) ([]pkix.Extension, error) {
	if len(sans) == 0 {
		return exts, nil
	}

	san, err := certExtSAN(sans)
	if err != nil {
		return nil, err
	}

	return append(exts, san), nil
}

// certParsed parses the result of x509.CreateCertificate.
func certParsed(der []byte, err error) (*certIssued, error) {
	if err != nil {
		return nil, err
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}

	return &certIssued{DER: der, Cert: cert}, nil
}

// certNewCSR is `openssl req -new` with req_extensions = v3_req: a request
// for key with the subject, and the extensions basicConstraints CA:FALSE,
// keyUsage and the subject alternative names as attributes.
func certNewCSR(key crypto.Signer, subject certDN, sans []string, digest string) ([]byte, error) {
	alg, err := certSigAlg(key.Public(), digest)
	if err != nil {
		return nil, err
	}

	subj, err := subject.marshal()
	if err != nil {
		return nil, err
	}

	exts, err := certRequestExtensions(sans)
	if err != nil {
		return nil, err
	}

	return x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		RawSubject:         subj,
		SignatureAlgorithm: alg,
		ExtraExtensions:    exts,
	}, key)
}

// certRequestExtensions are the extensions of [v3_req] of zmssl.cnf.
func certRequestExtensions(sans []string) ([]pkix.Extension, error) {
	basic, err := certExtBasic(false)
	if err != nil {
		return nil, err
	}

	usage, err := certExtKeyUsage()
	if err != nil {
		return nil, err
	}

	return certAppendSAN([]pkix.Extension{basic, usage}, sans)
}

// certSignReq is what `openssl ca -policy policy_anything -extensions v3_req`
// is given.
type certSignReq struct {
	CSR *x509.CertificateRequest
	// SANs are the names of v3_req of the config the command wrote.
	SANs   []string
	CA     *x509.Certificate
	CAKey  crypto.Signer
	Serial *big.Int
	Days   int
	Digest string
	Now    time.Time
}

// certSignCSR signs a request as openssl ca does: the subject cut to
// policy_anything, the extensions of v3_req (basicConstraints CA:FALSE,
// keyUsage and the subject alternative names of the request, which
// copy_extensions = copy brings along) and the key identifiers that OpenSSL 3
// adds.
func certSignCSR(r *certSignReq) (*certIssued, error) {
	if err := r.CSR.CheckSignature(); err != nil {
		return nil, fmt.Errorf("certificate request self signature verify failure: %w", err)
	}

	alg, err := certSigAlg(r.CAKey.Public(), r.Digest)
	if err != nil {
		return nil, err
	}

	subject, err := certParseDN(r.CSR.RawSubject)
	if err != nil {
		return nil, err
	}

	if subject, err = subject.policyAnything(); err != nil {
		return nil, err
	}

	subj, err := subject.marshal()
	if err != nil {
		return nil, err
	}

	exts, err := certLeafExtensions(r)
	if err != nil {
		return nil, err
	}

	tpl := certTemplate(r.Serial, subj, r.Now, r.Days, alg)
	tpl.ExtraExtensions = exts

	der, err := x509.CreateCertificate(rand.Reader, tpl, r.CA, r.CSR.PublicKey, r.CAKey)

	return certParsed(der, err)
}

// certLeafExtensions merges the extensions of an issued certificate: those of
// v3_req first, then the ones of the request that v3_req does not set, then
// the key identifiers (subject: hash; authority: keyid of the CA).
func certLeafExtensions(r *certSignReq) ([]pkix.Extension, error) {
	exts, err := certRequestExtensions(r.SANs)
	if err != nil {
		return nil, err
	}

	for _, e := range r.CSR.Extensions {
		if !certHasExt(exts, e.Id) && !e.Id.Equal(certOIDSKI) && !e.Id.Equal(certOIDAKI) {
			exts = append(exts, e)
		}
	}

	kid, err := certKeyID(r.CSR.PublicKey)
	if err != nil {
		return nil, err
	}

	ski, err := certExtSKI(kid)
	if err != nil {
		return nil, err
	}

	exts = append(exts, ski)

	if len(r.CA.SubjectKeyId) > 0 {
		aki, err := certExtAKI(r.CA.SubjectKeyId, nil, nil)
		if err != nil {
			return nil, err
		}

		exts = append(exts, aki)
	}

	return exts, nil
}

// certHasExt is whether the list holds an extension.
func certHasExt(exts []pkix.Extension, id asn1.ObjectIdentifier) bool {
	for _, e := range exts {
		if e.Id.Equal(id) {
			return true
		}
	}

	return false
}

// certPEMBlock is the PEM of a DER object.
func certPEMBlock(typ string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
}

// certParseCerts reads every certificate of PEM data; other blocks are
// skipped.
func certParseCerts(data []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate

	for rest := data; ; {
		var blk *pem.Block

		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}

		if blk.Type != certPEMCert && blk.Type != "X509 CERTIFICATE" && blk.Type != "TRUSTED CERTIFICATE" {
			continue
		}

		c, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			return nil, err
		}

		certs = append(certs, c)
	}

	return certs, nil
}

// certParseCSR reads the first request of PEM data.
func certParseCSR(data []byte) (*x509.CertificateRequest, error) {
	for rest := data; ; {
		var blk *pem.Block

		blk, rest = pem.Decode(rest)
		if blk == nil {
			return nil, errors.New("no certificate request found")
		}

		if blk.Type == certPEMReq || blk.Type == "NEW CERTIFICATE REQUEST" {
			return x509.ParseCertificateRequest(blk.Bytes)
		}
	}
}

// certSANLine is the names of the subject alternative name extension as
// OpenSSL prints them, comma-separated ("DNS:a, IP Address:1.2.3.4"); empty
// when there is none.
func certSANLine(exts []pkix.Extension) string {
	for _, e := range exts {
		if e.Id.Equal(certOIDSAN) {
			return certSANNames(e.Value)
		}
	}

	return ""
}

// certSANNames decodes a GeneralNames value.
func certSANNames(der []byte) string {
	var seq asn1.RawValue

	if _, err := asn1.Unmarshal(der, &seq); err != nil {
		return ""
	}

	var names []string

	for rest := seq.Bytes; len(rest) > 0; {
		var gn asn1.RawValue

		var err error

		if rest, err = asn1.Unmarshal(rest, &gn); err != nil {
			break
		}

		names = append(names, certGeneralName(gn))
	}

	return strings.Join(names, ", ")
}

// certGeneralName prints one GeneralName like OpenSSL (GENERAL_NAME_print).
func certGeneralName(gn asn1.RawValue) string {
	switch gn.Tag {
	case 1:
		return "email:" + string(gn.Bytes)
	case 2:
		return "DNS:" + string(gn.Bytes)
	case 6:
		return "URI:" + string(gn.Bytes)
	case 7:
		return "IP Address:" + certIPText(gn.Bytes)
	case 4:
		return certDirName(gn.Bytes)
	case 8:
		var oid asn1.ObjectIdentifier

		if _, err := asn1.UnmarshalWithParams(gn.FullBytes, &oid, "tag:8"); err == nil {
			return "Registered ID:" + oid.String()
		}
	}

	return fmt.Sprintf("othername:<unsupported %d>", gn.Tag)
}

// certIPText is an IP address as OpenSSL prints it: dotted for IPv4, the
// eight groups of hex digits for IPv6.
func certIPText(b []byte) string {
	switch len(b) {
	case net.IPv4len:
		return net.IP(b).String()
	case net.IPv6len:
		parts := make([]string, 8)
		for i := range parts {
			parts[i] = fmt.Sprintf("%X", uint16(b[2*i])<<8|uint16(b[2*i+1]))
		}

		return strings.Join(parts, ":")
	}

	return fmt.Sprintf("<invalid length=%d>", len(b))
}

// certDirName prints a directoryName GeneralName: "DirName:" and the name.
func certDirName(content []byte) string {
	dn, err := certParseDN(content)
	if err != nil {
		return "DirName:<invalid>"
	}

	return "DirName:" + dn.slash()
}

// certVerifyProblem is one finding of a chain verification, in the words
// `openssl verify` uses.
type certVerifyProblem struct {
	Code  int
	Depth int
	Text  string
	Cert  *x509.Certificate
}

// The error numbers of OpenSSL that certVerify reports.
const (
	certErrGetIssuer     = 2
	certErrNotYetValid   = 9
	certErrExpired       = 10
	certErrSelfSigned    = 18
	certErrSelfSignedChn = 19
	certErrGetLocal      = 20
	certErrPurpose       = 26
)

// certVerify checks a certificate chain as `openssl verify -purpose
// sslserver -CAfile cafile` does: the self-signed certificates of the CA file
// are the trust anchors and the others are intermediates. It returns the
// first problem, nil when the chain is valid.
func certVerify(leaf *x509.Certificate, cafile []*x509.Certificate, now time.Time) *certVerifyProblem {
	roots, inter := x509.NewCertPool(), x509.NewCertPool()

	for _, c := range cafile {
		if bytes.Equal(c.RawIssuer, c.RawSubject) {
			roots.AddCert(c)
		} else {
			inter.AddCert(c)
		}
	}

	_, err := leaf.Verify(x509.VerifyOptions{
		Roots: roots, Intermediates: inter, CurrentTime: now,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err == nil {
		return nil
	}

	return certProblem(leaf, err, now)
}

// certProblem names a verification error with the number and text of
// OpenSSL.
func certProblem(leaf *x509.Certificate, err error, now time.Time) *certVerifyProblem {
	var (
		invalid x509.CertificateInvalidError
		unknown x509.UnknownAuthorityError
	)

	switch {
	case errors.As(err, &invalid):
		return certInvalidProblem(leaf, &invalid, now)
	case errors.As(err, &unknown):
		if bytes.Equal(leaf.RawIssuer, leaf.RawSubject) {
			return &certVerifyProblem{Code: certErrSelfSigned, Text: "self-signed certificate", Cert: leaf}
		}

		return &certVerifyProblem{Code: certErrGetLocal, Text: "unable to get local issuer certificate", Cert: leaf}
	}

	return &certVerifyProblem{Code: certErrGetIssuer, Text: err.Error(), Cert: leaf}
}

// certInvalidProblem maps the reasons of x509.CertificateInvalidError.
func certInvalidProblem(leaf *x509.Certificate, inv *x509.CertificateInvalidError, now time.Time,
) *certVerifyProblem {
	depth := 0
	if inv.Cert != nil && !inv.Cert.Equal(leaf) {
		depth = 1
	}

	p := &certVerifyProblem{Depth: depth, Cert: inv.Cert}

	switch inv.Reason {
	case x509.Expired:
		p.Code, p.Text = certErrExpired, "certificate has expired"
		if inv.Cert != nil && now.Before(inv.Cert.NotBefore) {
			p.Code, p.Text = certErrNotYetValid, "certificate is not yet valid"
		}
	case x509.IncompatibleUsage:
		p.Code, p.Text = certErrPurpose, "unsuitable certificate purpose"
	default:
		p.Code, p.Text = certErrSelfSignedChn, inv.Error()
	}

	return p
}
