// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"syscall"

	lcfg "github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/zxadmin"
)

const (
	lcKeySoapServer = "zimbra_zmprov_default_soap_server"
	lcKeyAdminPort  = "zimbra_admin_service_port"
)

// runReload implements -l: ask mailboxd to reload its local config through the
// admin SOAP endpoint (legacy LocalConfigCLI.reload). Like the legacy tool, the
// target host/port and credentials come from the node's own localconfig.xml
// (LC), not from -c, and no other option is checked against -l.
func (o *localconfigOpts) runReload(ctx context.Context) error {
	// The zxadmin client logs every failure; legacy prints only the final
	// error, so keep the library diagnostics off the CLI's stderr.
	logger.InitStructuredLogging(&logger.Config{
		Format: logger.FormatText,
		Level:  logger.LogLevelError,
		Output: io.Discard,
	})

	cfg, err := loadNodeConfig(o.nodeConfigPath)
	if err != nil {
		return err
	}

	return reloadLocalConfig(ctx, cfg)
}

// loadNodeConfig loads the default localconfig.xml with defaults and variables
// resolved. A missing file is not an error (legacy only warns and uses defaults).
func loadNodeConfig(path string) (map[string]string, error) {
	cfg, err := lcfg.LoadResolvedConfigFromFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		cfg = make(map[string]string)
		lcfg.MergeDefaults(cfg)
		lcfg.Interpolate(cfg)

		return cfg, nil
	}

	return cfg, err
}

// reloadLocalConfig sends ReloadLocalConfigRequest to the server named by cfg.
// A refused connection is reported like legacy: "'mailbox' service is not running".
func reloadLocalConfig(ctx context.Context, cfg map[string]string) error {
	baseURL := "https://" + net.JoinHostPort(cfg[lcKeySoapServer], cfg[lcKeyAdminPort])

	if err := zxadmin.NewWithBaseURL(baseURL, cfg).ReloadLocalConfig(ctx); err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) {
			return failf("'mailbox' service is not running")
		}

		return err
	}

	return nil
}
