// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/user"
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

// SetKey reads localconfig.xml, sets the given key to value, and writes back.
// If the key doesn't exist, it is appended. File locking is used to prevent
// concurrent write corruption.
func SetKey(path, key, value string) error {
	return withFileLock(path, func() error {
		lc, err := readLocalConfigXML(path)
		if err != nil {
			return err
		}

		found := false

		for i := range lc.Keys {
			if lc.Keys[i].Name == key {
				lc.Keys[i].Value = value
				found = true

				break
			}
		}

		if !found {
			lc.Keys = append(lc.Keys, Key{Name: key, Value: value})
		}

		return SaveLocalConfig(path, lc)
	})
}

// RemoveKey reads localconfig.xml and removes the given key.
// If the key has a compiled-in default, its value is set to empty string instead.
// Returns an error if the key is not found in the file.
func RemoveKey(path, key string) error {
	return withFileLock(path, func() error {
		lc, err := readLocalConfigXML(path)
		if err != nil {
			return err
		}

		found := false

		for i := range lc.Keys {
			if lc.Keys[i].Name != key {
				continue
			}

			found = true

			// If key has a default, set to empty instead of removing
			if _, hasDefault := Defaults[key]; hasDefault {
				lc.Keys[i].Value = ""
			} else {
				lc.Keys = append(lc.Keys[:i], lc.Keys[i+1:]...)
			}

			break
		}

		if !found {
			return fmt.Errorf("key %s is not set", key)
		}

		return SaveLocalConfig(path, lc)
	})
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
		_ = lockFile.Close()
		_ = os.Remove(lockPath)
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
