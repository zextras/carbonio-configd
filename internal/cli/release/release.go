// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package release implements "configd release": the Carbonio release string
// and, on request, the installed carbonio package versions (legacy
// "zmcontrol -v" / "zmcontrol -V").
package release

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// osReleasePath is where the distribution ID is read from.
const osReleasePath = "/etc/os-release"

// Options are the parsed command-line flags of "configd release".
type Options struct {
	// Packages also lists the installed carbonio packages (-V).
	Packages bool
}

// rpmQueryFormat renders a package as `dnf list installed` does, which is what
// legacy zmcontrol -V printed: "<name>.<arch> [<epoch>:]<version>-<release>".
const rpmQueryFormat = `%{NAME}.%{ARCH} %|EPOCH?{%{EPOCH}:}|%{VERSION}-%{RELEASE}\n`

// installedPackages lists the installed carbonio packages, one
// "<name> <version>" per line (rpm: "<name>.<arch> <version>-<release>",
// sorted), for the distribution with the given os-release ID. A package
// manager that fails to run yields no output.
func installedPackages(ctx context.Context, x sys.Exec, distroID string) []byte {
	var out []byte

	switch distroID {
	case "ubuntu", "debian":
		out, _ = sys.Output(ctx, x, "dpkg-query", "-W", "-f", "${Package} ${Version}\n", "carbonio*")
	default:
		out, _ = sys.Output(ctx, x, "rpm", "-qa", "--qf", rpmQueryFormat, "carbonio*")
		out = sortLines(out)
	}

	return out
}

// InstalledPackageNames returns the names of the installed carbonio
// packages (rpm names without the ".<arch>" suffix).
func InstalledPackageNames(ctx context.Context, env sys.System) []string {
	out := installedPackages(ctx, env.Exec, distroID(env.FS, osReleasePath))

	var names []string

	for line := range strings.SplitSeq(string(out), "\n") {
		name, _, _ := strings.Cut(line, " ")
		if dot := strings.LastIndexByte(name, '.'); dot > 0 {
			name = name[:dot]
		}

		if name != "" {
			names = append(names, name)
		}
	}

	return names
}

// sortLines returns out with its lines in byte order (rpm -qa lists in
// database order, `dnf list installed` alphabetically).
func sortLines(out []byte) []byte {
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return out
	}

	slices.Sort(lines)

	return []byte(strings.Join(lines, "\n") + "\n")
}

// Run prints the release and returns the process exit status (always 0, like
// the legacy tool: a missing .version file yields an empty release).
func Run(ctx context.Context, env sys.System, stdout, stderr io.Writer, opts *Options) int {
	cliutil.InitCLILogging(stderr)

	// Legacy displayVersion: `cat .version` with a missing file yields an
	// empty release and exit 0.
	version, _ := env.FS.ReadFile(cliutil.BasePath(".version"))
	_, _ = fmt.Fprintf(stdout, "Carbonio Release %s\n", strings.TrimSpace(string(version)))

	if !opts.Packages {
		return 0
	}

	out := installedPackages(ctx, env.Exec, distroID(env.FS, osReleasePath))
	if len(out) == 0 {
		// Legacy displayPackagesVersion ends the release line with a blank one.
		_, _ = fmt.Fprintln(stdout)

		return 0
	}

	_, _ = fmt.Fprintln(stdout, "\nInstalled packages:")
	_, _ = fmt.Fprint(stdout, string(out))

	return 0
}

// distroID returns the ID= value of the os-release file at path in fsys, unquoted, or
// "" when the file is unreadable or has no ID.
func distroID(fsys sys.FS, path string) string {
	data, err := fsys.ReadFile(path)
	if err != nil {
		return ""
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if val, ok := strings.CutPrefix(line, "ID="); ok {
			return strings.Trim(val, "\"")
		}
	}

	return ""
}
