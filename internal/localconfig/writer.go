// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"slices"
	"strconv"
	"syscall"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/fileutil"
)

// SaveLocalConfig writes a LocalConfig struct to an XML file atomically.
// It writes to a temp file first, then renames to avoid partial writes.
func SaveLocalConfig(path string, lc *LocalConfig) error {
	data, err := xml.MarshalIndent(lc, "", "    ")
	if err != nil {
		return fmt.Errorf("failed to marshal localconfig: %w", err)
	}

	content := append([]byte(xml.Header), data...)
	content = append(content, '\n')

	return atomicWrite(path, content)
}

// SetKeys reads localconfig.xml, applies every pair in order (a later pair
// overrides an earlier one with the same name; unknown names are appended), and
// writes the file once, so a batch is all-or-nothing like legacy
// LocalConfig.set(...) x N followed by a single save(). A missing file is
// created (legacy starts from an empty config). File locking is used to
// prevent concurrent write corruption.
func SetKeys(path string, pairs []Key) error {
	return withFileLock(path, func() error {
		lc, err := readOrEmptyLocalConfig(path)
		if err != nil {
			return err
		}

		for _, pair := range pairs {
			setKey(lc, pair)
		}

		return SaveLocalConfig(path, lc)
	})
}

// readOrEmptyLocalConfig is readLocalConfigXML, except that a missing file is an
// empty config.
func readOrEmptyLocalConfig(path string) (*LocalConfig, error) {
	lc, err := readLocalConfigXML(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &LocalConfig{}, nil
	}

	return lc, err
}

// setKey sets pair.Name to pair.Value in lc, appending the key when absent.
func setKey(lc *LocalConfig, pair Key) {
	for i := range lc.Keys {
		if lc.Keys[i].Name == pair.Name {
			lc.Keys[i].Value = pair.Value

			return
		}
	}

	lc.Keys = append(lc.Keys, pair)
}

// KeyNotSetError reports a key that is neither configured in localconfig.xml nor
// a known key with a compiled-in default (legacy LocalConfig.isSet is false).
type KeyNotSetError struct{ Key string }

func (e *KeyNotSetError) Error() string { return "key " + e.Key + " is not set" }

// RemoveKeys removes every named key from localconfig.xml and writes the file
// once. Like legacy LocalConfig.remove, the entry is dropped from the file, so a
// known key falls back to its compiled-in default; a known key that is not in
// the file is a no-op (legacy isSet is true for known keys). A key that is
// neither configured nor known yields a *KeyNotSetError and nothing is written
// (legacy does all removals in memory before the single save). A missing file
// counts as an empty config and is created.
func RemoveKeys(path string, keys []string) error {
	return withFileLock(path, func() error {
		lc, err := readOrEmptyLocalConfig(path)
		if err != nil {
			return err
		}

		for _, key := range keys {
			if !removeKey(lc, key) && !IsKnownKey(key) {
				return &KeyNotSetError{Key: key}
			}
		}

		return SaveLocalConfig(path, lc)
	})
}

// removeKey drops key from lc and reports whether it was present.
func removeKey(lc *LocalConfig, key string) bool {
	before := len(lc.Keys)

	lc.Keys = slices.DeleteFunc(lc.Keys, func(k Key) bool { return k.Name == key })

	return len(lc.Keys) != before
}

// readLocalConfigXML reads and parses localconfig.xml preserving the full struct.
func readLocalConfigXML(path string) (*LocalConfig, error) {
	// #nosec G304 - path is intentionally provided by caller
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read localconfig file: %w", err)
	}

	var lc LocalConfig
	if err := xml.Unmarshal(data, &lc); err != nil {
		return nil, fmt.Errorf("failed to parse localconfig XML: %w", err)
	}

	return &lc, nil
}

// atomicWrite writes content to the target path via fileutil.AtomicWrite,
// preserving the target's existing permissions, or the temp file's default
// 0600 mode when path does not yet exist.
// If running as root, ensures the file is owned by zextras:zextras.
func atomicWrite(path string, content []byte) error {
	perm := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode()
	}

	if err := fileutil.AtomicWrite(path, content, perm); err != nil {
		return err
	}

	// If running as root, ensure file is owned by zextras
	if err := ensureZextrasOwnership(path); err != nil {
		return fmt.Errorf("failed to set ownership: %w", err)
	}

	return nil
}

// withFileLock acquires an exclusive flock on a .lock file adjacent to the
// config path, executes fn, then releases the lock.
func withFileLock(path string, fn func() error) error {
	lockPath := path + ".lock"

	// #nosec G304 - lock path derived from config path
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open lock file: %w", err)
	}

	defer func() {
		// The lock file is intentionally never unlinked: removing it would let
		// two processes hold the "exclusive" lock on different inodes.
		_ = lockFile.Close()
	}()

	// If running as root, ensure lock file is owned by zextras
	_ = ensureZextrasOwnership(lockPath)

	// #nosec G115 - fd conversion is safe for flock on Linux
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}

	defer func() {
		// #nosec G115 - fd conversion is safe for flock on Linux
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	}()

	return fn()
}

// ensureZextrasOwnership changes the ownership of the given path to zextras:zextras
// if the current user is root. It returns nil if the user is not root, if the
// zextras user does not exist on this system, or if ownership is changed successfully.
func ensureZextrasOwnership(path string) error {
	if os.Getuid() != 0 {
		return nil
	}

	zUser, err := user.Lookup(config.ZextrasUser)
	if err != nil {
		// zextras user not present (e.g. dev/CI environment) — skip chown.
		return nil //nolint:nilerr
	}

	uid, _ := strconv.Atoi(zUser.Uid)
	gid, _ := strconv.Atoi(zUser.Gid)

	if err := os.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("failed to chown %s to zextras: %w", path, err)
	}

	return nil
}
