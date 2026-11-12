// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCertParseKey(t *testing.T) {
	t.Parallel()

	rsaKey, ok := cmKeys()[0].(*rsa.PrivateKey)
	require.True(t, ok)

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	pkcs8 := func(k crypto.Signer) []byte {
		der, err := x509.MarshalPKCS8PrivateKey(k)
		require.NoError(t, err)

		return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	}
	sec1, err := x509.MarshalECPrivateKey(ecKey)
	require.NoError(t, err)

	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("x")})

	for _, tc := range []struct {
		name string
		data []byte
		want crypto.PrivateKey
		err  string
	}{
		{"pkcs8 rsa", pkcs8(rsaKey), rsaKey, ""},
		{"pkcs1 rsa", pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)}), rsaKey, ""},
		{"sec1 ec", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: sec1}), ecKey, ""},
		{"ed25519", pkcs8(edKey), edKey, ""},
		{"after a certificate", append(cert, pkcs8(rsaKey)...), rsaKey, ""},
		{"encrypted", pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte("x")}), nil,
			"encrypted private keys are not supported"},
		{"legacy encrypted", pem.EncodeToMemory(&pem.Block{
			Type: "RSA PRIVATE KEY", Headers: map[string]string{"Proc-Type": "4,ENCRYPTED"}, Bytes: []byte("x"),
		}), nil, "encrypted private keys are not supported"},
		{"broken", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("x")}), nil, "asn1"},
		{"only a certificate", cert, nil, "no private key found"},
		{"nothing", nil, nil, "no private key found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := certParseKey(tc.data)
			if tc.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.err)

				return
			}

			require.NoError(t, err)
			assert.True(t, got.(interface{ Equal(crypto.PrivateKey) bool }).Equal(tc.want))
		})
	}
}

func TestCertKeyPEMRoundTrips(t *testing.T) {
	t.Parallel()

	key, ok := cmKeys()[1].(*rsa.PrivateKey)
	require.True(t, ok)

	data, err := certKeyPEM(key)
	require.NoError(t, err)
	assert.Contains(t, string(data), "-----BEGIN PRIVATE KEY-----")

	got, err := certParseKey(data)
	require.NoError(t, err)
	assert.True(t, key.Equal(got))
}

func TestCertSigAlgForKeys(t *testing.T) {
	t.Parallel()

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	got, err := certSigAlg(ecKey.Public(), "sha384")
	require.NoError(t, err)
	assert.Equal(t, x509.ECDSAWithSHA384, got)

	got, err = certSigAlg(edKey.Public(), "sha1")
	require.NoError(t, err)
	assert.Equal(t, x509.PureEd25519, got, "Ed25519 signs without a digest")

	_, err = certSigAlg(struct{}{}, "sha256")
	require.EqualError(t, err, "unsupported key type")
}

func TestCertSerialText(t *testing.T) {
	t.Parallel()

	// openssl writes serials in upper case hex with an even number of digits.
	for n, want := range map[int64]string{1: "01", 255: "FF", 256: "0100", 1767225600: "6955B900"} {
		assert.Equal(t, want, certSerialText(big.NewInt(n)), n)
	}
}

func TestCertSerialIsPositiveAndRandom(t *testing.T) {
	t.Parallel()

	a, err := certSerial()
	require.NoError(t, err)

	b, err := certSerial()
	require.NoError(t, err)

	assert.Equal(t, 1, a.Sign())
	assert.NotZero(t, a.Cmp(b))
	assert.LessOrEqual(t, a.BitLen(), 160)
}

func TestCertGeneralNames(t *testing.T) {
	t.Parallel()

	name, err := asn1.Marshal(pkix.Name{CommonName: "x", Organization: []string{"Acme"}}.ToRDNSequence())
	require.NoError(t, err)

	oid, err := asn1.MarshalWithParams(asn1.ObjectIdentifier{1, 2, 3}, "tag:8")
	require.NoError(t, err)

	for _, tc := range []struct {
		name string
		gn   asn1.RawValue
		want string
	}{
		{"dns", asn1.RawValue{Tag: 2, Bytes: []byte("a.example.com")}, "DNS:a.example.com"},
		{"email", asn1.RawValue{Tag: 1, Bytes: []byte("a@example.com")}, "email:a@example.com"},
		{"uri", asn1.RawValue{Tag: 6, Bytes: []byte("http://a/")}, "URI:http://a/"},
		{"ipv4", asn1.RawValue{Tag: 7, Bytes: []byte{10, 1, 2, 3}}, "IP Address:10.1.2.3"},
		{"ipv6", asn1.RawValue{Tag: 7, Bytes: net16(1)}, "IP Address:0:0:0:0:0:0:0:1"},
		{"ip of a wrong length", asn1.RawValue{Tag: 7, Bytes: []byte{1, 2, 3}}, "IP Address:<invalid length=3>"},
		{"directory name", asn1.RawValue{Tag: 4, Bytes: name}, "DirName:/O=Acme/CN=x"},
		{"broken directory name", asn1.RawValue{Tag: 4, Bytes: []byte{1}}, "DirName:<invalid>"},
		{"registered id", asn1.RawValue{Tag: 8, FullBytes: oid}, "Registered ID:1.2.3"},
		{"other", asn1.RawValue{Tag: 0}, "othername:<unsupported 0>"},
	} {
		assert.Equal(t, tc.want, certGeneralName(tc.gn), tc.name)
	}
}

// net16 is an IPv6 address of the last group n.
func net16(n byte) []byte {
	b := make([]byte, 16)
	b[15] = n

	return b
}

func TestCertNameText(t *testing.T) {
	t.Parallel()

	cn := certAttrByName("CN")
	require.NotNil(t, cn)

	for _, tc := range []struct {
		name string
		val  asn1.RawValue
		want string
		ok   bool
	}{
		{"utf8", asn1.RawValue{Tag: certTagUTF8, Bytes: []byte("Zürich")}, "Zürich", true},
		{"t61 is latin 1", asn1.RawValue{Tag: certTagT61, Bytes: []byte{'Z', 0xfc}}, "Zü", true},
		{"bmp", asn1.RawValue{Tag: certTagBMP, Bytes: []byte{0, 'Z', 0, 0xfc}}, "Zü", true},
		{"bmp of odd length", asn1.RawValue{Tag: certTagBMP, Bytes: []byte{0}}, "", false},
		{"universal", asn1.RawValue{Tag: certTagUniv, Bytes: []byte{0, 0, 0, 'Z', 0, 0, 0, 0xfc}}, "Zü", true},
		{"universal of a wrong length", asn1.RawValue{Tag: certTagUniv, Bytes: []byte{0, 0}}, "", false},
		{"not a string", asn1.RawValue{Tag: asn1.TagInteger, Bytes: []byte{1}}, "", false},
	} {
		a := certATV{Type: cn.oid, Value: tc.val}
		got, ok := a.text()

		assert.Equal(t, tc.ok, ok, tc.name)
		assert.Equal(t, tc.want, got, tc.name)
	}
}

func TestCertSubjectDisplay(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, subj, oneline, slash string
	}{
		{"quoted", "/O=a,b/CN=h", `O = "a,b", CN = h`, "/O=a,b/CN=h"},
		{"leading and trailing space", `/O=\ x\ /CN=h`, `O = \ x\ , CN = h`, "/O= x /CN=h"},
		{"leading hash", `/O=#x/CN=h`, `O = \#x, CN = h`, "/O=#x/CN=h"},
		{"quote and backslash", `/O=a\"b\\c/CN=h`, `O = a\"b\\c, CN = h`, `/O=a"b\c/CN=h`},
		{"non ascii", "/O=Zürich/CN=h", `O = Z\C3\BCrich, CN = h`, `/O=Z\xC3\xBCrich/CN=h`},
		{"joined rdn", "/O=A+OU=B/CN=h", "O = A + OU = B, CN = h", "/O=A/OU=B/CN=h"},
		{"email is ia5", "/emailAddress=a@b.c/CN=h", "emailAddress = a@b.c, CN = h", "/emailAddress=a@b.c/CN=h"},
	} {
		dn, err := certParseSubject(tc.subj)
		require.NoError(t, err, tc.name)

		assert.Equal(t, tc.oneline, dn.oneline(), tc.name)
		assert.Equal(t, tc.slash, dn.slash(), tc.name)
	}
}

func TestCertSubjectLimitsAreEnforced(t *testing.T) {
	t.Parallel()

	for subj, want := range map[string]string{
		"/C=USA/CN=h":      "string too long: C field must be at most 2 characters long (3)",
		"/C=é/CN=h":        "C: invalid characters for a PrintableString: 'é'",
		"/emailAddress=é":  "emailAddress: invalid characters for an IA5String: 'é'",
		"/CN=\xff":         "CN: invalid UTF-8 value",
		"/XX=1":            "Invalid field name 'XX' in the subject",
		"CN=h":             "This name is not in that format",
		"/CN=h\\":          "This name is not in that format",
		"/CN":              "This name is not in that format",
		"/1.2.3.4=value":   "Invalid field name '1.2.3.4' in the subject",
		"/C=US+ST=NC/CN=h": "",
	} {
		_, err := certParseSubject(subj)
		if want == "" {
			assert.NoError(t, err, subj)

			continue
		}

		require.Error(t, err, subj)
		assert.Contains(t, err.Error(), want, subj)
	}
}

func TestCertParseDNRejectsWhatIsNotAName(t *testing.T) {
	t.Parallel()

	_, err := certParseDN([]byte{0x01})
	require.Error(t, err)

	good, err := certParseSubject("/CN=h")
	require.NoError(t, err)

	der, err := good.marshal()
	require.NoError(t, err)

	_, err = certParseDN(append(der, 0))
	require.EqualError(t, err, "trailing data after the name")

	back, err := certParseDN(der)
	require.NoError(t, err)
	assert.Equal(t, "/CN=h", back.slash())
}

// certTestLeaf is a certificate for the chain checks.
func certTestLeaf(t *testing.T, parent *x509.Certificate, parentKey crypto.Signer, tmpl *x509.Certificate,
) *x509.Certificate {
	t.Helper()

	tmpl.SerialNumber = big.NewInt(time.Now().UnixNano())
	key := cmKeys()[2]

	if parent == nil {
		parent, parentKey = tmpl, key
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, key.Public(), parentKey)
	require.NoError(t, err)

	crt, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	return crt
}

// The findings of the chain check, with the numbers and words of `openssl
// verify` (1450-1476 of certmgr.pl).
func TestCertVerifyFindings(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	caKey := cmKeys()[3]
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "CA"}, NotBefore: now.AddDate(-1, 0, 0),
		NotAfter: now.AddDate(5, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, caKey.Public(), caKey)
	require.NoError(t, err)

	ca, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	leaf := func(from, until time.Time, usage []x509.ExtKeyUsage) *x509.Certificate {
		return certTestLeaf(t, ca, caKey, &x509.Certificate{
			Subject: pkix.Name{CommonName: "host"}, NotBefore: from, NotAfter: until, ExtKeyUsage: usage,
		})
	}

	good := leaf(now.AddDate(0, -1, 0), now.AddDate(1, 0, 0), []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})

	for _, tc := range []struct {
		name  string
		leaf  *x509.Certificate
		roots []*x509.Certificate
		code  int
		text  string
	}{
		{"valid", good, []*x509.Certificate{ca}, 0, ""},
		{"no CA", good, nil, certErrGetLocal, "unable to get local issuer certificate"},
		{"self-signed", ca, nil, certErrSelfSigned, "self-signed certificate"},
		{"expired", leaf(now.AddDate(-2, 0, 0), now.AddDate(0, -1, 0), nil), []*x509.Certificate{ca}, certErrExpired,
			"certificate has expired"},
		{"not yet valid", leaf(now.AddDate(0, 1, 0), now.AddDate(1, 0, 0), nil), []*x509.Certificate{ca},
			certErrNotYetValid, "certificate is not yet valid"},
		{"not for servers", leaf(now.AddDate(0, -1, 0), now.AddDate(1, 0, 0), []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}),
			[]*x509.Certificate{ca}, certErrPurpose, "unsuitable certificate purpose"},
	} {
		p := certVerify(tc.leaf, tc.roots, now)
		if tc.code == 0 {
			assert.Nil(t, p, tc.name)

			continue
		}

		require.NotNil(t, p, tc.name)
		assert.Equal(t, tc.code, p.Code, tc.name)
		assert.Equal(t, tc.text, p.Text, tc.name)
	}
}

func TestRunCertMgr_SigningFailuresAreOpensslMessages(t *testing.T) {
	h := newCMTest(t, nil)
	h.ok("createca", "-new")
	h.ok("createcsr", "self", "-new")

	// 841-843: openssl ca fails; its message is shown with the status.
	for _, tc := range []struct {
		name, file, data, want string
	}{
		{"no CA certificate", cmCACrt, "", "Error reading CA certificate in " + cmCACrt},
		{"no CA key", cmCAKey, "", "Error reading CA private key in " + cmCAKey},
		{"unusable request", cmSelfCSR, "junk\n", "Error reading certificate request in " + cmSelfCSR},
	} {
		h2 := newCMTest(t, nil)
		h2.ok("createca", "-new")
		h2.ok("createcsr", "self", "-new")
		h2.fakes.FS.Put(tc.file, []byte(tc.data), 0o600)

		status, out, _ := h2.run("createcrt")
		assert.Equal(t, 1, status, tc.name)
		assert.Contains(t, out, "ERROR: openssl ca failed(1):\n"+tc.want, tc.name)
	}
}
