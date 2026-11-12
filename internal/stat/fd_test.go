// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
)

const mailboxdJava = "/opt/zextras/common/lib/jvm/java/bin/java\x00-Djetty.home=/opt/zextras/mailboxd\x00"

// stat-fd.pl get_fd_stat (69-76) and get_mbox_stat (77-99): the allocated
// descriptors of the system, and the entries of /proc/<pid>/fd of the oldest
// process pgrep -o -f finds for the mailboxd command (never the collector
// itself).
func TestFDGolden(t *testing.T) {
	t.Parallel()

	tree := files{
		"proc/sys/fs/file-nr": "3520\t0\t9223372036854775807\n",
		"proc/777/cmdline":    mailboxdJava, "proc/777/fd/0": "", "proc/777/fd/1": "", "proc/777/fd/2": "",
		"proc/800/cmdline": mailboxdJava, "proc/800/fd/0": "",
		"proc/4242/cmdline": mailboxdJava, "proc/4242/fd/0": "",
		"proc/50/cmdline": "/usr/bin/vim\x00mailboxd\x00",
	}
	te := newTestEnv(t, tree)
	c := &fd{env: te.Env}

	assert.Equal(t, "timestamp, fd_count, mailboxd_fd_count", c.Heading())
	assert.Equal(t, []string{stamp(0) + ", 3520, 3"}, te.sample(t, c, 0))
	assert.Empty(t, te.stderr.String())
}

func TestFDWithoutMailboxd(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{"proc/sys/fs/file-nr": "12 0 99\n"})
	c := &fd{env: te.Env}

	assert.Equal(t, []string{stamp(0) + ", 12, 0"}, te.sample(t, c, 0))
	assert.Equal(t, "Unable to determine mailboxd pid\n", te.stderr.String())
}

func TestFDMissingFileNr(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})
	_, err := (&fd{env: te.Env}).Sample(Tick{})

	require.ErrorContains(t, err, "/proc/sys/fs/file-nr")
}

// stat-fd.pl lines 26-44: "stop" kills the running collector (TERM, then KILL)
// and removes its pid file, also a stale one; "rotate" sends it SIGHUP.
func TestFDControl(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		args        []string
		running     bool
		wantSignals []syscall.Signal
		wantPidFile bool
	}{
		"stop a running collector": {args: []string{"stop"}, running: true,
			wantSignals: []syscall.Signal{0, syscall.SIGTERM}},
		"stop with a stale pid file": {args: []string{"stop"}, wantSignals: []syscall.Signal{0}},
		"rotate":                     {args: []string{"rotate"}, running: true, wantSignals: []syscall.Signal{syscall.SIGHUP}, wantPidFile: true},
	} {
		te := newTestEnv(t, files{})
		pidFile := filepath.Join(te.PidDir, "fd.pid")
		require.NoError(t, os.WriteFile(pidFile, []byte("555\n"), 0o644))

		if tc.running {
			te.fakes.Proc.Add(sys.ProcEntry{PID: 555})
		}

		code, done := fdControl(te.Env, tc.args)
		assert.True(t, done, name)
		assert.Zero(t, code, name)

		var got []syscall.Signal

		for _, s := range te.fakes.Proc.Signals() {
			assert.Equal(t, 555, s.PID, name)

			got = append(got, s.Sig)
		}

		assert.Equal(t, tc.wantSignals, got, name)
		assert.Equal(t, tc.wantPidFile, fileExists(pidFile), name)
	}
}

func TestFDControlLeavesOtherArgumentsAlone(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})

	for _, args := range [][]string{nil, {"-c"}, {"-i", "5"}} {
		_, done := fdControl(te.Env, args)
		assert.False(t, done, "%v", args)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

// stat-fd.pl lines 145-163: the collector may run as root, so the log has to
// resolve into /tmp, /var/log or the Carbonio log, data/tmp and zmstat
// directories (realpath, so a symlink out of them does not count).
func TestFDValidate(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})
	require.NoError(t, os.MkdirAll(filepath.Join(te.Base, "log"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(te.Base, "data/tmp"), 0o755))
	require.NoError(t, os.Symlink("/etc", filepath.Join(te.Dir, "escape")))

	refusal := "Error: Specified log file must be created in one of the following locations: /tmp /var/log " +
		filepath.Join(te.Base, "log") + " " + filepath.Join(te.Base, "data/tmp") + " " + te.Dir + "\n\n"

	for name, tc := range map[string]struct {
		opts options
		want string
	}{
		"in the zmstat directory": {opts: options{logPath: filepath.Join(te.Dir, "fd.csv")}},
		"in the log directory":    {opts: options{logPath: filepath.Join(te.Base, "log", "x", "fd.csv")}},
		"console":                 {opts: options{console: true, logPath: "-"}},
		"outside":                 {opts: options{logPath: "/etc/fd.csv"}, want: refusal},
		"symlink out":             {opts: options{logPath: filepath.Join(te.Dir, "escape", "fd.csv")}, want: refusal},
	} {
		err := fdValidate(te.Env, &tc.opts)
		if tc.want == "" {
			assert.NoError(t, err, name)

			continue
		}

		require.Error(t, err, name)
		assert.Equal(t, tc.want, err.Error(), name)
	}
}

// The log of a collector that may run as root is handed to zextras with mode
// 0640 (stat-fd.pl 174-175).
func TestFDOwn(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})

	var chowned []string

	te.Chown = func(p string) error { chowned = append(chowned, p); return nil }

	path := filepath.Join(te.Dir, "fd.csv")
	require.NoError(t, os.WriteFile(path, nil, 0o666))

	fdOwn(te.Env, path)

	st, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), st.Mode().Perm())
	assert.Equal(t, []string{path}, chowned)
}
