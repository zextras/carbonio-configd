// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package postfixconf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// RunFunc runs postconf (absolute path, clean environment) and returns its
// combined output.
type RunFunc func(ctx context.Context, path string, args []string) ([]byte, error)

// Options configures the root side. Production uses DefaultOptions; tests point
// every path into a temporary directory.
type Options struct {
	StagingDir string
	ConfDir    string
	Postconf   string
	LockPath   string

	// StagingUID is the owner accepted for staged files and the staging
	// directory (the zextras user); root is accepted too (configd running as
	// root in a container stages as root).
	StagingUID uint32
	// InstallUID and InstallGID own the installed files (root).
	InstallUID, InstallGID int

	// Run executes postconf; nil runs the real binary.
	Run RunFunc
}

// DefaultOptions returns the production options for the given staging owner.
func DefaultOptions(stagingUID uint32) Options {
	return Options{
		StagingDir: StagingDir,
		ConfDir:    ConfDir,
		Postconf:   PostconfPath,
		LockPath:   LockPath,
		StagingUID: stagingUID,
	}
}

func runPostconf(ctx context.Context, path string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, args...) //nolint:gosec // G204: absolute fixed path, arguments validated
	cmd.Dir = "/"
	// Clean environment: no MAIL_CONFIG, and never -c.
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w", path, strings.Join(args, " "), err)
	}

	return out, nil
}

func (o *Options) run(ctx context.Context, args []string) ([]byte, error) {
	if o.Run != nil {
		return o.Run(ctx, o.Postconf, args)
	}

	return runPostconf(ctx, o.Postconf, args)
}

// lock takes the exclusive helper lock; the returned func releases it.
func (o *Options) lock() (func(), error) {
	// The directory normally is the systemd unit's RuntimeDirectory; sudo,
	// uid-0 and container runs create it. Root-only, so nobody else can
	// replace the lock file.
	dir := filepath.Dir(o.LockPath)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil { //nolint:gosec // G301: /run-like parent
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}

	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}

	f, err := os.OpenFile(o.LockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", o.LockPath, err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil { //nolint:gosec // fd fits int
		_ = f.Close()

		return nil, fmt.Errorf("lock %s: %w", o.LockPath, err)
	}

	return func() { _ = f.Close() }, nil // closing drops the flock
}

// Commit validates change and installs it: files, then unset, then set.
// A validation error leaves everything untouched.
func (o *Options) Commit(ctx context.Context, change *Change) error {
	if err := change.Validate(); err != nil {
		return err
	}

	if change.Empty() {
		return nil
	}

	conf, err := o.openConfDir()
	if err != nil {
		return err
	}
	defer func() { _ = conf.Close() }()

	if err := o.ensureMainCf(conf); err != nil {
		return err
	}

	for _, name := range InstallableFiles {
		data, ok := change.Files[name]
		if !ok {
			continue
		}

		if err := o.install(conf, name, data); err != nil {
			return err
		}
	}

	return o.runPostconfSteps(ctx, change)
}

// runPostconfSteps runs postconf -X for the unset list, then postconf -e for the
// assignments.
func (o *Options) runPostconfSteps(ctx context.Context, change *Change) error {
	if len(change.Unset) > 0 {
		// postconf -X silently skips a parameter main.cf does not define
		// (postconf_edit.c pcf_edit_main), so any failure is real.
		if out, err := o.run(ctx, append([]string{"-X"}, change.Unset...)); err != nil {
			return fmt.Errorf("postconf -X failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}

	if len(change.Set) > 0 {
		args := []string{"-e"}
		for _, kv := range change.Set {
			args = append(args, kv.Key+"="+kv.Value)
		}

		if out, err := o.run(ctx, args); err != nil {
			return fmt.Errorf("postconf -e failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}

	return nil
}

// openConfDir opens ConfDir and refuses to continue unless it is owned by the
// install user, not group/world writable, and main.cf/master.cf (if present)
// are regular, root-owned files. Everything after this goes through the
// directory descriptor, so a path swap cannot redirect the writes.
func (o *Options) openConfDir() (*os.File, error) {
	fd, err := syscall.Open(o.ConfDir, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", o.ConfDir, err)
	}

	dir := os.NewFile(uintptr(fd), o.ConfDir) //nolint:gosec // fd from Open

	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		_ = dir.Close()

		return nil, fmt.Errorf("stat %s: %w", o.ConfDir, err)
	}

	if int(st.Uid) != o.InstallUID || int(st.Gid) != o.InstallGID || st.Mode&0o022 != 0 {
		_ = dir.Close()

		return nil, fmt.Errorf("%s must be owned by %d:%d and not group/world writable; refusing to touch it",
			o.ConfDir, o.InstallUID, o.InstallGID)
	}

	for _, name := range []string{MainCf, MasterCf} {
		if err := o.checkConfFile(name); err != nil {
			_ = dir.Close()

			return nil, err
		}
	}

	return dir, nil
}

func (o *Options) checkConfFile(name string) error {
	path := filepath.Join(o.ConfDir, name)

	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || !fi.Mode().IsRegular() || int(st.Uid) != o.InstallUID || fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s must be a regular file owned by %d and not group/world writable; refusing to continue",
			path, o.InstallUID)
	}

	return nil
}

// createAt creates a new file name in dir (O_EXCL, no symlink following).
func createAt(dir *os.File, name string) (*os.File, error) {
	fd, err := syscall.Openat(int(dir.Fd()), name, //nolint:gosec // fd fits int
		syscall.O_CREAT|syscall.O_EXCL|syscall.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", name, err)
	}

	return os.NewFile(uintptr(fd), name), nil //nolint:gosec // fd from Openat
}

// ensureMainCf creates an empty root-owned 0644 main.cf when missing.
func (o *Options) ensureMainCf(conf *os.File) error {
	f, err := createAt(conf, MainCf)
	if errors.Is(err, syscall.EEXIST) {
		return nil
	}

	if err != nil {
		return err
	}

	return finishFile(f, o.InstallUID, o.InstallGID)
}

// finishFile sets owner and mode, flushes and closes f.
func finishFile(f *os.File, uid, gid int) error {
	name := f.Name()

	steps := []func() error{
		func() error { return f.Chown(uid, gid) },
		func() error { return f.Chmod(InstalledFileMode) },
		f.Sync,
		f.Close,
	}

	for i, step := range steps {
		if err := step(); err != nil {
			if i < len(steps)-1 {
				_ = f.Close()
			}

			return fmt.Errorf("finish %s: %w", name, err)
		}
	}

	return nil
}

// install atomically replaces conf/name with data, owned by root, 0644, using
// only the directory descriptor.
func (o *Options) install(conf *os.File, name string, data []byte) error {
	var (
		tmp     *os.File
		tmpName string
	)

	for range 16 {
		tmpName = fmt.Sprintf(".%s.tmp-%016x", name, rand.Uint64()) //nolint:gosec // uniqueness only; O_EXCL guards

		f, err := createAt(conf, tmpName)
		if errors.Is(err, syscall.EEXIST) {
			continue
		}

		if err != nil {
			return err
		}

		tmp = f

		break
	}

	if tmp == nil {
		return fmt.Errorf("create temp for %s: no free name", name)
	}

	dirFD := int(conf.Fd()) //nolint:gosec // fd fits int
	committed := false

	defer func() {
		if !committed {
			_ = syscall.Unlinkat(dirFD, tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("write %s: %w", tmpName, err)
	}

	if err := finishFile(tmp, o.InstallUID, o.InstallGID); err != nil {
		return err
	}

	if err := syscall.Renameat(dirFD, tmpName, dirFD, name); err != nil {
		return fmt.Errorf("rename to %s: %w", filepath.Join(o.ConfDir, name), err)
	}

	committed = true

	_ = syscall.Fsync(dirFD)

	return nil
}

// Apply consumes the staging directory. An empty or missing directory is a
// no-op. Any entry that is not an allowed regular file owned by StagingUID is an
// error and nothing is applied.
func (o *Options) Apply(ctx context.Context) error {
	unlock, err := o.lock()
	if err != nil {
		return err
	}
	defer unlock()

	dirFD, err := syscall.Open(o.StagingDir, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if errors.Is(err, syscall.ENOENT) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("open staging dir %s: %w", o.StagingDir, err)
	}

	dir := os.NewFile(uintptr(dirFD), o.StagingDir) //nolint:gosec // fd from Open
	defer func() { _ = dir.Close() }()

	if err := o.checkDir(dirFD); err != nil {
		return err
	}

	entries, err := dir.ReadDir(-1)
	if err != nil {
		return fmt.Errorf("read staging dir: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	sort.Strings(names)

	change, err := o.readStaged(dirFD, names)
	if err != nil {
		return err
	}

	if err := o.Commit(ctx, change); err != nil {
		return err
	}

	for _, n := range names {
		if err := syscall.Unlinkat(dirFD, n); err != nil {
			return fmt.Errorf("remove staged %s: %w", n, err)
		}
	}

	return nil
}

func (o *Options) ownerOK(uid uint32) bool { return uid == o.StagingUID || uid == 0 }

func (o *Options) checkDir(fd int) error {
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return fmt.Errorf("stat staging dir: %w", err)
	}

	if !o.ownerOK(st.Uid) || st.Mode&0o022 != 0 {
		return validationf("staging dir %s must be owned by uid %d (or root) and not group/world writable",
			o.StagingDir, o.StagingUID)
	}

	return nil
}

// readStaged reads every staged entry through the directory descriptor and
// builds the Change. Unknown names, non-regular files (symlinks, FIFOs,
// directories), foreign owners and oversize files are rejected.
func (o *Options) readStaged(dirFD int, names []string) (*Change, error) {
	change := &Change{Files: map[string][]byte{}}

	for _, name := range names {
		if name != SetFile && name != UnsetFile && !IsInstallable(name) {
			return nil, validationf("unexpected staging entry %q", name)
		}

		data, err := o.readEntry(dirFD, name)
		if err != nil {
			return nil, err
		}

		switch name {
		case SetFile:
			if change.Set, err = parseSet(data); err != nil {
				return nil, err
			}
		case UnsetFile:
			change.Unset = parseUnset(data)
		default:
			change.Files[name] = data
		}
	}

	return change, nil
}

func (o *Options) readEntry(dirFD int, name string) ([]byte, error) {
	fd, err := syscall.Openat(dirFD, name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, validationf("open staged %q: %v", name, err)
	}

	f := os.NewFile(uintptr(fd), name) //nolint:gosec // fd from Openat
	defer func() { _ = f.Close() }()

	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return nil, fmt.Errorf("stat staged %q: %w", name, err)
	}

	if st.Mode&syscall.S_IFMT != syscall.S_IFREG {
		return nil, validationf("staged %q is not a regular file", name)
	}

	if !o.ownerOK(st.Uid) {
		return nil, validationf("staged %q is owned by uid %d, want %d or root", name, st.Uid, o.StagingUID)
	}

	data, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("read staged %q: %w", name, err)
	}

	if len(data) > MaxFileSize {
		return nil, validationf("staged %q is larger than %d bytes", name, MaxFileSize)
	}

	return data, nil
}

func parseSet(data []byte) ([]KV, error) {
	var out []KV

	for line := range bytes.SplitSeq(data, []byte("\n")) {
		if len(line) == 0 {
			continue
		}

		key, value, ok := strings.Cut(string(line), "=")
		if !ok {
			return nil, validationf("postconf.set contains a line that is not KEY=VALUE")
		}

		out = append(out, KV{Key: key, Value: value})
	}

	return out, nil
}

func parseUnset(data []byte) []string {
	var out []string

	for line := range strings.SplitSeq(string(data), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}

	return out
}
