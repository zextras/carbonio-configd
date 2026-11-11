// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrap

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"regexp"
	"slices"
	"strings"
)

// minAdminPassword is the zimbraPasswordMinLength default.
const minAdminPassword = 6

// requiredWhen lists, per key, when the key must have a value.
var requiredWhen = map[string]func(c *Config) bool{
	keyRoles:                       always,
	keyHostHostname:                always,
	keyHostPublicHostname:          always,
	keyLdapHost:                    always,
	keyLdapAdminPassword:           always,
	keyLdapRootPassword:            role(RoleDirectoryServer),
	keyLdapReplicationPassword:     role(RoleDirectoryServer),
	keyLdapReplicationType:         role(RoleDirectoryServer),
	keyLdapServerID:                func(c *Config) bool { return c.LDAP.ReplicationType == ReplicationMMR },
	keyLdapPostfixPassword:         anyRole(RoleDirectoryServer, RoleMTA),
	keyLdapAmavisPassword:          anyRole(RoleDirectoryServer, RoleMTA),
	keyLdapNginxPassword:           anyRole(RoleDirectoryServer, RoleProxy),
	keyDomainName:                  func(c *Config) bool { return c.Domain.Create },
	keyAdminAccount:                func(c *Config) bool { return c.Admin.Create },
	keyAdminPassword:               func(c *Config) bool { return c.Admin.Create },
	keyMtaMyNetworks:               role(RoleMTA),
	keyStoreSMTPHost:               role(RoleAppserver),
	keyStoreSpamAccount:            func(c *Config) bool { return c.HasRole(RoleAppserver) && c.Store.SpamTraining },
	keyStoreHamAccount:             func(c *Config) bool { return c.HasRole(RoleAppserver) && c.Store.SpamTraining },
	keyStoreVirusQuarantineAccount: role(RoleAppserver),
}

func always(*Config) bool { return true }

func role(r string) func(*Config) bool {
	return func(c *Config) bool { return c.HasRole(r) }
}

func anyRole(roles ...string) func(*Config) bool {
	return func(c *Config) bool { return slices.ContainsFunc(roles, c.HasRole) }
}

// IsRequired reports whether f must have a value in c.
func IsRequired(f *Field, c *Config) bool {
	when := requiredWhen[f.Path]

	return when != nil && when(c)
}

// Missing returns the required keys of c that have no value.
func Missing(c *Config) []*Field {
	var out []*Field

	for _, f := range Fields {
		if IsRequired(f, c) && f.IsZero(c) {
			out = append(out, f)
		}
	}

	return out
}

// MissingError words a missing key with the ways to set it.
func MissingError(f *Field) error {
	env := f.Env
	if f.Secret {
		env += "[_FILE]"
	}

	return fmt.Errorf("%s is required (set it in the YAML file, with --set or with %s)", f.Path, env)
}

// Validate returns every problem of c: missing required keys first, then
// malformed values.
func Validate(c *Config) error {
	var errs []error

	for _, f := range Missing(c) {
		errs = append(errs, MissingError(f))
	}

	for _, f := range Fields {
		if f.IsZero(c) {
			continue
		}

		if err := CheckValue(f, c); err != nil {
			errs = append(errs, err)
		}
	}

	errs = append(errs, checkPortClashes(c)...)
	errs = append(errs, checkReplication(c)...)

	return errors.Join(errs...)
}

var hostnameRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// CheckValue checks the format of the value of f in c (a set value only).
func CheckValue(f *Field, c *Config) error {
	switch f.Check {
	case "fqdn":
		return checkHostname(f.Path, f.Format(c))
	case "email":
		return checkAddress(f.Path, f.Format(c))
	case "password":
		if len(f.Format(c)) < minAdminPassword {
			return fmt.Errorf("%s must be at least %d characters", f.Path, minAdminPassword)
		}
	case "networks":
		for _, n := range f.Get(c).([]string) {
			if !validNetwork(n) {
				return fmt.Errorf("%s: %q is not an address or a network", f.Path, n)
			}
		}
	}

	if f.TypeName() == "int" {
		if n := f.Get(c).(int); n < 1 || n > 65535 {
			return fmt.Errorf("%s: %d is not a port (1-65535)", f.Path, n)
		}
	}

	return nil
}

func checkHostname(path, v string) error {
	if !hostnameRE.MatchString(strings.ToLower(v)) {
		return fmt.Errorf("%s: %q is not a fully qualified name", path, v)
	}

	return nil
}

func checkAddress(path, v string) error {
	a, err := mail.ParseAddress(v)
	if err != nil || a.Address != v {
		return fmt.Errorf("%s: %q is not an email address", path, v)
	}

	_, domain, _ := strings.Cut(v, "@")

	return checkHostname(path, domain)
}

// validNetwork accepts an IP or a CIDR, IPv6 optionally in brackets
// ("[::1]/128", the postfix form).
func validNetwork(n string) bool {
	if strings.HasPrefix(n, "[") {
		addr, bits, _ := strings.Cut(n[1:], "]")
		n = addr + bits
	}

	if net.ParseIP(n) != nil {
		return true
	}

	_, _, err := net.ParseCIDR(n)

	return err == nil
}

// checkPortClashes rejects a proxy and a mailbox on the same node listening
// on the same port.
func checkPortClashes(c *Config) []error {
	if !c.HasRole(RoleProxy) || !c.HasRole(RoleAppserver) {
		return nil
	}

	owner := make(map[int]string)

	var errs []error

	for _, f := range Fields {
		if !strings.HasPrefix(f.Path, "store.ports.") && !strings.HasPrefix(f.Path, "proxy.ports.") {
			continue
		}

		p := f.Get(c).(int)
		if p == 0 {
			continue
		}

		if prev, dup := owner[p]; dup {
			errs = append(errs, fmt.Errorf("%s and %s both use port %d", prev, f.Path, p))

			continue
		}

		owner[p] = f.Path
	}

	return errs
}

// checkReplication rejects a replica or multi-master peer that names itself
// as the node it replicates from, and a master that names another node.
func checkReplication(c *Config) []error {
	if !c.HasRole(RoleDirectoryServer) || c.LDAP.Host == "" {
		return nil
	}

	self := c.LDAP.Host == c.Host.Hostname

	switch c.LDAP.ReplicationType {
	case ReplicationReplica, ReplicationMMR:
		if self && !c.LDAP.ForceReplication {
			return []error{fmt.Errorf("ldap.replication_type %s needs ldap.host to be another directory server, not %s",
				c.LDAP.ReplicationType, c.Host.Hostname)}
		}
	case ReplicationMaster:
		if !self {
			return []error{fmt.Errorf("ldap.replication_type master needs ldap.host to be this node (%s), not %s",
				c.Host.Hostname, c.LDAP.Host)}
		}
	}

	return nil
}
