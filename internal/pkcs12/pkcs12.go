// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package pkcs12 reads and writes the PKCS#12 files the JDK uses as a trust
// store (the default type of keytool since Java 9): certificates under an
// alias, each marked as trusted. Unlike the libraries that decode PKCS#12
// files, it keeps the alias (friendly name) of every certificate and how the
// file was protected, so a store that is read and written again is the one
// keytool would have written: the aliases and certificates are the same, and
// so are the encryption and MAC algorithms and their iteration counts, which
// the JDK keeps when it rewrites a file.
package pkcs12

import (
	"crypto/rand"
	"crypto/subtle"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf16"
)

// errWrap is the format of an error of one kind (the first) with its cause.
const errWrap = "%w: %w"

var (
	// ErrPassword is returned when the password does not open the file or the
	// file was modified.
	ErrPassword = errors.New("keystore password was incorrect")
	// ErrFormat is returned when the data is not a PKCS#12 file.
	ErrFormat = errors.New("not a PKCS#12 file")
	// ErrForeign is returned when a store with entries other than trusted
	// certificates (private keys) is to be written: they would be lost.
	ErrForeign = errors.New("the store has entries that are not trusted certificates")
)

// Object identifiers of the structures of a PKCS#12 file.
var (
	oidData          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidEncryptedData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 6}
	oidCertBag       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 10, 1, 3}
	oidX509Cert      = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 22, 1}
	oidFriendlyName  = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 20}
	// oidTrusted is the attribute the JDK puts on a trusted certificate; its
	// value is the set of the extended key usages the certificate is trusted for.
	oidTrusted = asn1.ObjectIdentifier{2, 16, 840, 1, 113894, 746875, 1, 1}
	// oidAnyKeyUsage is the usage of the JDK for a trusted certificate: any.
	oidAnyKeyUsage = asn1.ObjectIdentifier{2, 5, 29, 37, 0}
)

const (
	pfxVersion = 3
	// defaultIterations, defaultSaltLen are the defaults of the JDK 18 and later
	// for a new file: PBES2 with HmacSHA256 and AES-256, MAC HmacSHA256.
	defaultIterations = 10000
	defaultSaltLen    = 20
	// bmpStringTag is the ASN.1 tag of a BMPString.
	bmpStringTag = 30
	// setTag is the ASN.1 tag of a SET.
	setTag = 17
)

type pfxPDU struct {
	Version  int
	AuthSafe contentInfo
	MacData  macData `asn1:"optional"`
}

type contentInfo struct {
	Type    asn1.ObjectIdentifier
	Content asn1.RawValue `asn1:"tag:0,explicit,optional"`
}

type macData struct {
	Mac        digestInfo
	Salt       []byte
	Iterations int `asn1:"optional"`
}

type digestInfo struct {
	Algorithm pkix.AlgorithmIdentifier
	Digest    []byte
}

type encryptedData struct {
	Version int
	Content encryptedContent
}

type encryptedContent struct {
	Type      asn1.ObjectIdentifier
	Algorithm pkix.AlgorithmIdentifier
	Data      []byte `asn1:"tag:0,optional"`
}

type safeBag struct {
	ID         asn1.ObjectIdentifier
	Value      asn1.RawValue `asn1:"tag:0,explicit"`
	Attributes []attribute   `asn1:"set,optional"`
}

type attribute struct {
	ID     asn1.ObjectIdentifier
	Values asn1.RawValue `asn1:"set"`
}

type certBag struct {
	ID   asn1.ObjectIdentifier
	Data []byte `asn1:"tag:0,explicit"`
}

// Entry is a certificate of a store under its alias.
type Entry struct {
	// Alias is the friendly name of the certificate, as it is in the file.
	Alias string
	// Cert is the DER of the certificate.
	Cert []byte
	// attrs are the attributes of the bag but the friendly name, as read.
	attrs []attribute
}

// Store is the content of a PKCS#12 trust store.
type Store struct {
	// Entries are the certificates, in the order of the file.
	Entries []Entry

	certs      scheme
	macDigest  asn1.ObjectIdentifier
	macIter    int
	macSaltLen int
	foreign    bool
}

// New is an empty store, protected as the JDK protects a new file.
func New() *Store {
	return &Store{
		certs:      scheme{oid: oidPBES2, iterations: defaultIterations, saltLen: defaultSaltLen},
		macDigest:  oidSHA256,
		macIter:    defaultIterations,
		macSaltLen: defaultSaltLen,
	}
}

// key is the name an alias is looked up by: the JDK ignores the case.
func key(alias string) string { return strings.ToLower(alias) }

func (s *Store) index(alias string) int {
	return slices.IndexFunc(s.Entries, func(e Entry) bool { return key(e.Alias) == key(alias) })
}

// Get is the entry of an alias.
func (s *Store) Get(alias string) (Entry, bool) {
	if i := s.index(alias); i >= 0 {
		return s.Entries[i], true
	}

	return Entry{}, false
}

// Set puts a certificate (DER) under an alias, in the place of the entry the
// alias had, or at the end. The alias is written as given: the JDK looks the
// aliases up in lower case but keeps their spelling in the file.
func (s *Store) Set(alias string, cert []byte) {
	e := Entry{Alias: alias, Cert: slices.Clone(cert), attrs: []attribute{trustedAttribute()}}

	if i := s.index(alias); i >= 0 {
		s.Entries[i] = e

		return
	}

	s.Entries = append(s.Entries, e)
}

// Delete removes the entry of an alias and reports whether it was there.
func (s *Store) Delete(alias string) bool {
	i := s.index(alias)
	if i >= 0 {
		s.Entries = slices.Delete(s.Entries, i, i+1)
	}

	return i >= 0
}

// Decode reads a PKCS#12 file. The certificate bags are the entries; a bag
// of another type (a private key), or a certificate that is not marked as
// trusted, is not kept, and the store cannot be written (see ErrForeign), but
// the entries can still be read.
func Decode(data []byte, password string) (*Store, error) {
	var pfx pfxPDU
	if err := unmarshalAll(data, &pfx); err != nil {
		return nil, fmt.Errorf(errWrap, ErrFormat, err)
	}

	if pfx.Version != pfxVersion || !pfx.AuthSafe.Type.Equal(oidData) {
		return nil, fmt.Errorf("%w: unsupported version or content", ErrFormat)
	}

	var authSafe []byte
	if err := unmarshalAll(pfx.AuthSafe.Content.Bytes, &authSafe); err != nil {
		return nil, fmt.Errorf(errWrap, ErrFormat, err)
	}

	pass := newPasswords(password)

	s := &Store{}
	if err := s.verify(&pfx.MacData, authSafe, pass); err != nil {
		return nil, err
	}

	var infos []contentInfo
	if err := unmarshalAll(authSafe, &infos); err != nil {
		return nil, fmt.Errorf(errWrap, ErrFormat, err)
	}

	for i := range infos {
		if err := s.load(&infos[i], pass); err != nil {
			return nil, err
		}
	}

	if len(s.certs.oid) == 0 {
		s.certs = New().certs
	}

	return s, nil
}

// verify checks the MAC and keeps how it was made.
func (s *Store) verify(md *macData, authSafe []byte, pass passwords) error {
	if len(md.Mac.Algorithm.Algorithm) == 0 {
		return fmt.Errorf("%w: no MAC", ErrFormat)
	}

	newHash, err := digestByOID(md.Mac.Algorithm.Algorithm)
	if err != nil {
		return err
	}

	s.macDigest, s.macIter, s.macSaltLen = md.Mac.Algorithm.Algorithm, max(md.Iterations, 1), len(md.Salt)

	if subtle.ConstantTimeCompare(mac(newHash, pass, md.Salt, s.macIter, authSafe), md.Mac.Digest) != 1 {
		return ErrPassword
	}

	return nil
}

// load reads the bags of an element of the authenticated safe.
func (s *Store) load(ci *contentInfo, pass passwords) error {
	var contents []byte

	switch {
	case ci.Type.Equal(oidData):
		if err := unmarshalAll(ci.Content.Bytes, &contents); err != nil {
			return fmt.Errorf(errWrap, ErrFormat, err)
		}
	case ci.Type.Equal(oidEncryptedData):
		plain, err := s.decryptData(ci, pass)
		if err != nil {
			return err
		}

		contents = plain
	default:
		return fmt.Errorf("%w: content type %v", errUnsupported, ci.Type)
	}

	var bags []safeBag
	if err := unmarshalAll(contents, &bags); err != nil {
		return fmt.Errorf(errWrap, ErrFormat, err)
	}

	for i := range bags {
		if err := s.addBag(&bags[i]); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) decryptData(ci *contentInfo, pass passwords) ([]byte, error) {
	var ed encryptedData
	if err := unmarshalAll(ci.Content.Bytes, &ed); err != nil {
		return nil, fmt.Errorf(errWrap, ErrFormat, err)
	}

	c, sch, err := pbeCipher(&ed.Content.Algorithm, pass)
	if err != nil {
		return nil, fmt.Errorf(errWrap, ErrFormat, err)
	}

	if len(s.certs.oid) == 0 {
		s.certs = sch
	}

	return c.decrypt(ed.Content.Data)
}

// addBag keeps the certificate of a bag; any other is only noted.
func (s *Store) addBag(bag *safeBag) error {
	if !bag.ID.Equal(oidCertBag) {
		s.foreign = true

		return nil
	}

	var cb certBag
	if err := unmarshalAll(bag.Value.Bytes, &cb); err != nil || !cb.ID.Equal(oidX509Cert) {
		return fmt.Errorf("%w: bad certificate bag", ErrFormat)
	}

	e := Entry{Cert: cb.Data}
	named, trusted := false, false

	for i := range bag.Attributes {
		a := &bag.Attributes[i]

		switch {
		case a.ID.Equal(oidFriendlyName):
			alias, err := friendlyName(a)
			if err != nil {
				return fmt.Errorf(errWrap, ErrFormat, err)
			}

			e.Alias, named = alias, true
		case a.ID.Equal(oidTrusted):
			trusted = true

			e.attrs = append(e.attrs, *a)
		default:
			e.attrs = append(e.attrs, *a)
		}
	}

	if !named || !trusted {
		s.foreign = true
	}

	s.Entries = append(s.Entries, e)

	return nil
}

// friendlyName is the text of a friendly name attribute (a BMPString).
func friendlyName(a *attribute) (string, error) {
	var name asn1.RawValue
	if err := unmarshalAll(a.Values.Bytes, &name); err != nil {
		return "", err
	}

	if name.Tag != bmpStringTag || len(name.Bytes)%2 != 0 {
		return "", errors.New("friendly name is not a BMPString")
	}

	units := make([]uint16, len(name.Bytes)/2)
	for i := range units {
		units[i] = uint16(name.Bytes[2*i])<<8 | uint16(name.Bytes[2*i+1])
	}

	return string(utf16.Decode(units)), nil
}

// trustedAttribute is the attribute of the JDK that marks a certificate as
// trusted for any use.
func trustedAttribute() attribute {
	usage, _ := asn1.Marshal(oidAnyKeyUsage)

	return attribute{ID: oidTrusted, Values: setOf(usage)}
}

// setOf is the SET of the elements, as an attribute holds its values.
func setOf(elements []byte) asn1.RawValue {
	v := asn1.RawValue{Tag: setTag, IsCompound: true, Bytes: elements}
	v.FullBytes, _ = asn1.Marshal(v)

	return v
}

// aliasAttribute is the friendly name attribute of an alias.
func aliasAttribute(alias string) (attribute, error) {
	var text []byte
	for _, u := range utf16.Encode([]rune(alias)) {
		text = append(text, byte(u>>8), byte(u)) //nolint:gosec // a UTF-16 unit: two bytes
	}

	name, err := asn1.Marshal(asn1.RawValue{Tag: bmpStringTag, Bytes: text})
	if err != nil {
		return attribute{}, err
	}

	return attribute{ID: oidFriendlyName, Values: setOf(name)}, nil
}

// Encode is the PKCS#12 file of the store, protected as the file it was
// read from was (or as the JDK protects a new one) with a new salt.
func (s *Store) Encode(password string) ([]byte, error) {
	return s.encode(rand.Reader, password)
}

func (s *Store) encode(random io.Reader, password string) ([]byte, error) {
	if s.foreign {
		return nil, ErrForeign
	}

	bags := make([]safeBag, 0, len(s.Entries))

	for _, e := range s.Entries {
		bag, err := e.bag()
		if err != nil {
			return nil, err
		}

		bags = append(bags, bag)
	}

	pass := newPasswords(password)
	infos := []contentInfo{}

	// Like the JDK, a store without entries has nothing to encrypt.
	if len(bags) > 0 {
		contents, err := asn1.Marshal(bags)
		if err != nil {
			return nil, err
		}

		ci, err := s.seal(random, contents, pass)
		if err != nil {
			return nil, err
		}

		infos = append(infos, ci)
	}

	authSafe, err := asn1.Marshal(infos)
	if err != nil {
		return nil, err
	}

	return s.assemble(random, authSafe, pass)
}

// bag is the certificate bag of an entry.
func (e Entry) bag() (safeBag, error) {
	inner, err := asn1.Marshal(certBag{ID: oidX509Cert, Data: e.Cert})
	if err != nil {
		return safeBag{}, err
	}

	name, err := aliasAttribute(e.Alias)
	if err != nil {
		return safeBag{}, err
	}

	return safeBag{
		ID:         oidCertBag,
		Value:      asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: inner},
		Attributes: append([]attribute{name}, e.attrs...),
	}, nil
}

// seal is the element of the authenticated safe that holds the bags
// encrypted.
func (s *Store) seal(random io.Reader, contents []byte, pass passwords) (contentInfo, error) {
	alg, c, err := s.certs.protect(pass, random)
	if err != nil {
		return contentInfo{}, err
	}

	ed, err := asn1.Marshal(encryptedData{Content: encryptedContent{
		Type: oidData, Algorithm: alg, Data: c.encrypt(contents),
	}})
	if err != nil {
		return contentInfo{}, err
	}

	return contentInfo{
		Type:    oidEncryptedData,
		Content: asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: ed},
	}, nil
}

// assemble is the file: the authenticated safe and its MAC.
func (s *Store) assemble(random io.Reader, authSafe []byte, pass passwords) ([]byte, error) {
	newHash, err := digestByOID(s.macDigest)
	if err != nil {
		return nil, err
	}

	salt := make([]byte, s.macSaltLen)
	if _, err := io.ReadFull(random, salt); err != nil {
		return nil, err
	}

	content, err := asn1.Marshal(authSafe)
	if err != nil {
		return nil, err
	}

	return asn1.Marshal(pfxPDU{
		Version: pfxVersion,
		AuthSafe: contentInfo{
			Type:    oidData,
			Content: asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: content},
		},
		MacData: macData{
			Mac: digestInfo{
				Algorithm: pkix.AlgorithmIdentifier{Algorithm: s.macDigest, Parameters: asn1.NullRawValue},
				Digest:    mac(newHash, pass, salt, s.macIter, authSafe),
			},
			Salt:       salt,
			Iterations: s.macIter,
		},
	})
}
