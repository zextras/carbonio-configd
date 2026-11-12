// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tui

import (
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
)

// edit starts editing the focused key: a toggle for a boolean, a list for an
// enum, a text input otherwise (masked for a secret).
func (m *Model) edit() tea.Cmd {
	f := m.focused()
	if f == nil {
		return nil
	}

	if m.locked(f) {
		m.say(true, f.Path+" is set by "+m.lockedBy(f)+": not editable here.")

		return nil
	}

	switch {
	case f.TypeName() == "bool":
		return m.apply(f, strconv.FormatBool(f.Format(&m.r.Config) != "true"))
	case len(f.Enum) > 0 && f.TypeName() == "string":
		m.enumOpen = true
		m.enumSel = max(0, slices.Index(f.Enum, f.Format(&m.r.Config)))
		m.field = f
	default:
		current := f.Format(&m.r.Config)
		if f.Secret {
			current = ""
		}

		m.field = f
		m.startInput(inputEdit, current)
	}

	return nil
}

// apply stores raw as the answer to f and recomputes the defaults.
func (m *Model) apply(f *bootstrap.Field, raw string) tea.Cmd {
	if err := m.r.Answer(f, raw); err != nil {
		m.say(true, err.Error())

		return nil
	}

	m.dirty = true

	return m.refresh()
}

func (m *Model) startInput(mode inputMode, value string) {
	m.mode = mode
	m.input = textinput.New()
	m.input.Prompt = ""
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.inputErr = ""
	m.repeating = false

	if mode == inputEdit && m.field != nil && m.field.Secret {
		m.input.EchoMode = textinput.EchoPassword
		m.input.EchoCharacter = '•'
	}

	m.input.Focus()
	m.liveCheck()
}

func (m *Model) stopInput() {
	m.mode = inputNone
	m.input.Blur()
	m.input.SetValue("")
	m.inputErr = ""
	m.firstPass = ""
	m.repeating = false
}

func (m *Model) keyInput(k string, msg tea.KeyPressMsg) tea.Cmd {
	switch k {
	case keyEsc, keyCtrlC:
		m.stopInput()
	case keyEnter:
		return m.commitInput()
	default:
		m.input, _ = m.input.Update(msg)
		m.liveCheck()
	}

	return nil
}

// liveCheck sets the validation message shown while the operator types.
func (m *Model) liveCheck() {
	m.inputErr = ""

	if m.mode != inputEdit || m.field == nil || m.repeating {
		return
	}

	raw := m.input.Value()
	if raw == "" {
		return
	}

	if err := bootstrap.CheckInput(m.field, &m.r.Config, raw); err != nil {
		m.inputErr = err.Error()
	}
}

func (m *Model) commitInput() tea.Cmd {
	switch m.mode {
	case inputSearch:
		m.search(m.input.Value())
		m.stopInput()
	case inputSave:
		m.save(strings.TrimSpace(m.input.Value()))
		m.stopInput()
	case inputEdit:
		return m.commitEdit()
	}

	return nil
}

func (m *Model) commitEdit() tea.Cmd {
	f, raw := m.field, m.input.Value()

	if raw == "" {
		if m.missing(f) {
			m.inputErr = f.Path + " is required"

			return nil
		}

		m.stopInput()

		return nil
	}

	if f.Secret && !m.repeating {
		if err := bootstrap.CheckInput(f, &m.r.Config, raw); err != nil {
			m.inputErr = err.Error()

			return nil
		}

		m.firstPass, m.repeating = raw, true
		m.input.SetValue("")

		return nil
	}

	if f.Secret && raw != m.firstPass {
		m.inputErr = "The two entries differ."
		m.repeating = false
		m.firstPass = ""
		m.input.SetValue("")

		return nil
	}

	if err := m.r.Answer(f, raw); err != nil {
		m.inputErr = err.Error()

		return nil
	}

	m.stopInput()
	m.dirty = true

	return m.refresh()
}

func (m *Model) keyEnum(k string) tea.Cmd {
	f := m.field

	switch k {
	case keyEsc, keyCtrlC, "q":
		m.enumOpen = false
	case "up", "k", keyLeft:
		m.enumSel = (m.enumSel + len(f.Enum) - 1) % len(f.Enum)
	case keyDown, "j", keyRight, keyTab:
		m.enumSel = (m.enumSel + 1) % len(f.Enum)
	case keyEnter, keySpace:
		m.enumOpen = false

		if f.Enum[m.enumSel] != f.Format(&m.r.Config) {
			return m.apply(f, f.Enum[m.enumSel])
		}
	}

	return nil
}

// search moves the focus to the next key whose label or path contains q.
func (m *Model) search(q string) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return
	}

	type pos struct{ sec, row int }

	var hits []pos

	for si, secIdx := range m.visible() {
		for ri, f := range m.rowsOf(secIdx) {
			if strings.Contains(strings.ToLower(labelOf(f)+" "+f.Path), q) {
				hits = append(hits, pos{si, ri})
			}
		}
	}

	if len(hits) == 0 {
		m.say(true, "Nothing matches "+strconv.Quote(q)+".")

		return
	}

	i := max(0, slices.IndexFunc(hits, func(p pos) bool { return p.sec > m.sec || (p.sec == m.sec && p.row > m.row) }))

	m.sec, m.row, m.pane = hits[i].sec, hits[i].row, paneFields
}

func labelOf(f *bootstrap.Field) string {
	if mt, ok := fieldMeta[f.Path]; ok {
		return mt.label
	}

	return f.Path
}

func (m *Model) save(path string) {
	if path == "" || m.opts.Save == nil {
		return
	}

	if err := m.opts.Save(path); err != nil {
		m.say(true, "Not saved: "+err.Error())

		return
	}

	m.say(false, "Saved to "+path+" (no passwords are written).")
}
