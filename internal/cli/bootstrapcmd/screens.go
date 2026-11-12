// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrapcmd

import (
	"context"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
	"github.com/zextras/carbonio-configd/internal/bootstrap/tui"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// defaultSavePath is where the screen offers to save the YAML.
const defaultSavePath = "/etc/carbonio/bootstrap.yaml"

// Screens are the full-screen pages (the tui package; fakes in tests).
type Screens struct {
	Configure func(ctx context.Context, in io.Reader, out io.Writer, o *tui.ConfigureOptions) (tui.Action, error)
	Apply     func(ctx context.Context, in io.Reader, out io.Writer, o *tui.ApplyOptions) error
}

// Session is what the pages of one "run" share.
type Session struct {
	// Notice is the licence text, shown once on the first page.
	Notice string
	// NoticeShown is set when the configure page has shown the notice.
	NoticeShown bool
	// Install is "new install" or "existing install".
	Install string
	// SavedPath is where the YAML was saved on the configure page.
	SavedPath string
	// LogPath is the log file of the run.
	LogPath string
	// Feed receives the log lines and the step events of the run.
	Feed *tui.Feed
}

// runConfigure shows the configure page: the answers go into r. Quitting is
// ErrAborted.
func runConfigure(ctx context.Context, env sys.System, stdin io.Reader, stdout io.Writer,
	r *bootstrap.Resolved, opts *Options, deps *Deps,
) error {
	s := deps.Session
	if s == nil {
		s = &Session{}
	}

	path := opts.Output
	if path == "" {
		path = defaultSavePath
	}

	s.NoticeShown = s.Notice != ""

	co := &tui.ConfigureOptions{
		Resolved: r,
		Host:     deps.Host,
		Resolver: deps.Resolver,
		Info:     tui.Info{OS: osName(env), Install: s.Install},
		Apply:    deps.Session != nil,
		Notice:   s.Notice,
		SavePath: path,
		NoColor:  noColor(opts.Environ),
		Save: func(p string) error {
			if err := save(env, r, p); err != nil {
				return err
			}

			s.SavedPath = p

			return nil
		},
	}

	action, err := deps.Screens.Configure(ctx, stdin, stdout, co)
	if err != nil {
		return err
	}

	if action == tui.ActionQuit {
		return bootstrap.ErrAborted
	}

	return nil
}

// noColor reports whether NO_COLOR is set (to anything non-empty).
func noColor(environ []string) bool {
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, "NO_COLOR="); ok && v != "" {
			return true
		}
	}

	return false
}

// portFree reports whether a TCP port can be bound on this node.
func portFree(port int) bool {
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return false
	}

	_ = l.Close()

	return true
}

// osName is the PRETTY_NAME of /etc/os-release ("" when unreadable).
func osName(env sys.System) string {
	data, err := env.FS.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}

	return ""
}
