// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrap

import (
	"bytes"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Mask replaces a secret value in printed output.
const Mask = "********"

// RenderMode selects what Render writes.
type RenderMode int

const (
	// Print shows every set key with its source; secrets are masked.
	Print RenderMode = iota
	// Save writes a reusable configuration file: no source comments, and
	// secrets only as the <key>_file they were read from. A secret set any
	// other way is left as an empty <key>_file with a comment, so the file
	// never holds a password.
	Save
)

// Render writes the configuration as YAML.
func (r *Resolved) Render(mode RenderMode) ([]byte, error) {
	root := &yaml.Node{Kind: yaml.MappingNode}

	if mode == Save {
		root.HeadComment = "carbonio bootstrap configuration (carbonio-bootstrap)"
	}

	for _, f := range Fields {
		if r.sources[f.Path] == SourceUnset {
			continue
		}

		parent, name := section(root, f.Path)
		key := &yaml.Node{Kind: yaml.ScalarNode, Value: name}
		val := r.valueNode(f)

		switch {
		case f.Secret && r.secretFiles[f.Path] != "":
			key.Value += fileSuffix
			val = scalar("!!str", r.secretFiles[f.Path])
		case f.Secret && mode == Save:
			key.Value += fileSuffix
			val = scalar("!!null", "")
			val.LineComment = "not saved (" + r.sources[f.Path].String() + "): give a file or set " + f.Env + "[_FILE]"
		case f.Secret:
			val = scalar("!!str", Mask)
		}

		if mode == Print && val.LineComment == "" {
			val.LineComment = r.sources[f.Path].String()
		}

		parent.Content = append(parent.Content, key, val)
	}

	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)

	if err := enc.Encode(root); err != nil {
		return nil, err
	}

	if err := enc.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (r *Resolved) valueNode(f *Field) *yaml.Node {
	switch v := f.Get(&r.Config).(type) {
	case []string:
		seq := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
		for _, it := range v {
			seq.Content = append(seq.Content, scalar("!!str", it))
		}

		return seq
	case bool:
		return scalar("!!bool", strconv.FormatBool(v))
	case int:
		return scalar("!!int", strconv.Itoa(v))
	default:
		return scalar("!!str", f.Format(&r.Config))
	}
}

func scalar(tag, value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
}

// section returns the mapping that holds the key at path, creating the
// intermediate mappings, and the key's own name.
func section(root *yaml.Node, path string) (parent *yaml.Node, name string) {
	parts := strings.Split(path, ".")
	node := root

	for _, p := range parts[:len(parts)-1] {
		var child *yaml.Node

		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == p {
				child = node.Content[i+1]

				break
			}
		}

		if child == nil {
			child = &yaml.Node{Kind: yaml.MappingNode}
			node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: p}, child)
		}

		node = child
	}

	return node, parts[len(parts)-1]
}
