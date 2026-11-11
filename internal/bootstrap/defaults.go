// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrap

import (
	"crypto/rand"
	"fmt"
	"strconv"
	"strings"
)

// Host is what the defaults read from the node.
type Host interface {
	// FQDN is the fully qualified hostname, lower case ("" if unknown).
	FQDN() string
	// InstalledRoles are the roles whose package is installed.
	InstalledRoles() []string
	// IPMode is "ipv6" on a node with IPv6 addresses only, else "ipv4".
	IPMode() string
	// Timezone is the Olson name of the system timezone ("" if unknown).
	Timezone() string
	// Networks are the networks of the non-loopback interfaces, in CIDR form.
	Networks() []string
	// MemoryMB is the total memory of the node (0 if unknown).
	MemoryMB() int
}

// defaultTimezone is the setup.pl fallback when the system zone is unknown.
const defaultTimezone = "America/Los_Angeles"

// Store and proxy ports: the proxy takes the standard ports when installed
// on the node, and the mailbox moves to the 7xxx ones.
var (
	storePortsBehindProxy = [6]int{7143, 7993, 7110, 7995, 80, 443}
	storePortsDirect      = [6]int{143, 993, 110, 995, 80, 443}
	proxyPortsLocal       = [6]int{143, 993, 110, 995, 8080, 8443}
	proxyPortsRemote      = [6]int{7143, 7993, 7110, 7995, 8080, 8443}
	portKeys              = [6]string{"imap", "imaps", "pop3", "pop3s", "http", "https"}
)

// ApplyDefaults fills every key no layer set. Defaults are computed in
// declaration order from the values already set, so a default may depend on
// keys before it (domain.name on host.hostname, admin.account on
// domain.name).
func (r *Resolved) ApplyDefaults(host Host) error {
	for _, s := range r.defaultSteps(host) {
		f := FieldByPath(s.path)
		if r.sources[s.path] != SourceUnset {
			continue
		}

		v, err := s.val()
		if err != nil {
			return fmt.Errorf("default of %s: %w", s.path, err)
		}

		if v == "" {
			continue
		}

		if err := r.Set(f, v, s.src); err != nil {
			return fmt.Errorf("default of %s: %w", s.path, err)
		}
	}

	return nil
}

// defaultSteps lists the defaults in the order ApplyDefaults computes them.
func (r *Resolved) defaultSteps(host Host) []defaultStep {
	c := &r.Config

	steps := []defaultStep{
		{keyRoles, SourceDetected, func() (string, error) { return strings.Join(host.InstalledRoles(), ","), nil }},
		{keyHostHostname, SourceDetected, constant(host.FQDN())},
		{keyHostPublicHostname, SourceDefault, func() (string, error) { return c.Host.Hostname, nil }},
		{keyHostIPMode, SourceDetected, constant(host.IPMode())},
		{keyHostTimezone, SourceDetected, func() (string, error) { return orDefault(host.Timezone(), defaultTimezone), nil }},
		{keyHostSSLDigest, SourceDefault, constant("sha256")},
		{keyHostRequireInterprocessSecurity, SourceDefault, constant("false")},
		{keyLdapHost, SourceDefault, func() (string, error) { return ifRole(c, RoleDirectoryServer, c.Host.Hostname), nil }},
		{keyLdapPort, SourceDefault, constant("389")},
		{keyLdapBaseDn, SourceDefault, constant("cn=zimbra")},
		{keyLdapReplicationType, SourceDefault, func() (string, error) { return defaultReplicationType(c), nil }},
		{keyLdapServerID, SourceDefault, func() (string, error) { return ifRole(c, RoleDirectoryServer, "2"), nil }},
		{keyLdapRootPassword, SourceGenerated, r.generateOnMaster()},
		{keyLdapAdminPassword, SourceGenerated, r.generateOnMaster()},
		{keyLdapPostfixPassword, SourceGenerated, r.generateOnMaster()},
		{keyLdapAmavisPassword, SourceGenerated, r.generateOnMaster()},
		{keyLdapNginxPassword, SourceGenerated, r.generateOnMaster()},
		{keyLdapReplicationPassword, SourceGenerated, r.generateOnMaster()},
		{keyDomainCreate, SourceDefault, hasRole(c, RoleDirectoryServer)},
		{keyDomainName, SourceDefault, func() (string, error) { return parentDomain(c.Host.Hostname), nil }},
		{keyAdminCreate, SourceDefault, hasRole(c, RoleAppserver)},
		{keyAdminAccount, SourceDefault, func() (string, error) { return mailbox("zextras", c.Domain.Name), nil }},
		{keyNotificationsSMTPSource, SourceDefault, func() (string, error) { return c.Admin.Account, nil }},
		{keyNotificationsSMTPDestination, SourceDefault, func() (string, error) { return c.Admin.Account, nil }},
		{keyNotificationsAvNotifyUser, SourceDefault, func() (string, error) { return c.Admin.Account, nil }},
		{keyMtaMyNetworks, SourceDetected, func() (string, error) { return myNetworks(c, host), nil }},
		{keyMtaAntispam, SourceDefault, constant("true")},
		{keyMtaAntivirus, SourceDefault, hasRole(c, RoleClamAV)},
		{keyMtaOpendkim, SourceDefault, constant("true")},
		{keyMtaCbpolicyd, SourceDefault, constant("false")},
		{keyStoreSMTPHost, SourceDefault, func() (string, error) { return ifRole(c, RoleMTA, c.Host.Hostname), nil }},
		{keyStoreMode, SourceDefault, constant("https")},
		{keyStoreMailProxy, SourceDefault, constant("true")},
		{keyStoreWebProxy, SourceDefault, constant("true")},
		{keyStoreSpamTraining, SourceDefault, constant("true")},
		{keyStoreKeyboardShortcuts, SourceDefault, constant("true")},
		{keyStoreMailboxdMemoryMb, SourceDetected, func() (string, error) { return mailboxdMemory(c, host), nil }},
		{keyStoreMysqlMemoryPercent, SourceDefault, func() (string, error) { return ifRole(c, RoleAppserver, "30"), nil }},
		{keyStoreSpamAccount, SourceGenerated, r.systemAccount("spam")},
		{keyStoreHamAccount, SourceGenerated, r.systemAccount("ham")},
		{keyStoreVirusQuarantineAccount, SourceGenerated, r.systemAccount("virus-quarantine")},
	}

	steps = append(steps, portSteps(c, "store.ports.", storePortsBehindProxy, storePortsDirect)...)

	steps = append(steps, []defaultStep{
		{keyProxyMode, SourceDefault, constant("https")},
		{keyProxyMailEnabled, SourceDefault, hasRole(c, RoleProxy)},
		{keyProxyWebEnabled, SourceDefault, hasRole(c, RoleProxy)},
		{keyProxyStrictServerName, SourceDefault, constant("false")},
	}...)

	steps = append(steps, portSteps(c, "proxy.ports.", proxyPortsLocal, proxyPortsRemote)...)

	return append(steps, defaultStep{keyStartServices, SourceDefault, constant("true")})
}

// portSteps defaults the six ports under prefix: onProxy when the node runs
// the proxy, otherwise notProxy.
func portSteps(c *Config, prefix string, onProxy, notProxy [6]int) []defaultStep {
	steps := make([]defaultStep, 0, len(portKeys))

	for i, k := range portKeys {
		steps = append(steps, defaultStep{prefix + k, SourceDefault, func() (string, error) {
			if c.HasRole(RoleProxy) {
				return strconv.Itoa(onProxy[i]), nil
			}

			return strconv.Itoa(notProxy[i]), nil
		}})
	}

	return steps
}

// myNetworks is the mta.mynetworks default: loopback plus the host's
// networks, on an MTA node.
func myNetworks(c *Config, host Host) string {
	if !c.HasRole(RoleMTA) {
		return ""
	}

	return strings.Join(append([]string{"127.0.0.0/8", "[::1]/128"}, host.Networks()...), ",")
}

// mailboxdMemory is the store.mailboxdmemorymb default, on an appserver
// with known memory.
func mailboxdMemory(c *Config, host Host) string {
	if !c.HasRole(RoleAppserver) || host.MemoryMB() == 0 {
		return ""
	}

	return strconv.Itoa(MailboxdMemoryMB(host.MemoryMB()))
}

// defaultReplicationType is setup.pl changeLdapHost: a directory server is
// the master of itself, a replica of any other ldap.host.
func defaultReplicationType(c *Config) string {
	switch {
	case !c.HasRole(RoleDirectoryServer):
		return ""
	case c.LDAP.Host == c.Host.Hostname:
		return ReplicationMaster
	default:
		return ReplicationReplica
	}
}

// MailboxdMemoryMB is the setup.pl heap of mailboxd: a quarter of the
// memory, a fifth above 16 GB.
func MailboxdMemoryMB(totalMB int) int {
	if totalMB > 16*1024 {
		return totalMB / 5
	}

	return totalMB / 4
}

// defaultStep computes the default of the key at path, recorded as src. An
// empty value leaves the key unset.
type defaultStep struct {
	path string
	src  Source
	val  func() (string, error)
}

func constant(v string) func() (string, error) {
	return func() (string, error) { return v, nil }
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

// hasRole is the default "true" on nodes running role, else "false".
func hasRole(c *Config, role string) func() (string, error) {
	return func() (string, error) { return strconv.FormatBool(c.HasRole(role)), nil }
}

func ifRole(c *Config, role, v string) string {
	if c.HasRole(role) {
		return v
	}

	return ""
}

// generateOnMaster generates a password on the LDAP master; elsewhere
// (replicas included) the password must be given, as it belongs to the
// master.
func (r *Resolved) generateOnMaster() func() (string, error) {
	return func() (string, error) {
		if !r.Config.IsLDAPMaster() {
			return "", nil
		}

		return GeneratePassword()
	}
}

// systemAccount generates "<prefix>.<random>@<domain>" on application
// servers, the setup.pl name of the spam training and quarantine accounts.
func (r *Resolved) systemAccount(prefix string) func() (string, error) {
	return func() (string, error) {
		if !r.Config.HasRole(RoleAppserver) || r.Config.Domain.Name == "" {
			return "", nil
		}

		suffix, err := randomString(10, lowerAlnum)
		if err != nil {
			return "", err
		}

		return prefix + "." + suffix + "@" + r.Config.Domain.Name, nil
	}
}

// parentDomain drops the first label of a hostname: mail.example.com →
// example.com. A single-label name is returned as is.
func parentDomain(hostname string) string {
	if _, rest, ok := strings.Cut(hostname, "."); ok && strings.Contains(rest, ".") {
		return rest
	}

	return hostname
}

func mailbox(local, domain string) string {
	if domain == "" {
		return ""
	}

	return local + "@" + domain
}

const (
	lowerAlnum    = "abcdefghijklmnopqrstuvwxyz0123456789"
	passwordChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

// GeneratePassword returns a random 20-character alphanumeric password.
// It is a variable so tests get stable output.
var GeneratePassword = func() (string, error) { return randomString(20, passwordChars) }

// randomString draws n characters of alphabet uniformly: bytes past the
// largest multiple of len(alphabet) are rejected, so no character is favoured.
func randomString(n int, alphabet string) (string, error) {
	limit := 256 - 256%len(alphabet)
	out := make([]byte, 0, n)
	buf := make([]byte, n)

	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}

		for _, b := range buf {
			if int(b) < limit && len(out) < n {
				out = append(out, alphabet[int(b)%len(alphabet)])
			}
		}
	}

	return string(out), nil
}
