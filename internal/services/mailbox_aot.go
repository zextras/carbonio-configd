// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
)

// AOT cache of the mailbox JVM (JDK 25+, JEP 514), the legacy-mode twin of
// carbonio-appserver.service: the JVM maps the loaded and linked classes of a
// training run from a cache instead of parsing and verifying them again. The
// cache only covers the built-in class loaders, so the Carbonio Advanced
// extension jars join the mailbox jars on one flat class path (ZAL delegates
// to its parent with -Dzal.flatClasspath=true) instead of a child-first loader.
//
// The flat class path and the flag are used on every JDK; the AOT flags only
// from JDK 25 (JDK 21 rejects the options). Without a cache the run is the
// training run and the JVM writes the cache when it exits (AOTCacheOutput); it
// never rewrites a stale one, so prepareMailboxAOT deletes the cache whenever
// what it was built from changed.
const (
	aotCacheName     = "mailboxd.aot"
	aotInputsName    = aotCacheName + ".inputs"
	aotLogName       = "aot.log"
	aotArgsName      = "mailboxd.args"
	aotMinJDKVersion = 25

	// mailboxMainJar is listed first in the class path, and not again among
	// the other jars: the JVM refuses the cache when a class path entry is
	// duplicated.
	mailboxMainJar = "mailbox.jar"

	// maxSymlinkHops bounds the symlink chains of resolveSymlinks (the Linux
	// limit).
	maxSymlinkHops = 40
)

// mailboxJVMArgsFile is the java argument file holding the class path, the
// ZAL flag and the AOT flags.
func (c *Controller) mailboxJVMArgsFile() string {
	return c.p.appserverState() + "/" + aotArgsName
}

// mailboxExtDir holds the jars of the Carbonio Advanced extensions.
func (c *Controller) mailboxExtDir() string {
	return c.p.lib + "/ext/carbonio"
}

// prepareMailboxAOT writes the JVM argument file (flat class path, ZAL flag,
// AOT flags on JDK 25+) and deletes the cache when it is stale. The caller
// starts the JVM with the previous command line when it returns an error: a
// start never fails because of the cache.
func (c *Controller) prepareMailboxAOT(_ context.Context, javaBin string, _ map[string]string) error {
	state := c.p.appserverState()
	jars := c.p.mailbox + "/jars"

	classpath, err := c.mailboxClasspath(jars, c.mailboxExtDir())
	if err != nil {
		return err
	}

	jdk, err := c.jdkHome(javaBin)
	if err != nil {
		return err
	}

	version, err := c.jdkFeatureVersion(jdk)
	if err != nil {
		return err
	}

	lines := []string{"-Dzal.flatClasspath=true", "-cp " + classpath}

	if version >= aotMinJDKVersion {
		aot, err := c.aotFlags(state, classpath, jdk, jars)
		if err != nil {
			return err
		}

		lines = append(lines, aot...)
	}

	content := strings.Join(lines, "\n") + "\n"
	if err := c.env.FS.WriteFileAtomic(c.mailboxJVMArgsFile(), []byte(content), 0o644); err != nil {
		return fmt.Errorf("write JVM argument file: %w", err)
	}

	return nil
}

// aotFlags drops a stale cache and returns the AOT options: the cache itself
// when one survived, otherwise the output option of the training run.
func (c *Controller) aotFlags(state, classpath, jdk, jars string) ([]string, error) {
	inputs, err := c.aotFingerprint(classpath, jdk, jars)
	if err != nil {
		return nil, err
	}

	if err := c.dropStaleCache(state, inputs); err != nil {
		return nil, err
	}

	cache := state + "/" + aotCacheName
	flags := []string{"-Xlog:aot=warning:file=" + state + "/" + aotLogName}

	if _, err := c.env.FS.Stat(cache); err == nil {
		return append([]string{"-XX:AOTCache=" + cache}, flags...), nil
	}

	return append([]string{"-XX:AOTCacheOutput=" + cache}, flags...), nil
}

// mailboxClasspath builds the flat class path: mailbox.jar first and once,
// every other mailbox jar in directory order (how the JVM expands the "*"
// wildcard: 334 classes exist in several jars with different bytes, so any
// other order could change which one wins), then the jars of the extensions
// directory in directory order. A missing extensions directory (no Advanced)
// adds nothing.
func (c *Controller) mailboxClasspath(jars, ext string) (string, error) {
	names, err := c.env.FS.ReadDirNames(jars)
	if err != nil {
		return "", fmt.Errorf("list mailbox jars: %w", err)
	}

	entries := []string{jars + "/" + mailboxMainJar}

	for _, name := range names {
		if name != mailboxMainJar && isJar(name) {
			entries = append(entries, jars+"/"+name)
		}
	}

	extNames, err := c.env.FS.ReadDirNames(ext)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("list extension jars: %w", err)
	}

	for _, name := range extNames {
		if isJar(name) {
			entries = append(entries, ext+"/"+name)
		}
	}

	return strings.Join(entries, ":"), nil
}

// isJar matches what the JVM expands "*" to in a class path: *.jar, in any
// case.
func isJar(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".jar")
}

// jdkHome returns the JDK root of javaBin: two levels above its real path.
func (c *Controller) jdkHome(javaBin string) (string, error) {
	resolved, err := c.resolveSymlinks(javaBin)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", javaBin, err)
	}

	return filepath.Dir(filepath.Dir(resolved)), nil
}

// jdkFeatureVersion reads the feature version (21, 25, ...) from the
// JAVA_VERSION line of <jdk>/release.
func (c *Controller) jdkFeatureVersion(jdk string) (int, error) {
	release, err := c.env.FS.ReadFile(jdk + "/release")
	if err != nil {
		return 0, fmt.Errorf("read JDK release file: %w", err)
	}

	for line := range strings.SplitSeq(string(release), "\n") {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), "JAVA_VERSION=")
		if !ok {
			continue
		}

		value = strings.Trim(value, `"'`)
		if end := strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }); end >= 0 {
			value = value[:end]
		}

		version, err := strconv.Atoi(value)
		if err != nil {
			return 0, fmt.Errorf("parse JAVA_VERSION %q: %w", value, err)
		}

		return version, nil
	}

	return 0, errors.New("no JAVA_VERSION in JDK release file")
}

// resolveSymlinks is filepath.EvalSymlinks over the FS boundary: every
// component of the absolute path p is resolved.
func (c *Controller) resolveSymlinks(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%q is not an absolute path", p)
	}

	// No lexical Clean: "link/.." is the parent of the link's target, not the
	// directory holding the link.
	pending := strings.Split(p, "/")
	resolved := "/"

	for hops := 0; len(pending) > 0; {
		name := pending[0]
		pending = pending[1:]

		switch name {
		case "", ".":
			continue
		case "..":
			resolved = filepath.Dir(resolved)

			continue
		}

		next := filepath.Join(resolved, name)

		target, isLink, err := c.readLink(next)
		if err != nil {
			return "", err
		}

		if !isLink {
			resolved = next

			continue
		}

		hops++
		if hops > maxSymlinkHops {
			return "", fmt.Errorf("too many symbolic links at %s", next)
		}

		if filepath.IsAbs(target) {
			resolved = "/"
		}

		pending = append(strings.Split(target, "/"), pending...)
	}

	return resolved, nil
}

// readLink returns the target of p when p is a symbolic link; the path must
// exist.
func (c *Controller) readLink(p string) (target string, isLink bool, err error) {
	info, err := c.env.FS.Lstat(p)
	if err != nil {
		return "", false, fmt.Errorf("lstat %s: %w", p, err)
	}

	if info.Mode()&fs.ModeSymlink == 0 {
		return "", false, nil
	}

	target, err = c.env.FS.Readlink(p)
	if err != nil {
		return "", false, fmt.Errorf("readlink %s: %w", p, err)
	}

	return target, true, nil
}

// aotFingerprint hashes what the cache depends on: the class path argument, the
// JDK (the runtime image and the VM library) and every jar of the mailbox and
// of the extensions directory, each as name, size and modification time. Any
// package upgrade changes it.
func (c *Controller) aotFingerprint(classpath, jdk, jars string) (string, error) {
	h := sha256.New()
	_, _ = h.Write([]byte(classpath))

	for _, f := range []string{jdk + "/lib/modules", jdk + "/lib/server/libjvm.so"} {
		c.hashFile(h, f)
	}

	for _, dir := range []string{jars, c.p.lib + "/ext"} {
		if err := c.hashJars(h, dir); err != nil {
			return "", err
		}
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashFile adds name, size and mtime of f (symlinks followed) to h. A missing
// file is part of the fingerprint too.
func (c *Controller) hashFile(h io.Writer, f string) {
	info, err := c.env.FS.Stat(f)
	if err != nil {
		_, _ = fmt.Fprintf(h, "%s missing\n", f)

		return
	}

	_, _ = fmt.Fprintf(h, "%s %d %d\n", f, info.Size(), info.ModTime().Unix())
}

// hashJars adds every jar below dir (recursively, symlinks followed) to h. A
// missing directory contributes nothing.
func (c *Controller) hashJars(h io.Writer, dir string) error {
	err := c.env.FS.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() && isJar(d.Name()) {
			c.hashFile(h, p)
		}

		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("walk %s: %w", dir, err)
	}

	return nil
}

// dropStaleCache deletes the cache when inputs differs from the stamp of the
// run that created it, and records inputs.
func (c *Controller) dropStaleCache(state, inputs string) error {
	stamp := state + "/" + aotInputsName

	if prev, err := c.env.FS.ReadFile(stamp); err == nil && bytes.Equal(bytes.TrimSpace(prev), []byte(inputs)) {
		return nil
	}

	if err := c.env.FS.Remove(state + "/" + aotCacheName); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove stale cache: %w", err)
	}

	if err := c.env.FS.WriteFileAtomic(stamp, []byte(inputs+"\n"), 0o644); err != nil {
		return fmt.Errorf("record cache inputs: %w", err)
	}

	return nil
}
