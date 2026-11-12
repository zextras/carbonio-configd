// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrapcmd

import (
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
	"github.com/zextras/carbonio-configd/internal/bootstrap/setup"
)

// envPrefix is the prefix of the bootstrap's environment keys.
const envPrefix = "CARBONIO_BOOTSTRAP_"

// logStart opens the log file with what the run is: the binary, the
// Carbonio version and where the configuration comes from.
func logStart(e *setup.Env, opts *RunOptions, deps *Deps) {
	e.Log.Detail("carbonio-bootstrap %s", buildVersion())

	if v, err := e.Sys.FS.ReadFile(e.Paths.Path(".version")); err == nil {
		e.Log.Detail("Carbonio %s", strings.TrimSpace(string(v)))
	}

	e.Log.Detail("Input: %s", strings.Join(inputsOf(opts, deps), ", "))
}

// inputsOf lists the configuration layers the operator gave, in the order
// they are applied.
func inputsOf(opts *RunOptions, deps *Deps) []string {
	var in []string

	if opts.LegacyPath != "" {
		in = append(in, "legacy file "+opts.LegacyPath)
	}

	if opts.ConfigPath != "" {
		in = append(in, "file "+opts.ConfigPath)
	}

	if n := envKeys(opts.Environ); n > 0 {
		in = append(in, plural(n, "environment key"))
	}

	if len(opts.Sets) > 0 {
		in = append(in, plural(len(opts.Sets), "--set value"))
	}

	switch {
	case opts.NonInteractive || !deps.Terminal:
		in = append(in, "no wizard")
	case opts.Wizard == WizardAll:
		in = append(in, "wizard (every key)")
	default:
		in = append(in, "wizard (missing keys)")
	}

	return in
}

func envKeys(environ []string) int {
	n := 0

	for _, kv := range environ {
		if strings.HasPrefix(kv, envPrefix) {
			n++
		}
	}

	return n
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}

	return strconv.Itoa(n) + " " + what + "s"
}

// logConfig writes the node and the resolved configuration to the log:
// secrets masked, each key with its source (default, legacy, wizard, ...).
func logConfig(e *setup.Env, r *bootstrap.Resolved) {
	f := e.Facts
	mode := "traditional (zmcontrol)"

	if f.Systemd {
		mode = "systemd"
	}

	e.Log.Detail("Node: %s, %s, %s, services managed by %s", e.Cfg.Host.Hostname, installKind(f), f.Platform, mode)
	e.Log.Detail("Enabled roles: %s", strings.Join(f.EnabledRoles(), ", "))

	out, err := r.Render(bootstrap.Print)
	if err != nil {
		e.Log.Detail("Configuration: not rendered: %v", err)

		return
	}

	e.Log.Detail("Configuration (passwords masked, each key with its source):")

	for line := range strings.SplitSeq(strings.TrimRight(string(out), "\n"), "\n") {
		e.Log.Detail("  %s", line)
	}
}

// buildVersion is the module version and commit the binary was built from.
func buildVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "(unknown)"
	}

	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 12 {
			return bi.Main.Version + " (" + s.Value[:12] + ")"
		}
	}

	return bi.Main.Version
}
