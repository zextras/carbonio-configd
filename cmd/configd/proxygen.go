// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/zextras/carbonio-configd/internal/cli/proxy"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
	proxygen "github.com/zextras/carbonio-configd/internal/proxy"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// generateProxy is the in-process generator behind "configd proxy gen": the
// generator the daemon's PROXYGEN directive runs, fed from the configuration
// loaded here (LDAP and localconfig) and steered by the legacy ProxyConfGen
// flags. Nothing goes through the daemon, so the files are on disk when it
// returns.
func generateProxy(env sys.System) proxy.GenFunc {
	return func(ctx context.Context, stdout io.Writer, o *proxy.GenOptions) error {
		return runProxyGen(ctx, env, stdout, o)
	}
}

func runProxyGen(ctx context.Context, env sys.System, stdout io.Writer, o *proxy.GenOptions) error {
	gen, err := newProxyGenerator(ctx, env, o)
	if err != nil {
		return err
	}

	gen.SetDryRun(ctx, o.DryRun)
	gen.SetVerbose(ctx, o.Verbose)
	gen.ApplyOverrides(ctx, o.Overrides)

	if o.Defaults || o.Definitions {
		return gen.WriteVariables(ctx, stdout)
	}

	if err := gen.GenerateAll(ctx); err != nil {
		return err
	}

	if !o.DryRun {
		logger.InfoContext(ctx, "Proxy configuration files are generated successfully")
	}

	return nil
}

func proxySettings(o *proxy.GenOptions) *proxygen.Settings {
	return &proxygen.Settings{
		TemplateDir:        o.TemplateDir,
		IncludeDir:         o.IncludeDir,
		ConfPrefix:         o.Prefix,
		TemplatePrefix:     o.TemplatePrefix,
		ForceDNSResolution: o.ForceDNS,
	}
}

// adjustForProxyGen applies the -w (work dir) and -s (server) flags.
func adjustForProxyGen(o *proxy.GenOptions) func(*config.Config) {
	return func(cfg *config.Config) {
		if o.WorkDir != "" {
			cfg.BaseDir = o.WorkDir
		}

		if o.Server != "" {
			cfg.Hostname = o.Server
		}
	}
}

// newProxyGenerator builds the generator over the configuration loaded from
// LDAP. -d needs no configuration, so the generator then holds the defaults
// and nothing is loaded (legacy -d never reads LDAP).
func newProxyGenerator(ctx context.Context, env sys.System, o *proxy.GenOptions) (*proxygen.Generator, error) {
	opts := &runtimeOpts{adjust: adjustForProxyGen(o)}

	if o.Defaults {
		cfg, err := newConfig(ctx, opts)
		if err != nil {
			return nil, err
		}

		return proxygen.LoadConfiguration(ctx, env, cfg, proxygen.Sources{Settings: proxySettings(o)})
	}

	rt, err := buildRuntime(ctx, env, opts)
	if err != nil {
		return nil, err
	}

	logger.InfoContext(ctx, "Loading server object: "+rt.cfg.Hostname)

	if err := rt.cm.LoadAllConfigs(ctx); err != nil {
		return nil, fmt.Errorf("cannot load the configuration (make sure the server specified with -s exists): %w", err)
	}

	return rt.cm.NewProxyGenerator(ctx, *proxySettings(o))
}
