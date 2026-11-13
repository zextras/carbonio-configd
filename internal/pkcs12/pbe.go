// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package pkcs12

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/des" //nolint:gosec // 3DES is one of the PBE schemes a PKCS#12 file may use
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha1" //nolint:gosec // SHA-1 is the digest of the PKCS#12 files of the JDK 8-17 and of its legacy cacerts
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"hash"
	"io"
	"unicode/utf16"
)

// Object identifiers of the password based schemes and digests.
var (
	oidPBERC240   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 1, 6}
	oidPBE3DES    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 1, 3}
	oidPBES2      = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 13}
	oidPBKDF2     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 12}
	oidHMACSHA1   = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 7}
	oidHMACSHA224 = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 8}
	oidHMACSHA256 = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 9}
	oidHMACSHA384 = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 10}
	oidHMACSHA512 = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 11}
	oidAES128CBC  = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 2}
	oidAES192CBC  = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 22}
	oidAES256CBC  = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 42}
	oidSHA1       = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	oidSHA224     = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 4}
	oidSHA256     = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidSHA384     = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 2}
	oidSHA512     = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 3}
)

const (
	// rc2KeyLen and rc2KeyBits are the key of PBEWithSHA1AndRC2_40.
	rc2KeyLen  = 5
	rc2KeyBits = 40
	// desKeyLen is the key of PBEWithSHA1AndDESede.
	desKeyLen = 24
	// kdfIDKey, kdfIDIV and kdfIDMAC are the purposes of RFC 7292 B.3.
	kdfIDKey = 1
	kdfIDIV  = 2
	kdfIDMAC = 3
	// aes256KeyLen is the key of the PBES2 scheme written.
	aes256KeyLen = 32
)

// errUnsupported is wrapped by the errors of an algorithm that is not known.
var errUnsupported = errors.New("unsupported algorithm")

// passwords are a password as the schemes of a file read it: the schemes of
// PKCS#12 use the BMPString (with its terminator), PBES2 uses the UTF-8.
type passwords struct {
	bmp  []byte
	utf8 string
}

func newPasswords(pass string) passwords {
	units := utf16.Encode([]rune(pass))
	bmp := make([]byte, 0, 2*len(units)+2)

	for _, u := range units {
		bmp = append(bmp, byte(u>>8), byte(u)) //nolint:gosec // a UTF-16 unit: two bytes
	}

	return passwords{bmp: append(bmp, 0, 0), utf8: pass}
}

// digests are the hashes of the MACs and of the PBKDF2 PRFs, by the object
// identifier of the digest.
var digests = []struct {
	digest asn1.ObjectIdentifier
	prf    asn1.ObjectIdentifier
	New    func() hash.Hash
}{
	{oidSHA1, oidHMACSHA1, sha1.New},
	{oidSHA224, oidHMACSHA224, sha256.New224},
	{oidSHA256, oidHMACSHA256, sha256.New},
	{oidSHA384, oidHMACSHA384, sha512.New384},
	{oidSHA512, oidHMACSHA512, sha512.New},
}

func digestByOID(oid asn1.ObjectIdentifier) (func() hash.Hash, error) {
	for _, d := range digests {
		if d.digest.Equal(oid) {
			return d.New, nil
		}
	}

	return nil, fmt.Errorf("%w: digest %v", errUnsupported, oid)
}

func prfByOID(oid asn1.ObjectIdentifier) (func() hash.Hash, error) {
	for _, d := range digests {
		if d.prf.Equal(oid) {
			return d.New, nil
		}
	}

	return nil, fmt.Errorf("%w: PBKDF2 PRF %v", errUnsupported, oid)
}

// fill repeats data up to the next multiple of size bytes.
func fill(data []byte, size int) []byte {
	if len(data) == 0 {
		return nil
	}

	out := make([]byte, (len(data)+size-1)/size*size)
	for i := range out {
		out[i] = data[i%len(data)]
	}

	return out
}

// kdf is the key derivation of RFC 7292 appendix B.
func kdf(newHash func() hash.Hash, pass, salt []byte, id byte, iterations, size int) []byte {
	h := newHash()
	v := h.BlockSize()
	block := bytes.Repeat([]byte{id}, v)
	input := append(fill(salt, v), fill(pass, v)...)
	out := make([]byte, 0, size+h.Size())

	for {
		h.Reset()
		h.Write(block)
		h.Write(input)
		a := h.Sum(nil)

		for i := 1; i < iterations; i++ {
			h.Reset()
			h.Write(a)
			a = h.Sum(nil)
		}

		if out = append(out, a...); len(out) >= size {
			return out[:size]
		}

		b := fill(a, v)
		for j := 0; j < len(input); j += v {
			addWithOne(input[j:j+v], b)
		}
	}
}

// addWithOne sets block to (block + b + 1) modulo 2^(8*len(block)).
func addWithOne(block, b []byte) {
	carry := 1

	for k := len(block) - 1; k >= 0; k-- {
		sum := int(block[k]) + int(b[k]) + carry
		block[k] = byte(sum) //nolint:gosec // the sum is kept in a byte, as RFC 7292 B.2 says
		carry = sum >> 8
	}
}

// pbeParams are the parameters of the schemes of PKCS#12.
type pbeParams struct {
	Salt       []byte
	Iterations int
}

// pbkdf2Params are the parameters of PBKDF2, as the JDK writes them.
type pbkdf2Params struct {
	Salt       []byte
	Iterations int
	KeyLength  int                      `asn1:"optional"`
	PRF        pkix.AlgorithmIdentifier `asn1:"optional"`
}

// pbes2Params are the parameters of PBES2.
type pbes2Params struct {
	KDF    pkix.AlgorithmIdentifier
	Scheme pkix.AlgorithmIdentifier
}

// scheme is how a safe contents or a bag was protected, with what is needed
// to protect another the same way.
type scheme struct {
	oid        asn1.ObjectIdentifier
	iterations int
	saltLen    int
}

// cbc is the cipher of a scheme: the block cipher and the IV.
type cbc struct {
	block cipher.Block
	iv    []byte
}

// pbeCipher is the CBC cipher a scheme with its parameters gives for pass.
func pbeCipher(alg *pkix.AlgorithmIdentifier, pass passwords) (cbc, scheme, error) {
	switch {
	case alg.Algorithm.Equal(oidPBES2):
		return pbes2Cipher(alg, pass)
	case alg.Algorithm.Equal(oidPBERC240), alg.Algorithm.Equal(oidPBE3DES):
		return pkcs12Cipher(alg, pass)
	}

	return cbc{}, scheme{}, fmt.Errorf("%w: encryption %v", errUnsupported, alg.Algorithm)
}

func pkcs12Cipher(alg *pkix.AlgorithmIdentifier, pass passwords) (cbc, scheme, error) {
	var params pbeParams
	if err := unmarshalAll(alg.Parameters.FullBytes, &params); err != nil {
		return cbc{}, scheme{}, err
	}

	if params.Iterations < 1 {
		return cbc{}, scheme{}, errors.New("no iterations")
	}

	s := scheme{oid: alg.Algorithm, iterations: params.Iterations, saltLen: len(params.Salt)}

	return newPKCS12Cipher(s, params.Salt, pass), s, nil
}

// newPKCS12Cipher is the cipher of PBEWithSHA1AndRC2_40 or of PBEWithSHA1AndDESede.
func newPKCS12Cipher(s scheme, salt []byte, pass passwords) cbc {
	keyLen := desKeyLen
	if s.oid.Equal(oidPBERC240) {
		keyLen = rc2KeyLen
	}

	key := kdf(sha1.New, pass.bmp, salt, kdfIDKey, s.iterations, keyLen)
	iv := kdf(sha1.New, pass.bmp, salt, kdfIDIV, s.iterations, des.BlockSize)

	if s.oid.Equal(oidPBERC240) {
		return cbc{block: newRC2(key, rc2KeyBits), iv: iv}
	}

	// A 24 bytes key is always valid.
	block, _ := des.NewTripleDESCipher(key) //nolint:gosec // see the import

	return cbc{block: block, iv: iv}
}

func pbes2Cipher(alg *pkix.AlgorithmIdentifier, pass passwords) (cbc, scheme, error) {
	var params pbes2Params
	if err := unmarshalAll(alg.Parameters.FullBytes, &params); err != nil {
		return cbc{}, scheme{}, err
	}

	if !params.KDF.Algorithm.Equal(oidPBKDF2) {
		return cbc{}, scheme{}, fmt.Errorf("%w: key derivation %v", errUnsupported, params.KDF.Algorithm)
	}

	var kp pbkdf2Params
	if err := unmarshalAll(params.KDF.Parameters.FullBytes, &kp); err != nil {
		return cbc{}, scheme{}, err
	}

	keyLen, err := aesKeyLen(params.Scheme.Algorithm)
	if err != nil {
		return cbc{}, scheme{}, err
	}

	prf := oidHMACSHA1
	if len(kp.PRF.Algorithm) > 0 {
		prf = kp.PRF.Algorithm
	}

	newHash, err := prfByOID(prf)
	if err != nil {
		return cbc{}, scheme{}, err
	}

	var iv []byte
	if err := unmarshalAll(params.Scheme.Parameters.FullBytes, &iv); err != nil {
		return cbc{}, scheme{}, err
	}

	if kp.Iterations < 1 {
		return cbc{}, scheme{}, errors.New("no iterations")
	}

	key, err := pbkdf2.Key(newHash, pass.utf8, kp.Salt, kp.Iterations, keyLen)
	if err != nil {
		return cbc{}, scheme{}, err
	}

	block, err := aes.NewCipher(key)
	if err != nil || len(iv) != aes.BlockSize {
		return cbc{}, scheme{}, errors.New("bad encryption parameters")
	}

	return cbc{block: block, iv: iv}, scheme{oid: oidPBES2, iterations: kp.Iterations, saltLen: len(kp.Salt)}, nil
}

// aesKeyLen is the key size of an AES-CBC object identifier.
func aesKeyLen(oid asn1.ObjectIdentifier) (int, error) {
	switch {
	case oid.Equal(oidAES128CBC):
		return 16, nil
	case oid.Equal(oidAES192CBC):
		return 24, nil
	case oid.Equal(oidAES256CBC):
		return aes256KeyLen, nil
	}

	return 0, fmt.Errorf("%w: cipher %v", errUnsupported, oid)
}

// decrypt is the PKCS#7 padded plain text of data.
func (c cbc) decrypt(data []byte) ([]byte, error) {
	size := c.block.BlockSize()
	if len(data) == 0 || len(data)%size != 0 {
		return nil, ErrPassword
	}

	plain := make([]byte, len(data))
	cipher.NewCBCDecrypter(c.block, c.iv).CryptBlocks(plain, data)

	pad := int(plain[len(plain)-1])
	if pad == 0 || pad > size || !bytes.HasSuffix(plain, bytes.Repeat([]byte{byte(pad)}, pad)) { //nolint:gosec // 1..16
		return nil, ErrPassword
	}

	return plain[:len(plain)-pad], nil
}

// encrypt is data padded with PKCS#7 and encrypted.
func (c cbc) encrypt(data []byte) []byte {
	size := c.block.BlockSize()
	pad := size - len(data)%size
	plain := append(bytes.Clone(data), bytes.Repeat([]byte{byte(pad)}, pad)...) //nolint:gosec // pad is 1..16

	cipher.NewCBCEncrypter(c.block, c.iv).CryptBlocks(plain, plain)

	return plain
}

// protect is the algorithm identifier and the cipher that protect with the
// scheme, with a new salt and IV.
func (s scheme) protect(pass passwords, random io.Reader) (pkix.AlgorithmIdentifier, cbc, error) {
	salt := make([]byte, s.saltLen)
	if _, err := io.ReadFull(random, salt); err != nil {
		return pkix.AlgorithmIdentifier{}, cbc{}, err
	}

	if s.oid.Equal(oidPBES2) {
		return s.protectPBES2(pass, salt, random)
	}

	params, err := asn1.Marshal(pbeParams{Salt: salt, Iterations: s.iterations})
	if err != nil {
		return pkix.AlgorithmIdentifier{}, cbc{}, err
	}

	alg := pkix.AlgorithmIdentifier{Algorithm: s.oid, Parameters: asn1.RawValue{FullBytes: params}}

	return alg, newPKCS12Cipher(s, salt, pass), nil
}

// protectPBES2 is PBES2 with PBKDF2 over HMAC-SHA256 and AES-256-CBC, which
// is what the JDK writes.
func (s scheme) protectPBES2(pass passwords, salt []byte, random io.Reader) (pkix.AlgorithmIdentifier, cbc, error) {
	iv := make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(random, iv); err != nil {
		return pkix.AlgorithmIdentifier{}, cbc{}, err
	}

	kp, err := asn1.Marshal(pbkdf2Params{
		Salt: salt, Iterations: s.iterations, KeyLength: aes256KeyLen,
		PRF: pkix.AlgorithmIdentifier{Algorithm: oidHMACSHA256, Parameters: asn1.NullRawValue},
	})
	if err != nil {
		return pkix.AlgorithmIdentifier{}, cbc{}, err
	}

	ivDER, err := asn1.Marshal(iv)
	if err != nil {
		return pkix.AlgorithmIdentifier{}, cbc{}, err
	}

	params, err := asn1.Marshal(pbes2Params{
		KDF:    pkix.AlgorithmIdentifier{Algorithm: oidPBKDF2, Parameters: asn1.RawValue{FullBytes: kp}},
		Scheme: pkix.AlgorithmIdentifier{Algorithm: oidAES256CBC, Parameters: asn1.RawValue{FullBytes: ivDER}},
	})
	if err != nil {
		return pkix.AlgorithmIdentifier{}, cbc{}, err
	}

	alg := pkix.AlgorithmIdentifier{Algorithm: oidPBES2, Parameters: asn1.RawValue{FullBytes: params}}

	c, _, err := pbes2Cipher(&alg, pass)

	return alg, c, err
}

// unmarshalAll is asn1.Unmarshal that does not leave bytes behind.
func unmarshalAll(der []byte, v any) error {
	rest, err := asn1.Unmarshal(der, v)
	if err != nil {
		return err
	}

	if len(rest) != 0 {
		return errors.New("trailing data")
	}

	return nil
}

// mac is the HMAC of data under the PKCS#12 derived key.
func mac(newHash func() hash.Hash, pass passwords, salt []byte, iterations int, data []byte) []byte {
	key := kdf(newHash, pass.bmp, salt, kdfIDMAC, iterations, newHash().Size())
	m := hmac.New(newHash, key)
	m.Write(data)

	return m.Sum(nil)
}
