// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tui

import (
	"errors"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/zextras/carbonio-configd/internal/bootstrap/setup"
)

// PlanStep is a step the run will go through.
type PlanStep struct {
	// Index is the position of the step in the list given to setup.Run.
	Index int
	Title string
	// Done: the node is already in the state the step produces.
	Done bool
}

// ApplyOptions are the inputs of the apply screen.
type ApplyOptions struct {
	Plan []PlanStep
	// Feed receives the events and the log lines of the run.
	Feed *Feed
	// Run does the work; it reports through obs and polls stop between steps.
	Run func(obs setup.Observer, stop func() bool) error
	// Preflight computes the checks; it runs while the steps do.
	Preflight func() []Check
	Hostname  string
	Info      Info
	// SavedPath is where the YAML was saved ("" if it was not).
	SavedPath string
	// LogPath is the log file of the run.
	LogPath string
	// Notice, if set, is shown on a welcome page before the run starts.
	Notice  string
	NoColor bool
	// Now is the clock (nil: time.Now).
	Now func() time.Time
}

type stepState int

const (
	statePending stepState = iota
	stateRunning
	stateDone
	stateAlready
	stateWarned
	stateFailed
	stateSkipped
)

type stepRow struct {
	index   int
	title   string
	state   stepState
	elapsed time.Duration
	err     string
}

type (
	preflightMsg []Check
	finishedMsg  struct{ err error }
)

// ApplyModel is the apply screen.
type ApplyModel struct {
	opts ApplyOptions
	th   theme
	w, h int

	welcome bool
	rows    []stepRow
	checks  []Check
	spin    spinner.Model

	started  time.Time
	ended    time.Time
	running  bool
	finished bool
	err      error
	stopping bool

	logView bool
	vp      viewport.Model
	quit    bool
}

// NewApply returns the apply screen.
func NewApply(opts *ApplyOptions) *ApplyModel {
	m := &ApplyModel{
		opts:    *opts,
		th:      newTheme(true, opts.NoColor),
		w:       minWidth,
		h:       minHeight,
		spin:    spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		vp:      viewport.New(),
		welcome: opts.Notice != "",
	}

	if m.opts.Now == nil {
		m.opts.Now = time.Now
	}

	for _, p := range opts.Plan {
		st := statePending
		if p.Done {
			st = stateAlready
		}

		m.rows = append(m.rows, stepRow{index: p.Index, title: p.Title, state: st})
	}

	return m
}

// Result returns the error that ended the run (nil: the node is configured)
// and whether the run ended.
func (m *ApplyModel) Result() (finished bool, err error) { return m.finished, m.err }

// Init starts the preflight checks and, unless a welcome page comes first,
// the run.
func (m *ApplyModel) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.RequestBackgroundColor, m.spin.Tick, m.preflight()}
	if !m.welcome {
		cmds = append(cmds, m.start())
	}

	return tea.Batch(cmds...)
}

func (m *ApplyModel) preflight() tea.Cmd {
	if m.opts.Preflight == nil {
		return nil
	}

	return func() tea.Msg { return preflightMsg(m.opts.Preflight()) }
}

// start launches the run in its own goroutine (a command) and reports when
// it ends.
func (m *ApplyModel) start() tea.Cmd {
	m.running = true
	m.started = m.opts.Now()

	return func() tea.Msg {
		return finishedMsg{m.opts.Run(m.opts.Feed.Observe, m.opts.Feed.Stopping)}
	}
}

// Update handles one message.
func (m *ApplyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.BackgroundColorMsg:
		m.th = newTheme(msg.IsDark(), m.opts.NoColor)
	case preflightMsg:
		m.checks = msg
	case finishedMsg:
		m.drain()

		m.running, m.finished, m.err = false, true, msg.err
		m.ended = m.opts.Now()
	case spinner.TickMsg:
		var cmd tea.Cmd

		m.spin, cmd = m.spin.Update(msg)
		m.drain()

		return m, cmd
	case tea.KeyPressMsg:
		cmd := m.key(msg.String())
		if m.quit {
			return m, tea.Quit
		}

		return m, cmd
	}

	return m, nil
}

// drain applies the events and refreshes the log viewer.
func (m *ApplyModel) drain() {
	for _, ev := range m.opts.Feed.drain() {
		m.onEvent(ev)
	}

	if m.logView {
		m.vp.SetContentLines(m.opts.Feed.logLines())
	}
}

func (m *ApplyModel) row(ev setup.Event) *stepRow {
	for i := range m.rows {
		if m.rows[i].index == ev.Index {
			return &m.rows[i]
		}
	}

	m.rows = append(m.rows, stepRow{index: ev.Index, title: ev.Step.Title})

	return &m.rows[len(m.rows)-1]
}

func (m *ApplyModel) onEvent(ev setup.Event) {
	r := m.row(ev)

	if ev.Kind == setup.EventStart {
		r.state = stateRunning

		return
	}

	r.elapsed = ev.Elapsed

	switch ev.Outcome {
	case setup.Applied:
		r.state = stateDone
	case setup.AlreadyDone:
		r.state = stateAlready
	case setup.Warned:
		r.state, r.err = stateWarned, errText(ev.Err)
	case setup.Failed:
		r.state, r.err = stateFailed, errText(ev.Err)
	default:
		r.state = stateSkipped
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

func (m *ApplyModel) key(k string) tea.Cmd {
	switch {
	case m.welcome:
		return m.keyWelcome(k)
	case m.logView:
		m.keyLog(k)
	case k == "l":
		m.logView = true
		m.vp.SetWidth(m.w - 4)
		m.vp.SetHeight(m.h - 5)
		m.vp.SetContentLines(m.opts.Feed.logLines())
		m.vp.GotoBottom()
	case k == keyCtrlC:
		m.interrupt()
	case m.finished && (k == "q" || k == keyEnter || k == keyEsc):
		m.quit = true
	}

	return nil
}

func (m *ApplyModel) keyWelcome(k string) tea.Cmd {
	switch k {
	case keyEnter:
		m.welcome = false

		return m.start()
	case "q", keyCtrlC:
		m.err = errors.New("stopped before the first step")
		m.finished, m.quit = true, true
	}

	return nil
}

func (m *ApplyModel) keyLog(k string) {
	switch k {
	case keyEsc, "l", "q":
		m.logView = false
	case "up", "k":
		m.vp.ScrollUp(1)
	case keyDown, "j":
		m.vp.ScrollDown(1)
	case "pgup":
		m.vp.PageUp()
	case "pgdown":
		m.vp.PageDown()
	case "g":
		m.vp.GotoTop()
	case "G":
		m.vp.GotoBottom()
	}
}

// interrupt is ctrl+c: while the run goes on it stops after the current
// step, never in the middle of one; afterwards it leaves the screen.
func (m *ApplyModel) interrupt() {
	if m.finished {
		m.quit = true

		return
	}

	m.stopping = true
	m.opts.Feed.RequestStop()
}

// progress counts the steps that are over and those the run goes through.
func (m *ApplyModel) progress() (done, total int) {
	for _, r := range m.rows {
		switch r.state {
		case stateSkipped:
		case stateDone, stateAlready, stateWarned:
			done++

			total++
		default:
			total++
		}
	}

	return done, total
}

func (m *ApplyModel) elapsed() time.Duration {
	switch {
	case m.started.IsZero():
		return 0
	case m.finished:
		return m.ended.Sub(m.started)
	default:
		return m.opts.Now().Sub(m.started)
	}
}

func errorsIsStopped(err error) bool { return errors.Is(err, setup.ErrStopped) }
