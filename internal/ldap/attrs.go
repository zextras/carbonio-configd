// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// defaultAttrsDir is the directory holding the attribute schema files the Java
// AttributeManager loads (every *.xml in it, attrs.xml included).
const defaultAttrsDir = config.ZextrasBase + "/conf/attrs"

// flagServerInherited is the attrs.xml flag marking an attribute whose value
// on a server entry defaults to the global config value.
const flagServerInherited = "serverInherited"

// ParseServerInheritedAttrs returns the names of the attributes flagged
// serverInherited in an attrs.xml document. Names are returned in document
// order, deduplicated. Malformed XML is an error.
func ParseServerInheritedAttrs(r io.Reader) ([]string, error) {
	dec := xml.NewDecoder(r)

	var names []string

	seen := make(map[string]struct{})

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return names, nil
		}

		if err != nil {
			return nil, fmt.Errorf("parse attrs xml: %w", err)
		}

		name, ok := serverInheritedName(tok)
		if !ok {
			continue
		}

		if _, dup := seen[name]; !dup {
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
}

// serverInheritedName returns the name of an <attr> start element flagged
// serverInherited; ok is false for any other token.
func serverInheritedName(tok xml.Token) (name string, ok bool) {
	start, isStart := tok.(xml.StartElement)
	if !isStart || start.Name.Local != "attr" {
		return "", false
	}

	var flags string

	for _, a := range start.Attr {
		switch a.Name.Local {
		case "name":
			name = a.Value
		case "flags":
			flags = a.Value
		}
	}

	if name == "" || !slices.Contains(strings.Split(flags, ","), flagServerInherited) {
		return "", false
	}

	return name, true
}

// attrsFileCache memoises the serverInherited attribute list per schema file,
// re-parsing only when the file's size or modification time changes.
type attrsFileCache struct {
	mu    sync.Mutex
	files map[string]attrsFileEntry
}

type attrsFileEntry struct {
	size  int64
	mtime time.Time
	names []string
}

// serverInheritedAttrs returns the serverInherited attribute names from every
// *.xml file in dir. An unreadable or malformed schema is an error, as the
// legacy AttributeManager aborts zmprov in that case.
func (c *attrsFileCache) serverInheritedAttrs(fsys sys.FS, dir string) ([]string, error) {
	paths, err := fsys.Glob(filepath.Join(dir, "*.xml"))
	if err != nil {
		return nil, fmt.Errorf("list attrs files: %w", err)
	}

	if len(paths) == 0 {
		return nil, fmt.Errorf("no attrs xml files in %s", dir)
	}

	slices.Sort(paths)

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.files == nil {
		c.files = make(map[string]attrsFileEntry, len(paths))
	}

	var all []string

	for _, p := range paths {
		names, err := c.load(fsys, p)
		if err != nil {
			return nil, err
		}

		all = append(all, names...)
	}

	return all, nil
}

func (c *attrsFileCache) load(fsys sys.FS, path string) ([]string, error) {
	fi, err := fsys.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat attrs file: %w", err)
	}

	if e, ok := c.files[path]; ok && e.size == fi.Size() && e.mtime.Equal(fi.ModTime()) {
		return e.names, nil
	}

	//nolint:gosec // G304: path is a fixed schema directory entry
	f, err := fsys.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open attrs file: %w", err)
	}
	defer func() { _ = f.Close() }()

	names, err := ParseServerInheritedAttrs(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	c.files[path] = attrsFileEntry{size: fi.Size(), mtime: fi.ModTime(), names: names}

	return names, nil
}
