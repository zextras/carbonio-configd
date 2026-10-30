// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package initcmd implements "configd init <component>": it asks the running
// daemon to generate the component's configs, then starts its service.
package initcmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/cli/rewrite"
	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/services"
)

const (
	componentMTA   = "mta"
	componentProxy = "proxy"
)

var initComponents = map[string]string{
	componentMTA:   "MTA (postfix, saslauthd, LDAP transport maps)",
	componentProxy: "Proxy (nginx reverse proxy configs)",
}

// Options are the parsed command-line flags of "configd init".
type Options struct {
	// Component to initialize (mta, proxy).
	Component string
	// Force allows reinitialization even if configs exist.
	Force bool
}

// Run executes the init subcommand and returns the process exit status.
// It triggers a REWRITE on the running daemon for the specified component,
// then starts the corresponding service.
func Run(ctx context.Context, stdout, stderr io.Writer, opts *Options) int {
	if !cliutil.RequireZextras(stderr) {
		return 1
	}

	return run(ctx, stdout, stderr, opts, localconfig.LoadResolvedConfig)
}

func run(
	ctx context.Context, stdout, stderr io.Writer, opts *Options, loadConfig func() (map[string]string, error),
) int {
	component := opts.Component

	desc, ok := initComponents[component]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "Unknown component: %s\n", component)

		return 1
	}

	// Check if configs already exist (basic guard)
	if !opts.Force && configsExist(component) {
		_, _ = fmt.Fprintf(stderr, "Configs for %s already exist. Use --force to reinitialize.\n", component)

		return 1
	}

	_, _ = fmt.Fprintf(stdout, "Initializing %s...\n", desc)

	// Send REWRITE to running daemon
	lc, err := loadConfig()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error loading localconfig: %v\n", err)

		return 1
	}

	listenPort, ipMode := rewrite.ListenerAddress(lc)

	if rewrite.ContactService("REWRITE", []string{component}, listenPort, ipMode) {
		_, _ = fmt.Fprintln(stderr, "Error: configd daemon is not running. Start it first:")
		_, _ = fmt.Fprintln(stderr, "  systemctl start carbonio-configd.service")

		return 1
	}

	_, _ = fmt.Fprintf(stdout, "Config generation complete for %s.\n", component)

	// Start the service
	_, _ = fmt.Fprintf(stdout, "Starting %s service...\n", component)

	if err := services.ServiceStart(ctx, component); err != nil {
		logger.WarnContext(ctx, "Service start failed", "component", component, "error", err)
		_, _ = fmt.Fprintf(stderr, "Warning: service start failed: %v\n", err)
		_, _ = fmt.Fprintln(stderr, "Configs were generated. Start the service manually.")
	} else {
		_, _ = fmt.Fprintf(stdout, "Done. %s is running.\n", component)
	}

	return 0
}

func configsExist(component string) bool {
	switch component {
	case componentMTA:
		_, err := os.Stat(cliutil.BasePath("common", "conf", "postfix", "main.cf"))
		return err == nil
	case componentProxy:
		_, err := os.Stat(cliutil.BasePath("conf", "nginx", "nginx.conf"))
		return err == nil
	}

	return false
}
