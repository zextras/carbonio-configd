// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"os"
	"testing"
)

func TestNewConfig(t *testing.T) {
	// Save original environment
	origZextrasHome := os.Getenv("ZEXTRAS_HOME")
	origZimbraHostname := os.Getenv("zimbra_server_hostname")
	defer func() {
		if origZextrasHome != "" {
			os.Setenv("ZEXTRAS_HOME", origZextrasHome)
		} else {
			os.Unsetenv("ZEXTRAS_HOME")
		}
		if origZimbraHostname != "" {
			os.Setenv("zimbra_server_hostname", origZimbraHostname)
		} else {
			os.Unsetenv("zimbra_server_hostname")
		}
	}()

	tests := []struct {
		name             string
		zextrasHome      string
		zimbraHostname   string
		expectedBaseDir  string
		expectedProgname string
		expectedInterval int
		expectedWatchdog bool
		expectedSkipIdle bool
	}{
		{
			name:             "default configuration",
			zextrasHome:      "",
			zimbraHostname:   "",
			expectedBaseDir:  "/opt/zextras",
			expectedProgname: "zmconfigd",
			expectedInterval: 300,
			expectedWatchdog: true,
			expectedSkipIdle: true,
		},
		{
			name:             "custom ZEXTRAS_HOME",
			zextrasHome:      "/custom/path",
			zimbraHostname:   "",
			expectedBaseDir:  "/custom/path",
			expectedProgname: "zmconfigd",
			expectedInterval: 300,
			expectedWatchdog: true,
			expectedSkipIdle: true,
		},
		{
			name:             "custom hostname override",
			zextrasHome:      "/opt/zextras",
			zimbraHostname:   "custom.example.com",
			expectedBaseDir:  "/opt/zextras",
			expectedProgname: "zmconfigd",
			expectedInterval: 300,
			expectedWatchdog: true,
			expectedSkipIdle: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment
			if tt.zextrasHome != "" {
				os.Setenv("ZEXTRAS_HOME", tt.zextrasHome)
			} else {
				os.Unsetenv("ZEXTRAS_HOME")
			}

			if tt.zimbraHostname != "" {
				os.Setenv("zimbra_server_hostname", tt.zimbraHostname)
			} else {
				os.Unsetenv("zimbra_server_hostname")
			}

			cfg, err := NewConfig()
			if err != nil {
				t.Fatalf("NewConfig failed: %v", err)
			}

			if cfg.BaseDir != tt.expectedBaseDir {
				t.Errorf("BaseDir = %v, want %v", cfg.BaseDir, tt.expectedBaseDir)
			}

			if cfg.Progname != tt.expectedProgname {
				t.Errorf("Progname = %v, want %v", cfg.Progname, tt.expectedProgname)
			}

			if cfg.Interval != tt.expectedInterval {
				t.Errorf("Interval = %v, want %v", cfg.Interval, tt.expectedInterval)
			}

			if cfg.Watchdog != tt.expectedWatchdog {
				t.Errorf("Watchdog = %v, want %v", cfg.Watchdog, tt.expectedWatchdog)
			}

			if cfg.SkipIdlePolls != tt.expectedSkipIdle {
				t.Errorf("SkipIdlePolls = %v, want %v", cfg.SkipIdlePolls, tt.expectedSkipIdle)
			}

			if cfg.WatchdogInterval != 120 {
				t.Errorf("WatchdogInterval = %v, want 120", cfg.WatchdogInterval)
			}

			if cfg.LogLevel != 3 {
				t.Errorf("LogLevel = %v, want %v", cfg.LogLevel, 3)
			}

			expectedConfigFile := tt.expectedBaseDir + "/conf/zmconfigd.cf"
			if cfg.ConfigFile != expectedConfigFile {
				t.Errorf("ConfigFile = %v, want %v", cfg.ConfigFile, expectedConfigFile)
			}

			expectedLogFile := tt.expectedBaseDir + "/log/zmconfigd.log"
			if cfg.LogFile != expectedLogFile {
				t.Errorf("LogFile = %v, want %v", cfg.LogFile, expectedLogFile)
			}

			if len(cfg.WdList) == 0 {
				t.Error("WdList should not be empty")
			}

			if tt.zimbraHostname != "" && cfg.Hostname != tt.zimbraHostname {
				t.Errorf("Hostname = %v, want %v", cfg.Hostname, tt.zimbraHostname)
			}
		})
	}
}

func TestConfig_SetVals(t *testing.T) {
	tests := []struct {
		name        string
		localConfig *LocalConfig
		validate    func(*testing.T, *Config)
	}{
		{
			name: "set LDAP master configuration",
			localConfig: &LocalConfig{
				Data: map[string]string{
					"ldap_is_master":         "TRUE",
					"ldap_root_password":     "secret123",
					"ldap_master_url":        "ldap://master.example.com",
					"ldap_starttls_required": "TRUE",
				},
			},
			validate: func(t *testing.T, cfg *Config) {
				if !cfg.LdapIsMaster {
					t.Error("LdapIsMaster should be true")
				}
				if cfg.LdapRootPassword != "secret123" {
					t.Errorf("LdapRootPassword = %v, want secret123", cfg.LdapRootPassword)
				}
				if cfg.LdapMasterURL != "ldap://master.example.com" {
					t.Errorf("LdapMasterURL = %v, want ldap://master.example.com", cfg.LdapMasterURL)
				}
				if !cfg.LdapStartTLSRequired {
					t.Error("LdapStartTLSRequired should be true")
				}
			},
		},
		{
			name: "set configd intervals",
			localConfig: &LocalConfig{
				Data: map[string]string{
					"zmconfigd_interval":          "600",
					"zmconfigd_watchdog_interval": "180",
				},
			},
			validate: func(t *testing.T, cfg *Config) {
				if cfg.Interval != 600 {
					t.Errorf("Interval = %v, want 600", cfg.Interval)
				}
				if cfg.WatchdogInterval != 180 {
					t.Errorf("WatchdogInterval = %v, want 180", cfg.WatchdogInterval)
				}
			},
		},
		{
			name: "set boolean flags",
			localConfig: &LocalConfig{
				Data: map[string]string{
					"zmconfigd_debug":                  "TRUE",
					"zmconfigd_watchdog":               "FALSE",
					"zmconfigd_skip_idle_polls":        "FALSE",
					"zmconfigd_enable_config_restarts": "TRUE",
				},
			},
			validate: func(t *testing.T, cfg *Config) {
				if !cfg.Debug {
					t.Error("Debug should be true")
				}
				if cfg.Watchdog {
					t.Error("Watchdog should be false")
				}
				if cfg.SkipIdlePolls {
					t.Error("SkipIdlePolls should be false")
				}
				if !cfg.RestartConfig {
					t.Error("RestartConfig should be true")
				}
			},
		},
		{
			name: "set watchdog services",
			localConfig: &LocalConfig{
				Data: map[string]string{
					"zmconfigd_watchdog_services": "antivirus antispam ldap",
				},
			},
			validate: func(t *testing.T, cfg *Config) {
				if len(cfg.WdList) != 3 {
					t.Errorf("WdList length = %v, want 3", len(cfg.WdList))
				}
				expected := []string{"antivirus", "antispam", "ldap"}
				for i, svc := range expected {
					if i >= len(cfg.WdList) || cfg.WdList[i] != svc {
						t.Errorf("WdList[%d] = %v, want %v", i, cfg.WdList[i], svc)
					}
				}
			},
		},
		{
			name: "case insensitive boolean parsing",
			localConfig: &LocalConfig{
				Data: map[string]string{
					"ldap_is_master":            "true",
					"ldap_starttls_required":    "false",
					"zmconfigd_watchdog":        "False",
					"zmconfigd_skip_idle_polls": "FALSE",
				},
			},
			validate: func(t *testing.T, cfg *Config) {
				if !cfg.LdapIsMaster {
					t.Error("LdapIsMaster should be true (case insensitive)")
				}
				if cfg.LdapStartTLSRequired {
					t.Error("LdapStartTLSRequired should be false")
				}
				if cfg.Watchdog {
					t.Error("Watchdog should be false")
				}
				if cfg.SkipIdlePolls {
					t.Error("SkipIdlePolls should be false")
				}
			},
		},
		{
			name: "invalid interval values ignored",
			localConfig: &LocalConfig{
				Data: map[string]string{
					"zmconfigd_interval":          "invalid",
					"zmconfigd_watchdog_interval": "not-a-number",
					"zmconfigd_log_level":         "abc",
				},
			},
			validate: func(t *testing.T, cfg *Config) {
				// Should keep default values when parsing fails
				if cfg.Interval != 300 {
					t.Errorf("Interval = %v, want 300 (default)", cfg.Interval)
				}
				if cfg.WatchdogInterval != 120 {
					t.Errorf("WatchdogInterval = %v, want 120 (default)", cfg.WatchdogInterval)
				}
				if cfg.LogLevel != 3 {
					t.Errorf("LogLevel = %v, want %v (default)", cfg.LogLevel, 3)
				}
			},
		},
		{
			name: "empty string values",
			localConfig: &LocalConfig{
				Data: map[string]string{
					"zmconfigd_interval":          "",
					"zmconfigd_watchdog_interval": "",
				},
			},
			validate: func(t *testing.T, cfg *Config) {
				// Empty strings should not override defaults
				if cfg.Interval != 300 {
					t.Errorf("Interval = %v, want 300 (default)", cfg.Interval)
				}
				if cfg.WatchdogInterval != 120 {
					t.Errorf("WatchdogInterval = %v, want 120 (default)", cfg.WatchdogInterval)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a minimal config for testing
			cfg := &Config{
				Interval:             300,
				WatchdogInterval:     120,
				LogLevel:             3, // Info level
				Watchdog:             true,
				SkipIdlePolls:        true,
				RestartConfig:        false,
				Debug:                false,
				LdapIsMaster:         false,
				LdapStartTLSRequired: true, // default is true in SetVals
			}

			cfg.SetVals(tt.localConfig)
			tt.validate(t, cfg)
		})
	}
}

func TestCacheTTLConstant(t *testing.T) {
	if CacheTTL != 300 {
		t.Errorf("CacheTTL = %v, want 300", CacheTTL)
	}
}

func TestMtaConfigSection(t *testing.T) {
	section := &MtaConfigSection{
		Name:         "test-section",
		Changed:      false,
		Depends:      make(map[string]bool),
		Rewrites:     make(map[string]RewriteEntry),
		Restarts:     make(map[string]bool),
		RequiredVars: make(map[string]string),
		Postconf:     make(map[string]string),
		Postconfd:    make(map[string]string),
		Ldap:         make(map[string]string),
		Proxygen:     false,
		Conditionals: []Conditional{},
	}

	if section.Name != "test-section" {
		t.Errorf("Name = %v, want test-section", section.Name)
	}

	if section.Changed {
		t.Error("Changed should be false")
	}

	if section.Proxygen {
		t.Error("Proxygen should be false")
	}

	// Test adding data
	section.Depends["service1"] = true
	section.Rewrites["file1"] = RewriteEntry{Value: "path/to/file", Mode: "0644"}
	section.Restarts["service2"] = true
	section.RequiredVars["var1"] = "VAR"
	section.Postconf["key1"] = "value1"

	if !section.Depends["service1"] {
		t.Error("Depends should contain service1")
	}

	if entry, ok := section.Rewrites["file1"]; !ok || entry.Value != "path/to/file" {
		t.Errorf("Rewrites entry = %+v, want {Value:path/to/file Mode:0644}", entry)
	}
}

func TestConditional(t *testing.T) {
	cond := Conditional{
		Type:      "SERVICE",
		Key:       "ldap",
		Negated:   false,
		Postconf:  make(map[string]string),
		Postconfd: make(map[string]string),
		Ldap:      make(map[string]string),
		Restarts:  make(map[string]bool),
		Nested:    []Conditional{},
	}

	if cond.Type != "SERVICE" {
		t.Errorf("Type = %v, want SERVICE", cond.Type)
	}

	if cond.Key != "ldap" {
		t.Errorf("Key = %v, want ldap", cond.Key)
	}

	if cond.Negated {
		t.Error("Negated should be false")
	}

	// Test nested conditional
	nested := Conditional{
		Type:    "VAR",
		Key:     "ssl_enabled",
		Negated: true,
	}
	cond.Nested = append(cond.Nested, nested)

	if len(cond.Nested) != 1 {
		t.Errorf("Nested length = %v, want 1", len(cond.Nested))
	}

	if !cond.Nested[0].Negated {
		t.Error("Nested conditional should be negated")
	}
}

func TestRewriteEntry(t *testing.T) {
	entry := RewriteEntry{
		Value: "/path/to/config",
		Mode:  "0644",
	}

	if entry.Value != "/path/to/config" {
		t.Errorf("Value = %v, want /path/to/config", entry.Value)
	}

	if entry.Mode != "0644" {
		t.Errorf("Mode = %v, want 0644", entry.Mode)
	}
}

func TestLocalConfig(t *testing.T) {
	lc := &LocalConfig{
		Data: make(map[string]string),
	}

	lc.Data["key1"] = "value1"
	lc.Data["key2"] = "value2"

	if lc.Data["key1"] != "value1" {
		t.Errorf("Data[key1] = %v, want value1", lc.Data["key1"])
	}

	if len(lc.Data) != 2 {
		t.Errorf("Data length = %v, want 2", len(lc.Data))
	}
}

func TestGlobalConfig(t *testing.T) {
	gc := &GlobalConfig{
		Data: make(map[string]string),
	}

	gc.Data["global_key"] = "global_value"

	if gc.Data["global_key"] != "global_value" {
		t.Errorf("Data[global_key] = %v, want global_value", gc.Data["global_key"])
	}
}

func TestServerConfig(t *testing.T) {
	sc := &ServerConfig{
		Data:          make(map[string]string),
		ServiceConfig: make(map[string]string),
	}

	sc.Data["server_key"] = "server_value"
	sc.ServiceConfig["service1"] = "enabled"

	if sc.Data["server_key"] != "server_value" {
		t.Errorf("Data[server_key] = %v, want server_value", sc.Data["server_key"])
	}

	if sc.ServiceConfig["service1"] != "enabled" {
		t.Errorf("ServiceConfig[service1] = %v, want enabled", sc.ServiceConfig["service1"])
	}
}

func TestMtaConfig(t *testing.T) {
	mc := &MtaConfig{
		Sections: make(map[string]*MtaConfigSection),
	}

	section := &MtaConfigSection{
		Name:    "main",
		Changed: false,
	}

	mc.Sections["main"] = section

	if mc.Sections["main"].Name != "main" {
		t.Errorf("Sections[main].Name = %v, want main", mc.Sections["main"].Name)
	}
}
