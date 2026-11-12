// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
)

const (
	leftWidth  = 28
	helpHeight = 7
)

// View renders the screen.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "Carbonio Bootstrap"

	return v
}

func (m *Model) render() string {
	if m.w < minWidth || m.h < minHeight {
		return tooSmall(m.w, m.h)
	}

	switch {
	case m.help:
		return m.helpScreen()
	case m.page == pageWelcome:
		return welcomeView(&m.th, m.opts.Notice, m.w, m.h)
	case m.page == pageReview:
		return m.reviewView()
	}

	body := lipgloss.JoinHorizontal(lipgloss.Top, m.leftPane(m.h-5), m.rightPane(m.h-5))

	head := m.header("configure › " + m.crumb())

	return strings.Join([]string{head, m.roleLine(), body, m.statusLine(), m.keyLine()}, "\n")
}

func (m *Model) crumb() string {
	vis := m.visible()
	if m.sec >= len(vis) {
		return "review & apply"
	}

	return strings.ToLower(sections[vis[m.sec]].title)
}

func (m *Model) header(crumb string) string {
	left := m.th.title.Render("CARBONIO BOOTSTRAP") + "  " + m.th.crumb.Render(crumb)

	host := m.r.Config.Host.Hostname
	right := m.th.muted.Render(joinNonEmpty(" · ", host, m.opts.Info.OS, m.opts.Info.Install))

	gap := max(1, m.w-lipgloss.Width(left)-lipgloss.Width(right)-2)

	return fit(" "+left+strings.Repeat(" ", gap)+right, m.w)
}

func joinNonEmpty(sep string, parts ...string) string {
	var out []string

	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}

	return strings.Join(out, sep)
}

func (m *Model) roleLine() string {
	var b strings.Builder

	b.WriteString(" " + m.th.muted.Render("Roles") + "  ")

	for i, role := range bootstrap.Roles {
		chip := "○ " + role
		st := m.th.muted

		if m.r.Config.HasRole(role) {
			chip, st = "● "+role, m.th.ok
		}

		if m.pane == paneRoles && i == m.chip {
			st = m.th.selected
		}

		b.WriteString(st.Render(chip) + "  ")
	}

	hint := ""
	if m.pane == paneRoles {
		hint = " · ←→ move, ⏎ toggle"
	}

	return fit(b.String(), m.w) + "\n" + fit("        "+m.th.muted.Render(m.roleNote()+hint), m.w)
}

func (m *Model) roleNote() string {
	f := bootstrap.FieldByPath("roles")
	note := "from " + m.r.Source(f.Path).String()

	switch m.r.Source(f.Path) {
	case bootstrap.SourceDetected:
		note = "from installed packages"
	case bootstrap.SourceUnset:
		note = "no package detected: choose the roles of this node"
	}

	if m.locked(f) {
		note = "🔒 " + note
	}

	return note
}

// sectionState is the status glyph of a section: complete, N required
// missing, or optional and untouched.
func (m *Model) sectionState(secIdx int) (glyph string, missing int) {
	touched := false
	hasRequired := false

	for _, f := range m.rowsOf(secIdx) {
		if m.missing(f) {
			missing++
		}

		hasRequired = hasRequired || bootstrap.IsRequired(f, &m.r.Config)
		touched = touched || m.r.Source(f.Path) == bootstrap.SourceWizard
	}

	switch {
	case missing > 0:
		return "●", missing
	case hasRequired || touched:
		return "✓", 0
	default:
		return "○", 0
	}
}

func (m *Model) leftPane(h int) string {
	var lines []string

	vis := m.visible()

	for i, secIdx := range vis {
		glyph, missing := m.sectionState(secIdx)
		lines = append(lines, m.sectionLine(i, glyph, sections[secIdx].title, missing))
	}

	lines = append(lines, "")

	review := "Review & apply"
	if n := len(m.missingAll()); n > 0 {
		review = "🔒 " + review
	} else {
		review = "→ " + review
	}

	line := " " + review
	if m.sec == len(vis) {
		line = m.cursorStyle().Render(fit(line, leftWidth-2))
	}

	lines = append(lines, line)

	st, tst := &m.th.border, &m.th.muted
	if m.pane == paneSections {
		st, tst = &m.th.borderFocus, &m.th.title
	}

	return box("Sections", lines, leftWidth, h, st, tst)
}

func (m *Model) cursorStyle() lipgloss.Style {
	return m.th.selected
}

func (m *Model) sectionLine(i int, glyph, title string, missing int) string {
	gst := m.th.ok

	switch glyph {
	case "●":
		gst = m.th.bad
	case "○":
		gst = m.th.muted
	}

	count := ""
	if missing > 0 {
		count = fmt.Sprintf(" %d req", missing)
	}

	text := fit(" "+glyph+" "+title, leftWidth-2-len(count)) + count

	if i == m.sec && m.pane == paneSections {
		return m.th.selected.Render(text)
	}

	out := " " + gst.Render(glyph) + " " + title

	if i == m.sec {
		out = m.th.selectedDim.Render(" " + glyph + " " + title)
	}

	return fit(out, leftWidth-2-len(count)) + m.th.bad.Render(count)
}

func (m *Model) rightPane(h int) string {
	w := m.w - leftWidth
	inner := w - 2

	st, tst := &m.th.border, &m.th.muted
	if m.pane == paneFields {
		st, tst = &m.th.borderFocus, &m.th.title
	}

	vis := m.visible()
	if m.sec >= len(vis) {
		return box("Review & apply", pad([]string{"", "Press ⏎ or r to review the configuration."}), w, h, st, tst)
	}

	sec := sections[vis[m.sec]]
	rows := m.rows()
	window := max(1, h-2-4-helpHeight)

	lines := []string{"", " " + m.th.title.Render(sec.title), " " + m.th.muted.Render(sec.desc), ""}
	lines = append(lines, " "+m.th.muted.Render(m.columns(inner-2).header()))

	start := min(max(0, m.row-window+1), max(0, len(rows)-window))
	end := min(len(rows), start+window)

	for i := start; i < end; i++ {
		lines = append(lines, m.fieldLine(rows[i], i == m.row && m.pane == paneFields, inner))
	}

	if end < len(rows) {
		lines[len(lines)-1] = " " + m.th.muted.Render(fmt.Sprintf("⋮ %d more", len(rows)-end+1))
	}

	for len(lines) < h-2-helpHeight {
		lines = append(lines, "")
	}

	lines = append(lines, m.helpBox(inner)...)

	return box(sec.title, lines, w, h, st, tst)
}

type cols struct{ label, value, source int }

func (m *Model) columns(inner int) cols {
	src := 9
	rest := inner - 3 - src - 2
	label := min(rest/2, 26)

	return cols{label: label, value: rest - label, source: src}
}

func (c cols) header() string {
	return "   " + fit("SETTING", c.label) + " " + fit("VALUE", c.value) + " " + "SOURCE"
}

func (m *Model) fieldLine(f *bootstrap.Field, focus bool, inner int) string {
	c := m.columns(inner - 2)
	text, vst := m.valueCell(f)
	src := m.r.Source(f.Path)
	srcText := ""

	if src != bootstrap.SourceUnset {
		srcText = src.String()
	}

	cursor, lock := " ", "  "
	if focus {
		cursor = "▸"
	}

	if m.locked(f) {
		lock = "🔒"
	}

	if focus {
		row := cursor + lock + fit(labelOf(f), c.label) + " " + fit(text, c.value) + " " + fit(srcText, c.source)

		return " " + m.th.selected.Render(row)
	}

	label := cursor + lock + fit(labelOf(f), c.label)

	return " " + label + " " + vst.Render(fit(text, c.value)) + " " + m.th.muted.Render(fit(srcText, c.source))
}

// valueCell is the text and style of the value of f: secrets are never shown.
func (m *Model) valueCell(f *bootstrap.Field) (string, lipgloss.Style) {
	set := !f.IsZero(&m.r.Config)

	switch {
	case f.TypeName() == "bool" && m.r.Source(f.Path) != bootstrap.SourceUnset:
		if f.Format(&m.r.Config) == "true" {
			return "yes", m.th.label
		}

		return "no", m.th.label
	case f.Secret && set:
		return "••••••••", m.th.secret
	case !set && m.missing(f):
		return "required", m.th.bad
	case !set:
		return "—", m.th.muted
	default:
		return f.Format(&m.r.Config), m.th.label
	}
}

// helpBox is the inline help of the focused key: the value being typed and
// its validation, the help text, the YAML key and the environment variable.
func (m *Model) helpBox(inner int) []string {
	w := inner - 4
	lines := []string{" " + m.th.muted.Render(strings.Repeat("─", inner-2))}

	f := m.focused()
	if f == nil || m.pane != paneFields {
		lines = append(lines, " "+m.th.muted.Render("Tab switches between the roles, the sections and the settings."))

		return padTo(lines, helpHeight)
	}

	lines = append(lines, m.interaction(f, w)...)

	for _, l := range wrapText(fieldMeta[f.Path].help, w) {
		lines = append(lines, " "+m.th.muted.Render(l))
	}

	env := f.Env
	if f.Secret {
		env += "[_FILE]"
	}

	lines = append(lines, " "+m.th.muted.Render("yaml: ")+f.Path+m.th.muted.Render("  env: ")+env)

	if dns := m.dnsLine(); dns != "" {
		lines = append(lines, " "+dns)
	}

	return padTo(lines, helpHeight)
}

func padTo(lines []string, n int) []string {
	for len(lines) < n {
		lines = append(lines, "")
	}

	return lines[:n]
}

// interaction is the line about the state of the key: editing, locked, a
// problem with the stored value, or a choice.
func (m *Model) interaction(f *bootstrap.Field, w int) []string {
	switch {
	case m.mode == inputEdit && m.field == f:
		return []string{" › " + m.inputView(f), " " + m.validation()}
	case m.enumOpen && m.field == f:
		return []string{" " + m.enumView(f), " " + m.th.muted.Render("↑↓ choose, ⏎ accept, esc cancel")}
	case m.locked(f):
		why := "🔒 set by " + m.lockedBy(f) + " (" + m.r.Source(f.Path).String() + "), not editable here"

		return []string{" " + m.th.locked.Render(why)}
	case m.missing(f):
		return []string{" " + m.th.bad.Render("! "+f.Path+" is required")}
	}

	if f.IsZero(&m.r.Config) {
		return nil
	}

	if err := bootstrap.CheckValue(f, &m.r.Config); err != nil {
		return []string{" " + m.th.bad.Render("✗ "+fitText(err.Error(), w))}
	}

	return nil
}

func fitText(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}

	return fit(s, w)
}

func (m *Model) inputView(f *bootstrap.Field) string {
	if f.Secret && m.repeating {
		return m.th.muted.Render("repeat: ") + m.input.View()
	}

	return m.input.View()
}

func (m *Model) validation() string {
	switch {
	case m.inputErr != "":
		return m.th.bad.Render("✗ " + m.inputErr)
	case m.repeating:
		return m.th.muted.Render("Type the secret again to confirm.")
	case m.input.Value() == "":
		return m.th.muted.Render("⏎ accept, esc cancel")
	default:
		return m.th.ok.Render("✓ looks valid")
	}
}

func (m *Model) enumView(f *bootstrap.Field) string {
	parts := make([]string, len(f.Enum))

	for i, e := range f.Enum {
		parts[i] = " " + e + " "
		if i == m.enumSel {
			parts[i] = m.th.selected.Render(parts[i])
		}
	}

	return strings.Join(parts, " ")
}

// statusLine counts what is missing and where the values come from.
func (m *Model) statusLine() string {
	left := m.th.ok.Render(" ✓ all required values set")
	if n := len(m.missingAll()); n > 0 {
		left = m.th.bad.Render(fmt.Sprintf(" ● %d required missing", n))
	}

	text := left + m.th.muted.Render("  "+m.sourceCounts())
	if m.msg != "" {
		st := m.th.ok
		if m.msgBad {
			st = m.th.bad
		}

		text = left + "  " + st.Render(m.msg)
	}

	return fit(text, m.w)
}

func (m *Model) sourceCounts() string {
	counts := map[bootstrap.Source]int{}

	for _, f := range bootstrap.Fields {
		if bootstrap.Applies(f, &m.r.Config) && !f.IsZero(&m.r.Config) {
			counts[m.r.Source(f.Path)]++
		}
	}

	var parts []string

	for src := bootstrap.SourceDefault; src <= bootstrap.SourceWizard; src++ {
		if n := counts[src]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", src, n))
		}
	}

	return strings.Join(parts, " · ")
}

func (m *Model) keyLine() string {
	if m.confirm {
		ask := m.th.warn.Render("Quit without applying the configuration?")

		return fit(" "+ask+" "+m.th.key.Render("y")+" quit  "+m.th.key.Render("n")+" stay", m.w)
	}

	if m.mode != inputNone || m.enumOpen {
		return fit(" "+m.th.key.Render("⏎")+" accept  "+m.th.key.Render(keyEsc)+" cancel", m.w)
	}

	hints := [][2]string{
		{"↑↓", "move"}, {"⏎", "edit"}, {keyTab, "pane"}, {"n", "next required"}, {"/", "search"},
		{"u", "reset"}, {"s", "save"}, {"r", "review"}, {"?", "help"}, {"q", hintQuit},
	}

	return fit(" "+hintLine(&m.th, hints), m.w)
}

func hintLine(th *theme, hints [][2]string) string {
	parts := make([]string, len(hints))
	for i, h := range hints {
		parts[i] = th.key.Render(h[0]) + " " + th.muted.Render(h[1])
	}

	return strings.Join(parts, "  ")
}

func (m *Model) helpScreen() string {
	rows := [][2]string{
		{"↑ ↓  j k", "move between sections or settings"},
		{"tab / shift+tab", "switch pane: roles, sections, settings"},
		{"⏎ / space", "edit the setting, toggle a switch or a role"},
		{"n", "jump to the next required value that is missing"},
		{"/", "search settings by name or YAML key"},
		{"u", "reset the setting to its default"},
		{"s", "save the configuration as YAML (no passwords)"},
		{"r", "review the YAML and apply (locked while a required value is missing)"},
		{"q / ctrl+c", "quit (asks first when there are changes)"},
		{"🔒", "set by the environment or a --set flag: not editable here"},
	}

	lines := []string{""}
	for _, r := range rows {
		lines = append(lines, " "+m.th.key.Render(fit(r[0], 18))+" "+r[1])
	}

	lines = append(lines, "", " "+m.th.muted.Render("Press any key to go back."))

	return m.header("help") + "\n" + box("Keys", lines, m.w, m.h-1, &m.th.borderFocus, &m.th.title)
}
