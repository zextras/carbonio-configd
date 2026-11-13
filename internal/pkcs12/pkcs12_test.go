// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package pkcs12

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixtures are trust stores keytool 21.0.12 (the JDK of Carbonio) wrote
// over generated certificates; all use this password.
const (
	fixturePass = "changeit"
	modernFile  = "testdata/modern.p12"
	legacyFile  = "testdata/legacy.p12"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(name)
	require.NoError(t, err)

	return data
}

// pemCert is the DER of a certificate fixture.
func pemCert(t *testing.T, name string) []byte {
	t.Helper()

	blk, _ := pem.Decode(fixture(t, name))
	require.NotNil(t, blk, name)

	return blk.Bytes
}

func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)

	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// summary is the aliases of a store with the fingerprints of their certificates.
func summary(s *Store) map[string]string {
	out := map[string]string{}

	for _, e := range s.Entries {
		out[e.Alias] = fingerprint(e.Cert)
	}

	return out
}

// The certificates of the fixtures by the alias they have in them.
var (
	fpOne   = "FB1722F3F88F9C735796C319816C0D3AE503E087BDE5A7D7216A3A61AFBADD09"
	fpTwo   = "C9573490F6BA2B2A6C21BBE3C6D5F3EA6B7F5E3125F9BD289B9C58A20A2DE055"
	fpThree = "5493B091BC9853F73E832236C221792FBC45199B2ECA0986C3D8108787BFB4B6"
)

func TestDecodeFixtures(t *testing.T) {
	tests := []struct {
		name   string
		file   string
		want   map[string]string
		scheme scheme
		digest asn1.ObjectIdentifier
		iter   int
	}{
		{
			name:   "what the JDK writes today",
			file:   modernFile,
			want:   map[string]string{"One_CA": fpOne, "zcs-user-two": fpTwo, "café-three": fpThree},
			scheme: scheme{oid: oidPBES2, iterations: 10000, saltLen: 20},
			digest: oidSHA256,
			iter:   10000,
		},
		{
			name:   "what the cacerts of Carbonio has (the JDK keeps it when it rewrites it)",
			file:   legacyFile,
			want:   map[string]string{"Legacy_One-1": fpOne, "zcs-user-two": fpTwo, "legacy three-3": fpThree},
			scheme: scheme{oid: oidPBERC240, iterations: 2048, saltLen: 20},
			digest: oidSHA1,
			iter:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Decode(fixture(t, tt.file), fixturePass)
			require.NoError(t, err)

			assert.Equal(t, tt.want, summary(s))
			assert.Len(t, s.Entries, len(tt.want), "an alias is not repeated")
			assert.Equal(t, tt.scheme, s.certs)
			assert.True(t, tt.digest.Equal(s.macDigest))
			assert.Equal(t, tt.iter, s.macIter)
			assert.False(t, s.foreign)
		})
	}
}

func TestEncodeKeepsWhatTheFileHas(t *testing.T) {
	for _, file := range []string{modernFile, legacyFile} {
		t.Run(filepath.Base(file), func(t *testing.T) {
			orig, err := Decode(fixture(t, file), fixturePass)
			require.NoError(t, err)

			data, err := orig.Encode(fixturePass)
			require.NoError(t, err)
			assert.NotEqual(t, fixture(t, file), data, "new salts")

			again, err := Decode(data, fixturePass)
			require.NoError(t, err)

			// The order, the aliases as they are spelled, the certificates, the
			// attributes and the protection are the same.
			assert.Equal(t, orig.Entries, again.Entries)
			assert.Equal(t, orig.certs, again.certs)
			assert.Equal(t, orig.macDigest, again.macDigest)
			assert.Equal(t, orig.macIter, again.macIter)
			assert.Equal(t, orig.macSaltLen, again.macSaltLen)
		})
	}
}

func TestDecodeFailures(t *testing.T) {
	data := fixture(t, modernFile)

	_, err := Decode(data, "wrong")
	assert.ErrorIs(t, err, ErrPassword)

	flipped := bytes.Clone(data)
	flipped[len(flipped)/2] ^= 0xFF
	_, err = Decode(flipped, fixturePass)
	assert.ErrorIs(t, err, ErrPassword, "a changed file is not told from a wrong password")

	for name, in := range map[string][]byte{
		"text":      []byte("not a keystore\n"),
		"empty":     nil,
		"truncated": data[:len(data)/2],
		"trailing":  append(bytes.Clone(data), 0),
	} {
		_, err = Decode(in, fixturePass)
		assert.ErrorIs(t, err, ErrFormat, name)
	}

	// A version that is not 3, content that is not data.
	for _, mutate := range []func(*pfxPDU){
		func(p *pfxPDU) { p.Version = 2 },
		func(p *pfxPDU) { p.AuthSafe.Type = oidEncryptedData },
		func(p *pfxPDU) { p.MacData = macData{} },
	} {
		var pfx pfxPDU
		require.NoError(t, unmarshalAll(data, &pfx))
		mutate(&pfx)

		out, err := asn1.Marshal(pfx)
		require.NoError(t, err)

		_, err = Decode(out, fixturePass)
		assert.ErrorIs(t, err, ErrFormat)
	}
}

func TestDecodeKeepsTheEntriesOfAStoreWithKeys(t *testing.T) {
	// A keystore of keytool with a private key and a trusted certificate: the
	// certificates are readable, the store is not to be written (the key would
	// be lost).
	s, err := Decode(fixture(t, "testdata/keyentry.p12"), fixturePass)
	require.NoError(t, err)

	got := summary(s)
	assert.Equal(t, fpOne, got["ca"])
	assert.Contains(t, got, "mailboxd")

	_, err = s.Encode(fixturePass)
	assert.ErrorIs(t, err, ErrForeign)
}

func TestSetGetDelete(t *testing.T) {
	s, err := Decode(fixture(t, modernFile), fixturePass)
	require.NoError(t, err)

	// The alias is not case sensitive, and its spelling is kept.
	e, ok := s.Get("ONE_ca")
	require.True(t, ok)
	assert.Equal(t, "One_CA", e.Alias)

	_, ok = s.Get("missing")
	assert.False(t, ok)

	// A set alias replaces the entry in its place.
	position := slices.IndexFunc(s.Entries, func(e Entry) bool { return e.Alias == "One_CA" })
	require.GreaterOrEqual(t, position, 0)

	two := pemCert(t, "testdata/two.pem")
	s.Set("one_CA", two)
	require.Len(t, s.Entries, 3)

	i := slices.IndexFunc(s.Entries, func(e Entry) bool { return e.Alias == "one_CA" })
	assert.Equal(t, position, i, "in its place")
	assert.Equal(t, two, s.Entries[i].Cert)

	// A new one goes to the end.
	s.Set("New", two)
	require.Len(t, s.Entries, 4)
	assert.Equal(t, "New", s.Entries[3].Alias)

	assert.True(t, s.Delete("NEW"))
	assert.False(t, s.Delete("new"))
	assert.Len(t, s.Entries, 3)

	// What was set is written as a trusted certificate: the JDK reads it.
	data, err := s.Encode(fixturePass)
	require.NoError(t, err)

	again, err := Decode(data, fixturePass)
	require.NoError(t, err)
	assert.Equal(t, s.Entries, again.Entries)
	assert.False(t, again.foreign)
}

func TestNewStoreIsWhatTheJDKWrites(t *testing.T) {
	s := New()
	s.Set("zcs-user-one", pemCert(t, "testdata/one.pem"))

	data, err := s.Encode(fixturePass)
	require.NoError(t, err)

	got, err := Decode(data, fixturePass)
	require.NoError(t, err)
	assert.Equal(t, scheme{oid: oidPBES2, iterations: 10000, saltLen: 20}, got.certs)
	assert.True(t, oidSHA256.Equal(got.macDigest))
	assert.Equal(t, 10000, got.macIter)
	assert.Equal(t, map[string]string{"zcs-user-one": fpOne}, summary(got))
}

func TestEmptyStoreIsWhatTheJDKWrites(t *testing.T) {
	s, err := Decode(fixture(t, modernFile), fixturePass)
	require.NoError(t, err)

	for _, alias := range []string{"one_ca", "zcs-user-two", "café-three"} {
		require.True(t, s.Delete(alias))
	}

	data, err := s.Encode(fixturePass)
	require.NoError(t, err)

	// An authenticated safe with nothing in it: the bytes of an empty
	// SEQUENCE (keytool wrote 3000 for the store it emptied).
	var pfx pfxPDU
	require.NoError(t, unmarshalAll(data, &pfx))

	var authSafe []byte
	require.NoError(t, unmarshalAll(pfx.AuthSafe.Content.Bytes, &authSafe))
	assert.Equal(t, []byte{0x30, 0x00}, authSafe)

	got, err := Decode(data, fixturePass)
	require.NoError(t, err)
	assert.Empty(t, got.Entries)
}

func TestSchemesRoundTrip(t *testing.T) {
	one := pemCert(t, "testdata/one.pem")
	schemes := []scheme{
		{oid: oidPBERC240, iterations: 2048, saltLen: 8},
		{oid: oidPBE3DES, iterations: 2048, saltLen: 8},
		{oid: oidPBES2, iterations: 2048, saltLen: 16},
	}

	for _, sch := range schemes {
		for _, digest := range []asn1.ObjectIdentifier{oidSHA1, oidSHA256, oidSHA512} {
			s := New()
			s.certs, s.macDigest, s.macIter, s.macSaltLen = sch, digest, 3, 16
			s.Set("Alias", one)

			data, err := s.Encode("pä$$wörd")
			require.NoError(t, err, sch.oid)

			got, err := Decode(data, "pä$$wörd")
			require.NoError(t, err, sch.oid)
			assert.Equal(t, s.Entries, got.Entries)
			assert.Equal(t, sch, got.certs)
			assert.True(t, digest.Equal(got.macDigest))

			_, err = Decode(data, "other")
			assert.ErrorIs(t, err, ErrPassword)
		}
	}
}

func TestSchemeErrors(t *testing.T) {
	pass := newPasswords(fixturePass)

	_, _, err := pbeCipher(&pkix.AlgorithmIdentifier{Algorithm: asn1.ObjectIdentifier{1, 2, 3}}, pass)
	assert.ErrorIs(t, err, errUnsupported)

	_, err = digestByOID(asn1.ObjectIdentifier{1, 2, 3})
	assert.ErrorIs(t, err, errUnsupported)

	_, err = prfByOID(asn1.ObjectIdentifier{1, 2, 3})
	assert.ErrorIs(t, err, errUnsupported)

	_, err = aesKeyLen(asn1.ObjectIdentifier{1, 2, 3})
	assert.ErrorIs(t, err, errUnsupported)

	// A store protected with an algorithm that is not known cannot be written.
	s := New()
	s.macDigest = asn1.ObjectIdentifier{1, 2, 3}
	s.Set("a", pemCert(t, "testdata/one.pem"))

	_, err = s.Encode(fixturePass)
	assert.ErrorIs(t, err, errUnsupported)

	// PBES2 with a key derivation or a cipher that is not known.
	for _, mutate := range []func(*pbes2Params){
		func(p *pbes2Params) { p.KDF.Algorithm = asn1.ObjectIdentifier{1, 2, 3} },
		func(p *pbes2Params) { p.Scheme.Algorithm = asn1.ObjectIdentifier{1, 2, 3} },
	} {
		alg, _, err := New().certs.protect(pass, bytes.NewReader(make([]byte, 100)))
		require.NoError(t, err)

		var params pbes2Params
		require.NoError(t, unmarshalAll(alg.Parameters.FullBytes, &params))
		mutate(&params)

		alg.Parameters.FullBytes, err = asn1.Marshal(params)
		require.NoError(t, err)

		_, _, err = pbeCipher(&alg, pass)
		assert.Error(t, err)
	}

	// The random source that fails.
	_, _, err = New().certs.protect(pass, bytes.NewReader(nil))
	assert.Error(t, err)

	_, err = New().encode(bytes.NewReader(nil), fixturePass)
	assert.Error(t, err, "the salt of the MAC")
}

func TestWeirdFriendlyName(t *testing.T) {
	_, err := friendlyName(&attribute{Values: asn1.RawValue{Bytes: []byte{0x04, 0x01, 'a'}}})
	assert.Error(t, err, "not a BMPString")

	_, err = friendlyName(&attribute{Values: asn1.RawValue{Bytes: []byte{bmpStringTag, 0x01, 'a'}}})
	assert.Error(t, err, "an odd number of bytes")

	_, err = friendlyName(&attribute{Values: asn1.RawValue{Bytes: []byte{0xFF}}})
	assert.Error(t, err, "not even DER")
}

func TestPasswordsOfTheSchemes(t *testing.T) {
	// The BMPString of PKCS#12 ends with a zero character; the surrogate
	// pairs are kept apart.
	assert.Equal(t, []byte{0, 'a', 0, 0xE9, 0, 0}, newPasswords("aé").bmp)
	assert.Equal(t, []byte{0xD8, 0x3D, 0xDE, 0x00, 0, 0}, newPasswords("😀").bmp)
	assert.Equal(t, []byte{0, 0}, newPasswords("").bmp)
}

// TestRC2 are the test vectors of RFC 2268.
func TestRC2(t *testing.T) {
	tests := []struct {
		key  string
		bits int
		plain,
		cipher string
	}{
		{"0000000000000000", 63, "0000000000000000", "ebb773f993278eff"},
		{"ffffffffffffffff", 64, "ffffffffffffffff", "278b27e42e2f0d49"},
		{"3000000000000000", 64, "1000000000000001", "30649edf9be7d2c2"},
		{"88", 64, "0000000000000000", "61a8a244adacccf0"},
		{"88bca90e90875a", 64, "0000000000000000", "6ccf4308974c267f"},
		{"88bca90e90875a7f0f79c384627bafb2", 64, "0000000000000000", "1a807d272bbe5db1"},
		{"88bca90e90875a7f0f79c384627bafb2", 128, "0000000000000000", "2269552ab0f85ca6"},
	}

	for _, tt := range tests {
		key, _ := hex.DecodeString(tt.key)
		plain, _ := hex.DecodeString(tt.plain)

		c := newRC2(key, tt.bits)
		got := make([]byte, rc2BlockSize)
		c.Encrypt(got, plain)
		assert.Equal(t, tt.cipher, hex.EncodeToString(got), tt.key)

		back := make([]byte, rc2BlockSize)
		c.Decrypt(back, got)
		assert.Equal(t, plain, back, tt.key)
	}
}

func TestKDF(t *testing.T) {
	// A key longer than a digest comes from more than one round.
	short := kdf(sha1.New, newPasswords("smeg").bmp, []byte{1, 2, 3, 4, 5, 6, 7, 8}, kdfIDKey, 1, 24)
	long := kdf(sha1.New, newPasswords("smeg").bmp, []byte{1, 2, 3, 4, 5, 6, 7, 8}, kdfIDKey, 1, 48)
	assert.Len(t, short, 24)
	assert.Equal(t, short, long[:24])
	assert.NotEqual(t, long[:24], long[24:])
}

// keytoolList is what keytool lists for a store but the dates, which are the
// day it was run.
func keytoolList(t *testing.T, keytool, file string) string {
	t.Helper()

	out, err := exec.Command(keytool, "-list", "-v", "-keystore", file, "-storepass", fixturePass).CombinedOutput()
	require.NoError(t, err, string(out))

	return regexp.MustCompile(`(?m)^Creation date:.*\n`).ReplaceAllString(string(out), "")
}

// TestKeytoolReadsWhatIsWritten checks the files against the real thing, where
// there is a keytool.
func TestKeytoolReadsWhatIsWritten(t *testing.T) {
	keytool, err := exec.LookPath("keytool")
	if err != nil {
		t.Skip("no keytool")
	}

	dir := t.TempDir()

	for _, file := range []string{modernFile, legacyFile} {
		s, err := Decode(fixture(t, file), fixturePass)
		require.NoError(t, err)

		data, err := s.Encode(fixturePass)
		require.NoError(t, err)

		out := filepath.Join(dir, filepath.Base(file))
		require.NoError(t, os.WriteFile(out, data, 0o600))
		assert.Equal(t, keytoolList(t, keytool, file), keytoolList(t, keytool, out), file)

		// An entry set natively is the one keytool lists, with the case of the
		// alias in the file.
		s.Set("Native_Alias", pemCert(t, "testdata/two.pem"))
		s.Delete("zcs-user-two")

		data, err = s.Encode(fixturePass)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(out, data, 0o600))

		list := keytoolList(t, keytool, out)
		assert.Contains(t, list, "Alias name: native_alias\n")
		assert.NotContains(t, list, "Alias name: zcs-user-two\n")
		assert.Contains(t, list, "trustedCertEntry")
		assert.Equal(t, 3, strings.Count(list, "Entry type: trustedCertEntry"))
	}
}

func TestDecodeFileOfOpenSSL(t *testing.T) {
	// openssl pkcs12 -export: the key in a data element and the certificate
	// encrypted with PBES2 over AES-128, the MAC SHA-512. Its certificate is not
	// marked as trusted: it belongs to the key, and the store is not to be
	// written.
	s, err := Decode(fixture(t, "testdata/openssl.p12"), fixturePass)
	require.NoError(t, err)
	require.Len(t, s.Entries, 1)
	assert.Equal(t, "Openssl Entry", s.Entries[0].Alias)
	assert.Equal(t, scheme{oid: oidPBES2, iterations: 2048, saltLen: 16}, s.certs)
	assert.True(t, oidSHA512.Equal(s.macDigest))

	_, err = s.Encode(fixturePass)
	assert.ErrorIs(t, err, ErrForeign)
}
