// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strconv"

	"github.com/zextras/carbonio-configd/internal/config"
)

// Where the packages install their systemd sysusers and tmpfiles snippets.
const (
	prepSysusersGlob = "/usr/lib/sysusers.d/carbonio-*.conf"
	prepTmpfilesGlob = "/usr/lib/tmpfiles.d/carbonio-*.conf"
)

// StepPrepareHost creates the system users and the directories the packages
// declare (the work the carbonio-bootstrap script did before it started
// setup.pl). It runs every time, before any other step, and has no
// .install_history stage.
func StepPrepareHost() Step {
	return Step{
		Title: "Preparing system users and directories",
		Apply: prepareHost,
	}
}

func prepareHost(e *Env) error {
	runSnippets(e, "systemd-sysusers", prepSysusersGlob, nil)

	conf := e.Paths.Path("conf")

	if _, err := e.Sys.FS.Stat(conf); errors.Is(err, fs.ErrNotExist) {
		e.Log.Detail("%s does not exist: not changing its owner", conf)
		runSnippets(e, "systemd-tmpfiles", prepTmpfilesGlob, []string{"--create"})

		return nil
	}

	uid, gid, err := zextrasIDs(e)
	if err != nil {
		return err
	}

	// systemd-tmpfiles skips the files under a directory that a non-root user
	// owns (an unsafe path transition), so conf is root's while it runs.
	if err := e.Sys.FS.Chown(conf, 0, 0); err != nil {
		return fmt.Errorf("chown root:root %s: %w", conf, err)
	}

	runSnippets(e, "systemd-tmpfiles", prepTmpfilesGlob, []string{"--create"})

	if err := e.Sys.FS.Chown(conf, uid, gid); err != nil {
		return fmt.Errorf("chown %s:%s %s: %w", config.ZextrasUser, config.ZextrasUser, conf, err)
	}

	return nil
}

// runSnippets runs tool with args and each file matching pattern. A failure is
// logged, never returned: the legacy script ignored it, and a host without
// systemd has no such tool.
func runSnippets(e *Env, tool, pattern string, args []string) {
	files, err := e.Sys.FS.Glob(pattern)
	if err != nil {
		e.Log.Detail("%s: %v", pattern, err)

		return
	}

	for _, f := range files {
		err := e.Root(tool, append(append([]string{}, args...), f)...)

		switch {
		case errors.Is(err, exec.ErrNotFound):
			e.Log.Detail("%s is not installed: skipping the %s files", tool, pattern)

			return
		case err != nil:
			e.Log.Detail("%s %s failed (ignored): %v", tool, f, err)
		}
	}
}

// zextrasIDs returns the uid and gid of the zextras user.
func zextrasIDs(e *Env) (uid, gid int, err error) {
	for _, q := range []struct {
		flag string
		dst  *int
	}{{"-u", &uid}, {"-g", &gid}} {
		out, err := srvZextrasID(e, q.flag)
		if err != nil {
			return 0, 0, err
		}

		if *q.dst, err = strconv.Atoi(out); err != nil {
			return 0, 0, fmt.Errorf("id %s %s: %q is not a number", q.flag, config.ZextrasUser, out)
		}
	}

	return uid, gid, nil
}
