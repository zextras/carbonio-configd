// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package config

import "testing"

// The LDAP change watcher is on by default: an absent key leaves it on, and only
// false, no, 0 and off are the opt-out.
func TestLdapChangeTriggerDefaultsOn(t *testing.T) {
	cfg, err := NewConfigWithHostname("")
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}

	if !cfg.LdapChangeTrigger {
		t.Error("LdapChangeTrigger must default to true")
	}

	cfg.SetVals(&LocalConfig{Data: testConfigMap(nil)})

	if !cfg.LdapChangeTrigger {
		t.Error("LdapChangeTrigger must stay true when the key is absent")
	}

	if cfg.LdapChangePollSeconds != 5 || cfg.LdapChangeDebounceSeconds != 2 {
		t.Errorf("defaults = poll %d debounce %d, want 5 and 2",
			cfg.LdapChangePollSeconds, cfg.LdapChangeDebounceSeconds)
	}
}

func TestLdapChangeTriggerKeys(t *testing.T) {
	tests := []struct {
		name         string
		data         map[string]string
		wantTrigger  bool
		wantPoll     int
		wantDebounce int
	}{
		{"absent is on", map[string]string{}, true, 5, 2},
		{"TRUE is on", map[string]string{"zmconfigd_ldap_change_trigger": "TRUE"}, true, 5, 2},
		{"FALSE opts out", map[string]string{"zmconfigd_ldap_change_trigger": "FALSE"}, false, 5, 2},
		{"false is case-insensitive", map[string]string{"zmconfigd_ldap_change_trigger": "false"}, false, 5, 2},
		{"no opts out", map[string]string{"zmconfigd_ldap_change_trigger": "no"}, false, 5, 2},
		{"No opts out", map[string]string{"zmconfigd_ldap_change_trigger": "No"}, false, 5, 2},
		{"0 opts out", map[string]string{"zmconfigd_ldap_change_trigger": "0"}, false, 5, 2},
		{"off opts out", map[string]string{"zmconfigd_ldap_change_trigger": "off"}, false, 5, 2},
		{"OFF opts out", map[string]string{"zmconfigd_ldap_change_trigger": "OFF"}, false, 5, 2},
		{"blanks are trimmed", map[string]string{"zmconfigd_ldap_change_trigger": "  Off \t"}, false, 5, 2},
		{"empty stays on", map[string]string{"zmconfigd_ldap_change_trigger": ""}, true, 5, 2},
		{"yes is on", map[string]string{"zmconfigd_ldap_change_trigger": "yes"}, true, 5, 2},
		{"1 is on", map[string]string{"zmconfigd_ldap_change_trigger": "1"}, true, 5, 2},
		{"junk is on", map[string]string{"zmconfigd_ldap_change_trigger": "maybe"}, true, 5, 2},
		{
			"timings", map[string]string{
				"zmconfigd_ldap_change_trigger":          "TRUE",
				"zmconfigd_ldap_change_poll_seconds":     "10",
				"zmconfigd_ldap_change_debounce_seconds": "0",
			}, true, 10, 0,
		},
		{
			"invalid timings keep the defaults", map[string]string{
				"zmconfigd_ldap_change_poll_seconds":     "soon",
				"zmconfigd_ldap_change_debounce_seconds": "later",
			}, true, 5, 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := NewConfigWithHostname("")
			if err != nil {
				t.Fatalf("NewConfig failed: %v", err)
			}

			cfg.SetVals(&LocalConfig{Data: testConfigMap(tt.data)})

			if cfg.LdapChangeTrigger != tt.wantTrigger {
				t.Errorf("LdapChangeTrigger = %v, want %v", cfg.LdapChangeTrigger, tt.wantTrigger)
			}

			if cfg.LdapChangePollSeconds != tt.wantPoll {
				t.Errorf("LdapChangePollSeconds = %d, want %d", cfg.LdapChangePollSeconds, tt.wantPoll)
			}

			if cfg.LdapChangeDebounceSeconds != tt.wantDebounce {
				t.Errorf("LdapChangeDebounceSeconds = %d, want %d",
					cfg.LdapChangeDebounceSeconds, tt.wantDebounce)
			}
		})
	}
}

// Setting the key to FALSE turns the watcher off at the next cycle (SetVals
// runs every cycle), and TRUE turns it back on. Like zmconfigd_skip_idle_polls,
// a key that is merely removed keeps its previous value until configd restarts.
func TestLdapChangeTriggerFollowsLocalconfigEachCycle(t *testing.T) {
	cfg, err := NewConfigWithHostname("")
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}

	cfg.SetVals(&LocalConfig{Data: testConfigMap(map[string]string{"zmconfigd_ldap_change_trigger": "FALSE"})})

	if cfg.LdapChangeTrigger {
		t.Fatal("LdapChangeTrigger should follow FALSE to off")
	}

	cfg.SetVals(&LocalConfig{Data: testConfigMap(map[string]string{"zmconfigd_ldap_change_trigger": "TRUE"})})

	if !cfg.LdapChangeTrigger {
		t.Error("LdapChangeTrigger should follow the key back to on")
	}
}
