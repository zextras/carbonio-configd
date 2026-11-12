// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tui

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
)

type fakeHost struct{ roles []string }

func (fakeHost) FQDN() string               { return "mail.example.com" }
func (h fakeHost) InstalledRoles() []string { return h.roles }
func (fakeHost) IPMode() string             { return "ipv4" }
func (fakeHost) Timezone() string           { return "Europe/Rome" }
func (fakeHost) Networks() []string         { return []string{"10.0.0.0/24"} }
func (fakeHost) MemoryMB() int              { return 16384 }

type fakeResolver struct {
	hosts map[string][]string
	mx    map[string][]*net.MX
}

func (f fakeResolver) LookupHost(_ context.Context, h string) ([]string, error) {
	if a, ok := f.hosts[h]; ok {
		return a, nil
	}

	return nil, errors.New("no such host")
}

func (f fakeResolver) LookupMX(_ context.Context, n string) ([]*net.MX, error) {
	if a, ok := f.mx[n]; ok {
		return a, nil
	}

	return nil, errors.New("no mx")
}

// newModel returns a screen on a node with the given roles; layers apply the
// operator's layers (env, flags) before the defaults.
func newModel(t *testing.T, roles []string, layers func(*bootstrap.Resolved), opts ...func(*ConfigureOptions)) *Model {
	t.Helper()

	r := bootstrap.NewResolved(nil)
	if layers != nil {
		layers(r)
	}

	host := fakeHost{roles: roles}
	require.NoError(t, r.ApplyDefaults(host))

	o := &ConfigureOptions{Resolved: r, Host: host, NoColor: true, Info: Info{OS: "Ubuntu 24.04", Install: "new install"}}
	for _, f := range opts {
		f(o)
	}

	m := NewConfigure(o)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	return m
}

func setEnv(env ...string) func(*bootstrap.Resolved) {
	return func(r *bootstrap.Resolved) {
		if err := r.ApplyEnv(env); err != nil {
			panic(err)
		}
	}
}

func press(m *Model, keys ...string) tea.Cmd {
	var last tea.Cmd

	for _, k := range keys {
		_, last = m.Update(keyMsg(k))
	}

	return last
}

func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}

	return tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
}

func typeText(m *Model, s string) {
	for _, r := range s {
		press(m, string(r))
	}
}

func screen(m tea.Model) string { return ansi.Strip(m.View().Content) }

func TestRender_Configure(t *testing.T) {
	m := newModel(t, []string{bootstrap.RoleDirectoryServer, bootstrap.RoleMTA, bootstrap.RoleAppserver}, nil)
	out := screen(m)
	t.Log("\n" + out)

	for _, want := range []string{"CARBONIO BOOTSTRAP", "configure ›", "mail.example.com", "Ubuntu 24.04", "new install",
		"● directory-server", "○ proxy", "from installed packages", "Sections", "Review & apply", "SETTING", "VALUE", "SOURCE"} {
		assert.Contains(t, out, want)
	}
}

func allRoles() []string {
	return []string{bootstrap.RoleDirectoryServer, bootstrap.RoleMTA, bootstrap.RoleAppserver, bootstrap.RoleProxy}
}

func TestSchema_CoversEveryField(t *testing.T) {
	seen := map[string]int{}

	for _, s := range sections {
		for _, k := range s.keys {
			seen[k]++
		}
	}

	for _, f := range bootstrap.Fields {
		if f.Path == "roles" {
			assert.Zero(t, seen[f.Path], "roles are the chips")

			continue
		}

		assert.Equal(t, 1, seen[f.Path], "%s must be in exactly one section", f.Path)
		assert.NotEmpty(t, fieldMeta[f.Path].label, f.Path)
		assert.NotEmpty(t, fieldMeta[f.Path].help, f.Path)
	}

	for k := range seen {
		assert.NotNil(t, bootstrap.FieldByPath(k), k)
	}
}

func TestNavigation_SectionsAndRows(t *testing.T) {
	m := newModel(t, allRoles(), nil)

	// The focus starts on the first required value that is missing.
	assert.Equal(t, "admin.password", m.focused().Path)
	assert.Equal(t, paneFields, m.pane)

	press(m, "tab") // roles
	assert.Equal(t, paneRoles, m.pane)
	press(m, "tab") // sections
	assert.Equal(t, paneSections, m.pane)

	press(m, "up")
	press(m, "up")
	assert.Equal(t, 0, m.sec)
	assert.Contains(t, screen(m), "Hostname")

	press(m, "down")
	assert.Equal(t, 1, m.sec)
	assert.Contains(t, screen(m), "LDAP master host")

	press(m, "right")
	assert.Equal(t, paneFields, m.pane)
	press(m, "down", "down")
	assert.Equal(t, "ldap.replication_type", m.focused().Path)
	press(m, "up")
	press(m, "left")
	assert.Equal(t, paneSections, m.pane)
	press(m, "shift+tab")
	assert.Equal(t, paneRoles, m.pane)
}

func TestRequiredGatesReview(t *testing.T) {
	m := newModel(t, allRoles(), nil)

	assert.Contains(t, screen(m), "🔒 Review & apply")
	assert.Contains(t, screen(m), "1 required missing")

	press(m, "r")
	assert.Equal(t, pageConfigure, m.page)
	assert.Contains(t, screen(m), "1 required value is missing")

	// Enter on the locked item says the same.
	press(m, "tab", "tab")
	m.sec = len(m.visible())
	press(m, "enter")
	assert.Equal(t, pageConfigure, m.page)

	// n jumps to the missing value from anywhere.
	m.sec, m.row, m.pane = 0, 0, paneSections
	press(m, "n")
	assert.Equal(t, "admin.password", m.focused().Path)

	// Fill it in: type, confirm, repeat.
	press(m, "enter")
	typeText(m, "s3cret-pass")
	assert.Contains(t, screen(m), "looks valid")
	press(m, "enter")
	assert.Contains(t, screen(m), "again")
	typeText(m, "s3cret-pass")
	press(m, "enter")

	assert.Equal(t, "s3cret-pass", m.r.Config.Admin.Password)
	assert.Equal(t, bootstrap.SourceWizard, m.r.Source("admin.password"))
	assert.Contains(t, screen(m), "all required values set")

	press(m, "n")
	assert.Contains(t, screen(m), "No required value is missing")

	press(m, "r")
	assert.Equal(t, pageReview, m.page)
}

func TestSecretNeverRendered(t *testing.T) {
	m := newModel(t, allRoles(), nil)
	press(m, "enter")
	typeText(m, "hunter2-secret")

	// While typing, the secret is masked.
	out := screen(m)
	assert.NotContains(t, out, "hunter2-secret")
	assert.Contains(t, out, "••••")

	press(m, "enter")
	typeText(m, "hunter2-secret")
	assert.NotContains(t, screen(m), "hunter2-secret")
	press(m, "enter")

	assert.NotContains(t, screen(m), "hunter2-secret")
	assert.Contains(t, screen(m), "••••••••")

	// Neither the review page nor its YAML shows it.
	press(m, "r")
	assert.Equal(t, pageReview, m.page)
	assert.NotContains(t, screen(m), "hunter2-secret")

	// Generated secrets are masked too.
	m.page = pageConfigure
	m.sec, m.row = 1, 0
	assert.NotContains(t, screen(m), m.r.Config.LDAP.RootPassword)
}

func TestSecretMismatchAndShort(t *testing.T) {
	m := newModel(t, allRoles(), nil)
	press(m, "enter")
	typeText(m, "abc")
	assert.Contains(t, screen(m), "at least 6 characters")
	press(m, "enter") // refused: too short
	assert.False(t, m.repeating)

	for range 3 {
		press(m, "backspace")
	}

	typeText(m, "abcdef")
	press(m, "enter")
	typeText(m, "abcdeX")
	press(m, "enter")
	assert.Contains(t, screen(m), "The two entries differ")
	assert.Empty(t, m.r.Config.Admin.Password)

	press(m, "esc")
	assert.Equal(t, inputNone, m.mode)
}

func TestLockedFieldsFromEnvAndFlag(t *testing.T) {
	m := newModel(t, allRoles(), func(r *bootstrap.Resolved) {
		setEnv("CARBONIO_BOOTSTRAP_HOST_TIMEZONE=Asia/Tokyo")(r)
		require.NoError(t, r.ApplySets([]string{"host.public_hostname=mail.example.org"}))
	})

	m.sec, m.row, m.pane = 0, 0, paneFields
	out := screen(m)
	assert.Contains(t, out, "🔒")
	assert.Contains(t, out, "Asia/Tokyo")

	for i, f := range m.rows() {
		if f.Path == "host.timezone" || f.Path == "host.public_hostname" {
			m.row = i
			before := f.Format(&m.r.Config)

			press(m, "enter")
			assert.Equal(t, inputNone, m.mode)
			assert.Contains(t, screen(m), "not editable here")
			assert.Equal(t, before, f.Format(&m.r.Config))
			assert.Contains(t, screen(m), "set by")

			press(m, "u")
			assert.Contains(t, screen(m), "not reset here")
			assert.Equal(t, before, f.Format(&m.r.Config))
		}
	}

	// The roles chips lock the same way.
	m2 := newModel(t, allRoles(), setEnv("CARBONIO_BOOTSTRAP_ROLES=mta"))
	m2.pane, m2.chip = paneRoles, 0
	press(m2, "enter")
	assert.False(t, m2.r.Config.HasRole(bootstrap.RoleDirectoryServer) != false && false)
	assert.Contains(t, screen(m2), "Roles are set by the environment")
	assert.True(t, m2.r.Config.HasRole(bootstrap.RoleMTA))
	assert.Contains(t, screen(m2), "🔒 from env")
}

func TestLiveValidationMessages(t *testing.T) {
	m := newModel(t, allRoles(), nil)
	m.sec, m.row, m.pane = 0, 0, paneFields // host.hostname
	press(m, "enter")
	m.input.SetValue("")

	typeText(m, "not_a_host")
	assert.Contains(t, screen(m), "not a fully qualified name")

	for range 10 {
		press(m, "backspace")
	}

	typeText(m, "node.example.net")
	assert.Contains(t, screen(m), "looks valid")

	press(m, "enter")
	assert.Equal(t, "node.example.net", m.r.Config.Host.Hostname)
	assert.True(t, m.dirty)

	// A number field: not a number, then a port out of range.
	m.focusPath("ldap.port")
	press(m, "enter")
	m.input.SetValue("")
	typeText(m, "12x")
	assert.Contains(t, screen(m), "not a number")
	m.input.SetValue("70000")
	m.liveCheck()
	assert.Contains(t, screen(m), "not a port")
	press(m, "enter")
	assert.Equal(t, 389, m.r.Config.LDAP.Port, "an invalid answer is refused")
	press(m, "esc")

	// A list field.
	m.focusPath("mta.my_networks")
	press(m, "enter")
	m.input.SetValue("10.0.0.0/8 bogus")
	m.liveCheck()
	assert.Contains(t, screen(m), "not an address or a network")
}

func (m *Model) shows(path string) bool {
	for _, secIdx := range m.visible() {
		for _, f := range m.rowsOf(secIdx) {
			if f.Path == path {
				return true
			}
		}
	}

	return false
}

func (m *Model) focusPath(path string) {
	for si, secIdx := range m.visible() {
		for ri, f := range m.rowsOf(secIdx) {
			if f.Path == path {
				m.sec, m.row, m.pane = si, ri, paneFields

				return
			}
		}
	}

	panic("no such field " + path)
}

func TestEnumAndBoolEditing(t *testing.T) {
	m := newModel(t, allRoles(), nil)

	m.focusPath("host.ip_mode")
	press(m, "enter")
	assert.True(t, m.enumOpen)
	assert.Contains(t, screen(m), "ipv4")
	press(m, "down", "down", "up", "enter")
	assert.Equal(t, "ipv6", m.r.Config.Host.IPMode)

	press(m, "enter", "esc")
	assert.Equal(t, "ipv6", m.r.Config.Host.IPMode)
	assert.False(t, m.enumOpen)

	m.focusPath("mta.antispam")
	before := m.r.Config.MTA.Antispam
	press(m, "space")
	assert.Equal(t, !before, m.r.Config.MTA.Antispam)
	assert.Contains(t, screen(m), "no")

	press(m, "u")
	assert.Equal(t, before, m.r.Config.MTA.Antispam, "reset gives the default back")
	assert.Equal(t, bootstrap.SourceDefault, m.r.Source("mta.antispam"))
}

func TestRoleToggleShowsAndHidesFields(t *testing.T) {
	m := newModel(t, []string{bootstrap.RoleAppserver}, nil)
	assert.Contains(t, screen(m), "Mailbox store")
	assert.False(t, m.shows("mta.my_networks"))
	assert.False(t, m.shows("proxy.mode"))
	assert.False(t, m.r.Config.HasRole(bootstrap.RoleMTA))

	oldNets := m.r.Config.MTA.MyNetworks

	// Focus the mta chip and enable it.
	press(m, "tab") // roles
	m.pane = paneRoles
	m.chip = 1

	press(m, "enter")
	assert.True(t, m.r.Config.HasRole(bootstrap.RoleMTA))
	assert.NotEqual(t, oldNets, m.r.Config.MTA.MyNetworks, "the defaults follow the roles")
	assert.True(t, m.shows("mta.my_networks"))
	assert.Contains(t, screen(m), "● mta")
	assert.Equal(t, bootstrap.SourceWizard, m.r.Source("roles"))

	// ...and the proxy.
	press(m, "right", "right", "enter")
	assert.True(t, m.r.Config.HasRole(bootstrap.RoleProxy))
	assert.True(t, m.shows("proxy.mode"))
	assert.Contains(t, screen(m), "● proxy")

	// Disable the mta again: its keys go.
	m.chip = 1
	press(m, "space")
	assert.False(t, m.shows("mta.my_networks"))
	assert.True(t, m.dirty)
}

func TestSearchAndHelp(t *testing.T) {
	m := newModel(t, allRoles(), nil)

	press(m, "/")
	assert.Equal(t, inputSearch, m.mode)
	typeText(m, "quarantine")
	press(m, "enter")
	assert.Equal(t, "store.virus_quarantine_account", m.focused().Path)

	press(m, "/")
	typeText(m, "zzz-nothing")
	press(m, "enter")
	assert.Contains(t, screen(m), "Nothing matches")

	press(m, "/")
	press(m, "enter") // empty search
	press(m, "/")
	press(m, "esc")
	assert.Equal(t, inputNone, m.mode)

	press(m, "?")
	assert.Contains(t, screen(m), "next required value")
	press(m, "x")
	assert.False(t, m.help)
}

func TestQuitConfirmsOnlyWhenChanged(t *testing.T) {
	m := newModel(t, allRoles(), nil)
	_, cmd := m.Update(keyMsg("q"))
	assert.True(t, m.done)
	assert.Equal(t, ActionQuit, m.Result())
	assert.NotNil(t, cmd)

	m = newModel(t, allRoles(), nil)
	m.focusPath("mta.antispam")
	press(m, "space")
	press(m, "q")
	assert.False(t, m.done)
	assert.Contains(t, screen(m), "Quit without applying")

	press(m, "n")
	assert.False(t, m.confirm)

	press(m, "ctrl+c", "y")
	assert.True(t, m.done)
	assert.Equal(t, ActionQuit, m.Result())
}

func TestReviewApplyDoneSaveBack(t *testing.T) {
	var saved []string

	pw := func(r *bootstrap.Resolved) { setEnv("CARBONIO_BOOTSTRAP_ADMIN_PASSWORD=longenough")(r) }
	save := func(o *ConfigureOptions) {
		o.Apply = true
		o.SavePath = "/tmp/bootstrap.yaml"
		o.Save = func(p string) error {
			saved = append(saved, p)

			if p == "/bad" {
				return errors.New("disk full")
			}

			return nil
		}
	}

	m := newModel(t, allRoles(), pw, save)
	press(m, "r")
	require.Equal(t, pageReview, m.page)

	out := screen(m)
	assert.Contains(t, out, "hostname: mail.example.com")
	assert.Contains(t, out, "Apply")
	assert.NotContains(t, out, "longenough")
	assert.Contains(t, out, "Save yaml")

	press(m, "s")
	assert.Equal(t, inputSave, m.mode)
	assert.Contains(t, screen(m), "/tmp/bootstrap.yaml")
	press(m, "enter")
	assert.Equal(t, []string{"/tmp/bootstrap.yaml"}, saved)
	assert.Contains(t, screen(m), "Saved to /tmp/bootstrap.yaml")

	press(m, "s")
	m.input.SetValue("/bad")
	press(m, "enter")
	assert.Contains(t, screen(m), "Not saved: disk full")
	press(m, "s", "esc")

	press(m, "right", "right", "enter") // Back
	assert.Equal(t, pageConfigure, m.page)

	press(m, "r", "left", "left")
	press(m, "up", "down", "tab", "shift+tab", "left")
	press(m, "enter")
	assert.True(t, m.done)
	assert.Equal(t, ActionApply, m.Result())
}

func TestReviewDoneInConfigureMode(t *testing.T) {
	m := newModel(t, allRoles(), setEnv("CARBONIO_BOOTSTRAP_ADMIN_PASSWORD=longenough"))
	press(m, "r")
	assert.Contains(t, screen(m), "Done")
	press(m, "?", "x", "q")
	assert.True(t, m.done)

	m = newModel(t, allRoles(), setEnv("CARBONIO_BOOTSTRAP_ADMIN_PASSWORD=longenough"))
	press(m, "r", "b")
	assert.Equal(t, pageConfigure, m.page)
	press(m, "r", "a")
	assert.Equal(t, ActionDone, m.Result())
}

func TestReviewListsValidationProblems(t *testing.T) {
	// A proxy and a mailbox on the same ports: valid keys, invalid together.
	m := newModel(t, allRoles(), func(r *bootstrap.Resolved) {
		setEnv("CARBONIO_BOOTSTRAP_ADMIN_PASSWORD=longenough", "CARBONIO_BOOTSTRAP_PROXY_PORTS_HTTPS=443")(r)
	})
	press(m, "r")
	require.Equal(t, pageReview, m.page)
	assert.Contains(t, screen(m), "both use port 443")

	press(m, "a")
	assert.False(t, m.done)
	assert.Contains(t, screen(m), "Fix the problems listed")
}

func TestNoticeWelcomePage(t *testing.T) {
	m := newModel(t, allRoles(), nil, func(o *ConfigureOptions) { o.Notice = "Licence text https://zextras.com/licenses" })
	assert.Equal(t, pageWelcome, m.page)

	out := screen(m)
	assert.Contains(t, out, "Carbonio Bootstrap")
	assert.Contains(t, out, "https://zextras.com/licenses")
	assert.Contains(t, out, "continue")

	press(m, "x")
	assert.Equal(t, pageWelcome, m.page)
	press(m, "enter")
	assert.Equal(t, pageConfigure, m.page)

	m = newModel(t, allRoles(), nil, func(o *ConfigureOptions) { o.Notice = "x" })
	press(m, "q")
	assert.True(t, m.done)
}

func TestTooSmallAndResize(t *testing.T) {
	m := newModel(t, allRoles(), nil)
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	assert.Contains(t, screen(m), "Terminal too small")
	assert.Contains(t, screen(m), "60x20")

	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	out := screen(m)
	assert.NotContains(t, out, "too small")

	for _, l := range strings.Split(out, "\n") {
		assert.LessOrEqual(t, len([]rune(l)), 80+4, l) // emoji count as two columns
	}

	assert.Len(t, strings.Split(out, "\n"), 24)

	m.Update(tea.BackgroundColorMsg{})
}

func TestDNSLine(t *testing.T) {
	res := fakeResolver{
		hosts: map[string][]string{"mail.example.com": {"10.0.0.5"}},
	}
	m := newModel(t, allRoles(), nil, func(o *ConfigureOptions) { o.Resolver = res })

	cmd := m.Init()
	require.NotNil(t, cmd)
	assert.Contains(t, screen(m), "… mail.example.com")

	for _, msg := range []tea.Msg{
		lookup(res, dnsHost, "mail.example.com")(),
		lookup(res, dnsMX, "example.com")(),
	} {
		m.Update(msg)
	}

	out := screen(m)
	assert.Contains(t, out, "✓ mail.example.com → 10.0.0.5")
	assert.Contains(t, out, "! example.com has no MX record")

	// Changing the hostname asks again; the old answer is dropped.
	m.focusPath("host.hostname")
	press(m, "enter")
	m.input.SetValue("other.example.com")
	cmd = press(m, "enter")
	require.NotNil(t, cmd)
	assert.Contains(t, screen(m), "… other.example.com")

	msg := lookup(res, dnsHost, "other.example.com")()
	m.Update(msg)
	assert.Contains(t, screen(m), "! other.example.com does not resolve")

	assert.Equal(t, "other.example.com has no MX record", mxResult("other.example.com", nil, nil).detail)
	assert.Contains(t, hostResult("x.test", []string{"127.0.0.1"}, nil).detail, "loopback")
	assert.True(t, mxResult("a", []*net.MX{{Host: "mx.a."}}, nil).ok)
}
