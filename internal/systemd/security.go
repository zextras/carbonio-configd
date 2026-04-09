// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package systemd provides security profile definitions for systemd service hardening.
// Security profiles define systemd directives that improve service isolation and reduce
// the attack surface according to systemd-analyze security recommendations.
package systemd

import (
	"fmt"
	"sort"
	"strings"
)

// SecurityLevel represents the hardening level for a service.
type SecurityLevel string

const (
	// SecurityLevelStrict applies maximum hardening for services with minimal system access needs.
	// Suitable for: memcached, opendkim, freshclam, saslauthd, stats
	SecurityLevelStrict SecurityLevel = "strict"

	// SecurityLevelStandard applies balanced hardening for network services.
	// Suitable for: nginx, postfix, milter, mailthreat
	SecurityLevelStandard SecurityLevel = "standard"

	// SecurityLevelMinimal applies basic protection for complex services requiring elevated privileges.
	// Suitable for: configd, appserver, appserver-db, openldap, antivirus
	SecurityLevelMinimal SecurityLevel = "minimal"

	// yesValue is the systemd boolean string used in directives.
	yesValue = "yes"
)

// SecurityProfile defines systemd security directives for service hardening.
type SecurityProfile struct {
	// Level is the security hardening level
	Level SecurityLevel

	// ProtectSystem controls access to /usr, /boot, /efi, /etc
	// Values: "yes", "full", "strict"
	ProtectSystem string

	// ProtectHome controls access to /home, /root, /run/user
	// Values: "yes", "read-only", "tmpfs"
	ProtectHome string

	// PrivateTmp creates private /tmp and /var/tmp
	PrivateTmp bool

	// PrivateDevices restricts access to /dev
	PrivateDevices bool

	// PrivateNetwork creates private network namespace (no network access)
	PrivateNetwork bool

	// NoNewPrivileges prevents privilege escalation
	NoNewPrivileges bool

	// ProtectKernelModules prevents module loading
	ProtectKernelModules bool

	// ProtectKernelTunables makes /proc and /sys read-only
	ProtectKernelTunables bool

	// ProtectControlGroups makes cgroup hierarchy read-only
	ProtectControlGroups bool

	// MemoryDenyWriteExecute prevents W^X memory mappings
	MemoryDenyWriteExecute bool

	// RestrictNamespaces prevents namespace creation
	RestrictNamespaces bool

	// RestrictRealtime prevents realtime scheduling
	RestrictRealtime bool

	// RestrictAddressFamilies limits socket address families
	// Empty slice means no restriction
	RestrictAddressFamilies []string

	// CapabilityBoundingSet limits capabilities (empty means drop all except listed)
	CapabilityBoundingSet []string

	// SystemCallFilter restricts system calls
	// Use @system-service for general services
	SystemCallFilter []string

	// SystemCallErrorNumber is the errno to return for blocked syscalls
	SystemCallErrorNumber string

	// ProtectHostname prevents hostname changes
	ProtectHostname bool

	// ProtectClock prevents clock changes
	ProtectClock bool

	// LockPersonality prevents personality changes
	LockPersonality bool

	// RestrictSUIDSGID prevents SUID/SGID file creation
	RestrictSUIDSGID bool
}

// ServiceSecurityMapping maps service names to their security levels.
var ServiceSecurityMapping = map[string]SecurityLevel{
	// Strict profile - minimal access services
	"carbonio-memcached.service": SecurityLevelStrict,
	"carbonio-opendkim.service":  SecurityLevelStrict,
	"carbonio-freshclam.service": SecurityLevelStrict,
	"carbonio-saslauthd.service": SecurityLevelStrict,
	"carbonio-stats.service":     SecurityLevelStrict,
	"carbonio-altermime.service": SecurityLevelStrict,

	// Standard profile - network services
	"carbonio-nginx.service":      SecurityLevelStandard,
	"carbonio-postfix.service":    SecurityLevelStandard,
	"carbonio-milter.service":     SecurityLevelStandard,
	"carbonio-mailthreat.service": SecurityLevelStandard,
	"carbonio-policyd.service":    SecurityLevelStandard,

	// Minimal profile - complex services
	"carbonio-configd.service":      SecurityLevelMinimal,
	"carbonio-appserver.service":    SecurityLevelMinimal,
	"carbonio-appserver-db.service": SecurityLevelMinimal,
	"carbonio-openldap.service":     SecurityLevelMinimal,
	"carbonio-antivirus.service":    SecurityLevelMinimal,
}

// GetStrictProfile returns the strictest security profile.
// Used for services with minimal system access needs.
func GetStrictProfile() *SecurityProfile {
	return &SecurityProfile{
		Level:                  SecurityLevelStrict,
		ProtectSystem:          "strict",
		ProtectHome:            yesValue,
		PrivateTmp:             true,
		PrivateDevices:         true,
		PrivateNetwork:         false, // Most services need network
		NoNewPrivileges:        true,
		ProtectKernelModules:   true,
		ProtectKernelTunables:  true,
		ProtectControlGroups:   true,
		MemoryDenyWriteExecute: true,
		RestrictNamespaces:     true,
		RestrictRealtime:       true,
		RestrictAddressFamilies: []string{
			"AF_INET",
			"AF_INET6",
			"AF_UNIX",
		},
		CapabilityBoundingSet: []string{}, // Drop all capabilities
		SystemCallFilter: []string{
			"@system-service",
		},
		SystemCallErrorNumber: "EPERM",
		ProtectHostname:       true,
		ProtectClock:          true,
		LockPersonality:       true,
		RestrictSUIDSGID:      true,
	}
}

// GetStandardProfile returns a balanced security profile for network services.
// Relaxes some restrictions for services that need network binding.
func GetStandardProfile() *SecurityProfile {
	return &SecurityProfile{
		Level:                  SecurityLevelStandard,
		ProtectSystem:          "full",
		ProtectHome:            yesValue,
		PrivateTmp:             true,
		PrivateDevices:         true,
		PrivateNetwork:         false,
		NoNewPrivileges:        true,
		ProtectKernelModules:   true,
		ProtectKernelTunables:  true,
		ProtectControlGroups:   true,
		MemoryDenyWriteExecute: false, // Some services need JIT
		RestrictNamespaces:     true,
		RestrictRealtime:       true,
		RestrictAddressFamilies: []string{
			"AF_INET",
			"AF_INET6",
			"AF_UNIX",
			"AF_NETLINK", // Needed for some network operations
		},
		CapabilityBoundingSet: []string{
			"CAP_NET_BIND_SERVICE", // Allow binding to privileged ports
		},
		SystemCallFilter: []string{
			"@system-service",
		},
		SystemCallErrorNumber: "EPERM",
		ProtectHostname:       true,
		ProtectClock:          true,
		LockPersonality:       true,
		RestrictSUIDSGID:      true,
	}
}

// GetMinimalProfile returns a minimal security profile for complex services.
// Provides basic protection while allowing elevated privileges.
func GetMinimalProfile() *SecurityProfile {
	return &SecurityProfile{
		Level:                   SecurityLevelMinimal,
		ProtectSystem:           "full",
		ProtectHome:             "read-only",
		PrivateTmp:              false, // Some services need shared tmp
		PrivateDevices:          false, // Some services need device access
		PrivateNetwork:          false,
		NoNewPrivileges:         true,
		ProtectKernelModules:    true,
		ProtectKernelTunables:   false, // Some services may read tunables
		ProtectControlGroups:    true,
		MemoryDenyWriteExecute:  false,
		RestrictNamespaces:      false, // Some services create namespaces
		RestrictRealtime:        true,
		RestrictAddressFamilies: []string{}, // No restriction
		CapabilityBoundingSet: []string{
			"CAP_NET_BIND_SERVICE",
			"CAP_SETGID",
			"CAP_SETUID",
			"CAP_CHOWN",
			"CAP_DAC_OVERRIDE",
		},
		SystemCallFilter:      []string{}, // No filter for complex services
		SystemCallErrorNumber: "",
		ProtectHostname:       true,
		ProtectClock:          true,
		LockPersonality:       true,
		RestrictSUIDSGID:      false, // Some services create SUID files
	}
}

// GetProfileForLevel returns the security profile for a given level.
func GetProfileForLevel(level SecurityLevel) *SecurityProfile {
	switch level {
	case SecurityLevelStrict:
		return GetStrictProfile()
	case SecurityLevelStandard:
		return GetStandardProfile()
	case SecurityLevelMinimal:
		return GetMinimalProfile()
	default:
		return GetMinimalProfile()
	}
}

// GetProfileForService returns the appropriate security profile for a service.
func GetProfileForService(service string) *SecurityProfile {
	level, exists := ServiceSecurityMapping[service]
	if !exists {
		// Default to minimal for unknown services
		return GetMinimalProfile()
	}

	return GetProfileForLevel(level)
}

// ToDropInContent generates systemd drop-in file content for the security profile.
func (p *SecurityProfile) ToDropInContent() string {
	var lines []string

	lines = append(lines,
		"[Service]",
		fmt.Sprintf("# Security profile: %s", p.Level),
		"# Generated by configd - do not edit manually",
		"",
	)

	// File system protection
	if p.ProtectSystem != "" {
		lines = append(lines, fmt.Sprintf("ProtectSystem=%s", p.ProtectSystem))
	}

	if p.ProtectHome != "" {
		lines = append(lines, fmt.Sprintf("ProtectHome=%s", p.ProtectHome))
	}

	// Namespace isolation
	lines = append(lines,
		fmt.Sprintf("PrivateTmp=%s", boolToYesNo(p.PrivateTmp)),
		fmt.Sprintf("PrivateDevices=%s", boolToYesNo(p.PrivateDevices)),
	)
	if p.PrivateNetwork {
		lines = append(lines, "PrivateNetwork=yes")
	}

	// Privilege restrictions
	// Kernel protection
	lines = append(lines,
		fmt.Sprintf("NoNewPrivileges=%s", boolToYesNo(p.NoNewPrivileges)),
		fmt.Sprintf("ProtectKernelModules=%s", boolToYesNo(p.ProtectKernelModules)),
	)
	if p.ProtectKernelTunables {
		lines = append(lines, "ProtectKernelTunables=yes")
	}

	lines = append(lines, fmt.Sprintf("ProtectControlGroups=%s", boolToYesNo(p.ProtectControlGroups)))

	// Memory protection
	if p.MemoryDenyWriteExecute {
		lines = append(lines, "MemoryDenyWriteExecute=yes")
	}

	// Namespace restrictions
	if p.RestrictNamespaces {
		lines = append(lines, "RestrictNamespaces=yes")
	}

	lines = append(lines, fmt.Sprintf("RestrictRealtime=%s", boolToYesNo(p.RestrictRealtime)))

	// Address family restrictions
	if len(p.RestrictAddressFamilies) > 0 {
		lines = append(lines, fmt.Sprintf("RestrictAddressFamilies=%s",
			strings.Join(p.RestrictAddressFamilies, " ")))
	}

	// Capability restrictions
	if len(p.CapabilityBoundingSet) > 0 {
		lines = append(lines, fmt.Sprintf("CapabilityBoundingSet=%s",
			strings.Join(p.CapabilityBoundingSet, " ")))
	} else if p.Level == SecurityLevelStrict {
		// For strict profile, explicitly drop all capabilities
		lines = append(lines, "CapabilityBoundingSet=")
	}

	// System call filtering
	if len(p.SystemCallFilter) > 0 {
		lines = append(lines, fmt.Sprintf("SystemCallFilter=%s",
			strings.Join(p.SystemCallFilter, " ")))
		if p.SystemCallErrorNumber != "" {
			lines = append(lines, fmt.Sprintf("SystemCallErrorNumber=%s", p.SystemCallErrorNumber))
		}
	}

	// Additional hardening
	lines = append(lines,
		fmt.Sprintf("ProtectHostname=%s", boolToYesNo(p.ProtectHostname)),
		fmt.Sprintf("ProtectClock=%s", boolToYesNo(p.ProtectClock)),
		fmt.Sprintf("LockPersonality=%s", boolToYesNo(p.LockPersonality)),
	)
	if p.RestrictSUIDSGID {
		lines = append(lines, "RestrictSUIDSGID=yes")
	}

	return strings.Join(lines, "\n") + "\n"
}

// GetDirectives returns all security directives as a map for inspection.
func (p *SecurityProfile) GetDirectives() map[string]string {
	directives := make(map[string]string)

	if p.ProtectSystem != "" {
		directives["ProtectSystem"] = p.ProtectSystem
	}

	if p.ProtectHome != "" {
		directives["ProtectHome"] = p.ProtectHome
	}

	directives["PrivateTmp"] = boolToYesNo(p.PrivateTmp)

	directives["PrivateDevices"] = boolToYesNo(p.PrivateDevices)
	if p.PrivateNetwork {
		directives["PrivateNetwork"] = yesValue
	}

	directives["NoNewPrivileges"] = boolToYesNo(p.NoNewPrivileges)

	directives["ProtectKernelModules"] = boolToYesNo(p.ProtectKernelModules)
	if p.ProtectKernelTunables {
		directives["ProtectKernelTunables"] = yesValue
	}

	directives["ProtectControlGroups"] = boolToYesNo(p.ProtectControlGroups)
	if p.MemoryDenyWriteExecute {
		directives["MemoryDenyWriteExecute"] = yesValue
	}

	if p.RestrictNamespaces {
		directives["RestrictNamespaces"] = yesValue
	}

	directives["RestrictRealtime"] = boolToYesNo(p.RestrictRealtime)
	if len(p.RestrictAddressFamilies) > 0 {
		directives["RestrictAddressFamilies"] = strings.Join(p.RestrictAddressFamilies, " ")
	}

	if len(p.CapabilityBoundingSet) > 0 {
		directives["CapabilityBoundingSet"] = strings.Join(p.CapabilityBoundingSet, " ")
	}

	if len(p.SystemCallFilter) > 0 {
		directives["SystemCallFilter"] = strings.Join(p.SystemCallFilter, " ")
	}

	if p.SystemCallErrorNumber != "" {
		directives["SystemCallErrorNumber"] = p.SystemCallErrorNumber
	}

	directives["ProtectHostname"] = boolToYesNo(p.ProtectHostname)
	directives["ProtectClock"] = boolToYesNo(p.ProtectClock)

	directives["LockPersonality"] = boolToYesNo(p.LockPersonality)
	if p.RestrictSUIDSGID {
		directives["RestrictSUIDSGID"] = yesValue
	}

	return directives
}

// GetAllServiceSecurityLevels returns all services sorted by security level.
func GetAllServiceSecurityLevels() map[SecurityLevel][]string {
	result := map[SecurityLevel][]string{
		SecurityLevelStrict:   {},
		SecurityLevelStandard: {},
		SecurityLevelMinimal:  {},
	}

	for service, level := range ServiceSecurityMapping {
		result[level] = append(result[level], service)
	}

	// Sort each list for consistent output
	for level := range result {
		sort.Strings(result[level])
	}

	return result
}

func boolToYesNo(b bool) string {
	if b {
		return yesValue
	}

	return "no"
}
