// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"testing"
)

func TestJoinWithCommas_Multiple(t *testing.T) {
	got := joinWithCommas([]string{"mta", "proxy", "ldap"})
	if got != "mta, proxy, ldap" {
		t.Errorf("expected 'mta, proxy, ldap', got %q", got)
	}
}

func TestJoinWithCommas_Single(t *testing.T) {
	got := joinWithCommas([]string{"mta"})
	if got != "mta" {
		t.Errorf("expected 'mta', got %q", got)
	}
}

func TestJoinWithCommas_Empty(t *testing.T) {
	got := joinWithCommas([]string{})
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestRewriteConfigs_Contents(t *testing.T) {
	expected := []string{"sasl", "webxml", "mailbox", "service", "zextras", "zextrasAdmin", "zimlet"}
	if len(rewriteConfigs) != len(expected) {
		t.Fatalf("expected %d rewrite configs, got %d", len(expected), len(rewriteConfigs))
	}
	for i, v := range expected {
		if rewriteConfigs[i] != v {
			t.Errorf("rewriteConfigs[%d]: expected %q, got %q", i, v, rewriteConfigs[i])
		}
	}
}

func TestTLSCmd_Structure(t *testing.T) {
	cmd := &TLSCmd{
		Mode:  "https",
		Force: true,
		Host:  "mail.example.com",
	}
	if cmd.Mode != "https" {
		t.Errorf("expected Mode 'https', got %q", cmd.Mode)
	}
	if !cmd.Force {
		t.Error("expected Force true")
	}
	if cmd.Host != "mail.example.com" {
		t.Errorf("expected Host 'mail.example.com', got %q", cmd.Host)
	}
}
