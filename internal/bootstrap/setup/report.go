// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/zextras/carbonio-configd/internal/bootstrap/brand"
	"github.com/zextras/carbonio-configd/internal/redact"
)

// Layout of the plain report.
const (
	// reportWidth is the width the lines are kept within.
	reportWidth = 100
	// maxTitleWidth caps the title column.
	maxTitleWidth = 50
	// statusWidth is the status column, with the gap to the detail.
	statusWidth = 8
	// liveInterval is how often the live line is redrawn.
	liveInterval = 100 * time.Millisecond
	// resumeHint closes a failed run.
	resumeHint = "Fix the cause and run carbonio-bootstrap again: the steps already done are skipped."
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// The status words of a step line.
const (
	statusOK   = "ok"
	statusSkip = "skip"
	statusWarn = "warn"
	statusFail = "FAIL"
)

// PlanStep is a step of the run as the report lists it.
type PlanStep struct {
	// Index is the position of the step in the list given to Run.
	Index int
	Title string
}

// ReportOptions configure a Reporter.
type ReportOptions struct {
	// Out gets the report, Err the failure summary.
	Out, Err io.Writer
	// Plan are the steps that apply to the node, in order: the N of "n/N".
	Plan []PlanStep
	// Color paints the statuses; Live redraws a line for the running step.
	// Both are for a terminal only.
	Color, Live bool
	// Dark picks the shades of the palette (see brand.DarkBackground).
	Dark bool
	// Width is the terminal width, for the live line (0: reportWidth).
	Width int
	// Now is the clock (nil: time.Now).
	Now func() time.Time
}

// Header is what the report says before the steps.
type Header struct {
	Version  string
	Hostname string
	Install  string
	// Config is the configuration file read ("" when none) and ConfigKind
	// its format ("legacy", "yaml").
	Config, ConfigKind string
	// Dropped is the number of legacy keys not imported; DropLines their
	// list, printed only when the operator asked for it.
	Dropped   int
	DropLines []string
	// LogPath is the log file ("" in a dry run).
	LogPath string
}

// Summary closes the report.
type Summary struct {
	// Err is why the run ended (nil: it completed).
	Err  error
	Next Next
}

// Reporter prints the progress of a run to a terminal or a log: a line per
// step with its status and duration, the step's own messages under it, and
// a summary. It is the Observer of the run and the note handler of its Log.
// Steps are listed one per line when the output is not a terminal; on a
// terminal the running step has a live line that its final line replaces.
type Reporter struct {
	mu   sync.Mutex
	o    ReportOptions
	w    io.Writer
	now  func() time.Time
	st   styles
	num  map[int]int
	next int
	// widths of the title column and of the step counter.
	titleW, numW int

	started time.Time

	running  bool
	runNum   int
	runTitle string
	runStart time.Time
	frame    int
	stop     chan struct{}
	notes    []Note

	applied, already int
	warnings         []string
	failed           *failure
}

type failure struct {
	num   int
	title string
	err   string
}

type styles struct {
	title, muted, ok, warn, bad, accent lipgloss.Style
	color                               bool
}

// NewReporter returns a reporter for the plan.
func NewReporter(o *ReportOptions) *Reporter {
	r := &Reporter{o: *o, w: o.Out, now: o.Now, num: map[int]int{}}
	if r.now == nil {
		r.now = time.Now
	}

	if o.Color {
		cw := colorprofile.NewWriter(o.Out, os.Environ())
		if _, isFile := o.Out.(*os.File); !isFile {
			cw.Profile = colorprofile.TrueColor
		}

		r.w = cw
	}

	r.st = newStyles(o.Color, o.Dark)

	for i, p := range o.Plan {
		r.num[p.Index] = i + 1
		r.titleW = max(r.titleW, min(len([]rune(p.Title)), maxTitleWidth))
	}

	r.next = len(o.Plan)
	r.numW = len(fmt.Sprint(len(o.Plan)))
	r.started = r.now()

	return r
}

func newStyles(color, dark bool) styles {
	fg := func(s brand.Shade) lipgloss.Style {
		if !color {
			return lipgloss.NewStyle()
		}

		return lipgloss.NewStyle().Foreground(s.Color(dark))
	}

	st := styles{
		muted:  fg(brand.Secondary),
		ok:     fg(brand.Success),
		warn:   fg(brand.Warning),
		bad:    fg(brand.Error).Bold(color),
		accent: fg(brand.Primary),
		color:  color,
	}
	st.title = st.accent.Bold(color)

	return st
}

func (s *styles) paint(st *lipgloss.Style, text string) string {
	if !s.color {
		return text
	}

	return st.Render(text)
}

func (r *Reporter) print(format string, args ...any) {
	_, _ = fmt.Fprintf(r.w, format, args...)
}

// Header prints the lines that tell what is being configured.
func (r *Reporter) Header(h *Header) {
	r.mu.Lock()
	defer r.mu.Unlock()

	title := "Carbonio Bootstrap"
	if h.Version != "" {
		title += " " + h.Version
	}

	rest := make([]string, 0, 2)

	for _, p := range []string{h.Hostname, h.Install} {
		if p != "" {
			rest = append(rest, p)
		}
	}

	r.print("%s", r.st.paint(&r.st.title, title))

	if len(rest) > 0 {
		r.print(" %s", r.st.paint(&r.st.muted, "· "+strings.Join(rest, " · ")))
	}

	r.print("\n")

	if h.Config != "" {
		r.print("Config:  %s\n", configLine(h))

		for _, l := range h.DropLines {
			r.print("         %s\n", r.st.paint(&r.st.muted, "· "+l))
		}
	}

	if h.LogPath != "" {
		r.print("Log:     %s\n", h.LogPath)
	}

	r.print("\n")
}

func configLine(h *Header) string {
	line := h.Config
	if h.ConfigKind != "" {
		line += " (" + h.ConfigKind + ")"
	}

	switch {
	case h.Dropped == 0:
	case len(h.DropLines) > 0:
		line += fmt.Sprintf(" · %s not imported:", plural(h.Dropped, "key"))
	case h.LogPath != "":
		line += fmt.Sprintf(" · %s not imported, see log", plural(h.Dropped, "key"))
	default:
		line += fmt.Sprintf(" · %s not imported, use --verbose", plural(h.Dropped, "key"))
	}

	return line
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}

	return fmt.Sprintf("%d %ss", n, noun)
}

// Observe is the setup.Observer of the run.
func (r *Reporter) Observe(ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch {
	case ev.Kind == EventStart:
		r.start(&ev)
	case ev.Outcome != Skipped:
		r.finish(&ev)
	}
}

// Note takes a message of a step: shown under the step line once the step
// ends. A warning outside any step goes to the summary.
func (r *Reporter) Note(n Note) {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch {
	case r.running:
		r.notes = append(r.notes, n)
	case n.Warn:
		r.warnings = append(r.warnings, n.Text)
	}
}

func (r *Reporter) numberOf(index int) int {
	n, ok := r.num[index]
	if !ok {
		r.next++
		n = r.next
		r.num[index] = n
	}

	return n
}

func (r *Reporter) start(ev *Event) {
	r.running = true
	r.runNum = r.numberOf(ev.Index)
	r.runTitle = ev.Step.Title
	r.runStart = r.now()
	r.notes = nil
	r.titleW = max(r.titleW, min(len([]rune(ev.Step.Title)), maxTitleWidth))

	if !r.o.Live {
		return
	}

	r.draw()

	stop := make(chan struct{})
	r.stop = stop

	go func() {
		t := time.NewTicker(liveInterval)
		defer t.Stop()

		for {
			select {
			case <-stop:
				return
			case <-t.C:
				r.mu.Lock()
				r.draw()
				r.mu.Unlock()
			}
		}
	}()
}

// draw redraws the live line of the running step.
func (r *Reporter) draw() {
	if !r.running || !r.o.Live {
		return
	}

	width := r.o.Width
	if width <= 0 {
		width = reportWidth
	}

	elapsed := fmtDuration(r.now().Sub(r.runStart))
	title := r.runTitle
	room := max(1, min(r.titleW, width-1-r.prefixWidth()-2-2-len([]rune(elapsed))))
	title = cut(title, room)

	line := r.counter(r.runNum) + " " + r.st.paint(&r.st.accent, title) +
		strings.Repeat(" ", max(0, room-len([]rune(title)))) + "  " +
		r.st.paint(&r.st.accent, spinnerFrames[r.frame%len(spinnerFrames)]) + " " + elapsed
	r.frame++

	r.print("\r\x1b[2K%s", line)
}

func (r *Reporter) counter(n int) string {
	return r.st.paint(&r.st.muted, fmt.Sprintf("[%*d/%d]", r.numW, n, len(r.o.Plan)))
}

// cut shortens s to width columns, ending with an ellipsis.
func cut(s string, width int) string {
	rs := []rune(s)
	if len(rs) <= width {
		return s
	}

	return string(rs[:max(0, width-1)]) + "…"
}

// stepLine formats a final line.
func (r *Reporter) stepLine(num int, title, status, detail string) string {
	title = cut(title, maxTitleWidth)
	pad := strings.Repeat(" ", max(0, r.titleW-len([]rune(title))))
	word := fmt.Sprintf("%-*s", statusWidth, status)

	st := &r.st.bad

	switch status {
	case statusOK:
		st = &r.st.ok
	case statusSkip:
		st = &r.st.muted
	case statusWarn:
		st = &r.st.warn
	}

	return fmt.Sprintf("%s %s%s  %s%s", r.counter(num), title, pad, r.st.paint(st, word), strings.TrimRight(detail, " "))
}

// prefixWidth is the width of "[ n/N] ": the indent of the details.
func (r *Reporter) prefixWidth() int { return r.numW*2 + 4 }

func (r *Reporter) fits(detail string) bool {
	line := r.prefixWidth() + r.titleW + 2 + statusWidth + len([]rune(detail))

	return line <= reportWidth && !strings.Contains(detail, "\n")
}

type described struct {
	status, detail string
	bullets        []Note
	err            string
}

func (r *Reporter) describe(ev *Event) described {
	d := described{status: statusOK, detail: fmtDuration(ev.Elapsed), bullets: r.notes}
	title := ev.Step.Title

	switch {
	case ev.Outcome == AlreadyDone:
		d.status, d.detail = statusSkip, "already done"
		r.already++
	case ev.Outcome == Warned:
		cause := firstLine(errText(ev.Err))
		d.status = statusWarn

		r.warnings = append(r.warnings, title+": "+cause)

		if r.fits(cause) {
			d.detail = cause
		} else {
			d.bullets = append(d.bullets, Note{Warn: true, Text: errText(ev.Err)})
		}
	case ev.Outcome == Failed:
		d.status, d.err = statusFail, errText(ev.Err)
		d.bullets = append(d.bullets, Note{Text: d.err})
	case len(ev.Warnings) > 0:
		r.applied++
		d.status = statusWarn

		for _, w := range ev.Warnings {
			r.warnings = append(r.warnings, title+": "+w)
		}

		if first := ev.Warnings[0]; r.fits(first) {
			d.detail = first
			d.bullets = dropNote(d.bullets, first)
		}
	default:
		r.applied++
	}

	return d
}

func dropNote(notes []Note, text string) []Note {
	out := make([]Note, 0, len(notes))
	dropped := false

	for _, n := range notes {
		if !dropped && n.Warn && n.Text == text {
			dropped = true

			continue
		}

		out = append(out, n)
	}

	return out
}

func errText(err error) string {
	if err == nil {
		return ""
	}

	text, _ := redact.Text(err.Error())

	return text
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")

	return line
}

func (r *Reporter) finish(ev *Event) {
	num := r.numberOf(ev.Index)
	r.titleW = max(r.titleW, min(len([]rune(ev.Step.Title)), maxTitleWidth))

	if ev.Outcome == AlreadyDone {
		r.notes = nil
	}

	d := r.describe(ev)

	if d.status == statusFail {
		r.failed = &failure{num: num, title: ev.Step.Title, err: d.err}
	}

	if r.running {
		r.running = false

		if r.stop != nil {
			close(r.stop)
			r.stop = nil
		}
	}

	if r.o.Live {
		r.print("\r\x1b[2K")
	}

	r.print("%s\n", r.stepLine(num, ev.Step.Title, d.status, d.detail))

	indent := strings.Repeat(" ", r.prefixWidth())

	for _, b := range d.bullets {
		r.bullet(indent, b)
	}

	r.notes = nil
}

func (r *Reporter) bullet(indent string, n Note) {
	st := &r.st.muted
	if n.Warn {
		st = &r.st.warn
	}

	for i, l := range Wrap(n.Text, max(20, reportWidth-len(indent)-2)) {
		lead := "· "
		if i > 0 {
			lead = "  "
		}

		r.print("%s%s\n", indent, r.st.paint(st, lead+l))
	}
}

// Summary prints how the run ended: the totals, the warnings and what to do
// next on success; the failed step and how to go on otherwise.
func (r *Reporter) Summary(s *Summary) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if s.Err != nil {
		r.failure(s.Err)

		return
	}

	parts := []string{
		"Done in " + fmtDuration(r.now().Sub(r.started)),
		fmt.Sprintf("%d applied", r.applied),
		fmt.Sprintf("%d already done", r.already),
	}

	if n := len(r.warnings); n > 0 {
		parts = append(parts, plural(n, "warning"))
	}

	r.print("\n%s\n", r.st.paint(&r.st.ok, strings.Join(parts, " · ")))

	if len(r.warnings) > 0 {
		r.print("%s\n", r.st.paint(&r.st.warn, "Warnings:"))

		for _, w := range r.warnings {
			r.print("  · %s\n", w)
		}
	}

	if s.Next.StartCommand != "" {
		r.print("Services not started (start_services: no). Start them with: %s\n", s.Next.StartCommand)
	}

	r.urls(&s.Next)
}

func (r *Reporter) urls(n *Next) {
	var parts []string

	if n.Admin != "" {
		parts = append(parts, "Admin console: "+n.Admin)
	}

	if n.Webmail != "" {
		parts = append(parts, "Webmail: "+n.Webmail)
	}

	if len(parts) > 0 {
		r.print("%s\n", strings.Join(parts, "   "))
	}
}

// failure writes the end of a failed run to Err, without colour: stderr may
// not be the terminal stdout is.
func (r *Reporter) failure(err error) {
	w := r.o.Err
	if w == nil {
		w = r.o.Out
	}

	if f := r.failed; f != nil {
		_, _ = fmt.Fprintf(w, "\nFailed at step %d/%d: %s\n  %s\n", f.num, len(r.o.Plan), f.title,
			strings.ReplaceAll(f.err, "\n", "\n  "))
	} else {
		_, _ = fmt.Fprintf(w, "\nError: %s\n", errText(err))
	}

	_, _ = fmt.Fprintf(w, "%s\n", resumeHint)
}

// fmtDuration renders a step or run duration: 0.4s, 12s, 1:05.
func fmtDuration(d time.Duration) string {
	switch {
	case d < 10*time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}

	secs := int(d.Round(time.Second).Seconds())

	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}
