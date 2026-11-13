// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package provision_test

import (
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/provision"
)

func TestSchemaCheck_ValuesByType(t *testing.T) {
	s := schema(t)

	for name, tc := range map[string]struct {
		attr, value string
		ok          bool
	}{
		"boolean":          {"zimbraIsAdminAccount", "TRUE", true},
		"boolean lower":    {"zimbraIsAdminAccount", "true", false},
		"enum":             {"zimbraMailStatus", "enabled", true},
		"enum other":       {"zimbraMailStatus", "on", false},
		"integer":          {"zimbraPasswordMinLength", "8", true},
		"integer negative": {"zimbraPasswordMinLength", "-1", false},
		"integer text":     {"zimbraPasswordMinLength", "x", false},
		"long":             {"zimbraMailQuota", "0", true},
		"long text":        {"zimbraMailQuota", "x", false},
		"port":             {"zimbraMailPort", "8080", true},
		"port high":        {"zimbraMailPort", "70000", false},
		"id":               {"zimbraId", "0f1e2d3c-0000-4000-8000-000000000001", true},
		"id other":         {"zimbraId", "abc", false},
		"duration days":    {"zimbraMailMessageLifetime", "30d", true},
		"duration seconds": {"zimbraMailMessageLifetime", "30", true},
		"duration unit":    {"zimbraMailMessageLifetime", "30w", false},
		"gentime":          {"zimbraCreateTimestamp", "20261112223616.047Z", true},
		"gentime legacy":   {"zimbraCreateTimestamp", "20261112223616Z", true},
		"gentime other":    {"zimbraCreateTimestamp", "2026-11-12", false},
		"email":            {"carbonioNotificationFrom", "a@example.com", true},
		"email other":      {"carbonioNotificationFrom", "a", false},
		"string":           {"zimbraDefaultDomainName", "example.com", true},
		"string too long":  {"zimbraDefaultDomainName", strings.Repeat("x", 300), false},
		"binary":           {"zimbraSSLDHParam", "bytes", true},
		"unknown":          {"zimbraNoSuchThing", "x", true},
		"empty unsets":     {"zimbraMailPort", "", true},
	} {
		err := s.Check(tc.attr, tc.value, false)
		assert.Equal(t, tc.ok, err == nil, "%s: %v", name, err)

		if !tc.ok {
			assert.True(t, hasCode(err, provision.CodeInvalidAttrValue), name)
		}
	}

	assert.Error(t, s.Check("zimbraId", "0f1e2d3c-0000-4000-8000-000000000001", true), "an immutable attribute")
	assert.Nil(t, s.Lookup("nosuch"))
	assert.True(t, s.Binary("zimbraSSLDHParam"))
	assert.True(t, s.ServerInherited("zimbraMailPort"))
	assert.False(t, s.ServerInherited("zimbraMailQuota"))
}

func TestLoadSchema_Errors(t *testing.T) {
	_, err := provision.LoadSchema(osFS{}, "testdata/nodir")
	assert.Error(t, err)

	_, err = provision.ParseSchema(strings.NewReader("<attrs><attr name="))
	assert.Error(t, err)

	_, err = provision.ParseSchema(strings.NewReader(`<attrs><attr id="1" name="x" type="integer" min="a"/></attrs>`))
	assert.NoError(t, err, "limits that are not numbers fall back to the defaults")
}

func TestParseMods(t *testing.T) {
	r := newRig(t)
	r.files["/tmp/dh.pem"] = []byte("PARAMS")
	s := schema(t)

	m, err := provision.ParseMods(s, func(p string) ([]byte, error) { return r.ReadFile(p) },
		[]string{"zimbramtamynetworks", "a", "+zimbraMtaMyNetworks", "b", "zimbraSSLDHParam", "/tmp/dh.pem"})
	require.NoError(t, err)
	assert.Equal(t, provision.Mods{
		"zimbraMtaMyNetworks": {"a"}, "+zimbraMtaMyNetworks": {"b"}, "zimbraSSLDHParam": {"PARAMS"},
	}, m, "the names are the schema's and a binary value is the content of its file")

	_, err = provision.ParseMods(s, r.ReadFile, []string{"zimbraSSLDHParam", "/no/file"})
	assert.True(t, hasCode(err, provision.CodeFailure), "%v", err)

	_, err = provision.ParseMods(s, r.ReadFile, []string{"a"})
	assert.Error(t, err)

	_, err = provision.ParseMods(s, r.ReadFile, []string{"", "v"})
	assert.True(t, hasCode(err, provision.CodeInvalidAttrName), "%v", err)
}

func TestModify_ReplaceAddRemoveAndUnset(t *testing.T) {
	r := newRig(t)
	r.run(t, "cs", host)

	r.run(t, "ms", host, "zimbraServiceEnabled", "a", "zimbraServiceEnabled", "b")
	assert.Equal(t, []string{"a", "b"}, r.dir.Values(serverDN, "zimbraServiceEnabled"))

	r.run(t, "ms", host, "+zimbraServiceEnabled", "c", "+zimbraServiceEnabled", "a")
	assert.Equal(t, []string{"a", "b", "c"}, r.dir.Values(serverDN, "zimbraServiceEnabled"))

	r.run(t, "ms", host, "-zimbraServiceEnabled", "b")
	assert.Equal(t, []string{"a", "c"}, r.dir.Values(serverDN, "zimbraServiceEnabled"))

	r.run(t, "ms", host, "zimbraServiceEnabled", "")
	assert.Empty(t, r.dir.Values(serverDN, "zimbraServiceEnabled"))

	for name, args := range map[string][]string{
		"plain and plus":    {"ms", host, "zimbraServiceEnabled", "a", "+zimbraServiceEnabled", "b"},
		"empty among many":  {"ms", host, "zimbraServiceEnabled", "a", "zimbraServiceEnabled", ""},
		"unknown server":    {"ms", "nosuch.example.com", "zimbraServiceEnabled", "a"},
		"unknown cos":       {"mc", "nosuch", "zimbraPasswordMinLength", "8"},
		"too few arguments": {"ms", host},
		"unknown verb":      {"zz", "x"},
		"no command":        {},
		"immutable":         {"ms", host, "zimbraId", "0f1e2d3c-0000-4000-8000-000000000001"},
	} {
		_, err := r.Exec(args)
		assert.Error(t, err, name)
	}
}

func TestModify_GlobalConfigAndClassOfServiceByNameAndID(t *testing.T) {
	r := newRig(t)
	r.files["/tmp/dh.pem"] = []byte("PARAMS")

	r.run(t, "mcf", "zimbraDefaultDomainName", "example.com", "zimbraSSLDHParam", "/tmp/dh.pem")
	assert.Equal(t, []string{"PARAMS"}, r.dir.Values(globalDN, "zimbraSSLDHParam"))
	assert.Equal(t, []string{"example.com"}, r.dir.Values(globalDN, "zimbraDefaultDomainName"))

	r.run(t, "mc", "default", "zimbraPrefTimeZoneId", "Europe/Rome")
	r.run(t, "mc", "0f1e2d3c-0000-4000-8000-000000000001", "+zimbraPrefTimeZoneId", "Asia/Tokyo")
	assert.Equal(t, []string{"Europe/Rome", "Asia/Tokyo"}, r.dir.Values(cosDN, "zimbraPrefTimeZoneId"))

	_, err := r.Exec([]string{"mcf", "zimbraMailPort", "x"})
	assert.True(t, hasCode(err, provision.CodeInvalidAttrValue))

	_, err = r.Exec([]string{"mcf", "zimbraTestUnportedCallback", "x"})
	assert.True(t, hasCode(err, provision.CodeInvalidRequest), "%v", err)

	r.dir.FailModify[globalDN] = assert.AnError
	_, err = r.Exec([]string{"mcf", "zimbraDefaultDomainName", "x.com"})
	assert.True(t, hasCode(err, provision.CodeFailure))
}

func TestPortConflicts(t *testing.T) {
	r := newRig(t)
	r.run(t, "cs", host)
	r.run(t, "cs", "other.example.com")
	r.run(t, "ms", host, "zimbraMailSSLPort", "8443")

	_, err := r.Exec([]string{"ms", host, "zimbraImapBindPort", "8443"})
	require.Error(t, err)
	assert.True(t, hasCode(err, provision.CodeInvalidRequest), "%v", err)
	assert.Contains(t, err.Error(), "zimbraImapBindPort")
	assert.Empty(t, r.dir.Values(serverDN, "zimbraImapBindPort"), "nothing is written")

	// The same port on another server is no conflict.
	r.run(t, "ms", "other.example.com", "zimbraImapBindPort", "8443")

	// A global default that a server without its own value would inherit.
	r.run(t, "mcf", "zimbraPop3BindPort", "7110")
	_, err = r.Exec([]string{"ms", host, "zimbraAdminPort", "7110"})
	assert.Error(t, err, "the inherited default conflicts")

	// A new global default must not collide with the ports the servers have.
	_, err = r.Exec([]string{"mcf", "zimbraLmtpBindPort", "8443"})
	assert.Error(t, err)
}

func TestMailPort_KeepsTheLocalConfigOfTheLocalServerInStep(t *testing.T) {
	r := newRig(t)
	r.run(t, "cs", host)
	r.run(t, "cs", "other.example.com")

	r.run(t, "ms", "other.example.com", "zimbraMailPort", "8081")
	assert.Empty(t, r.lc, "another server's port is not the local one")

	r.run(t, "ms", host, "zimbraMailPort", "8080")
	assert.Equal(t, map[string]string{"zimbra_mail_service_port": "8080"}, r.lc)
}

func TestLocalBind(t *testing.T) {
	r := newRig(t)
	r.run(t, "cs", host)
	r.ips["bind.example.com"] = []net.IP{net.ParseIP("10.0.0.5")}

	r.run(t, "ms", host, "zimbraMailBindAddress", "127.0.0.1", "zimbraAdminBindAddress", "bind.example.com")
	assert.Equal(t, []string{"FALSE"}, r.dir.Values(serverDN, "zimbraMailLocalBind"), "a loopback address is not a bind to the network")
	assert.Equal(t, []string{"TRUE"}, r.dir.Values(serverDN, "zimbraAdminLocalBind"), "the address resolves to a network one")

	r.run(t, "ms", host, "zimbraMailMode", "https")
	assert.Equal(t, []string{"TRUE"}, r.dir.Values(serverDN, "zimbraMailLocalBind"), "https binds always")

	r.run(t, "ms", host, "zimbraMailMode", "http", "zimbraMailSSLBindAddress", "10.0.0.5")
	assert.Equal(t, []string{"FALSE"}, r.dir.Values(serverDN, "zimbraMailLocalBind"))

	r.run(t, "ms", host, "zimbraMailBindAddress", "10.0.0.5")
	assert.Equal(t, []string{"TRUE"}, r.dir.Values(serverDN, "zimbraMailLocalBind"))

	r.run(t, "mcf", "zimbraMailMode", "https")
	assert.Equal(t, []string{"TRUE"}, r.dir.Values(serverDN, "zimbraMailLocalBind"), "a global change reaches the servers")
}

func TestMailHostPoolRefusesAnIdThatIsNoUsableServer(t *testing.T) {
	r := newRig(t)
	id := r.run(t, "cs", host)

	_, err := r.Exec([]string{"mc", "default", "zimbraMailHostPool", id})
	assert.True(t, hasCode(err, provision.CodeInvalidRequest), "the server runs no mail client service: %v", err)

	r.run(t, "ms", host, "zimbraServiceEnabled", "service")
	r.run(t, "mc", "default", "zimbraMailHostPool", id)
	assert.Equal(t, []string{id}, r.dir.Values(cosDN, "zimbraMailHostPool"))

	_, err = r.Exec([]string{"mc", "default", "zimbraMailHostPool", "0f1e2d3c-0000-4000-8000-0000000000ff"})
	assert.Error(t, err, "no such server")

	r.run(t, "mc", "default", "-zimbraMailHostPool", id)
	assert.Empty(t, r.dir.Values(cosDN, "zimbraMailHostPool"))
}

func TestMailHostMustBeAMailServerWithAMatchingTransport(t *testing.T) {
	r := newRig(t)
	r.withServer(t)
	r.withDomain(t)
	r.run(t, "ca", "u@example.com", "")
	user := "uid=u,ou=people,dc=example,dc=com"

	r.run(t, "cs", "idle.example.com")
	_, err := r.Exec([]string{"ca", "v@example.com", "", "zimbraMailHost", "idle.example.com"})
	assert.True(t, hasCode(err, provision.CodeInvalidRequest), "no mail client service: %v", err)

	assert.Equal(t, []string{host}, r.dir.Values(user, "zimbraMailHost"))
}

func TestStatusAndIntervalCallbacks(t *testing.T) {
	r := newRig(t)
	r.withServer(t)
	r.withDomain(t)
	r.run(t, "ca", "u@example.com", "")

	_, err := r.Exec([]string{"mcf", "zimbraDomainStatus", "shutdown"})
	assert.True(t, hasCode(err, provision.CodeInvalidRequest), "shutdown is internal: %v", err)

	r.run(t, "mcf", "zimbraDomainStatus", "suspended")
	assert.Equal(t, []string{"suspended"}, r.dir.Values(globalDN, "zimbraDomainStatus"))

	_, err = r.Exec([]string{"mc", "default", "zimbraAuthTokenLifetime", "0"})
	assert.True(t, hasCode(err, provision.CodeInvalidRequest), "a time interval must be positive: %v", err)

	r.run(t, "mc", "default", "zimbraAuthTokenLifetime", "12h")
	assert.Equal(t, []string{"12h"}, r.dir.Values(cosDN, "zimbraAuthTokenLifetime"))

	r.run(t, "mcf", "zimbraMtaRelayHost", "relay.example.com:25")
	assert.Equal(t, []string{"relay.example.com:25"}, r.dir.Values(globalDN, "zimbraMtaRelayHost"))
}
