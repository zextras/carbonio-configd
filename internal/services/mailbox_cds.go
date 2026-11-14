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
	"strings"
)

// Class data sharing (AppCDS) of the mailbox JVM, the legacy-mode twin of
// carbonio-appserver.service and its ExecStartPre, carbonio-appserver-cds:
// the JVM maps the classes of the previous run from an archive instead of
// parsing and verifying them again. With AutoCreateSharedArchive it writes the
// archive when it exits and the archive is missing, but it never rewrites a
// stale one, so prepareMailboxCDS deletes the archive whenever what it was
// built from changed.
const (
	cdsArchiveName   = "mailboxd.jsa"
	cdsInputsName    = cdsArchiveName + ".inputs"
	cdsClasspathName = "classpath.args"

	// cdsMainJar is listed first in the class path, and not again among the
	// other jars: the JVM refuses the archive when a class path entry is
	// duplicated ("shared class paths mismatch").
	cdsMainJar = "mailbox.jar"

	// maxSymlinkHops bounds the symlink chains of resolveSymlinks (the Linux
	// limit).
	maxSymlinkHops = 40
)

// mailboxClasspathArgs is the java argument file holding the class path.
func (c *Controller) mailboxClasspathArgs() string {
	return c.p.appserverState() + "/" + cdsClasspathName
}

// prepareMailboxCDS writes the class path argument file and deletes the
// archive when it is stale. The caller starts the JVM without class data
// sharing when it returns an error: a start never fails because of the cache.
func (c *Controller) prepareMailboxCDS(_ context.Context, javaBin string, _ map[string]string) error {
	state := c.p.appserverState()
	jars := c.p.mailbox + "/jars"

	argsFile, err := c.mailboxClasspath(jars)
	if err != nil {
		return err
	}

	if err := c.env.FS.WriteFileAtomic(c.mailboxClasspathArgs(), argsFile, 0o644); err != nil {
		return fmt.Errorf("write class path argument file: %w", err)
	}

	jdk, err := c.jdkHome(javaBin)
	if err != nil {
		return err
	}

	inputs, err := c.cdsFingerprint(argsFile, jdk, jars)
	if err != nil {
		return err
	}

	return c.dropStaleArchive(state, inputs)
}

// mailboxClasspath builds the content of the argument file: the class path
// "jars/mailbox.jar:jars/*" spelled out. mailbox.jar comes first, then every
// other jar in directory order, which is how the JVM expands the wildcard:
// 334 classes exist in several jars with different bytes, so any other order
// could change which one wins.
func (c *Controller) mailboxClasspath(jars string) ([]byte, error) {
	names, err := c.env.FS.ReadDirNames(jars)
	if err != nil {
		return nil, fmt.Errorf("list mailbox jars: %w", err)
	}

	var b strings.Builder

	b.WriteString("-cp " + jars + "/" + cdsMainJar)

	for _, name := range names {
		if name != cdsMainJar && isJar(name) {
			b.WriteString(":" + jars + "/" + name)
		}
	}

	b.WriteString("\n")

	return []byte(b.String()), nil
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

// cdsFingerprint hashes what the archive depends on: the argument file, the
// JDK (the runtime image and the VM library) and every jar of the mailbox and
// of the extensions directory, each as name, size and modification time. Any
// package upgrade changes it.
func (c *Controller) cdsFingerprint(argsFile []byte, jdk, jars string) (string, error) {
	h := sha256.New()
	_, _ = h.Write(argsFile)

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

// dropStaleArchive deletes the archive when inputs differs from the stamp of
// the run that created it, and records inputs.
func (c *Controller) dropStaleArchive(state, inputs string) error {
	stamp := state + "/" + cdsInputsName

	if prev, err := c.env.FS.ReadFile(stamp); err == nil && bytes.Equal(bytes.TrimSpace(prev), []byte(inputs)) {
		return nil
	}

	if err := c.env.FS.Remove(state + "/" + cdsArchiveName); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove stale archive: %w", err)
	}

	if err := c.env.FS.WriteFileAtomic(stamp, []byte(inputs+"\n"), 0o644); err != nil {
		return fmt.Errorf("record archive inputs: %w", err)
	}

	return nil
}
