// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package provision

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
)

// attrType is the type of an attribute in the attrs.xml schema.
type attrType string

// The attribute types of attrs.xml (AttributeType in the Java
// AttributeManager).
const (
	typeBoolean     attrType = "boolean"
	typeBinary      attrType = "binary"
	typeCertificate attrType = "certificate"
	typeDuration    attrType = "duration"
	typeEmail       attrType = "email"
	typeEmailP      attrType = "emailp"
	typeCSEmailP    attrType = "cs_emailp"
	typeEnum        attrType = "enum"
	typeGentime     attrType = "gentime"
	typeID          attrType = "id"
	typeInteger     attrType = "integer"
	typeLong        attrType = "long"
	typePort        attrType = "port"
	typeRegex       attrType = "regex"
)

// flagServerInherited marks an attribute whose value on a server entry
// defaults to the global config value.
const flagServerInherited = "serverInherited"

// The patterns of LdapAttributeInfo.
var (
	idPattern       = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	gentimePattern  = regexp.MustCompile(`^\d{14}(\.\d{1,3})?[zZ]$`)
	durationPattern = regexp.MustCompile(`^(\d+)([hmsd]|ms)?$`)
)

// Attr is the schema of one attribute, from an <attr> element of attrs.xml.
type Attr struct {
	Name      string
	Type      attrType
	Multi     bool
	Immutable bool
	// Callback is the name of the Java attribute callback, "" for none.
	Callback        string
	ServerInherited bool

	min, max int64
	enum     []string
	regex    *regexp.Regexp
	// regexErr is why the regex of the attribute does not compile here.
	regexErr error
	value    string
}

// Schema is the attribute schema of the directory: every <attr> of every
// attrs XML file, by lower-case name.
type Schema struct {
	attrs map[string]*Attr
}

// FileSystem is what LoadSchema reads the schema files with; sys.FS is one.
type FileSystem interface {
	Glob(pattern string) ([]string, error)
	ReadFile(name string) ([]byte, error)
}

// LoadSchema reads every *.xml file of dir, as the Java AttributeManager does
// (attrs.xml, amavisd-new-attrs.xml, ...). A missing or malformed schema is an
// error: the native provisioning cannot check values against nothing.
func LoadSchema(fsys FileSystem, dir string) (*Schema, error) {
	paths, err := fsys.Glob(filepath.Join(dir, "*.xml"))
	if err != nil {
		return nil, fmt.Errorf("list attribute schema files: %w", err)
	}

	if len(paths) == 0 {
		return nil, fmt.Errorf("no attribute schema (*.xml) in %s", dir)
	}

	slices.Sort(paths)

	s := &Schema{attrs: map[string]*Attr{}}

	for _, p := range paths {
		data, err := fsys.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read attribute schema: %w", err)
		}

		one, err := ParseSchema(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}

		maps.Copy(s.attrs, one.attrs)
	}

	return s, nil
}

// ParseSchema reads one attrs XML document.
func ParseSchema(r io.Reader) (*Schema, error) {
	s := &Schema{attrs: map[string]*Attr{}}

	err := s.parse(r)

	return s, err
}

func (s *Schema) parse(r io.Reader) error {
	dec := xml.NewDecoder(r)

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("parse attribute schema: %w", err)
		}

		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "attr" {
			continue
		}

		a, err := newAttr(start.Attr)
		if err != nil {
			return err
		}

		s.attrs[strings.ToLower(a.Name)] = a
	}
}

// newAttr builds an Attr from the XML attributes of an <attr> element, with
// the defaults of the Java AttributeInfo constructor.
func newAttr(xattrs []xml.Attr) (*Attr, error) {
	props := make(map[string]string, len(xattrs))
	for _, x := range xattrs {
		props[x.Name.Local] = x.Value
	}

	a := &Attr{
		Name:      props["name"],
		Type:      attrType(props["type"]),
		Multi:     props["cardinality"] == "multi",
		Immutable: props["immutable"] == "1" || props["immutable"] == "true",
		Callback:  props["callback"],
		value:     props["value"],
		min:       math.MinInt64,
		max:       math.MaxInt64,
	}

	if a.Name == "" {
		return nil, errors.New("attribute schema: <attr> without a name")
	}

	a.ServerInherited = slices.Contains(strings.Split(props["flags"], ","), flagServerInherited)

	a.setLimits(props["min"], props["max"])

	return a, nil
}

// setLimits applies the min and max of the element the way AttributeInfo
// does: integer and long default to their type range, duration to [0, max long]
// in milliseconds, anything else to the whole long range. A value that does not
// parse keeps the default (the Java code logs it and does the same).
func (a *Attr) setLimits(minStr, maxStr string) {
	switch a.Type {
	case typeInteger:
		a.min, a.max = math.MinInt32, math.MaxInt32
		a.min = parseOr(minStr, a.min)
		a.max = parseOr(maxStr, a.max)
	case typeDuration:
		a.min = durationOr(minStr, 0)
		a.max = durationOr(maxStr, math.MaxInt64)
	case typeEnum:
		a.enum = strings.Split(a.value, ",")
	case typeRegex:
		a.regex, a.regexErr = regexp.Compile("^(?:" + a.value + ")$")
	default:
		a.min = parseOr(minStr, a.min)
		a.max = parseOr(maxStr, a.max)
	}
}

func parseOr(s string, def int64) int64 {
	if s == "" {
		return def
	}

	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def
	}

	return v
}

func durationOr(s string, def int64) int64 {
	if s == "" {
		return def
	}

	v, err := parseDuration(s)
	if err != nil || v < 0 {
		return def
	}

	return v
}

// parseDuration is DateUtil.getTimeInterval: milliseconds of "<n>[d|h|m|s|ms]",
// seconds when there is no unit.
func parseDuration(s string) (int64, error) {
	m := durationPattern.FindStringSubmatch(s)
	if m == nil {
		return 0, invalidRequest("Invalid duration: %s", s)
	}

	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, invalidRequest("Invalid duration: %s", s)
	}

	unit := map[string]int64{"d": 86_400_000, "h": 3_600_000, "m": 60_000, "ms": 1, "s": 1000, "": 1000}[m[2]]

	return n * unit, nil
}

// Lookup returns the schema of the attribute (name in any case), nil when the
// schema does not define it.
func (s *Schema) Lookup(name string) *Attr { return s.attrs[strings.ToLower(name)] }

// Binary reports whether the attribute is binary: its zmprov value is the path
// of a file holding the value.
func (s *Schema) Binary(name string) bool {
	a := s.Lookup(name)

	return a != nil && (a.Type == typeBinary || a.Type == typeCertificate)
}

// ServerInherited lists the attributes of the global config that a server
// entry inherits.
func (s *Schema) ServerInherited(name string) bool {
	a := s.Lookup(name)

	return a != nil && a.ServerInherited
}

// Check validates one value of the attribute name as LdapAttributeInfo
// checkValue does: with checkImmutable the attribute must be modifiable, an
// empty value (an unset) passes, and the value must fit the type. An attribute
// the schema does not define passes (the Java code only logs it; the directory
// refuses it).
func (s *Schema) Check(name, value string, checkImmutable bool) error {
	a := s.Lookup(name)
	if a == nil {
		return nil
	}

	if checkImmutable && a.Immutable {
		return invalidRequest("%s is immutable", a.Name)
	}

	if value == "" {
		return nil
	}

	return a.check(value)
}

// check dispatches on the type; the order and messages follow checkValue.
func (a *Attr) check(value string) error {
	switch a.Type {
	case typeBoolean, typeEnum, typeGentime, typeID, typePort:
		return a.checkFormat(value)
	case typeBinary, typeCertificate:
		return a.checkLength(int64(len(value)))
	case typeDuration:
		return a.checkDuration(value)
	case typeEmail, typeEmailP, typeCSEmailP:
		return a.checkEmail(value)
	case typeInteger:
		return a.checkNumber(value, 32, "integer")
	case typeLong:
		return a.checkNumber(value, 64, "long")
	case typeRegex:
		return a.checkRegex(value)
	default:
		// string, astring, ostring, cstring, phone: the length.
		return a.checkLength(int64(len(utf16.Encode([]rune(value)))))
	}
}

// checkFormat checks the types whose values have a fixed form.
func (a *Attr) checkFormat(value string) error {
	switch a.Type {
	case typeBoolean:
		if value != "TRUE" && value != "FALSE" {
			return invalidValue("%s must be TRUE or FALSE", a.Name)
		}
	case typeEnum:
		if !slices.Contains(a.enum, value) {
			return invalidValue("%s must be one of: %s", a.Name, a.value)
		}
	case typeGentime:
		if !gentimePattern.MatchString(value) {
			return invalidValue("%s must be a valid generalized time: yyyyMMddHHmmssZ or yyyyMMddHHmmss.SSSZ", a.Name)
		}
	case typeID:
		if !idPattern.MatchString(value) {
			return invalidValue("%s must be a valid id", a.Name)
		}
	default: // typePort
		if p, err := strconv.Atoi(value); err != nil || p < 0 || p > 65535 {
			return invalidValue("%s must be a valid port: %s", a.Name, value)
		}
	}

	return nil
}

func (a *Attr) checkLength(n int64) error {
	if n > a.max {
		return invalidValue("%s value length(%d) larger than max allowed: %d", a.Name, n, a.max)
	}

	return nil
}

func (a *Attr) checkDuration(value string) error {
	if !durationPattern.MatchString(value) {
		return invalidValue("%s must be a valid duration: <n>[d|h|m|s|ms], in seconds when no unit is given", a.Name)
	}

	d, err := parseDuration(value)
	if err != nil {
		return invalidValue("%s must be a valid duration: %s", a.Name, value)
	}

	if d < a.min {
		return invalidValue("%s is shorter than minimum allowed: %d", a.Name, a.min)
	}

	if d > a.max {
		return invalidValue("%s is longer than max allowed: %d", a.Name, a.max)
	}

	return nil
}

func (a *Attr) checkNumber(value string, bits int, kind string) error {
	v, err := strconv.ParseInt(value, 10, bits)
	if err != nil {
		return invalidValue("%s must be a valid %s: %s", a.Name, kind, value)
	}

	if v < a.min {
		return invalidValue("%s value(%d) smaller than minimum allowed: %d", a.Name, v, a.min)
	}

	if v > a.max {
		return invalidValue("%s value(%d) larger than max allowed: %d", a.Name, v, a.max)
	}

	return nil
}

func (a *Attr) checkRegex(value string) error {
	if a.regexErr != nil {
		return invalidValue("%s has a regex this tool cannot evaluate: %v", a.Name, a.regexErr)
	}

	if !a.regex.MatchString(value) {
		return invalidValue("%s must match the regex: %s", a.Name, a.value)
	}

	return nil
}

func (a *Attr) checkEmail(value string) error {
	if err := a.checkLength(int64(len(utf16.Encode([]rune(value))))); err != nil {
		return err
	}

	addrs := []string{value}
	if a.Type == typeCSEmailP {
		addrs = strings.Split(value, ",")
	}

	for _, addr := range addrs {
		if err := validEmail(addr, a.Type != typeEmail); err != nil {
			return err
		}
	}

	return nil
}
