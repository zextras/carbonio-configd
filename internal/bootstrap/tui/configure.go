// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
)

// Action is what the operator chose when the configuration screen ended.
type Action int

// The ways the configuration screen ends.
const (
	// ActionQuit: the operator left without accepting the configuration.
	ActionQuit Action = iota
	// ActionDone: the configuration is accepted (configure only).
	ActionDone
	// ActionApply: the configuration is accepted and the node is to be
	// configured with it.
	ActionApply
)

// Info is what the header says about the node.
type Info struct {
	// OS is the operating system, e.g. "Ubuntu 24.04".
	OS string
	// Install is "new install", "existing install" or "".
	Install string
}

// ConfigureOptions are the inputs of the configuration screen.
type ConfigureOptions struct {
	// Resolved is edited in place.
	Resolved *bootstrap.Resolved
	// Host recomputes the defaults after each change.
	Host bootstrap.Host
	// Resolver, if set, checks the hostname and the domain MX in DNS.
	Resolver bootstrap.Resolver
	Info     Info
	// Apply: the review page offers Apply (bootstrap run) instead of Done.
	Apply bool
	// Notice, if set, is shown on a welcome page first.
	Notice string
	// SavePath is the file the review page proposes for the YAML.
	SavePath string
	// Save writes the configuration (no secrets) to path.
	Save func(path string) error
	// NoColor draws without colours (NO_COLOR).
	NoColor bool
}

type pane int

const (
	paneRoles pane = iota
	paneSections
	paneFields
)

type page int

const (
	pageWelcome page = iota
	pageConfigure
	pageReview
)

type inputMode int

const (
	inputNone inputMode = iota
	inputEdit
	inputSearch
	inputSave
)

// Model is the configuration screen.
type Model struct {
	opts ConfigureOptions
	r    *bootstrap.Resolved
	th   theme

	w, h int
	page page
	pane pane

	chip int
	sec  int
	row  int

	input     textinput.Model
	mode      inputMode
	field     *bootstrap.Field
	inputErr  string
	firstPass string
	repeating bool

	enumOpen bool
	enumSel  int

	msg     string
	msgBad  bool
	dirty   bool
	confirm bool
	help    bool

	dns    map[string]dnsResult
	dnsAsk map[string]string

	vp        viewport.Model
	reviewErr []string
	button    int

	action Action
	done   bool
}

// NewConfigure returns the configuration screen; the first section with a
// missing required key has the focus.
func NewConfigure(opts *ConfigureOptions) *Model {
	m := &Model{
		opts:   *opts,
		r:      opts.Resolved,
		th:     newTheme(true, opts.NoColor),
		w:      minWidth,
		h:      minHeight,
		page:   pageConfigure,
		pane:   paneFields,
		input:  textinput.New(),
		dns:    map[string]dnsResult{},
		dnsAsk: map[string]string{},
		vp:     viewport.New(),
	}
	m.input.Prompt = ""

	if opts.Notice != "" {
		m.page = pageWelcome
	}

	m.focusFirstMissing()

	return m
}

// Result is how the screen ended.
func (m *Model) Result() Action { return m.action }

// Init asks the terminal for its background and starts the DNS checks.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.dnsCmds())
}

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.resizeReview()
	case tea.BackgroundColorMsg:
		m.th = newTheme(msg.IsDark(), m.opts.NoColor)
	case dnsMsg:
		m.dns[msg.kind] = msg.result
	case tea.KeyPressMsg:
		cmd := m.key(msg)
		if m.done {
			return m, tea.Quit
		}

		return m, cmd
	}

	return m, nil
}

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	k := msg.String()

	switch {
	case m.help:
		m.help = false
	case m.confirm:
		m.keyConfirm(k)
	case m.page == pageWelcome:
		m.keyWelcome(k)
	case m.mode != inputNone:
		return m.keyInput(k, msg)
	case m.enumOpen:
		return m.keyEnum(k)
	case m.page == pageReview:
		return m.keyReview(k)
	default:
		return m.keyConfigure(k)
	}

	return nil
}

func (m *Model) keyConfirm(k string) {
	switch k {
	case "y", "Y":
		m.finish(ActionQuit)
	default:
		m.confirm = false
	}
}

func (m *Model) keyWelcome(k string) {
	switch k {
	case keyEnter:
		m.page = pageConfigure
	case "q", keyCtrlC:
		m.finish(ActionQuit)
	}
}

func (m *Model) finish(a Action) {
	m.action = a
	m.done = true
}

func (m *Model) quit() {
	if m.dirty {
		m.confirm = true

		return
	}

	m.finish(ActionQuit)
}

func (m *Model) keyConfigure(k string) tea.Cmd {
	m.msg = ""

	if fn, ok := configureKeys[k]; ok {
		return fn(m)
	}

	return nil
}

var configureKeys = map[string]func(*Model) tea.Cmd{
	"q":         func(m *Model) tea.Cmd { m.quit(); return nil },
	keyCtrlC:    func(m *Model) tea.Cmd { m.quit(); return nil },
	"?":         func(m *Model) tea.Cmd { m.help = true; return nil },
	keyTab:      func(m *Model) tea.Cmd { m.cyclePane(1); return nil },
	"shift+tab": func(m *Model) tea.Cmd { m.cyclePane(-1); return nil },
	"up":        func(m *Model) tea.Cmd { m.move(-1); return nil },
	"k":         func(m *Model) tea.Cmd { m.move(-1); return nil },
	keyDown:     func(m *Model) tea.Cmd { m.move(1); return nil },
	"j":         func(m *Model) tea.Cmd { m.move(1); return nil },
	keyLeft:     func(m *Model) tea.Cmd { m.sideways(-1); return nil },
	keyRight:    func(m *Model) tea.Cmd { m.sideways(1); return nil },
	keyEnter:    (*Model).activate,
	keySpace:    (*Model).activate,
	"n":         func(m *Model) tea.Cmd { m.nextRequired(); return nil },
	"/":         func(m *Model) tea.Cmd { m.startInput(inputSearch, ""); return nil },
	"u":         (*Model).resetField,
	"s":         func(m *Model) tea.Cmd { m.startInput(inputSave, m.opts.SavePath); return nil },
	"r":         func(m *Model) tea.Cmd { m.openReview(); return nil },
}

func (m *Model) cyclePane(d int) {
	m.pane = pane((int(m.pane) + d + 3) % 3)
}

func (m *Model) sideways(d int) {
	switch m.pane {
	case paneRoles:
		m.chip = (m.chip + d + len(bootstrap.Roles)) % len(bootstrap.Roles)
	case paneSections:
		if d > 0 {
			m.pane = paneFields
		}
	case paneFields:
		if d < 0 {
			m.pane = paneSections
		}
	}
}

func (m *Model) move(d int) {
	switch m.pane {
	case paneRoles:
		m.sideways(d)
	case paneSections:
		m.sec = min(max(m.sec+d, 0), len(m.visible()))
		m.row = 0
	case paneFields:
		m.row = min(max(m.row+d, 0), max(0, len(m.rows())-1))
	}
}

// activate is Enter or Space on the focused item.
func (m *Model) activate() tea.Cmd {
	switch m.pane {
	case paneRoles:
		return m.toggleRole(bootstrap.Roles[m.chip])
	case paneSections:
		if m.sec == len(m.visible()) {
			m.openReview()

			return nil
		}

		m.pane = paneFields
	case paneFields:
		return m.edit()
	}

	return nil
}

// toggleRole enables or disables a role and recomputes everything that
// follows from the roles.
func (m *Model) toggleRole(role string) tea.Cmd {
	f := bootstrap.FieldByPath("roles")
	if m.locked(f) {
		m.say(true, "Roles are set by "+m.lockedBy(f)+": not editable here.")

		return nil
	}

	var roles []string

	for _, r := range bootstrap.Roles {
		if m.r.Config.HasRole(r) != (r == role) {
			roles = append(roles, r)
		}
	}

	if err := m.r.Answer(f, strings.Join(roles, ",")); err != nil {
		m.say(true, err.Error())

		return nil
	}

	m.dirty = true

	return m.refresh()
}

// refresh recomputes the defaults and keeps the focus on a visible row.
func (m *Model) refresh() tea.Cmd {
	if err := m.r.Refresh(m.opts.Host); err != nil {
		m.say(true, err.Error())
	}

	m.clamp()

	return m.dnsCmds()
}

func (m *Model) clamp() {
	m.sec = min(m.sec, len(m.visible()))
	m.row = min(m.row, max(0, len(m.rows())-1))
}

func (m *Model) say(bad bool, text string) {
	m.msg, m.msgBad = text, bad
}

// resetField gives the focused key back its default.
func (m *Model) resetField() tea.Cmd {
	f := m.focused()
	if f == nil || m.pane != paneFields {
		return nil
	}

	if m.locked(f) {
		m.say(true, f.Path+" is set by "+m.lockedBy(f)+": not reset here.")

		return nil
	}

	m.r.Unset(f)
	m.dirty = true
	m.say(false, "Reset "+f.Path+" to its default.")

	return m.refresh()
}

// nextRequired moves the focus to the next key that is required and empty.
func (m *Model) nextRequired() {
	row := -1
	if m.pane == paneFields {
		row = m.row
	}

	if !m.jumpMissing(m.sec, row) {
		m.say(false, "No required value is missing.")
	}
}

// jumpMissing focuses the first required, empty key after (sec, row), wrapping
// around; it reports whether there is one.
func (m *Model) jumpMissing(sec, row int) bool {
	type pos struct{ sec, row int }

	var all []pos

	for si, secIdx := range m.visible() {
		for ri, f := range m.rowsOf(secIdx) {
			if m.missing(f) {
				all = append(all, pos{si, ri})
			}
		}
	}

	if len(all) == 0 {
		return false
	}

	i := max(0, slices.IndexFunc(all, func(p pos) bool { return p.sec > sec || (p.sec == sec && p.row > row) }))

	m.sec, m.row, m.pane = all[i].sec, all[i].row, paneFields

	return true
}

func (m *Model) focusFirstMissing() {
	if !m.jumpMissing(0, -1) {
		m.sec, m.row, m.pane = 0, 0, paneFields
	}
}

func (m *Model) missingAll() []*bootstrap.Field { return bootstrap.Missing(&m.r.Config) }

// --- derived state ---

// visible lists the indexes into sections of the sections that have a key
// that applies to the node.
func (m *Model) visible() []int {
	var out []int

	for i := range sections {
		if len(m.rowsOf(i)) > 0 {
			out = append(out, i)
		}
	}

	return out
}

// rowsOf lists the keys of section i that apply to the node.
func (m *Model) rowsOf(i int) []*bootstrap.Field {
	var out []*bootstrap.Field

	for _, p := range sections[i].keys {
		if f := bootstrap.FieldByPath(p); f != nil && bootstrap.Applies(f, &m.r.Config) {
			out = append(out, f)
		}
	}

	return out
}

// rows lists the keys of the section that has the focus.
func (m *Model) rows() []*bootstrap.Field {
	vis := m.visible()
	if m.sec >= len(vis) {
		return nil
	}

	return m.rowsOf(vis[m.sec])
}

func (m *Model) focused() *bootstrap.Field {
	rows := m.rows()
	if m.row >= len(rows) {
		return nil
	}

	return rows[m.row]
}

func (m *Model) missing(f *bootstrap.Field) bool {
	return bootstrap.IsRequired(f, &m.r.Config) && f.IsZero(&m.r.Config)
}

// locked reports whether the environment or a flag set f: the screen shows
// it but does not change it.
func (m *Model) locked(f *bootstrap.Field) bool {
	src := m.r.Source(f.Path)

	return src == bootstrap.SourceEnv || src == bootstrap.SourceFlag
}

func (m *Model) lockedBy(f *bootstrap.Field) string {
	if m.r.Source(f.Path) == bootstrap.SourceFlag {
		return "a --set flag"
	}

	return "the environment"
}
