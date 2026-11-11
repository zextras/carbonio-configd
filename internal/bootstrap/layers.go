// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrap

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ReadFileFunc reads a file; secrets given as <key>_file go through it.
type ReadFileFunc func(name string) ([]byte, error)

// Resolved is a Config with the layer that set each key.
type Resolved struct {
	Config Config

	sources map[string]Source
	// secretFiles maps a secret key to the file it was read from.
	secretFiles map[string]string
	readFile    ReadFileFunc
}

// NewResolved returns an empty configuration whose secret files are read
// with readFile.
func NewResolved(readFile ReadFileFunc) *Resolved {
	return &Resolved{
		sources:     make(map[string]Source),
		secretFiles: make(map[string]string),
		readFile:    readFile,
	}
}

// Source returns the layer that set the key at path.
func (r *Resolved) Source(path string) Source { return r.sources[path] }

// SecretFile returns the file the secret at path was read from, or "".
func (r *Resolved) SecretFile(path string) string { return r.secretFiles[path] }

// Set parses raw into the field and records src as its source.
func (r *Resolved) Set(f *Field, raw string, src Source) error {
	if err := f.Set(&r.Config, raw); err != nil {
		return err
	}

	r.mark(f, src)

	return nil
}

func (r *Resolved) mark(f *Field, src Source) {
	r.sources[f.Path] = src
	delete(r.secretFiles, f.Path)
}

// setSecretFile reads the secret of f from name.
func (r *Resolved) setSecretFile(f *Field, name string, src Source) error {
	data, err := r.readFile(name)
	if err != nil {
		return fmt.Errorf("%s: reading the secret file: %w", f.Path, err)
	}

	value := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if err := r.Set(f, value, src); err != nil {
		return err
	}

	r.secretFiles[f.Path] = name

	return nil
}

// setKey sets the key named by path (a field path, or a secret's
// <path>_file) to raw.
func (r *Resolved) setKey(path, raw string, src Source) error {
	if f := FieldByPath(path); f != nil {
		return r.Set(f, raw, src)
	}

	if base, ok := strings.CutSuffix(path, fileSuffix); ok {
		if f := FieldByPath(base); f != nil && f.Secret {
			return r.setSecretFile(f, raw, src)
		}
	}

	return fmt.Errorf("unknown key %q", path)
}

// ApplyYAML applies a YAML configuration file. Unknown keys are errors,
// reported with their line.
func (r *Resolved) ApplyYAML(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parsing YAML: %w", err)
	}

	if len(doc.Content) == 0 {
		return nil
	}

	var errs []error

	r.applyNode(doc.Content[0], "", &errs)

	return errors.Join(errs...)
}

func (r *Resolved) applyNode(node *yaml.Node, prefix string, errs *[]error) {
	if node.Kind != yaml.MappingNode {
		*errs = append(*errs, fmt.Errorf("line %d: %s: expected a mapping", node.Line, orRoot(prefix)))

		return
	}

	for i := 0; i+1 < len(node.Content); i += 2 {
		key, val := node.Content[i], node.Content[i+1]
		path := key.Value

		if prefix != "" {
			path = prefix + "." + key.Value
		}

		if val.Tag == "!!null" {
			continue
		}

		if isSection(path) {
			r.applyNode(val, path, errs)

			continue
		}

		if err := r.applyValue(path, val); err != nil {
			*errs = append(*errs, fmt.Errorf("line %d: %w", key.Line, err))
		}
	}
}

func (r *Resolved) applyValue(path string, val *yaml.Node) error {
	switch val.Kind {
	case yaml.ScalarNode:
		return r.setKey(path, val.Value, SourceFile)
	case yaml.SequenceNode:
		f := FieldByPath(path)
		if f == nil {
			return fmt.Errorf("unknown key %q", path)
		}

		items := make([]string, 0, len(val.Content))

		for _, it := range val.Content {
			if it.Kind != yaml.ScalarNode {
				return fmt.Errorf("%s: list items must be plain values", path)
			}

			items = append(items, it.Value)
		}

		if err := f.SetList(&r.Config, items); err != nil {
			return err
		}

		r.mark(f, SourceFile)

		return nil
	default:
		if FieldByPath(path) == nil {
			return fmt.Errorf("unknown key %q", path)
		}

		return fmt.Errorf("%s: expected a value", path)
	}
}

// isSection reports whether path names a group of keys, e.g. "ldap".
func isSection(path string) bool {
	return slices.ContainsFunc(Fields, func(f *Field) bool { return strings.HasPrefix(f.Path, path+".") })
}

func orRoot(path string) string {
	if path == "" {
		return "document"
	}

	return path
}

// ApplyEnv applies the CARBONIO_BOOTSTRAP_* variables of environ
// ("NAME=value" entries). An unknown variable, or a secret set both
// directly and through <VAR>_FILE, is an error.
func (r *Resolved) ApplyEnv(environ []string) error {
	byEnv := make(map[string]*Field, len(Fields))
	for _, f := range Fields {
		byEnv[f.Env] = f
	}

	vars := make(map[string]string)

	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if ok && strings.HasPrefix(name, EnvPrefix) {
			vars[name] = value
		}
	}

	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}

	slices.Sort(names)

	var errs []error

	for _, name := range names {
		value := vars[name]

		if f := byEnv[name]; f != nil {
			if _, both := vars[name+"_FILE"]; both {
				errs = append(errs, fmt.Errorf("%s and %s_FILE are both set", name, name))

				continue
			}

			if err := r.Set(f, value, SourceEnv); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", name, err))
			}

			continue
		}

		f := secretFileVar(byEnv, name)
		if f == nil {
			errs = append(errs, fmt.Errorf("unknown environment variable %s", name))

			continue
		}

		if _, both := vars[f.Env]; both {
			continue // reported with the direct variable
		}

		if err := r.setSecretFile(f, value, SourceEnv); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}

	return errors.Join(errs...)
}

// secretFileVar returns the secret field that the <VAR>_FILE variable name
// sets, or nil.
func secretFileVar(byEnv map[string]*Field, name string) *Field {
	base, ok := strings.CutSuffix(name, "_FILE")
	if !ok {
		return nil
	}

	if f := byEnv[base]; f != nil && f.Secret {
		return f
	}

	return nil
}

// ApplyExisting applies the values read from an existing install (key path
// to value, in the --set syntax). They rank above the defaults and below
// every operator layer.
func (r *Resolved) ApplyExisting(values map[string]string) error {
	paths := make([]string, 0, len(values))
	for p := range values {
		paths = append(paths, p)
	}

	slices.Sort(paths)

	var errs []error

	for _, p := range paths {
		if err := r.setKey(p, values[p], SourceExisting); err != nil {
			errs = append(errs, fmt.Errorf("existing install: %w", err))
		}
	}

	return errors.Join(errs...)
}

// ApplySets applies "path=value" assignments (the --set flags).
func (r *Resolved) ApplySets(sets []string) error {
	var errs []error

	for _, s := range sets {
		path, value, ok := strings.Cut(s, "=")
		if !ok {
			errs = append(errs, fmt.Errorf("--set %q: expected key=value", s))

			continue
		}

		if err := r.setKey(strings.TrimSpace(path), value, SourceFlag); err != nil {
			errs = append(errs, fmt.Errorf("--set %s: %w", path, err))
		}
	}

	return errors.Join(errs...)
}
