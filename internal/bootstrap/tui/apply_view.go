// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/zextras/carbonio-configd/internal/bootstrap/brand"
)

const hintFullLog = "full log"

const (
	applyLeftWidth = 34
	logLines       = 3
)

// View renders the screen.
func (m *ApplyModel) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "Carbonio Bootstrap"

	return v
}

func (m *ApplyModel) render() string {
	switch {
	case m.w < minWidth || m.h < minHeight:
		return tooSmall(m.w, m.h)
	case m.welcome:
		return welcomeView(&m.th, m.opts.Notice, m.w, m.h)
	case m.logView:
		return m.logScreen()
	}

	bh := m.h - 2 - (logLines + 2)
	body := lipgloss.JoinHorizontal(lipgloss.Top, m.leftPane(bh), m.stepsPane(bh))

	return strings.Join([]string{m.header(), body, m.logPane(), m.keys()}, "\n")
}

func (m *ApplyModel) header() string {
	left := m.th.title.Render("CARBONIO BOOTSTRAP") + " " + m.th.muted.Render(brand.Wordmark) + "  " +
		m.th.crumb.Render("review & apply")
	right := m.th.muted.Render(joinNonEmpty(" · ", m.opts.Hostname, m.opts.Info.OS, m.opts.Info.Install))
	gap := max(1, m.w-lipgloss.Width(left)-lipgloss.Width(right)-2)

	return fit(" "+left+strings.Repeat(" ", gap)+right, m.w)
}

func (m *ApplyModel) leftPane(h int) string {
	inner := applyLeftWidth - 4
	lines := []string{"", " " + m.th.title.Render("PREFLIGHT")}

	if m.checks == nil {
		lines = append(lines, " "+m.th.muted.Render("checking…"))
	}

	for _, c := range m.checks {
		glyph, st := "✓", m.th.ok
		if c.Status == CheckWarn {
			glyph, st = "!", m.th.warn
		}

		for i, l := range wrapText(c.Text, inner) {
			g := " "
			if i == 0 {
				g = st.Render(glyph)
			}

			lines = append(lines, " "+g+" "+l)
		}
	}

	lines = append(lines, "", " "+m.th.title.Render("PLAN"))
	lines = append(lines, m.planLines(inner)...)

	return box("Preflight & plan", lines, applyLeftWidth, h, &m.th.border, &m.th.muted)
}

func (m *ApplyModel) planLines(inner int) []string {
	already := 0

	for _, p := range m.opts.Plan {
		if p.Done {
			already++
		}
	}

	lines := []string{
		" " + fmt.Sprintf("%d steps", len(m.opts.Plan)),
		" " + fmt.Sprintf("%d already done", already),
	}

	if m.opts.SavedPath != "" {
		for _, l := range wrapText("yaml saved: "+m.opts.SavedPath, inner) {
			lines = append(lines, " "+l)
		}
	}

	return lines
}

func (m *ApplyModel) stepsPane(h int) string {
	w := m.w - applyLeftWidth
	inner := w - 2

	done, total := m.progress()
	lines := []string{"", " " + m.progressBar(done, total, inner-2), ""}
	tail := m.outcomeLines(inner - 2)
	window := max(1, h-2-len(lines)-len(tail))

	lines = append(lines, m.stepLines(window, inner)...)
	lines = append(lines, tail...)

	return box("Steps", lines, w, h, &m.th.borderFocus, &m.th.title)
}

func (m *ApplyModel) progressBar(done, total, width int) string {
	label := fmt.Sprintf(" %d / %d · %s", done, total, fmtDuration(m.elapsed()))
	barW := max(4, width-lipgloss.Width(label))
	filled := 0

	if total > 0 {
		filled = barW * done / total
	}

	return m.th.ok.Render(strings.Repeat("█", filled)) + m.th.muted.Render(strings.Repeat("░", barW-filled)) + label
}

// stepLines lists the steps that fit window lines, the running one in view;
// the steps that do not fit are counted in a "⋮ N more" line.
func (m *ApplyModel) stepLines(window, inner int) []string {
	var visible []stepRow

	for _, r := range m.rows {
		if r.state != stateSkipped {
			visible = append(visible, r)
		}
	}

	focus := 0

	for i, r := range visible {
		if r.state == stateRunning || r.state == stateFailed {
			focus = i
		}
	}

	start := 0
	if len(visible) > window {
		start = min(max(0, focus-window/3), len(visible)-window+1)
	}

	end := min(len(visible), start+window)
	out := make([]string, 0, window)

	for _, r := range visible[start:end] {
		out = append(out, m.stepLine(r, inner))
	}

	if end < len(visible) {
		out[len(out)-1] = " " + m.th.muted.Render(fmt.Sprintf("⋮ %d more", len(visible)-end+1))
	}

	return out
}

func (m *ApplyModel) stepLine(r stepRow, inner int) string {
	glyph, st, extra := "·", m.th.muted, ""

	switch r.state {
	case stateDone:
		glyph, st, extra = "✓", m.th.ok, fmtDuration(r.elapsed)
	case stateAlready:
		glyph, st, extra = "↷", m.th.muted, "already done"
	case stateWarned:
		glyph, st, extra = "!", m.th.warn, "warning"
	case stateFailed:
		glyph, st, extra = "✗", m.th.bad, "failed"
	case stateRunning:
		glyph, st = strings.TrimSpace(m.spin.View()), m.th.accent
	}

	title := fit(r.title, max(1, inner-4-lipgloss.Width(extra)-1))
	if r.state == stateRunning {
		title = m.th.accent.Render(title)
	}

	return " " + st.Render(glyph) + " " + title + " " + m.th.muted.Render(extra)
}

// outcomeLines say how the run ended: the failure with its error, or that
// the operator stopped it, or that it is complete.
func (m *ApplyModel) outcomeLines(w int) []string {
	switch {
	case m.finished && m.err == nil:
		return []string{"", " " + m.th.ok.Render("✓ Configuration complete.")}
	case m.finished:
		lines := []string{""}

		for _, l := range wrapText("✗ "+m.err.Error(), w) {
			lines = append(lines, " "+m.th.bad.Render(l))
		}

		resume := "Fix the cause and run carbonio-bootstrap again: the steps already done are skipped."
		if errorsIsStopped(m.err) {
			resume = "Run carbonio-bootstrap again to resume: the steps already done are skipped."
		}

		for _, l := range wrapText(resume, w) {
			lines = append(lines, " "+m.th.muted.Render(l))
		}

		return lines
	case m.stopping:
		return []string{"", " " + m.th.warn.Render("Stopping after the current step…")}
	}

	return nil
}

func (m *ApplyModel) logPane() string {
	lines := m.opts.Feed.logLines()
	if len(lines) > logLines {
		lines = lines[len(lines)-logLines:]
	}

	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = " " + m.th.muted.Render(l)
	}

	title := "Log"
	if m.opts.LogPath != "" {
		title += " " + m.opts.LogPath
	}

	return box(title, out, m.w, logLines+2, &m.th.border, &m.th.muted)
}

func (m *ApplyModel) keys() string {
	hints := [][2]string{{"l", hintFullLog}, {keyCtrlC, "stop after the current step"}}

	switch {
	case m.finished:
		hints = [][2]string{{"l", hintFullLog}, {"q", hintQuit}}
	case m.stopping:
		hints = [][2]string{{"l", hintFullLog}}
	}

	return fit(" "+hintLine(&m.th, hints), m.w)
}

func (m *ApplyModel) logScreen() string {
	title := "Log"
	if m.opts.LogPath != "" {
		title += " " + m.opts.LogPath
	}

	lines := strings.Split(m.vp.View(), "\n")
	for i, l := range lines {
		lines[i] = " " + l
	}

	return strings.Join([]string{
		m.header(),
		box(title, lines, m.w, m.h-2, &m.th.borderFocus, &m.th.title),
		fit(" "+hintLine(&m.th, [][2]string{{"↑↓", "scroll"}, {"g/G", "top/bottom"}, {keyEsc, "back"}}), m.w),
	}, "\n")
}

func fmtDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}

	return d.Round(time.Second).String()
}
