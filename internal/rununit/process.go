// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package rununit

import (
	"errors"

	"golang.org/x/sys/unix"
)

// waitPidExit blocks until the process pid is gone. The process is not a child
// of the caller (systemd started it), so it is watched through a pidfd, which
// becomes readable when the process exits; without pidfd_open (Linux < 5.3,
// or a seccomp filter) the process table is polled instead.
func (r *Runner) waitPidExit(pid int) error {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		if errors.Is(err, unix.ESRCH) {
			return nil
		}

		return r.pollPidExit(pid)
	}

	defer func() { _ = unix.Close(fd) }()

	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}} //nolint:gosec // an fd fits an int32

	for {
		if _, err := unix.Poll(fds, -1); err == nil {
			return nil
		} else if !errors.Is(err, unix.EINTR) {
			return r.pollPidExit(pid)
		}
	}
}

// pollPidExit polls the process table until pid has exited or turned zombie.
func (r *Runner) pollPidExit(pid int) error {
	for r.env.Proc.Alive(pid) {
		<-r.env.Clock.After(pollInterval)
	}

	return nil
}
