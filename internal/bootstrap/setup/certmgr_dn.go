// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"crypto/sha1" //nolint:gosec // the name hash of OpenSSL (c_rehash links) is SHA-1
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// certATV is one AttributeTypeAndValue of a distinguished name; the value is
// kept as the raw ASN.1 string, so that a name read from a request is signed
// back with the string types it had (openssl ca does the same).
type certATV struct {
	Type  asn1.ObjectIdentifier
	Value asn1.RawValue
}

// certRDNSET is one RelativeDistinguishedName: encoding/asn1 writes a type
// whose name ends in "SET" as a SET OF.
type certRDNSET []certATV

// certDN is a distinguished name, in the order of its RDNs.
type certDN []certRDNSET

// The string types of ASN.1 that names use.
const (
	certTagUTF8      = asn1.TagUTF8String
	certTagPrintable = asn1.TagPrintableString
	certTagIA5       = asn1.TagIA5String
	certTagT61       = asn1.TagT61String
	certTagBMP       = asn1.TagBMPString
	certTagUniv      = 28
)

// certNameAttr is one attribute type a subject may name: the short name
// OpenSSL prints, its aliases for -subj, the OID, the string type it is
// written as, and the length limit of its ASN.1 string table (0: none).
type certNameAttr struct {
	short  string
	names  []string
	oid    asn1.ObjectIdentifier
	tag    int
	maxLen int
}

// certNameAttrs are the attributes of the name tables of OpenSSL (obj_dat,
// a_strnid) that a subject or an index.txt line shows. With the utf8only
// string mask OpenSSL uses, the directory-string attributes are UTF8String
// and those that allow nothing else keep their type.
var certNameAttrs = []certNameAttr{
	{"C", []string{"c", "countryname"}, asn1.ObjectIdentifier{2, 5, 4, 6}, certTagPrintable, 2},
	{"ST", []string{"st", "stateorprovincename"}, asn1.ObjectIdentifier{2, 5, 4, 8}, certTagUTF8, 128},
	{"L", []string{"l", "localityname"}, asn1.ObjectIdentifier{2, 5, 4, 7}, certTagUTF8, 128},
	{"O", []string{"o", "organizationname"}, asn1.ObjectIdentifier{2, 5, 4, 10}, certTagUTF8, 64},
	{"OU", []string{"ou", "organizationalunitname"}, asn1.ObjectIdentifier{2, 5, 4, 11}, certTagUTF8, 64},
	{"CN", []string{"cn", "commonname"}, asn1.ObjectIdentifier{2, 5, 4, 3}, certTagUTF8, 64},
	{"emailAddress", []string{"emailaddress", "email"},
		asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 1}, certTagIA5, 128},
	{"serialNumber", []string{"serialnumber"}, asn1.ObjectIdentifier{2, 5, 4, 5}, certTagPrintable, 64},
	{"DC", []string{"dc", "domaincomponent"},
		asn1.ObjectIdentifier{0, 9, 2342, 19200300, 100, 1, 25}, certTagIA5, 0},
	{"UID", []string{"uid", "userid"}, asn1.ObjectIdentifier{0, 9, 2342, 19200300, 100, 1, 1}, certTagUTF8, 256},
	{"street", []string{"street", "streetaddress"}, asn1.ObjectIdentifier{2, 5, 4, 9}, certTagUTF8, 128},
	{"title", []string{"title"}, asn1.ObjectIdentifier{2, 5, 4, 12}, certTagUTF8, 64},
	{"SN", []string{"sn", "surname"}, asn1.ObjectIdentifier{2, 5, 4, 4}, certTagUTF8, 64},
	{"GN", []string{"gn", "givenname"}, asn1.ObjectIdentifier{2, 5, 4, 42}, certTagUTF8, 64},
	{"initials", []string{"initials"}, asn1.ObjectIdentifier{2, 5, 4, 43}, certTagUTF8, 64},
	{"dnQualifier", []string{"dnqualifier"}, asn1.ObjectIdentifier{2, 5, 4, 46}, certTagPrintable, 0},
	{"postalCode", []string{"postalcode"}, asn1.ObjectIdentifier{2, 5, 4, 17}, certTagUTF8, 40},
	{"name", []string{"name"}, asn1.ObjectIdentifier{2, 5, 4, 41}, certTagUTF8, 32768},
	{"businessCategory", []string{"businesscategory"}, asn1.ObjectIdentifier{2, 5, 4, 15}, certTagUTF8, 128},
	{"pseudonym", []string{"pseudonym"}, asn1.ObjectIdentifier{2, 5, 4, 65}, certTagUTF8, 128},
}

// certPolicyOrder is the order policy_anything of zmssl.cnf gives the fields
// of an issued certificate; every other attribute is dropped (openssl ca
// "optional" fields it does not list are removed with preserve = no).
var certPolicyOrder = []string{"C", "ST", "L", "O", "OU", "CN", "emailAddress"}

// certAttrByName finds the attribute a -subj type names (any case).
func certAttrByName(name string) *certNameAttr {
	name = strings.ToLower(name)

	for i := range certNameAttrs {
		if slices.Contains(certNameAttrs[i].names, name) {
			return &certNameAttrs[i]
		}
	}

	return nil
}

// certAttrByOID is the attribute with an OID, nil for one OpenSSL has no
// table entry for here.
func certAttrByOID(oid asn1.ObjectIdentifier) *certNameAttr {
	for i := range certNameAttrs {
		if certNameAttrs[i].oid.Equal(oid) {
			return &certNameAttrs[i]
		}
	}

	return nil
}

// certSubjFormatError is what openssl req says of a -subj it cannot read.
func certSubjFormatError(subj string) error {
	return fmt.Errorf("subject name is expected to be in the format /type0=value0/type1=value1/type2=... "+
		"where characters may be escaped by \\. This name is not in that format: '%s'", subj)
}

// certParseSubject reads the -subj syntax of openssl req (apps.c parse_name):
// "/type=value/..." where a backslash escapes the next character and "+"
// continues the same RDN. Empty values are skipped.
func certParseSubject(subj string) (certDN, error) {
	if subj == "" || subj[0] != '/' {
		return nil, certSubjFormatError(subj)
	}

	var (
		dn    certDN
		multi bool
	)

	for rest := subj[1:]; rest != ""; {
		typ, val, next, more, err := certSubjField(rest, subj)
		if err != nil {
			return nil, err
		}

		rest = next

		attr := certAttrByName(typ)
		if attr == nil {
			return nil, fmt.Errorf("%s: Invalid field name '%s' in the subject", subj, typ)
		}

		if val != "" {
			if dn, err = certAddATV(dn, attr, val, multi); err != nil {
				return nil, err
			}
		}

		multi = more
	}

	return dn, nil
}

// certAddATV adds an attribute to a name: to the last RDN when the field
// before ended in "+", else in an RDN of its own.
func certAddATV(dn certDN, attr *certNameAttr, val string, multi bool) (certDN, error) {
	atv, err := certNewATV(attr, val)
	if err != nil {
		return nil, err
	}

	if multi && len(dn) > 0 {
		dn[len(dn)-1] = append(dn[len(dn)-1], atv)

		return dn, nil
	}

	return append(dn, certRDNSET{atv}), nil
}

// certSubjField reads "type=value" up to the next "/" or "+" (not escaped);
// more tells that a "+" ended it (the next field joins the RDN).
func certSubjField(rest, subj string) (typ, val, next string, more bool, err error) {
	var cur strings.Builder

	haveType := false
	i := 0

loop:
	for ; i < len(rest); i++ {
		c := rest[i]

		switch {
		case c == '\\' && i+1 < len(rest):
			i++

			cur.WriteByte(rest[i])
		case c == '\\':
			return "", "", "", false, certSubjFormatError(subj)
		case c == '=' && !haveType:
			typ, haveType = cur.String(), true

			cur.Reset()
		case c == '/' || c == '+':
			more = c == '+'
			i++

			break loop
		default:
			cur.WriteByte(c)
		}
	}

	if !haveType {
		return "", "", "", false, certSubjFormatError(subj)
	}

	return typ, cur.String(), rest[min(i, len(rest)):], more, nil
}

// certNewATV builds an attribute from a UTF-8 value, enforcing the length
// limit of its string table as openssl does.
func certNewATV(attr *certNameAttr, val string) (certATV, error) {
	if !utf8.ValidString(val) {
		return certATV{}, fmt.Errorf("%s: invalid UTF-8 value", attr.short)
	}

	n := utf8.RuneCountInString(val)
	if attr.maxLen > 0 && n > attr.maxLen {
		return certATV{}, fmt.Errorf("string too long: %s field must be at most %d characters long (%d)",
			attr.short, attr.maxLen, n)
	}

	if attr.tag == certTagPrintable && !certPrintable(val) {
		return certATV{}, fmt.Errorf("%s: invalid characters for a PrintableString: '%s'", attr.short, val)
	}

	if attr.tag == certTagIA5 && !certASCII(val) {
		return certATV{}, fmt.Errorf("%s: invalid characters for an IA5String: '%s'", attr.short, val)
	}

	return certATV{
		Type:  attr.oid,
		Value: asn1.RawValue{Class: asn1.ClassUniversal, Tag: attr.tag, Bytes: []byte(val)},
	}, nil
}

// certPrintable is whether every rune is in the PrintableString alphabet.
func certPrintable(s string) bool {
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			strings.ContainsRune(" '()+,-./:=?", r)
		if !ok {
			return false
		}
	}

	return true
}

// certASCII is whether s is 7-bit.
func certASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= 0x80 {
			return false
		}
	}

	return true
}

// marshal is the DER of the name.
func (dn certDN) marshal() ([]byte, error) {
	return asn1.Marshal(dn)
}

// certParseDN reads a DER name.
func certParseDN(raw []byte) (certDN, error) {
	var dn certDN

	rest, err := asn1.Unmarshal(raw, &dn)
	if err != nil {
		return nil, err
	}

	if len(rest) != 0 {
		return nil, errors.New("trailing data after the name")
	}

	return dn, nil
}

// text is the value as characters, per its string type; ok is false for a
// value that is not a string.
func (a *certATV) text() (s string, ok bool) {
	b := a.Value.Bytes

	switch a.Value.Tag {
	case certTagUTF8, certTagPrintable, certTagIA5:
		return string(b), true
	case certTagT61:
		rs := make([]rune, len(b))
		for i, c := range b {
			rs[i] = rune(c)
		}

		return string(rs), true
	case certTagBMP:
		if len(b)%2 != 0 {
			return "", false
		}

		u := make([]uint16, len(b)/2)
		for i := range u {
			u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
		}

		return string(utf16.Decode(u)), true
	case certTagUniv:
		if len(b)%4 != 0 {
			return "", false
		}

		var sb strings.Builder

		for i := 0; i < len(b); i += 4 {
			//nolint:gosec // a UCS-4 code point, as the BMP/Universal string holds it
			sb.WriteRune(rune(uint32(b[i])<<24 | uint32(b[i+1])<<16 | uint32(b[i+2])<<8 | uint32(b[i+3])))
		}

		return sb.String(), true
	}

	return "", false
}

// shortName is what OpenSSL prints for the attribute type: its short name,
// or the dotted OID.
func (a *certATV) shortName() string {
	if attr := certAttrByOID(a.Type); attr != nil {
		return attr.short
	}

	return a.Type.String()
}

// oneline is the name as `openssl x509 -subject` of OpenSSL 3 shows it:
// "C=US, O=Example", values escaped as RFC 2253 with quotes.
func (dn certDN) oneline() string {
	rdns := make([]string, len(dn))

	for i, rdn := range dn {
		attrs := make([]string, len(rdn))
		for j, a := range rdn {
			attrs[j] = a.shortName() + "=" + a.display()
		}

		rdns[i] = strings.Join(attrs, " + ")
	}

	return strings.Join(rdns, ", ")
}

// display is the value of the attribute as XN_FLAG_ONELINE prints it:
// escapes with a backslash or quotes, control characters and non-ASCII
// bytes as \XX, and "#<hex>" for a value that is not a string.
func (a *certATV) display() string {
	s, ok := a.text()
	if !ok {
		return "#" + hex.EncodeToString(a.Value.FullBytes)
	}

	var (
		sb     strings.Builder
		quotes bool
	)

	for i := range len(s) {
		c := s[i]

		switch {
		case c < 0x20 || c == 0x7f || c >= 0x80:
			fmt.Fprintf(&sb, "\\%02X", c)
		case certNeedsBackslash(s, i):
			sb.WriteByte('\\')
			sb.WriteByte(c)
		default:
			quotes = quotes || strings.IndexByte(",+<>;", c) >= 0

			sb.WriteByte(c)
		}
	}

	if quotes {
		return `"` + sb.String() + `"`
	}

	return sb.String()
}

// certNeedsBackslash is true for the byte of s at i that XN_FLAG_ONELINE
// escapes: quotes, backslashes, a leading space or "#" and a trailing space.
func certNeedsBackslash(s string, i int) bool {
	c := s[i]

	return c == '"' || c == '\\' || i == 0 && (c == ' ' || c == '#') || i == len(s)-1 && c == ' '
}

// slash is the "/C=US/O=Example" form (X509_NAME_oneline) that index.txt of
// openssl ca holds.
func (dn certDN) slash() string {
	var sb strings.Builder

	for _, rdn := range dn {
		for _, a := range rdn {
			s, _ := a.text()

			sb.WriteString("/" + a.shortName() + "=" + certSlashEscape(s))
		}
	}

	return sb.String()
}

// certSlashEscape writes the bytes X509_NAME_oneline does not print as is
// (anything outside 0x20-0x7e) as \xHH.
func certSlashEscape(s string) string {
	var sb strings.Builder

	for i := range len(s) {
		if c := s[i]; c < 0x20 || c > 0x7e {
			fmt.Fprintf(&sb, "\\x%02X", c)
		} else {
			sb.WriteByte(c)
		}
	}

	return sb.String()
}

// policyAnything keeps the attributes policy_anything of zmssl.cnf lists, in
// its order (openssl ca reorders the subject of the request this way);
// attributes it does not list are dropped. It reports the fields that the
// policy requires and are missing ("commonName = supplied" is the only one).
func (dn certDN) policyAnything() (certDN, error) {
	var out certDN

	for _, short := range certPolicyOrder {
		for _, rdn := range dn {
			for _, a := range rdn {
				if a.shortName() == short {
					out = append(out, certRDNSET{a})
				}
			}
		}
	}

	if !out.has("CN") {
		//nolint:staticcheck // the text of openssl ca
		return nil, errors.New("The commonName field needed to be supplied and was missing")
	}

	return out, nil
}

// has is whether the name holds an attribute with that short name.
func (dn certDN) has(short string) bool {
	for _, rdn := range dn {
		for _, a := range rdn {
			if a.shortName() == short {
				return true
			}
		}
	}

	return false
}

// canonHash is the hash openssl x509 -hash prints and c_rehash links by
// (X509_NAME_hash): the first four bytes, little endian, of the SHA-1 of the
// canonical encoding of the name: each value as UTF8String with its ASCII
// letters in lower case, runs of white space made one space and the ends
// trimmed, the RDNs written without the outer SEQUENCE header.
func (dn certDN) canonHash() (string, error) {
	var canon []byte

	for _, rdn := range dn {
		set := make(certRDNSET, len(rdn))

		for i, a := range rdn {
			set[i] = a.canon()
		}

		der, err := asn1.Marshal(set)
		if err != nil {
			return "", err
		}

		canon = append(canon, der...)
	}

	sum := sha1.Sum(canon) //nolint:gosec // see the import

	return fmt.Sprintf("%08x", uint32(sum[0])|uint32(sum[1])<<8|uint32(sum[2])<<16|uint32(sum[3])<<24), nil
}

// canon is the attribute in the canonical form of X509_NAME: a string value
// becomes a UTF8String of its lower-cased, space-collapsed text.
func (a *certATV) canon() certATV {
	s, ok := a.text()
	if !ok {
		return *a
	}

	var sb strings.Builder

	space := false

	for _, r := range strings.TrimFunc(s, certIsSpace) {
		if certIsSpace(r) {
			space = true

			continue
		}

		if space {
			sb.WriteByte(' ')

			space = false
		}

		if r < utf8.RuneSelf && r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}

		sb.WriteRune(r)
	}

	return certATV{
		Type:  a.Type,
		Value: asn1.RawValue{Class: asn1.ClassUniversal, Tag: certTagUTF8, Bytes: []byte(sb.String())},
	}
}

// certIsSpace is the white space of the C locale (isspace).
func certIsSpace(r rune) bool {
	return r == ' ' || r >= '\t' && r <= '\r'
}
