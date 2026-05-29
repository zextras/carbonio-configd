// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/testutil"
)

// newComparatorWithLookup builds a Comparator backed by a fresh state, the given
// lookup data, and a mock service manager. These tests exercise the Comparator
// directly (not via ConfigManager) so they do not incur retry delays and remain
// -short friendly.
func newComparatorWithLookup(
	data map[string]map[string]string,
	sm *testutil.MockServiceManager,
) (*Comparator, *state.State) {
	st := state.NewState()

	cl := testutil.NewMockConfigLookupWithData(data)
	if sm == nil {
		sm = &testutil.MockServiceManager{}
	}

	return NewComparator(cl, st, sm), st
}

// TestNewComparator verifies the constructor wires up its dependencies.
func TestNewComparator(t *testing.T) {
	c, st := newComparatorWithLookup(nil, nil)
	if c == nil {
		t.Fatal("NewComparator returned nil")
	}
	if c.state != st {
		t.Error("NewComparator did not store the provided state")
	}
	if c.lookup == nil {
		t.Error("NewComparator did not store the provided lookup")
	}
	if c.serviceMgr == nil {
		t.Error("NewComparator did not store the provided service manager")
	}
}

// TestComparator_CheckConditional covers true/false/negation/lookup-error paths.
func TestComparator_CheckConditional(t *testing.T) {
	tests := []struct {
		name    string
		data    map[string]map[string]string
		cfgType string
		key     string
		want    bool
	}{
		{
			name:    "true value",
			data:    map[string]map[string]string{"SERVICE": {"imapd": "TRUE"}},
			cfgType: "SERVICE",
			key:     "imapd",
			want:    true,
		},
		{
			name:    "false value",
			data:    map[string]map[string]string{"SERVICE": {"imapd": "FALSE"}},
			cfgType: "SERVICE",
			key:     "imapd",
			want:    false,
		},
		{
			name:    "negated true value yields false",
			data:    map[string]map[string]string{"SERVICE": {"imapd": "TRUE"}},
			cfgType: "SERVICE",
			key:     "!imapd",
			want:    false,
		},
		{
			name:    "negated false value yields true",
			data:    map[string]map[string]string{"SERVICE": {"imapd": "FALSE"}},
			cfgType: "SERVICE",
			key:     "!imapd",
			want:    true,
		},
		{
			name:    "lookup error treated as false",
			data:    map[string]map[string]string{},
			cfgType: "SERVICE",
			key:     "missing",
			want:    false,
		},
		{
			name:    "negated lookup error yields true",
			data:    map[string]map[string]string{},
			cfgType: "SERVICE",
			key:     "!missing",
			want:    true,
		},
		{
			name:    "zero value treated as false",
			data:    map[string]map[string]string{"VAR": {"counter": "0"}},
			cfgType: "VAR",
			key:     "counter",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newComparatorWithLookup(tt.data, nil)
			got, err := c.CheckConditional(context.Background(), tt.cfgType, tt.key)
			if err != nil {
				t.Fatalf("CheckConditional() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("CheckConditional() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestComparator_CompareKeys_AllServicesDisabled verifies the guard error fires
// when more than one tracked service is disabled.
func TestComparator_CompareKeys_AllServicesDisabled(t *testing.T) {
	c, st := newComparatorWithLookup(map[string]map[string]string{}, nil)

	// Two tracked services, neither present in lookup => all disabled.
	st.CurrentActions.Services["imapd"] = "running"
	st.CurrentActions.Services["mta"] = "running"

	err := c.CompareKeys(context.Background())
	if err == nil {
		t.Fatal("CompareKeys() expected error when all services disabled")
	}
	if err.Error() != "all services detected disabled" {
		t.Errorf("CompareKeys() error = %v, want 'all services detected disabled'", err)
	}
}

// TestComparator_CompareKeys_FirstRunTracksService verifies first-run service
// initialization records a running status from ServiceConfig.
func TestComparator_CompareKeys_FirstRunTracksService(t *testing.T) {
	c, st := newComparatorWithLookup(
		map[string]map[string]string{"SERVICE": {"imapd": "TRUE"}},
		&testutil.MockServiceManager{
			HasCommandFn: func(string) bool { return true },
		},
	)
	st.FirstRun = true
	st.ServerConfig.ServiceConfig.Set("imapd", "TRUE")

	if err := c.CompareKeys(context.Background()); err != nil {
		t.Fatalf("CompareKeys() unexpected error: %v", err)
	}

	if status, ok := st.CurrentActions.Services["imapd"]; !ok {
		t.Error("expected imapd tracked in CurrentActions.Services")
	} else if status != "running" {
		t.Errorf("expected imapd status 'running', got %q", status)
	}
}

// TestComparator_CompareKeys_FirstRunStoppedService verifies a service disabled
// in ServiceConfig is recorded as stopped on first run.
func TestComparator_CompareKeys_FirstRunStoppedService(t *testing.T) {
	c, st := newComparatorWithLookup(
		map[string]map[string]string{"SERVICE": {"imapd": "TRUE"}},
		nil,
	)
	st.FirstRun = true
	st.ServerConfig.ServiceConfig.Set("imapd", "FALSE")

	if err := c.CompareKeys(context.Background()); err != nil {
		t.Fatalf("CompareKeys() unexpected error: %v", err)
	}

	if status := st.CurrentActions.Services["imapd"]; status != "stopped" {
		t.Errorf("expected imapd status 'stopped', got %q", status)
	}
}

// TestComparator_CompareKeys_ServiceDisabledQueuesStop verifies a previously
// running service that is now disabled gets queued for stop (restart action 0).
func TestComparator_CompareKeys_ServiceDisabledQueuesStop(t *testing.T) {
	c, st := newComparatorWithLookup(map[string]map[string]string{}, nil)
	st.FirstRun = false
	st.CurrentActions.Services["imapd"] = "running"

	if err := c.CompareKeys(context.Background()); err != nil {
		t.Fatalf("CompareKeys() unexpected error: %v", err)
	}

	if action, ok := st.CurrentActions.Restarts["imapd"]; !ok {
		t.Error("expected imapd queued in restarts")
	} else if action != 0 {
		t.Errorf("expected restart action 0 (stop), got %d", action)
	}
}

// TestComparator_CompareKeys_ServiceEnabledQueuesStart verifies a newly enabled
// service (present in ServiceConfig but not CurrentActions) is queued for start.
func TestComparator_CompareKeys_ServiceEnabledQueuesStart(t *testing.T) {
	c, st := newComparatorWithLookup(
		map[string]map[string]string{"SERVICE": {"imapd": "TRUE"}},
		&testutil.MockServiceManager{
			HasCommandFn: func(string) bool { return true },
		},
	)
	st.FirstRun = false
	st.ServerConfig.ServiceConfig.Set("imapd", "TRUE")

	if err := c.CompareKeys(context.Background()); err != nil {
		t.Fatalf("CompareKeys() unexpected error: %v", err)
	}

	if action, ok := st.CurrentActions.Restarts["imapd"]; !ok {
		t.Error("expected imapd queued in restarts")
	} else if action != 1 {
		t.Errorf("expected restart action 1 (start), got %d", action)
	}
}

// TestComparator_CompareKeys_KeyChanged verifies a changed required var marks the
// section changed and records the key.
func TestComparator_CompareKeys_KeyChanged(t *testing.T) {
	c, st := newComparatorWithLookup(
		map[string]map[string]string{"VAR": {"zimbraImapBindPort": "7143"}},
		nil,
	)
	st.FirstRun = false
	st.MtaConfig.Sections["imap"] = &config.MtaConfigSection{
		Name:         "imap",
		RequiredVars: map[string]string{"zimbraImapBindPort": "VAR"},
		Changed:      false,
	}
	st.LastVal(context.Background(), "imap", "VAR", "zimbraImapBindPort", "143")

	if err := c.CompareKeys(context.Background()); err != nil {
		t.Fatalf("CompareKeys() unexpected error: %v", err)
	}

	if !st.MtaConfig.Sections["imap"].Changed {
		t.Error("expected imap section marked changed")
	}

	found := false
	for _, k := range st.ChangedKeys["imap"] {
		if k == "zimbraImapBindPort" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected zimbraImapBindPort recorded in changed keys")
	}
}

// TestComparator_CompareKeys_KeyBecameUndefined verifies a key with a previous
// value but no current value marks the section changed (deletion path).
func TestComparator_CompareKeys_KeyBecameUndefined(t *testing.T) {
	c, st := newComparatorWithLookup(map[string]map[string]string{}, nil)
	st.FirstRun = false
	st.MtaConfig.Sections["imap"] = &config.MtaConfigSection{
		Name:         "imap",
		RequiredVars: map[string]string{"zimbraImapBindPort": "VAR"},
		Changed:      false,
	}
	st.LastVal(context.Background(), "imap", "VAR", "zimbraImapBindPort", "143")

	if err := c.CompareKeys(context.Background()); err != nil {
		t.Fatalf("CompareKeys() unexpected error: %v", err)
	}

	if !st.MtaConfig.Sections["imap"].Changed {
		t.Error("expected imap section marked changed when value becomes undefined")
	}
}

// TestComparator_CompareKeys_ForcedConfigSkips verifies forced-config mode skips
// non-forced sections.
func TestComparator_CompareKeys_ForcedConfigSkips(t *testing.T) {
	c, st := newComparatorWithLookup(
		map[string]map[string]string{
			"VAR": {"zimbraImapBindPort": "143", "zimbraSmtpPort": "25"},
		},
		nil,
	)
	st.FirstRun = false
	st.MtaConfig.Sections["imap"] = &config.MtaConfigSection{
		Name:         "imap",
		RequiredVars: map[string]string{"zimbraImapBindPort": "VAR"},
	}
	st.MtaConfig.Sections["smtp"] = &config.MtaConfigSection{
		Name:         "smtp",
		RequiredVars: map[string]string{"zimbraSmtpPort": "VAR"},
	}
	st.ForcedConfig["imap"] = "1"
	st.LastVal(context.Background(), "imap", "VAR", "zimbraImapBindPort", "7143")
	st.LastVal(context.Background(), "smtp", "VAR", "zimbraSmtpPort", "587")

	if err := c.CompareKeys(context.Background()); err != nil {
		t.Fatalf("CompareKeys() unexpected error: %v", err)
	}

	if !st.MtaConfig.Sections["imap"].Changed {
		t.Error("expected forced imap section to be processed and changed")
	}
	if st.MtaConfig.Sections["smtp"].Changed {
		t.Error("expected non-forced smtp section to be skipped (not changed)")
	}
}
