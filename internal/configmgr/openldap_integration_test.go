// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build integration

package configmgr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/commands"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/mtaops"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/restart"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
	"github.com/zextras/carbonio-configd/internal/testutil"
	"github.com/zextras/carbonio-configd/internal/transformer"
	"github.com/zextras/carbonio-configd/test"
)

// The suite runs configd's real loaders, plan and Execute against a slapd
// container seeded from testdata/openldap (a reduced Carbonio schema and a
// sanitized directory). The host side is fake: postconf and every other
// command go to a recording systest.Exec, files to an in-memory systest.FS.
// Run it with `make test-integration-ldap`; it skips when no container
// runtime is reachable.

const (
	olTestHost  = "mail.test.example"
	olOtherHost = "other.test.example"
	olBaseDir   = "/opt/zextras"
	olAttrsPath = olBaseDir + "/conf/attrs/attrs.xml"

	olTemplate = "size=%%zimbraMtaMaxMessageSize%%\nmilter=%%zimbraMilterServerEnabled%%\n"
	olConfigCF = `SECTION mta
	VAR zimbraMtaMaxMessageSize
	VAR zimbraMilterServerEnabled
	REWRITE conf/mta.conf.in conf/mta.conf
	POSTCONF message_size_limit VAR zimbraMtaMaxMessageSize
	RESTART mta

SECTION ldap
	LOCAL ldap_common_loglevel
	LOCAL ldap_common_toolthreads
	LDAP ldap_common_loglevel LOCAL ldap_common_loglevel
	LDAP ldap_common_toolthreads LOCAL ldap_common_toolthreads
`
)

// olHarness is a ConfigManager wired to the container and to fake host I/O.
type olHarness struct {
	t      *testing.T
	cm     *ConfigManager
	fakes  *systest.Fakes
	client *ldap.Client
	data   *goldap.Conn
	conf   *goldap.Conn

	cf string // the zmconfigd.cf the manager parses every cycle

	mu    sync.Mutex
	local map[string]string
}

func (h *olHarness) setLocal(key, value string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.local[key] = value
}

func (h *olHarness) loadLocal() (map[string]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	out := make(map[string]string, len(h.local))
	for k, v := range h.local {
		out[k] = v
	}

	return out, nil
}

func dialAs(t *testing.T, url, dn, password string) *goldap.Conn {
	t.Helper()

	conn, err := goldap.DialURL(url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.Bind(dn, password))

	return conn
}

func newOpenLDAPHarness(t *testing.T) *olHarness {
	t.Helper()

	ctx := context.Background()
	ol := test.StartOpenLDAP(ctx, t, test.OpenLDAPFixtures{
		Slapd:  "testdata/openldap/slapd.ldif",
		Schema: "testdata/openldap/zimbra-schema.ldif",
		Seed:   "testdata/openldap/seed.ldif",
	})

	attrs, err := os.ReadFile("../ldap/testdata/attrs/attrs.xml")
	require.NoError(t, err)

	fakes := systest.New()
	fakes.FS.Put(olAttrsPath, attrs, 0o644)
	fakes.FS.Put(olBaseDir+"/conf/mta.conf.in", []byte(olTemplate), 0o644)
	fakes.FS.PutDir(olBaseDir+"/conf", 0o755)
	env := fakes.System()

	client, err := ldap.NewClient(&ldap.ClientConfig{
		URL:      ol.URL,
		BindDN:   test.OpenLDAPDataBindDN,
		Password: test.OpenLDAPDataPassword,
		BaseDN:   "cn=zimbra",
		StartTLS: false,
		FS:       env.FS,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	configClient, err := ldap.NewClient(&ldap.ClientConfig{
		URL:      ol.URL,
		BindDN:   test.OpenLDAPConfigBindDN,
		Password: test.OpenLDAPConfigPassword,
		BaseDN:   ldap.ConfigBackendDN,
		PoolSize: 1,
		StartTLS: false,
		FS:       env.FS,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = configClient.Close() })

	mainCfg := &config.Config{BaseDir: olBaseDir, Hostname: olTestHost, RestartConfig: true}
	st := state.NewState()
	st.SetFirstRun(true)

	h := &olHarness{
		t:      t,
		cf:     filepath.Join(t.TempDir(), "zmconfigd.cf"),
		fakes:  fakes,
		client: client,
		data:   dialAs(t, ol.URL, test.OpenLDAPDataBindDN, test.OpenLDAPDataPassword),
		conf:   dialAs(t, ol.URL, test.OpenLDAPConfigBindDN, test.OpenLDAPConfigPassword),
		local: map[string]string{
			"zimbra_server_hostname":  olTestHost,
			"ldap_common_loglevel":    "64",
			"ldap_common_toolthreads": "2",
		},
	}

	require.NoError(t, os.WriteFile(h.cf, []byte(olConfigCF), 0o600))

	ldapMgr := ldap.NewLdap(ctx, mainCfg)
	ldapMgr.SetNativeClient(ctx, client)
	ldapMgr.SetConfigClient(ctx, configClient)

	registry := commands.NewRegistry(olBaseDir, env)
	registry.RegisterLDAPCommands(commands.NewCommandExecutor(client))

	h.cm = &ConfigManager{
		env:                     env,
		mainConfig:              mainCfg,
		State:                   st,
		LdapClient:              ldapMgr,
		NativeLdapClient:        client,
		ConfigLdapClient:        configClient,
		CommandRegistry:         registry,
		ServiceMgr:              &testutil.MockServiceManager{},
		Cache:                   cache.New(ctx, false),
		mtaExecutor:             mtaops.NewExecutor(olBaseDir, ldapMgr, env),
		mtaResolver:             mtaops.NewResolver(olBaseDir, env),
		loadResolvedLocalConfig: h.loadLocal,
	}
	h.cm.Transformer = transformer.NewTransformer(h.cm)
	h.cm.policy = restart.New(h.cm.ServiceMgr, env.Clock)

	return h
}

// modify replaces attr of dn through the data connection, as zmprov would.
func (h *olHarness) modify(dn, attr string, values ...string) {
	h.t.Helper()

	req := goldap.NewModifyRequest(dn, nil)
	req.Replace(attr, values)
	require.NoError(h.t, h.data.Modify(req))
}

// configAttr reads attr of the slapd-config entry dn.
func (h *olHarness) configAttr(dn, attr string) string {
	h.t.Helper()

	res, err := h.conf.Search(goldap.NewSearchRequest(dn, goldap.ScopeBaseObject,
		goldap.NeverDerefAliases, 0, 0, false, "(objectClass=*)", []string{attr}, nil))
	require.NoError(h.t, err)
	require.Len(h.t, res.Entries, 1)

	return res.Entries[0].GetAttributeValue(attr)
}

// load runs the loaders and parses zmconfigd.cf, as the daemon does at the
// start of every cycle (LDAP directives are resolved at parse time).
func (h *olHarness) load() {
	h.t.Helper()

	ctx := context.Background()
	require.NoError(h.t, h.cm.LoadAllConfigs(ctx))
	require.NoError(h.t, h.cm.ParseMtaConfig(ctx, h.cf))
}

// TestOpenLDAP_ConfigdCycle is one scenario run in order: load the directory,
// compare it with the direct LDAP view, run the first cycle, then modify the
// directory and localconfig and check what the next cycle plans and does.
func TestOpenLDAP_ConfigdCycle(t *testing.T) {
	h := newOpenLDAPHarness(t)

	// The container is slow to start; its subtests share it and run in order.
	t.Run("snapshot", h.checkSnapshot)
	t.Run("gs_gacf_parity", h.checkParity)
	t.Run("first_cycle", h.checkFirstCycle)
	t.Run("modify_plan", h.checkModifyPlan)
	t.Run("no_change", h.checkNoChange)
}

func (h *olHarness) checkSnapshot(t *testing.T) {
	h.load()

	snap := h.cm.State.Snapshot()

	// Global config: seeded values; multi-valued attributes are newline-joined,
	// SSL cipher lists are sorted and get an XML companion.
	global := snap.GlobalMap()
	assert.Equal(t, "10240000", global["zimbraMtaMaxMessageSize"])
	assert.Equal(t, "443", global["zimbraMailSSLPort"])
	assert.Equal(t, "reject_non_fqdn_sender\nreject_unknown_client_hostname", global["zimbraMtaRestriction"])
	assert.Equal(t, "SSL_RSA_WITH_RC4_128_MD5 TLS_RSA_WITH_AES_128_CBC_SHA", global["zimbraSSLExcludeCipherSuites"])
	assert.Equal(t, "<Item>SSL_RSA_WITH_RC4_128_MD5</Item>\n<Item>TLS_RSA_WITH_AES_128_CBC_SHA</Item>",
		global["zimbraSSLExcludeCipherSuitesXML"])
	assert.Equal(t, constFALSE, global["zimbraQuarantineBannedItems"])

	// Server config: own attributes, plus the serverInherited ones from the
	// global config only when the server does not set them.
	server := snap.ServerMap()
	assert.Equal(t, olTestHost, server["zimbraServiceHostname"])
	assert.Equal(t, "11111111-2222-3333-4444-555555555555", server["zimbraId"])
	assert.Equal(t, "FALSE", server["zimbraMilterServerEnabled"], "the server value wins over the global TRUE")
	assert.Equal(t, "7026", server["zimbraMilterBindPort"], "inherited from the global config")
	assert.Equal(t, "127.0.0.0/8 [::1]/128", server["zimbraMtaMyNetworks"], "inherited from the global config")
	assert.Equal(t, "127.0.0.0/8\n[::1]/128", server["zimbraMtaMyNetworksPerLine"])
	assert.NotContains(t, server, "zimbraMtaMaxMessageSize", "not serverInherited in the attrs fixture")

	// Services come from zimbraServiceEnabled, with the derived sections.
	assert.ElementsMatch(t, []string{"ldap", "mailbox", "mailboxd", "mta", "proxy", "sasl"}, snap.ServiceNames())

	local, ok := snap.Local("ldap_common_loglevel")
	assert.True(t, ok)
	assert.Equal(t, "64", local)
}

func (h *olHarness) checkParity(t *testing.T) {
	snap := h.cm.State.Snapshot()

	// gacf: every attribute of the entry is in the snapshot with its value.
	rawGlobal, err := h.client.GetGlobalConfig()
	require.NoError(t, err)

	global := snap.GlobalMap()

	for k, v := range rawGlobal {
		assertParity(t, "gacf "+k, k, v, global[k])
	}

	// gs: the snapshot carries what the client returns for the server.
	rawServer, err := h.client.GetServerConfig(olTestHost)
	require.NoError(t, err)

	server := snap.ServerMap()

	for k, v := range rawServer {
		assertParity(t, "gs "+k, k, v, server[k])
	}

	// A server that sets none of the inherited attributes gets the global ones.
	other, err := h.client.GetServerConfig(olOtherHost)
	require.NoError(t, err)
	assert.Equal(t, "TRUE", other["zimbraMilterServerEnabled"])
	assert.Equal(t, rawGlobal["zimbraMtaMyNetworks"], other["zimbraMtaMyNetworks"])
	assert.Equal(t, "7026", other["zimbraMilterBindPort"])

	servers, err := h.client.GetAllServers()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{olTestHost, olOtherHost}, servers)

	domains, err := h.client.GetAllDomains()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"test.example", "alias.example"}, domains)
}

// assertParity compares the loaded value of an attribute with the client's.
// The SSL list attributes are the only ones the loaders reorder (sorted,
// space-joined), so those are compared as sets.
func assertParity(t *testing.T, msg, key, raw, loaded string) {
	t.Helper()

	switch key {
	case "zimbraMailboxdSSLProtocols", "zimbraSSLExcludeCipherSuites", "zimbraSSLIncludeCipherSuites":
		assert.ElementsMatch(t, strings.Fields(raw), strings.Fields(loaded), msg)
	default:
		assert.Equal(t, normalizedValue(raw), loaded, msg)
	}
}

// normalizedValue applies what the loaders do to every value line.
func normalizedValue(v string) string {
	lines := strings.Split(v, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}

	return strings.Join(lines, "\n")
}

func (h *olHarness) checkFirstCycle(t *testing.T) {
	h.fakes.Rec.Reset()

	pl := mustCycle(t, h.cm)
	h.cm.State.SetFirstRun(false)

	assert.True(t, pl.FirstRun)
	assert.Equal(t, []string{"ldap", "mta"}, sectionNames(pl))

	for _, s := range pl.Sections {
		require.NotEmpty(t, s.Reasons)
		assert.Equal(t, plan.ReasonFirstRun, s.Reasons[0].Kind, s.Name)
	}

	// The template is rendered from the loaded config into the fake FS.
	out, ok := h.fakes.FS.Data(olBaseDir + "/conf/mta.conf")
	require.True(t, ok, "rewrite output missing; FS: %v", h.fakes.FS.Paths())
	assert.Equal(t, "size=10240000\nmilter=FALSE\n", string(out))

	// postconf ran through the fake exec, never a real binary.
	assert.Contains(t, postconfLines(h), olBaseDir+"/common/sbin/postconf -e message_size_limit=10240000")

	// The LDAP directives reached cn=config of the container.
	assert.Equal(t, "64", h.configAttr("cn=config", "olcLogLevel"))
	assert.Equal(t, "2", h.configAttr("cn=config", "olcToolThreads"))

	// First run restarts nothing.
	assert.Empty(t, pl.Restarts)
}

func postconfLines(h *olHarness) []string {
	var out []string

	for _, l := range h.fakes.Rec.Of("exec") {
		if strings.Contains(l, "postconf") {
			out = append(out, strings.TrimPrefix(l, "exec "))
		}
	}

	return out
}

func (h *olHarness) checkModifyPlan(t *testing.T) {
	// zmprov mcf zimbraMtaMaxMessageSize, and a localconfig edit.
	h.modify("cn=config,cn=zimbra", "zimbraMtaMaxMessageSize", "20480000")
	h.setLocal("ldap_common_loglevel", "128")
	h.load()

	snap := h.cm.State.Snapshot()
	v, _ := snap.Global("zimbraMtaMaxMessageSize")
	require.Equal(t, "20480000", v)

	h.fakes.Rec.Reset()

	pl := mustCycle(t, h.cm)

	assert.False(t, pl.FirstRun)
	assert.Equal(t, []string{"ldap", "mta"}, sectionNames(pl))
	assert.Equal(t, map[string][2]string{
		"zimbraMtaMaxMessageSize": {"10240000", "20480000"},
	}, varChanges(pl, "mta"))
	assert.Equal(t, map[string][2]string{
		"ldap_common_loglevel": {"64", "128"},
	}, varChanges(pl, "ldap"))

	// The new value reaches postconf, the template and cn=config.
	assert.Contains(t, postconfLines(h), olBaseDir+"/common/sbin/postconf -e message_size_limit=20480000")

	out, ok := h.fakes.FS.Data(olBaseDir + "/conf/mta.conf")
	require.True(t, ok)
	assert.Equal(t, "size=20480000\nmilter=FALSE\n", string(out))

	assert.Equal(t, "128", h.configAttr("cn=config", "olcLogLevel"))
	assert.Equal(t, "2", h.configAttr("cn=config", "olcToolThreads"), "unchanged directive keeps its value")

	// The whole ldap section is rewritten; the unchanged directive is a no-op
	// in cn=config (asserted above).
	require.Len(t, pl.Ldap, 2)

	require.Len(t, pl.Restarts, 1)
	assert.Equal(t, "mta", pl.Restarts[0].Service)
}

func (h *olHarness) checkNoChange(t *testing.T) {
	h.load()
	h.fakes.Rec.Reset()

	pl := mustCycle(t, h.cm)

	assert.True(t, pl.IsEmpty(), "an unchanged directory plans nothing: %+v", pl)
	assert.Empty(t, postconfLines(h))
}
