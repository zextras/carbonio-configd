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

// SetVals updates config values based on provided local configuration.
// This function mirrors the setVals method in conf.py.
//
//nolint:gocyclo,cyclop // Configuration mapping requires checking many fields
func (c *Config) SetVals(localConfig *LocalConfig) {
	if val, ok := localConfig.Data["ldap_is_master"]; ok {
		c.LdapIsMaster = strings.EqualFold(val, "TRUE")
	}

	if val, ok := localConfig.Data["ldap_root_password"]; ok {
		c.LdapRootPassword = val
	}

	if val, ok := localConfig.Data["ldap_master_url"]; ok {
		c.LdapMasterURL = val
	}

	// Default to true for security. Only disable if explicitly set to FALSE.
	c.LdapStartTLSRequired = true

	if val, ok := localConfig.Data["ldap_starttls_required"]; ok {
		if strings.EqualFold(val, "FALSE") {
			c.LdapStartTLSRequired = false
		}
	}

	if val, ok := localConfig.Data["zmconfigd_log_level"]; ok {
		if i, err := strconv.Atoi(val); err == nil {
			c.LogLevel = i
		}
	}

	if val, ok := localConfig.Data["zmconfigd_interval"]; ok && val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			c.Interval = i
		}
	}

	if val, ok := localConfig.Data["zmconfigd_watchdog_interval"]; ok && val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			c.WatchdogInterval = i
		}
	}

	if val, ok := localConfig.Data["zmconfigd_skip_idle_polls"]; ok {
		c.SkipIdlePolls = !strings.EqualFold(val, "FALSE")
	}

	if val, ok := localConfig.Data["zmconfigd_debug"]; ok {
		c.Debug = strings.EqualFold(val, "TRUE")
	}

	if val, ok := localConfig.Data["zmconfigd_watchdog"]; ok {
		c.Watchdog = !strings.EqualFold(val, "FALSE")
	}

	if val, ok := localConfig.Data["zmconfigd_enable_config_restarts"]; ok {
		c.RestartConfig = !strings.EqualFold(val, "FALSE")
	}

	if val, ok := localConfig.Data["zmconfigd_watchdog_services"]; ok {
		c.WdList = strings.Fields(val)
	}

	// Note: Log level changes require reinitialization of the logger
	// via logger.InitStructuredLogging with the new level
}
