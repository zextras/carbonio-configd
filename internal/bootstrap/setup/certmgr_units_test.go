// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCertParseSubject(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, subj, slash, err string
	}{
		{"plain", "/C=US/O=Acme/CN=host", "/C=US/O=Acme/CN=host", ""},
		{"escaped slash", `/O=A\/B/CN=h`, "/O=A/B/CN=h", ""},
		{"empty values are skipped", "/O=/CN=h", "/CN=h", ""},
		{"no leading slash", "CN=h", "", "subject name is expected to be in the format"},
		{"unknown attribute", "/XX=1", "", "XX"},
		{"no equal sign", "/CN", "", "This name is not in that format"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dn, err := certParseSubject(tc.subj)
			if tc.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.slash, dn.slash())
		})
	}
}

// The hash of the CA subject of the legacy tool's `openssl x509 -hash`
// (the name of the link in conf/ca).
func TestCertCanonHash(t *testing.T) {
	t.Parallel()

	dn, err := certParseSubject("/O=CA/OU=Zextras Carbonio/CN=mail.example.com")
	require.NoError(t, err)

	got, err := dn.canonHash()
	require.NoError(t, err)
	assert.Equal(t, "3cafeb07", got)
}

func TestCertSANLine(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("http://u.example.com/")
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber:   big.NewInt(1),
		Subject:        pkix.Name{CommonName: "x"},
		DNSNames:       []string{"a.example.com"},
		EmailAddresses: []string{"x@example.com"},
		IPAddresses:    []net.IP{net.ParseIP("10.1.2.3"), net.ParseIP("2001:db8::1")},
		URIs:           []*url.URL{u},
	}

	der, err := x509.CreateCertificate(bytes.NewReader(make([]byte, 4096)), tmpl, tmpl, cmKeys()[0].Public(), cmKeys()[0])
	if err != nil {
		t.Skipf("no certificate to read the names from: %v", err)
	}

	crt, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	assert.Equal(t, "DNS:a.example.com, email:x@example.com, IP Address:10.1.2.3, "+
		"IP Address:2001:DB8:0:0:0:0:0:1, URI:http://u.example.com/", certSANLine(crt.Extensions))
	assert.Empty(t, certSANLine(nil))
}

func TestCertParseArgs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
		ok   bool
		errs string
		chk  func(*testing.T, *certOpts)
	}{
		{"abbreviated options", []string{"createcrt", "-subjectalt", "a, b", "-days=7", "--new"}, true, "",
			func(t *testing.T, o *certOpts) {
				assert.Equal(t, []string{"a", "b"}, o.SubjectAltNames)
				assert.Equal(t, 7, o.Days)
				assert.True(t, o.HasDays)
				assert.True(t, o.New)
			}},
		{"debug without a level", []string{"savecrt", "self", "-debug"}, true, "",
			func(t *testing.T, o *certOpts) { assert.Equal(t, 1, o.Debug) }},
		{"debug level", []string{"savecrt", "self", "-debug", "3"}, true, "",
			func(t *testing.T, o *certOpts) { assert.Equal(t, 3, o.Debug) }},
		{"deploy is repeatable", []string{"deploycrt", "self", "-deploy", "ldap,mta", "-deploy", "proxy"}, true, "",
			func(t *testing.T, o *certOpts) { assert.Equal(t, []string{"ldap", "mta", "proxy"}, o.Deploy) }},
		{"unknown option", []string{"createca", "-bogus"}, false, "Unknown option: bogus", nil},
		{"missing value", []string{"createca", "-digest"}, false, "Option digest requires an argument", nil},
		{"ambiguous", []string{"createca", "-n"}, false, "Option n is ambiguous", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var errs bytes.Buffer

			p := certParseArgs(tc.args, &errs)

			assert.Equal(t, tc.ok, p.optsOK)
			assert.Contains(t, errs.String(), tc.errs)

			if tc.chk != nil {
				tc.chk(t, p.opts)
			}
		})
	}
}

func TestCertSigAlg(t *testing.T) {
	t.Parallel()

	pub := cmKeys()[0].Public()

	for digest, want := range map[string]x509.SignatureAlgorithm{
		"sha1": x509.SHA1WithRSA, "sha256": x509.SHA256WithRSA,
		"sha384": x509.SHA384WithRSA, "sha512": x509.SHA512WithRSA,
	} {
		got, err := certSigAlg(pub, digest)
		require.NoError(t, err, digest)
		assert.Equal(t, want, got, digest)
	}

	_, err := certSigAlg(pub, "sha224")
	require.EqualError(t, err, "unsupported digest 'sha224' (supported: sha1, sha256, sha384, sha512)")
}

// The codes of `openssl verify` that the chain check reports (verifycrtchain
// and the checks of deploycrt comm, 1450-1476).
func TestCertVerifyProblems(t *testing.T) {
	t.Parallel()

	h := newCMTest(t, nil)
	h.bootstrap()

	ca, crt := h.cert(cmCACrt), h.cert(cmSelfCrt)
	now := h.e.Sys.Clock.Now()

	for _, tc := range []struct {
		name string
		at   time.Time
		ca   []*x509.Certificate
		code int
	}{
		{"valid", now, []*x509.Certificate{ca}, 0},
		{"no issuer", now, nil, 20},
		{"expired", now.AddDate(10, 0, 0), []*x509.Certificate{ca}, 10},
		{"not yet valid", now.AddDate(-1, 0, 0), []*x509.Certificate{ca}, 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := certVerify(crt, tc.ca, tc.at)
			if tc.code == 0 {
				assert.Nil(t, p)

				return
			}

			require.NotNil(t, p)
			assert.Equal(t, tc.code, p.Code)
		})
	}
}
