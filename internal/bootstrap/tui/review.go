// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
)

// The buttons of the review page.
const (
	buttonGo = iota
	buttonSave
	buttonBack
	buttonCount
)

// maxReviewErrors is how many problems the review page lists.
const maxReviewErrors = 4

// openReview shows the review page, unless a required value is missing.
func (m *Model) openReview() {
	if n := len(m.missingAll()); n > 0 {
		m.say(true, plural(n, "required value is", "required values are")+" missing: press n to jump to the next.")

		return
	}

	m.reviewErr = m.reviewErr[:0]

	if err := bootstrap.Validate(&m.r.Config); err != nil {
		for _, e := range flatten(err) {
			m.reviewErr = append(m.reviewErr, e.Error())
		}
	}

	out, err := m.r.Render(bootstrap.Print)
	if err != nil {
		m.reviewErr = append(m.reviewErr, err.Error())
	}

	m.page = pageReview
	m.button = buttonGo
	m.resizeReview()
	m.vp.SetContent(strings.TrimRight(string(out), "\n"))
	m.vp.GotoTop()
}

func flatten(err error) []error {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		return j.Unwrap()
	}

	return []error{err}
}

// resizeReview sizes the YAML viewport to the room the page leaves it.
func (m *Model) resizeReview() {
	m.vp.SetWidth(max(10, m.w-6))
	m.vp.SetHeight(max(3, m.h-9-min(len(m.reviewErr), maxReviewErrors)))
}

func (m *Model) goLabel() string {
	if m.opts.Apply {
		return "Apply"
	}

	return "Done"
}

func (m *Model) keyReview(k string) tea.Cmd {
	m.msg = ""

	switch k {
	case keyLeft, "shift+tab":
		m.button = (m.button + buttonCount - 1) % buttonCount
	case keyRight, keyTab:
		m.button = (m.button + 1) % buttonCount
	case "up", "k":
		m.vp.ScrollUp(1)
	case keyDown, "j":
		m.vp.ScrollDown(1)
	case "pgup":
		m.vp.PageUp()
	case "pgdown":
		m.vp.PageDown()
	case keyEnter, keySpace:
		return m.press(m.button)
	case "a":
		return m.press(buttonGo)
	case "s":
		return m.press(buttonSave)
	case "b", keyEsc:
		return m.press(buttonBack)
	case "q", keyCtrlC:
		m.quit()
	case "?":
		m.help = true
	}

	return nil
}

func (m *Model) press(button int) tea.Cmd {
	switch button {
	case buttonGo:
		if len(m.reviewErr) > 0 {
			m.say(true, "Fix the problems listed before going on.")

			return nil
		}

		if m.opts.Apply {
			m.finish(ActionApply)
		} else {
			m.finish(ActionDone)
		}
	case buttonSave:
		m.startInput(inputSave, m.opts.SavePath)
	case buttonBack:
		m.page = pageConfigure
	}

	return nil
}

func (m *Model) reviewView() string {
	var lines []string

	lines = append(lines, "", " "+m.th.muted.Render("Configuration (no passwords; sources in the comments)"), "")

	for _, e := range m.reviewErr[:min(len(m.reviewErr), maxReviewErrors)] {
		lines = append(lines, " "+m.th.bad.Render("✗ "+e))
	}

	for l := range strings.SplitSeq(m.vp.View(), "\n") {
		lines = append(lines, " "+l)
	}

	height := m.h - 4
	body := box("Review", lines, m.w, height, &m.th.borderFocus, &m.th.title)

	return strings.Join([]string{m.header("configure › review"), body, m.reviewButtons(), m.reviewKeys()}, "\n")
}

func (m *Model) reviewButtons() string {
	if m.mode == inputSave {
		return fit(" Save to: "+m.input.View(), m.w)
	}

	labels := [buttonCount]string{m.goLabel(), "Save yaml", "Back"}

	var parts []string

	for i, l := range labels {
		st := m.th.muted
		if i == m.button {
			st = m.th.selected
		}

		if i == buttonGo && len(m.reviewErr) > 0 {
			st = m.th.muted.Strikethrough(true)
		}

		parts = append(parts, st.Render(" "+l+" "))
	}

	text := " " + strings.Join(parts, "  ")

	if m.msg != "" {
		st := m.th.ok
		if m.msgBad {
			st = m.th.bad
		}

		text += "   " + st.Render(m.msg)
	}

	return fit(text, m.w)
}

func (m *Model) reviewKeys() string {
	if m.mode == inputSave {
		return fit(" "+hintLine(&m.th, [][2]string{{"⏎", hintSave}, {keyEsc, "cancel"}}), m.w)
	}

	return fit(" "+hintLine(&m.th, [][2]string{
		{"←→", "choose"}, {"⏎", "press"}, {"a", strings.ToLower(m.goLabel())}, {"s", "save"}, {"b", "back"},
		{"↑↓", "scroll"}, {"q", hintQuit},
	}), m.w)
}
