// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrap

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// EnvPrefix prefixes the environment variables that set bootstrap keys.
const EnvPrefix = "CARBONIO_BOOTSTRAP_"

// fileSuffix names the key that reads a secret from a file: <key>_file in
// YAML and --set, <VAR>_FILE in the environment.
const fileSuffix = "_file"

// Source is the layer that set a key.
type Source int

// The layers, lowest precedence first.
const (
	SourceUnset Source = iota
	SourceDefault
	SourceDetected
	SourceGenerated
	SourceExisting
	SourceLegacy
	SourceFile
	SourceEnv
	SourceFlag
	SourceWizard
)

var sourceNames = [...]string{
	"unset", "default", "detected", "generated", "existing", "legacy", "file", "env", "flag", "wizard",
}

func (s Source) String() string { return sourceNames[s] }

// Field is one leaf key of Config.
type Field struct {
	// Path is the dotted YAML path, e.g. "ldap.root_password".
	Path string
	// Env is the environment variable that sets the key.
	Env string
	// Legacy are the setup.pl config keys that set the field (none: no
	// legacy key).
	Legacy []string
	// Secret keys are never printed or saved and may be read from a file.
	Secret bool
	// Enum lists the allowed values, or nil.
	Enum []string
	// Check names the format of the value (see the check tag), or "".
	Check string

	index []int
	kind  reflect.Kind
}

// Fields lists every leaf key of Config in declaration order.
var Fields = collectFields(reflect.TypeFor[Config](), "", nil)

var fieldsByPath = func() map[string]*Field {
	m := make(map[string]*Field, len(Fields))
	for _, f := range Fields {
		m[f.Path] = f
	}

	return m
}()

// FieldByPath returns the field at path, or nil.
func FieldByPath(path string) *Field { return fieldsByPath[path] }

func collectFields(t reflect.Type, prefix string, index []int) []*Field {
	var out []*Field

	for i := range t.NumField() {
		sf := t.Field(i)
		name := sf.Tag.Get("yaml")
		path := name

		if prefix != "" {
			path = prefix + "." + name
		}

		idx := append(slices.Clone(index), i)

		if sf.Type.Kind() == reflect.Struct {
			out = append(out, collectFields(sf.Type, path, idx)...)

			continue
		}

		f := &Field{
			Path:   path,
			Env:    EnvPrefix + strings.ToUpper(strings.ReplaceAll(path, ".", "_")),
			Legacy: splitTag(sf.Tag.Get("legacy")),
			Secret: sf.Tag.Get("secret") == "true",
			Check:  sf.Tag.Get("check"),
			index:  idx,
			kind:   sf.Type.Kind(),
		}

		if e := sf.Tag.Get("enum"); e != "" {
			f.Enum = strings.Split(e, "|")
		}

		out = append(out, f)
	}

	return out
}

func splitTag(tag string) []string {
	if tag == "" {
		return nil
	}

	return strings.Split(tag, ",")
}

// TypeName describes the value the field takes.
func (f *Field) TypeName() string {
	switch f.kind {
	case reflect.Int:
		return "int"
	case reflect.Bool:
		return "bool"
	case reflect.Slice:
		return "list"
	default:
		return "string"
	}
}

func (f *Field) value(c *Config) reflect.Value {
	return reflect.ValueOf(c).Elem().FieldByIndex(f.index)
}

// Get returns the value of the field in c.
func (f *Field) Get(c *Config) any { return f.value(c).Interface() }

// Format renders the value of the field in c as text, the form Set parses.
func (f *Field) Format(c *Config) string {
	v := f.value(c)

	switch f.kind {
	case reflect.Int:
		if v.Int() == 0 {
			return ""
		}

		return strconv.FormatInt(v.Int(), 10)
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.Slice:
		return strings.Join(v.Interface().([]string), ",")
	default:
		return v.String()
	}
}

// Set parses raw and stores it in c. Lists are comma-separated; booleans
// accept true/false, yes/no, 1/0 in any case.
func (f *Field) Set(c *Config, raw string) error {
	v := f.value(c)

	switch f.kind {
	case reflect.Int:
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("%s: %q is not a number", f.Path, raw)
		}

		v.SetInt(int64(n))
	case reflect.Bool:
		b, err := ParseBool(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}

		v.SetBool(b)
	case reflect.Slice:
		return f.SetList(c, splitList(raw))
	default:
		if err := f.checkEnum(raw); err != nil {
			return err
		}

		v.SetString(raw)
	}

	return nil
}

// SetList stores items in the list field of c.
func (f *Field) SetList(c *Config, items []string) error {
	if f.kind != reflect.Slice {
		return fmt.Errorf("%s: expected a single %s, got a list", f.Path, f.TypeName())
	}

	for _, it := range items {
		if err := f.checkEnum(it); err != nil {
			return err
		}
	}

	if len(items) == 0 {
		f.value(c).SetZero()

		return nil
	}

	f.value(c).Set(reflect.ValueOf(slices.Clone(items)))

	return nil
}

func (f *Field) checkEnum(s string) error {
	if f.Enum == nil || slices.Contains(f.Enum, s) {
		return nil
	}

	return fmt.Errorf("%s: %q is not one of %s", f.Path, s, strings.Join(f.Enum, ", "))
}

// reflectCopy returns a copy of v that does not share a list's backing array.
func reflectCopy(v reflect.Value) reflect.Value {
	c := reflect.New(v.Type()).Elem()
	c.Set(v)

	if v.Kind() == reflect.Slice && !v.IsNil() {
		c.Set(reflect.ValueOf(slices.Clone(v.Interface().([]string))))
	}

	return c
}

// IsZero reports whether the field of c holds its zero value.
func (f *Field) IsZero(c *Config) bool { return f.value(c).IsZero() }

// ParseBool parses the boolean spellings of setup.pl and YAML.
func ParseBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "yes", "1", "on":
		return true, nil
	case "false", "no", "0", "off":
		return false, nil
	}

	return false, fmt.Errorf("%q is not a boolean (true/false, yes/no)", raw)
}

// splitList splits a list on commas and whitespace, dropping empty items.
func splitList(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
}
