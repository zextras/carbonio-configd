// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package config

import "testing"

// The LDAP change watcher is opt-in: an absent or non-TRUE key leaves it off.
func TestLdapChangeTriggerDefaultsOff(t *testing.T) {
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}

	if cfg.LdapChangeTrigger {
		t.Error("LdapChangeTrigger must default to false")
	}

	cfg.SetVals(&LocalConfig{Data: testConfigMap(nil)})

	if cfg.LdapChangeTrigger {
		t.Error("LdapChangeTrigger must stay false when the key is absent")
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
		{"TRUE enables", map[string]string{"zmconfigd_ldap_change_trigger": "TRUE"}, true, 5, 2},
		{"true is case-insensitive", map[string]string{"zmconfigd_ldap_change_trigger": "true"}, true, 5, 2},
		{"FALSE stays off", map[string]string{"zmconfigd_ldap_change_trigger": "FALSE"}, false, 5, 2},
		{"yes is not TRUE", map[string]string{"zmconfigd_ldap_change_trigger": "yes"}, false, 5, 2},
		{"1 is not TRUE", map[string]string{"zmconfigd_ldap_change_trigger": "1"}, false, 5, 2},
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
			}, false, 5, 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := NewConfig()
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

// Setting the key to FALSE after it was TRUE turns the watcher back off at the
// next cycle (SetVals runs every cycle). Like zmconfigd_skip_idle_polls, a key
// that is merely removed keeps its previous value until configd restarts.
func TestLdapChangeTriggerFollowsLocalconfigEachCycle(t *testing.T) {
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}

	cfg.SetVals(&LocalConfig{Data: testConfigMap(map[string]string{"zmconfigd_ldap_change_trigger": "TRUE"})})

	if !cfg.LdapChangeTrigger {
		t.Fatal("LdapChangeTrigger should be on")
	}

	cfg.SetVals(&LocalConfig{Data: testConfigMap(map[string]string{"zmconfigd_ldap_change_trigger": "FALSE"})})

	if cfg.LdapChangeTrigger {
		t.Error("LdapChangeTrigger should follow the key back to off")
	}
}
