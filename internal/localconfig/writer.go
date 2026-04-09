// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

// SaveLocalConfig writes a LocalConfig struct to an XML file atomically.
// It writes to a temp file first, then renames to avoid partial writes.
func SaveLocalConfig(path string, config *LocalConfig) error {
	data, err := xml.MarshalIndent(config, "", "    ")
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
		config, err := readLocalConfigXML(path)
		if err != nil {
			return err
		}

		found := false

		for i := range config.Keys {
			if config.Keys[i].Name == key {
				config.Keys[i].Value = value
				found = true

				break
			}
		}

		if !found {
			config.Keys = append(config.Keys, Key{Name: key, Value: value})
		}

		return SaveLocalConfig(path, config)
	})
}

// RemoveKey reads localconfig.xml and removes the given key.
// If the key has a compiled-in default, its value is set to empty string instead.
// Returns an error if the key is not found in the file.
func RemoveKey(path, key string) error {
	return withFileLock(path, func() error {
		config, err := readLocalConfigXML(path)
		if err != nil {
			return err
		}

		found := false

		for i := range config.Keys {
			if config.Keys[i].Name != key {
				continue
			}

			found = true

			// If key has a default, set to empty instead of removing
			if _, hasDefault := Defaults[key]; hasDefault {
				config.Keys[i].Value = ""
			} else {
				config.Keys = append(config.Keys[:i], config.Keys[i+1:]...)
			}

			break
		}

		if !found {
			return fmt.Errorf("key %s is not set", key)
		}

		return SaveLocalConfig(path, config)
	})
}

// readLocalConfigXML reads and parses localconfig.xml preserving the full struct.
func readLocalConfigXML(path string) (*LocalConfig, error) {
	// #nosec G304 - path is intentionally provided by caller
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read localconfig file: %w", err)
	}

	var config LocalConfig
	if err := xml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse localconfig XML: %w", err)
	}

	return &config, nil
}

// atomicWrite writes content to a temp file then renames it to the target path.
// If running as root, ensures the file is owned by zextras:zextras.
func atomicWrite(path string, content []byte) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".localconfig-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	tmpPath := tmp.Name()

	// Preserve original file permissions
	if info, statErr := os.Stat(path); statErr == nil {
		if chmodErr := tmp.Chmod(info.Mode()); chmodErr != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)

			return fmt.Errorf("failed to set permissions: %w", chmodErr)
		}
	}

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)

		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)

		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// If running as root, ensure file is owned by zextras
	if err := ensureZextrasOwnership(tmpPath); err != nil {
		_ = os.Remove(tmpPath)

		return fmt.Errorf("failed to set ownership: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)

		return fmt.Errorf("failed to rename temp file: %w", err)
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

	zUser, err := user.Lookup("zextras")
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
