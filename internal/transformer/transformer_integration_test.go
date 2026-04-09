// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build integration

package transformer

import "context"
import (
	"bufio"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/state"
)

// transformMultiLine is a helper function that transforms multi-line input
// by processing each line through the transformer
func transformMultiLine(transformer *Transformer, input string) string {
	var result strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(input))

	for scanner.Scan() {
		line := scanner.Text()
		transformed := transformer.Transform(context.Background(), line)
		result.WriteString(transformed)
		// Add newline if not already present (for lines without special chars)
		if !strings.HasSuffix(transformed, "\n") {
			result.WriteString("\n")
		}
	}

	return result.String()
}

// TestPostfixSMTPDRestrictionsIntegration tests transformation of a realistic
// Postfix smtpd_recipient_restrictions configuration file.
// This test is based on conf/zmconfigd/smtpd_recipient_restrictions.cf
func TestPostfixSMTPDRestrictionsIntegration(t *testing.T) {
	input := `%%contains VAR:zimbraMtaRestriction check_client_access lmdb:/opt/zextras/conf/postfix_blacklist%%
%%contains VAR:zimbraServiceEnabled cbpolicyd^ check_policy_service inet:localhost:10031%%
reject_non_fqdn_recipient
permit_sasl_authenticated
permit_mynetworks
reject_unlisted_recipient
%%exact VAR:zimbraMtaRestriction reject_invalid_helo_hostname%%
%%exact VAR:zimbraMtaRestriction reject_non_fqdn_helo_hostname%%
%%exact VAR:zimbraMtaRestriction reject_non_fqdn_sender%%
%%exact VAR:zimbraMtaRestriction reject_unknown_client_hostname%%
%%exact VAR:zimbraMtaRestriction reject_unknown_reverse_client_hostname%%
%%exact VAR:zimbraMtaRestriction reject_unknown_helo_hostname%%
%%exact VAR:zimbraMtaRestriction reject_unknown_sender_domain%%
%%exact VAR:zimbraMtaRestriction reject_unverified_recipient%%
%%contains VAR:zimbraMtaRestriction check_recipient_access lmdb:/opt/zextras/conf/postfix_recipient_access%%
%%contains VAR:zimbraMtaRestriction check_client_access lmdb:/opt/zextras/conf/postfix_rbl_override%%
%%contains VAR:zimbraMtaRestriction check_reverse_client_hostname_access pcre:/opt/zextras/conf/fqrdns.pcre%%
%%explode reject_rbl_client VAR:zimbraMtaRestrictionRBLs%%
%%explode reject_rhsbl_client VAR:zimbraMtaRestrictionRHSBLCs%%
%%explode reject_rhsbl_reverse_client VAR:zimbraMtaRestrictionRHSBLRCs%%
%%explode reject_rhsbl_sender VAR:zimbraMtaRestrictionRHSBLSs%%
%%contains VAR:zimbraMtaRestriction check_policy_service unix:private/policy%%
%%contains VAR:zimbraMtaRestriction check_recipient_access ldap:/opt/zextras/conf/ldap-splitdomain.cf%%
%%exact VAR:zimbraMtaRestriction reject%%
permit
`

	// Setup mock config with typical values
	mock := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"zimbraMtaRestriction": "reject_invalid_helo_hostname reject_non_fqdn_helo_hostname " +
					"reject_unknown_sender_domain check_client_access lmdb:/opt/zextras/conf/postfix_blacklist " +
					"check_recipient_access lmdb:/opt/zextras/conf/postfix_recipient_access reject",
				"zimbraServiceEnabled":         "cbpolicyd antivirus antispam",
				"zimbraMtaRestrictionRBLs":     "bl.spamcop.net zen.spamhaus.org",
				"zimbraMtaRestrictionRHSBLCs":  "rhsbl.example.com",
				"zimbraMtaRestrictionRHSBLRCs": "",
				"zimbraMtaRestrictionRHSBLSs":  "rhsbl.sender.example.com",
			},
		},
	}

	st := &state.State{}
	transformer := NewTransformer(mock, st)

	output := transformMultiLine(transformer, input)

	// Verify expected lines are present
	expectedLines := []string{
		"check_client_access lmdb:/opt/zextras/conf/postfix_blacklist",
		"check_policy_service inet:localhost:10031",
		"reject_non_fqdn_recipient",
		"permit_sasl_authenticated",
		"permit_mynetworks",
		"reject_unlisted_recipient",
		"reject_invalid_helo_hostname",
		"reject_non_fqdn_helo_hostname",
		"reject_unknown_sender_domain",
		"check_recipient_access lmdb:/opt/zextras/conf/postfix_recipient_access",
		"reject_rbl_client bl.spamcop.net",
		"reject_rbl_client zen.spamhaus.org",
		"reject_rhsbl_client rhsbl.example.com",
		"reject_rhsbl_sender rhsbl.sender.example.com",
		"reject",
		"permit",
	}

	for _, expected := range expectedLines {
		if !strings.Contains(output, expected) {
			t.Errorf("Expected line not found in output: %q", expected)
		}
	}

	// Verify lines that should NOT be present (no match in exact/contains)
	notExpectedLines := []string{
		"reject_non_fqdn_sender",                                                  // not in zimbraMtaRestriction
		"reject_unknown_client_hostname",                                          // not in zimbraMtaRestriction
		"reject_unknown_reverse_client_hostname",                                  // not in zimbraMtaRestriction
		"reject_unknown_helo_hostname",                                            // not in zimbraMtaRestriction
		"reject_unverified_recipient",                                             // not in zimbraMtaRestriction
		"check_client_access lmdb:/opt/zextras/conf/postfix_rbl_override",         // not in zimbraMtaRestriction
		"check_reverse_client_hostname_access pcre:/opt/zextras/conf/fqrdns.pcre", // not in zimbraMtaRestriction
		"check_policy_service unix:private/policy",                                // not in zimbraMtaRestriction
		"check_recipient_access ldap:/opt/zextras/conf/ldap-splitdomain.cf",       // not in zimbraMtaRestriction
		"reject_rhsbl_reverse_client",                                             // empty value, no output
	}

	for _, notExpected := range notExpectedLines {
		if strings.Contains(output, notExpected) {
			t.Errorf("Unexpected line found in output: %q", notExpected)
		}
	}
}

// TestAmavisConfigIntegration tests transformation of a realistic
// Amavis configuration file with various directives.
// This test is based on conf/amavisd.conf.in
func TestAmavisConfigIntegration(t *testing.T) {
	input := `use strict;

# COMMONLY ADJUSTED SETTINGS:

%%uncomment VAR:carbonioAmavisDisableVirusCheck%% @bypass_virus_checks_maps = (1);  # uncomment to DISABLE anti-virus code
%%comment SERVICE:antispam%% @bypass_spam_checks_maps  = (1);  # uncomment to DISABLE anti-spam code

$enable_ldap = 1;
$default_ldap = {
	hostname      => [ split (' ','@@ldap_url@@') ],
	timeout       => 30,
	tls           => @@ldap_starttls_supported@@,
};

$max_servers = %%zimbraAmavisMaxServers%%;
$log_level = %%zimbraAmavisLogLevel%%;
$enable_dkim_verification = %%binary VAR:zimbraAmavisEnableDKIMVerification%%;

@mynetworks = qw( %%zimbraMtaMyNetworks%% );
`

	// Setup mock config
	mock := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"carbonioAmavisDisableVirusCheck":    "FALSE",
				"zimbraAmavisMaxServers":             "5",
				"zimbraAmavisLogLevel":               "2",
				"zimbraAmavisEnableDKIMVerification": "TRUE",
				"zimbraMtaMyNetworks":                "127.0.0.0/8 10.0.0.0/8 172.16.0.0/12",
			},
			"SERVICE": {
				"antispam": "TRUE",
			},
			"LOCAL": {
				"ldap_url":                "ldap://ldap1.example.com:389 ldap://ldap2.example.com:389",
				"ldap_starttls_supported": "1",
			},
		},
	}

	st := &state.State{}
	transformer := NewTransformer(mock, st)

	output := transformMultiLine(transformer, input)

	// Verify expected transformations
	expectedLines := []string{
		"# @bypass_virus_checks_maps = (1);", // commented (FALSE)
		"# @bypass_spam_checks_maps  = (1);", // commented (SERVICE enabled)
		"hostname      => [ split (' ','ldap://ldap1.example.com:389 ldap://ldap2.example.com:389') ],", // LOCAL substitution
		"tls           => 1,",            // LOCAL substitution
		"$max_servers = 5;",              // VAR substitution
		"$log_level = 2;",                // VAR substitution
		"$enable_dkim_verification = 1;", // binary TRUE -> 1
		"@mynetworks = qw( 127.0.0.0/8 10.0.0.0/8 172.16.0.0/12 );", // VAR substitution
	}

	for _, expected := range expectedLines {
		if !strings.Contains(output, expected) {
			t.Errorf("Expected line not found in output: %q\nActual output:\n%s", expected, output)
		}
	}
}

// TestMultiDirectiveConfigIntegration tests a configuration with multiple
// types of directives in sequence, ensuring proper order of evaluation.
func TestMultiDirectiveConfigIntegration(t *testing.T) {
	input := `# Test config with multiple directive types
server_name %%zimbraMailHost%%;
listen %%zimbraMailProxyPort%%;

%%uncomment VAR:zimbraReverseProxySSLToUpstreamEnabled%% ssl_protocols TLSv1.2 TLSv1.3;
%%comment VAR:zimbraReverseProxySSLToUpstreamEnabled%% # SSL disabled

upstream backend {
%%explode    server VAR:zimbraMailHost%%
}

set $relayhost "%%zimbraMtaRelayHost%%";
set $use_ssl %%binary VAR:zimbraMtaRelayTLS%%;
set $mynetworks "%%list VAR:zimbraMtaMyNetworks ,%%";

# Restrictions
%%exact VAR:zimbraMtaRestriction reject_invalid_hostname%%
%%exact VAR:zimbraMtaRestriction permit_mynetworks%%
%%contains VAR:zimbraMtaRestriction check_policy%%
`

	// Setup mock config
	mock := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"zimbraMailHost":                         "mail1.example.com mail2.example.com mail3.example.com",
				"zimbraMailProxyPort":                    "8080",
				"zimbraReverseProxySSLToUpstreamEnabled": "TRUE",
				"zimbraMtaRelayHost":                     "relay.example.com",
				"zimbraMtaRelayTLS":                      "TRUE",
				"zimbraMtaMyNetworks":                    "127.0.0.0/8 10.0.0.0/8 172.16.0.0/12",
				"zimbraMtaRestriction":                   "reject_invalid_hostname permit_mynetworks",
			},
		},
	}

	st := &state.State{}
	transformer := NewTransformer(mock, st)

	output := transformMultiLine(transformer, input)

	// Verify expected transformations
	expectedLines := []string{
		"server_name mail1.example.com mail2.example.com mail3.example.com;",
		"listen 8080;",
		"ssl_protocols TLSv1.2 TLSv1.3;",
		"server mail1.example.com",
		"server mail2.example.com",
		"server mail3.example.com",
		`set $relayhost "relay.example.com";`,
		"set $use_ssl 1;",
		`set $mynetworks "127.0.0.0/8,10.0.0.0/8,172.16.0.0/12";`,
		"reject_invalid_hostname",
		"permit_mynetworks",
	}

	for _, expected := range expectedLines {
		if !strings.Contains(output, expected) {
			t.Errorf("Expected line not found in output: %q\nActual output:\n%s", expected, output)
		}
	}

	// Verify lines that should NOT be present
	notExpectedLines := []string{
		"check_policy", // not an exact match, contains didn't find it
	}

	// When zimbraReverseProxySSLToUpstreamEnabled is TRUE, the "# SSL disabled" line should be commented
	if !strings.Contains(output, "# # SSL disabled") {
		t.Errorf("Expected '# SSL disabled' line to be commented when feature is enabled")
	}

	for _, notExpected := range notExpectedLines {
		if strings.Contains(output, notExpected) {
			t.Errorf("Unexpected line found in output: %q", notExpected)
		}
	}
}

// TestNestedDirectivesIntegration tests prefix directives (comment/uncomment)
// followed by inline directives on the same line.
func TestNestedDirectivesIntegration(t *testing.T) {
	input := `# Configuration with nested directives
%%uncomment VAR:enableFeature%% feature_option = %%featureValue%%;
%%comment VAR:disableFeature%% disabled_option = %%disabledValue%%;
%%uncomment VAR:binaryFeature%% binary_value = %%binary VAR:binaryOption%%;
`

	// Setup mock config
	mock := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"enableFeature":  "TRUE",
				"featureValue":   "enabled_value",
				"disableFeature": "TRUE",
				"disabledValue":  "should_be_commented",
				"binaryFeature":  "TRUE",
				"binaryOption":   "TRUE",
			},
		},
	}

	st := &state.State{}
	transformer := NewTransformer(mock, st)

	output := transformMultiLine(transformer, input)

	// Verify expected transformations
	expectedLines := []string{
		"feature_option = enabled_value;",          // uncommented and VAR substituted
		"# disabled_option = should_be_commented;", // commented and VAR substituted
		"binary_value = 1;",                        // uncommented and binary directive applied
	}

	for _, expected := range expectedLines {
		if !strings.Contains(output, expected) {
			t.Errorf("Expected line not found in output: %q\nActual output:\n%s", expected, output)
		}
	}
}

// TestComplexRealWorldScenario tests a complex scenario with multiple
// passes and various directive types mixed together.
func TestComplexRealWorldScenario(t *testing.T) {
	input := `# Complex configuration
# Server settings
listen %%zimbraMailProxyPort%%;
server_name @@hostname@@;

# SSL Configuration
%%uncomment VAR:zimbraReverseProxySSLEnabled%% ssl_certificate /opt/zextras/ssl/zimbra/server/server.crt;
%%uncomment VAR:zimbraReverseProxySSLEnabled%% ssl_certificate_key /opt/zextras/ssl/zimbra/server/server.key;
%%uncomment VAR:zimbraReverseProxySSLEnabled%% ssl_protocols %%zimbraReverseProxySSLProtocols%%;

# Upstream servers
upstream backend {
%%explode    server VAR:zimbraMailHost%%
    keepalive 32;
}

# Access control
%%exact VAR:zimbraMtaRestriction reject_invalid_hostname%%
%%exact VAR:zimbraMtaRestriction reject_non_fqdn_hostname%%
%%contains VAR:zimbraMtaRestriction check_policy_service unix:private/policy%%
%%explode reject_rbl_client VAR:zimbraMtaRestrictionRBLs%%

# Feature flags
set $debug_mode %%binary VAR:zimbraDebugMode%%;
set $use_cache %%truefalse VAR:zimbraProxyCacheEnabled%%;
set $trusted_nets "%%list VAR:zimbraMtaMyNetworks ,%%";

# End of config
`

	// Setup mock config with typical production values
	mock := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"zimbraMailProxyPort":            "8443",
				"zimbraReverseProxySSLEnabled":   "TRUE",
				"zimbraReverseProxySSLProtocols": "TLSv1.2 TLSv1.3",
				"zimbraMailHost":                 "mail1.prod.example.com mail2.prod.example.com mail3.prod.example.com",
				"zimbraMtaRestriction":           "reject_invalid_hostname check_policy_service unix:private/policy",
				"zimbraMtaRestrictionRBLs":       "zen.spamhaus.org bl.spamcop.net dnsbl.sorbs.net",
				"zimbraDebugMode":                "FALSE",
				"zimbraProxyCacheEnabled":        "TRUE",
				"zimbraMtaMyNetworks":            "127.0.0.0/8 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16",
			},
			"LOCAL": {
				"hostname": "mailproxy.prod.example.com",
			},
		},
	}

	st := &state.State{}
	transformer := NewTransformer(mock, st)

	output := transformMultiLine(transformer, input)

	// Verify all expected transformations
	expectedLines := []string{
		"listen 8443;",
		"server_name mailproxy.prod.example.com;",
		"ssl_certificate /opt/zextras/ssl/zimbra/server/server.crt;",
		"ssl_certificate_key /opt/zextras/ssl/zimbra/server/server.key;",
		"ssl_protocols TLSv1.2 TLSv1.3;",
		"server mail1.prod.example.com",
		"server mail2.prod.example.com",
		"server mail3.prod.example.com",
		"    keepalive 32;",
		"reject_invalid_hostname",
		"check_policy_service unix:private/policy",
		"reject_rbl_client zen.spamhaus.org",
		"reject_rbl_client bl.spamcop.net",
		"reject_rbl_client dnsbl.sorbs.net",
		"set $debug_mode 0;",
		"set $use_cache true;",
		`set $trusted_nets "127.0.0.0/8,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16";`,
	}

	for _, expected := range expectedLines {
		if !strings.Contains(output, expected) {
			t.Errorf("Expected line not found in output: %q\nActual output:\n%s", expected, output)
		}
	}

	// Verify lines that should NOT be present
	notExpectedLines := []string{
		"reject_non_fqdn_hostname", // not in zimbraMtaRestriction
		"# ssl_certificate",        // should be uncommented (zimbraReverseProxySSLEnabled is TRUE)
		"%%",                       // no directives should remain
		"@@",                       // no local config patterns should remain
	}

	for _, notExpected := range notExpectedLines {
		if strings.Contains(output, notExpected) {
			t.Errorf("Unexpected pattern found in output: %q", notExpected)
		}
	}

	// Verify line count is reasonable (multiple exploded lines)
	// Note: Some lines may be combined due to single-pass transformation
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 15 {
		t.Errorf("Expected at least 15 output lines, got %d", len(lines))
	}
}

// TestEmptyAndWhitespaceHandling tests how the transformer handles
// empty lines and whitespace in realistic configurations.
func TestEmptyAndWhitespaceHandling(t *testing.T) {
	input := `# Test whitespace handling

server {
    listen %%port%%;

    # Empty line above and below

    server_name %%hostname%%;
}

%%explode reject_rbl_client VAR:rbls%%

# End
`

	mock := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"port":     "8080",
				"hostname": "example.com",
				"rbls":     "rbl1.com rbl2.com",
			},
		},
	}

	st := &state.State{}
	transformer := NewTransformer(mock, st)

	output := transformMultiLine(transformer, input)

	// Verify structure is preserved
	if !strings.Contains(output, "listen 8080;") {
		t.Error("Port substitution failed")
	}
	if !strings.Contains(output, "server_name example.com;") {
		t.Error("Hostname substitution failed")
	}
	if !strings.Contains(output, "reject_rbl_client rbl1.com") {
		t.Error("RBL explode failed")
	}
	if !strings.Contains(output, "reject_rbl_client rbl2.com") {
		t.Error("RBL explode failed")
	}

	// Verify empty lines are preserved
	lines := strings.Split(output, "\n")
	emptyLineCount := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			emptyLineCount++
		}
	}
	if emptyLineCount < 2 {
		t.Errorf("Expected at least 2 empty lines to be preserved, got %d", emptyLineCount)
	}
}
