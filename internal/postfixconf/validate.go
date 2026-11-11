// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package postfixconf is the privilege boundary for Postfix configuration
// under /opt/zextras/common/conf (CO-4621).
//
// Postfix master runs as root and trusts main.cf and master.cf there, so the
// directory is root-owned and the unprivileged configd (user zextras) never
// writes it. It stages the wanted change in a zextras-owned directory and
// triggers the root helper postfix-conf, which validates the staged
// change and installs it. This package holds both halves: the validator and
// the root-side apply logic (used by cmd/postfix-conf), and the
// Client configd uses to stage and trigger.
package postfixconf

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Fixed locations and names shared by configd and the helper.
const (
	// HelperPath is the root-owned helper binary.
	HelperPath = "/opt/zextras/libexec/postfix-conf"
	// UnitName is the systemd oneshot unit that runs "HelperPath apply".
	UnitName = "carbonio-postfix-conf.service"
	// StagingDir is the zextras-owned (0700) directory configd stages into.
	StagingDir = "/opt/zextras/data/postfix/conf-staging"
	// ConfDir is the root-owned Postfix configuration directory.
	ConfDir = "/opt/zextras/common/conf"
	// PostconfPath is the postconf binary the helper runs.
	PostconfPath = "/opt/zextras/common/sbin/postconf"
	// LockDir is the root-only runtime directory of the helper (the systemd
	// unit's RuntimeDirectory); LockPath serializes helper runs.
	LockDir  = "/run/postfix-conf"
	LockPath = LockDir + "/lock"

	// MaxFileSize caps every staged file.
	MaxFileSize = 1 << 20
	// MaxValueSize caps one parameter value (postconf takes it as an argument).
	MaxValueSize = 64 << 10

	// SetFile and UnsetFile are the staged postconf instructions.
	SetFile   = "postconf.set"
	UnsetFile = "postconf.unset"

	// MasterCf and the two .re files are the staged whole files.
	MasterCf           = "master.cf"
	TagAsForeignRe     = "tag_as_foreign.re"
	TagAsOriginatingRe = "tag_as_originating.re"

	// MainCf is created empty when missing.
	MainCf = "main.cf"
)

// ErrValidation marks a request the helper refuses; nothing is applied.
var ErrValidation = errors.New("validation error")

// InstallableFiles are the whole files the helper installs into ConfDir, in
// the fixed order they are applied.
var InstallableFiles = []string{MasterCf, TagAsForeignRe, TagAsOriginatingRe}

// IsInstallable reports whether name is a whole file the helper installs.
func IsInstallable(name string) bool { return slices.Contains(InstallableFiles, name) }

var keyRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// Packaged Postfix accounts; mail_owner and setgid_group must be exactly these.
const (
	KeyMailOwner        = "mail_owner"
	KeySetgidGroup      = "setgid_group"
	PackagedMailOwner   = "postfix"
	PackagedSetgidGroup = "postdrop"
)

func validationf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrValidation, fmt.Sprintf(format, args...))
}

// IsAllowedKey reports whether key is a main.cf parameter configd may change.
func IsAllowedKey(key string) bool {
	_, ok := allowedKeys[key]

	return ok
}

// ValidateKey checks a main.cf parameter name against the allowlist. Staged
// content is never echoed back unless it is a well-formed name.
func ValidateKey(key string) error {
	if !keyRe.MatchString(key) {
		return validationf("invalid parameter name (%d bytes)", len(key))
	}

	if !IsAllowedKey(key) {
		return validationf("parameter %q is not allowed to be changed", key)
	}

	return nil
}

func validateValue(key, value string) error {
	if len(value) > MaxValueSize {
		return validationf("value of %q is larger than %d bytes", key, MaxValueSize)
	}

	if strings.ContainsAny(value, "\x00\r\n") {
		return validationf("value of %q contains NUL, CR or LF", key)
	}

	return nil
}

// ValidateAssignment checks one main.cf KEY=VALUE.
func ValidateAssignment(key, value string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}

	if err := validateValue(key, value); err != nil {
		return err
	}

	switch key {
	case KeyMailOwner:
		if value != PackagedMailOwner {
			return validationf("%s must be %q", KeyMailOwner, PackagedMailOwner)
		}
	case KeySetgidGroup:
		if value != PackagedSetgidGroup {
			return validationf("%s must be %q", KeySetgidGroup, PackagedSetgidGroup)
		}
	}

	return nil
}

// KV is one parameter assignment.
type KV struct{ Key, Value string }

// Change is everything one apply installs.
type Change struct {
	// Files maps an installable file name to its full new content.
	Files map[string][]byte
	// Unset lists the parameters to delete (postconf -X).
	Unset []string
	// Set lists the assignments, in order (postconf -e).
	Set []KV
}

// Empty reports whether the change does nothing.
func (c *Change) Empty() bool { return len(c.Files) == 0 && len(c.Unset) == 0 && len(c.Set) == 0 }

// Validate checks the whole change; nothing is applied unless it passes.
func (c *Change) Validate() error {
	for name, data := range c.Files {
		if err := ValidateFile(name, data); err != nil {
			return err
		}
	}

	for _, k := range c.Unset {
		if err := ValidateKey(k); err != nil {
			return err
		}
	}

	for _, kv := range c.Set {
		if err := ValidateAssignment(kv.Key, kv.Value); err != nil {
			return err
		}
	}

	return nil
}

// ValidateFile checks one installable file.
func ValidateFile(name string, data []byte) error {
	if !IsInstallable(name) {
		return validationf("%q is not a file the helper installs", name)
	}

	if len(data) > MaxFileSize {
		return validationf("%s is larger than %d bytes", name, MaxFileSize)
	}

	if name == MasterCf {
		return ValidateMasterCf(data)
	}

	return nil
}
