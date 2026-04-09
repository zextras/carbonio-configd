// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

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

// InitCmd handles the "configd init <component>" subcommand.
type InitCmd struct {
	Component string `arg:"" help:"Component to initialize (mta, proxy)"`
	Force     bool   `short:"f" name:"force" help:"Allow reinitialization even if configs exist"`
}

// Run executes the init subcommand.
// It triggers a REWRITE on the running daemon for the specified component,
// then starts the corresponding service.
//
//nolint:unparam // Kong interface requires error return
func (c *InitCmd) Run() error {
	requireZextras()

	component := c.Component

	desc, ok := initComponents[component]
	if !ok {
		fmt.Fprintf(os.Stderr, "Unknown component: %s\n", component)
		os.Exit(1)
	}

	ctx := context.Background()

	// Check if configs already exist (basic guard)
	if !c.Force && configsExist(component) {
		fmt.Fprintf(os.Stderr,
			"Configs for %s already exist. Use --force to reinitialize.\n", component)
		os.Exit(1)
	}

	fmt.Printf("Initializing %s...\n", desc)

	// Send REWRITE to running daemon
	lc, err := localconfig.LoadResolvedConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading localconfig: %v\n", err)
		os.Exit(1)
	}

	listenPort, _ := strconv.Atoi(lc["zmconfigd_listen_port"])
	if listenPort == 0 {
		listenPort = 7171
	}

	ipMode := lc["zimbraIPMode"]
	if ipMode == "" {
		ipMode = ipModeIPv4
	}

	if ContactService("REWRITE", []string{component}, listenPort, ipMode) {
		fmt.Fprintln(os.Stderr, "Error: configd daemon is not running. Start it first:")
		fmt.Fprintln(os.Stderr, "  systemctl start carbonio-configd.service")
		os.Exit(1)
	}

	fmt.Printf("Config generation complete for %s.\n", component)

	// Start the service
	fmt.Printf("Starting %s service...\n", component)

	if err := services.ServiceStart(ctx, component); err != nil {
		logger.WarnContext(ctx, "Service start failed", "component", component, "error", err)
		fmt.Fprintf(os.Stderr, "Warning: service start failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "Configs were generated. Start the service manually.")
	} else {
		fmt.Printf("Done. %s is running.\n", component)
	}

	return nil
}

func configsExist(component string) bool {
	switch component {
	case componentMTA:
		_, err := os.Stat("/opt/zextras/common/conf/postfix/main.cf")
		return err == nil
	case componentProxy:
		_, err := os.Stat("/opt/zextras/conf/nginx/nginx.conf")
		return err == nil
	}

	return false
}
