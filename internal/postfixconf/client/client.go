// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package client is configd's side of the postfixconf privilege boundary: it
// stages a change and triggers the root helper.
package client

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/postfixconf"
	"github.com/zextras/carbonio-configd/internal/sys"
)

const systemctlBin = "/usr/bin/systemctl"
const sudoBin = "/usr/bin/sudo"

// triggerMu serializes staging and triggering across every Client in the
// process: the staging directory is a single mailbox.
var triggerMu sync.Mutex

// Client is configd's side: it stages a Change in the zextras-owned staging
// directory and triggers the root helper. It never writes the Postfix
// configuration directory itself.
type Client struct {
	baseDir string
	env     sys.System
	systemd func() bool
}

// NewClient builds a Client for the installation under baseDir. systemd
// reports whether configd runs in systemd mode (nil means it does not).
func NewClient(baseDir string, env sys.System, systemd func() bool) *Client {
	return &Client{baseDir: baseDir, env: env, systemd: systemd}
}

func (c *Client) stagingDir() string {
	return filepath.Join(c.baseDir, "data", "postfix", "conf-staging")
}

func (c *Client) helper() string {
	return filepath.Join(c.baseDir, "libexec", "postfix-conf")
}

// IsConfPath reports whether path (absolute, or relative to the base
// directory) lies under the root-owned Postfix configuration directory, so
// writes to it must go through Apply.
func (c *Client) IsConfPath(path string) bool {
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.baseDir, path)
	}

	rel, err := filepath.Rel(filepath.Join(c.baseDir, "common", "conf"), filepath.Clean(path))

	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

// Apply validates change, stages it and runs the helper, returning when the
// helper has finished. Stale staged files of an earlier failed run are
// discarded first and, on failure, again afterwards.
func (c *Client) Apply(ctx context.Context, change postfixconf.Change) error {
	if change.Empty() {
		return nil
	}

	if err := change.Validate(); err != nil {
		return fmt.Errorf("postfix config change rejected: %w", err)
	}

	triggerMu.Lock()
	defer triggerMu.Unlock()

	if err := c.stage(&change); err != nil {
		return err
	}

	if err := c.trigger(ctx); err != nil {
		c.clearStaging(ctx)

		return err
	}

	return nil
}

func (c *Client) clearStaging(ctx context.Context) {
	entries, err := c.env.FS.ReadDir(c.stagingDir())
	if err != nil {
		return
	}

	for _, e := range entries {
		if err := c.env.FS.RemoveAll(filepath.Join(c.stagingDir(), e.Name())); err != nil {
			logger.WarnContext(ctx, "Failed to remove stale staged file", "name", e.Name(), "error", err)
		}
	}
}

func (c *Client) stage(change *postfixconf.Change) error {
	dir := c.stagingDir()

	if err := c.env.FS.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create postfix staging dir %s: %w", dir, err)
	}

	c.clearStaging(context.Background())

	files := maps.Clone(change.Files)
	if files == nil {
		files = map[string][]byte{}
	}

	if len(change.Unset) > 0 {
		files[postfixconf.UnsetFile] = []byte(strings.Join(change.Unset, "\n") + "\n")
	}

	if len(change.Set) > 0 {
		var b strings.Builder
		for _, kv := range change.Set {
			b.WriteString(kv.Key + "=" + kv.Value + "\n")
		}

		files[postfixconf.SetFile] = []byte(b.String())
	}

	for name, data := range files {
		if err := c.env.FS.WriteFileAtomic(filepath.Join(dir, name), data, 0o600); err != nil {
			return fmt.Errorf("stage %s: %w", name, err)
		}
	}

	return nil
}

// trigger runs the helper: through systemd in systemd mode, directly as root,
// else through sudo. It never falls back to writing the configuration itself.
func (c *Client) trigger(ctx context.Context) error {
	var (
		bin  string
		args []string
		how  string
	)

	switch {
	case c.systemd != nil && c.systemd():
		bin, args, how = systemctlBin, []string{"start", postfixconf.UnitName}, "systemctl start "+postfixconf.UnitName
	case c.env.Proc.UID() == 0:
		bin, args, how = c.helper(), []string{"apply"}, c.helper()+" apply"
	default:
		bin, args, how = sudoBin, []string{"-n", c.helper(), "apply"}, "sudo -n "+c.helper()+" apply"
	}

	out, err := sys.CombinedOutput(ctx, c.env.Exec, bin, args...)
	if err != nil {
		return fmt.Errorf("apply postfix configuration via %q failed: %w: %s "+
			"(Postfix configuration is root-owned and only %s may change it; "+
			"run configd under systemd, as root, or with a sudo grant for that helper)",
			how, err, strings.TrimSpace(string(out)), c.helper())
	}

	return nil
}
