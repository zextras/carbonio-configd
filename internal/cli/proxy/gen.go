// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// GenOptions are the legacy ProxyConfGen flags of "configd proxy gen".
type GenOptions struct {
	Verbose        bool     // -v
	DryRun         bool     // -n
	Defaults       bool     // -d: print the default variable map
	Definitions    bool     // -D: print the variable map after loading the configuration
	ForceDNS       bool     // -f
	WorkDir        string   // -w
	TemplateDir    string   // -t
	Prefix         string   // -p
	TemplatePrefix string   // -P
	IncludeDir     string   // -i
	Server         string   // -s
	Overrides      []string // -c name=value
	// Args are the positional arguments left over; legacy ProxyConfGen
	// prints its usage and exits 0 when there are any.
	Args []string
}

// GenFunc generates the proxy configuration in this process and returns the
// error that makes the command exit 1. It writes -d/-D output to stdout.
type GenFunc func(ctx context.Context, stdout io.Writer, opts *GenOptions) error

// genUsage is printed for stray positional arguments, as the Java usage().
const genUsage = `ProxyConfGen [options]
where [options] are one of:
 -c,--config <arg>             Override a config variable (name=value)
 -D,--definitions              Print variable map Definitions after loading
                               the LDAP configuration
 -d,--defaults                 Print default variable map
 -f,--force-dns-resolution     Fail if a virtual hostname does not resolve
 -h,--help                     show this usage text
 -i,--include-dir <arg>        Directory (relative to $workdir/conf) of the
                               included files (default nginx/includes)
 -n,--dry-run                  Do not write any configuration
 -P,--template-prefix <arg>    Template file prefix (default $prefix)
 -p,--prefix <arg>             Config file prefix (default nginx.conf)
 -s,--server <arg>             Generate for this server object (default: the
                               local server)
 -t,--templatedir <arg>        Template directory
                               (default $workdir/conf/nginx/templates)
 -v,--verbose                  be verbose
 -w,--workdir <arg>            Working directory (default /opt/zextras)
ProxyConfGen generates the NGINX Proxy configuration files
`

// runGen is "configd proxy gen": synchronous in-process generation. Exit
// status: 0 on success (also for -d, -D and stray arguments, as legacy), 1 on
// any error.
func runGen(ctx context.Context, stdout, stderr io.Writer, opts *Options) int {
	g := opts.Gen
	if g == nil {
		g = &GenOptions{}
	}

	// Writing needs the zextras user; the read-only modes do not.
	if !g.DryRun && !g.Defaults && !g.Definitions && !cliutil.RequireZextras(stderr) {
		return 1
	}

	ctx = initGenLogging(ctx, stderr, g.Verbose)

	if opts.Generate == nil {
		_, _ = fmt.Fprintln(stderr, "Error: proxy generation is not available")

		return 1
	}

	if len(g.Args) > 0 && !g.Defaults && !g.Definitions {
		_, _ = fmt.Fprint(stdout, genUsage)

		return 0
	}

	if err := opts.Generate(ctx, stdout, g); err != nil {
		logger.ErrorContext(ctx, "Proxy configuration files generation is interrupted by errors", "error", err)
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

		return 1
	}

	return 0
}

// initGenLogging logs at INFO to stderr like ProxyConfGen's toolSetup("INFO"),
// DEBUG with -v.
func initGenLogging(_ context.Context, stderr io.Writer, verbose bool) context.Context {
	cfg := logger.DefaultConfig()
	cliutil.ConfigureLogFormat(cfg, stderr)
	cliutil.ConfigureLogLevel(cfg)

	if verbose {
		cfg.Level = logger.LogLevelDebug
	}

	cfg.Output = stderr

	logger.InitStructuredLogging(cfg)

	return logger.NewCorrelationID(context.Background())
}

// SplitOverrides returns the -c override values of a legacy command line
// "-c a=b c=d": commons-cli gives -c unlimited arguments, so every extra
// positional argument of the form name=value is one more override. Anything
// else stays positional.
func SplitOverrides(overrides, args []string) (all, rest []string) {
	all = append(all, overrides...)

	for _, a := range args {
		if strings.Contains(a, "=") {
			all = append(all, a)
		} else {
			rest = append(rest, a)
		}
	}

	return all, rest
}
