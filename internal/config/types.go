// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package config defines configuration data structures and default settings for configd.
// It provides types for local, global, server, and MTA configurations, along with
// initialization and update functions for managing configuration state.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// CacheTTL defines the default time-to-live for cache entries in seconds.
const CacheTTL = 300

// Config holds the application-wide configuration parameters.
type Config struct {
	Progname         string
	Hostname         string
	WdAll            bool
	Debug            bool
	BaseDir          string
	ConfigFile       string
	LogFile          string
	Interval         int
	WatchdogInterval int  // Watchdog check interval in seconds
	SkipIdlePolls    bool // Skip config reloads when no events received
	RestartConfig    bool
	Watchdog         bool
	WdList           []string
	LogLevel         int
	LdapIsMaster     bool
	LdapRootPassword string
	LdapMasterURL    string
	// LdapStartTLSRequired indicates whether LDAP connections must use StartTLS.
	// Defaults to true for security; set to false only for development/testing.
	LdapStartTLSRequired bool
}

// LocalConfig represents the local configuration settings.
type LocalConfig struct {
	Data map[string]string
}

// GlobalConfig represents the global configuration settings from LDAP.
type GlobalConfig struct {
	Data map[string]string
}

// MiscConfig represents miscellaneous configuration settings.
type MiscConfig struct {
	Data map[string]string
}

// ServerConfig represents server-specific configuration settings from LDAP.
type ServerConfig struct {
	Data          map[string]string
	ServiceConfig map[string]string
}

// MtaConfig represents the parsed zmconfigd.cf configuration.
type MtaConfig struct {
	Sections map[string]*MtaConfigSection
}

// MtaConfigSection represents a section within zmconfigd.cf.
type MtaConfigSection struct {
	Name         string
	Changed      bool
	Depends      map[string]bool
	Rewrites     map[string]RewriteEntry // (val, mode)
	Restarts     map[string]bool
	RequiredVars map[string]string // varName -> type (VAR, LOCAL, FILE, MAPLOCAL)
	Postconf     map[string]string
	Postconfd    map[string]string
	Ldap         map[string]string
	Proxygen     bool
	Conditionals []Conditional // if/fi blocks
}

// Conditional represents an if/fi block with its condition and nested directives.
type Conditional struct {
	Type      string // "SERVICE", "VAR"
	Key       string // service name or variable name
	Negated   bool   // true if condition is negated (!)
	Postconf  map[string]string
	Postconfd map[string]string
	Ldap      map[string]string
	Restarts  map[string]bool
	Nested    []Conditional // for nested conditionals
}

// RewriteEntry holds the value and mode for a rewrite rule.
type RewriteEntry struct {
	Value string
	Mode  string
}

// CachedData represents a cached data entry for API compatibility.
type CachedData struct {
	Data      any
	Timestamp time.Time
	Hash      string
	TTL       int
}

// NewConfig initializes a new Config with default values.
func NewConfig() (*Config, error) {
	// Determine base directory from environment or default
	baseDir := os.Getenv("ZEXTRAS_HOME")
	if baseDir == "" {
		baseDir = "/opt/zextras"
	}

	c := &Config{
		Progname:         "zmconfigd",
		WdAll:            false,
		Debug:            false,
		BaseDir:          baseDir,
		ConfigFile:       baseDir + "/conf/zmconfigd.cf",
		LogFile:          baseDir + "/log/zmconfigd.log",
		Interval:         300,  // Increased from 60s to 5min (300s) to reduce zm* process spawns
		WatchdogInterval: 120,  // Check services every 2 minutes instead of 60s
		SkipIdlePolls:    true, // Skip polling when no rewrite events received
		RestartConfig:    true, // Default matches  zmconfigd_enable_config_restarts=true
		Watchdog:         true,
		WdList:           []string{"antivirus"},
		LogLevel:         3, // Info level (3 = Info, see logger.SetLogLevel)
	}

	// Get hostname, similar to os.popen("/opt/zextras/bin/zmhostname").readline().strip()
	// This will be replaced with actual command execution later.
	hostnameBytes, err := os.ReadFile("/etc/hostname") // Placeholder for zmhostname command
	if err != nil {
		return nil, fmt.Errorf("could not determine hostname: %w", err)
	}

	c.Hostname = strings.TrimSpace(string(hostnameBytes))

	// Override hostname if zimbra_server_hostname environment variable is set
	if os.Getenv("zimbra_server_hostname") != "" {
		c.Hostname = os.Getenv("zimbra_server_hostname")
	}

	return c, nil
}

// localStr sets *dst to data[key] when the key is present.
func localStr(data map[string]string, key string, dst *string) {
	if v, ok := data[key]; ok {
		*dst = v
	}
}

// localInt sets *dst to the integer value of data[key] when present and parseable.
func localInt(data map[string]string, key string, dst *int) {
	if v, ok := data[key]; ok && v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			*dst = i
		}
	}
}

// localBoolTrue sets *dst to true when data[key] equals "TRUE" (case-insensitive).
func localBoolTrue(data map[string]string, key string, dst *bool) {
	if v, ok := data[key]; ok {
		*dst = strings.EqualFold(v, "TRUE")
	}
}

// localBoolNotFalse sets *dst to false only when data[key] equals "FALSE" (case-insensitive).
func localBoolNotFalse(data map[string]string, key string, dst *bool) {
	if v, ok := data[key]; ok {
		*dst = !strings.EqualFold(v, "FALSE")
	}
}

// SetVals updates config values based on provided local configuration.
// This function mirrors the setVals method in conf.py.
func (c *Config) SetVals(localConfig *LocalConfig) {
	data := localConfig.Data

	localBoolTrue(data, "ldap_is_master", &c.LdapIsMaster)
	localStr(data, "ldap_root_password", &c.LdapRootPassword)
	localStr(data, "ldap_master_url", &c.LdapMasterURL)

	// Default to true for security. Only disable if explicitly set to FALSE.
	c.LdapStartTLSRequired = true
	localBoolNotFalse(data, "ldap_starttls_required", &c.LdapStartTLSRequired)

	localInt(data, "zmconfigd_log_level", &c.LogLevel)
	localInt(data, "zmconfigd_interval", &c.Interval)
	localInt(data, "zmconfigd_watchdog_interval", &c.WatchdogInterval)

	localBoolNotFalse(data, "zmconfigd_skip_idle_polls", &c.SkipIdlePolls)
	localBoolTrue(data, "zmconfigd_debug", &c.Debug)
	localBoolNotFalse(data, "zmconfigd_watchdog", &c.Watchdog)
	localBoolNotFalse(data, "zmconfigd_enable_config_restarts", &c.RestartConfig)

	if v, ok := data["zmconfigd_watchdog_services"]; ok {
		c.WdList = strings.Fields(v)
	}

	// Note: Log level changes require reinitialization of the logger
	// via logger.InitStructuredLogging with the new level
}
