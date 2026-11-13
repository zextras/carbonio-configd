<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Statistics collectors (`configd stat`)

`configd stat <name> [options]` runs one statistics collector in the
foreground, and `configd stat run <collector>...` runs several in one process.
They are the Go port of the `zmstat-*` Perl scripts of `carbonio-core`, and
they are in the configd binary (`/opt/zextras/bin/configd`), not in the
bootstrap binary.

`/opt/zextras/libexec/setup stat ...` (`carbonio-bootstrap`) is the same
command, kept for the `zmstat-<name>` wrappers of the `carbonio-core` releases
that were written for it; it only calls the same code. New wrappers call
`configd stat`.

Collectors: `proc cpu vm io df fd allprocs mysql nginx mtaqueue ldap`. Only
the user `zextras` may run them, and root for `fd`.

## The scripts

carbonio-core installs `libexec/zmstat-proc`, `-cpu`, `-vm`, `-io`, `-df`,
`-fd`, `-allprocs`, `-mysql`, `-nginx`, `-mtaqueue` and `-ldap` as `exec
/opt/zextras/bin/configd stat <name> "$@"`. Each collector keeps the
command line (`-i/--interval`, `-l/--log`, `-c/--console`, and for mtaqueue
`--max-qsize`, `--backoff-interval`, `--max-interval`; `zmstat-io -x` is the
extended io collector), the usage text, the pid file (`/run/carbonio/stats`),
the CSV in `zmstat/` (name, heading, column order, number formatting,
`MM/DD/YYYY HH:MM:SS` timestamps), the rotation (SIGHUP, and at midnight to
`<dir>/YYYY-MM-DD/<name>.gz`) and the syslog lines of `Zextras::Mon::Stat.pm`,
so `zmstat-chart`, the chart tool and log rotation keep working. The interval
comes from `zmstat_interval` (`zmstat_disk_interval` for df). The `stats`
service runs the collectors itself (`zmstat_mysql_enabled`,
`zmstat_nginx_enabled` and `zmstat_mtaqueue_enabled` are still the keys of the
optional ones; `ldap` has no key and is run by hand).

## One process for all the collectors

`configd stat run <collector>...` runs several collectors in one process: one
Go runtime instead of one per collector. The eight collectors every node runs
took 157 MB resident as eight processes (about 20 MB each) and take 24 MB as
one, measured over 60 s on a Carbonio node; the Perl collectors took about
84 MB. The process sets the garbage collection target to 25%
(`debug.SetGCPercent`, no environment variable): that is 23.8 MB resident
instead of 26.7 MB with the default, for 0.04 s of CPU more per minute. A
memory limit did not help below that and risks collecting continuously on a
host with many processes, so there is none.

Each argument is the command line of one collector as the `zmstat-<name>`
script takes it, so `configd stat run proc cpu "io -x" df "mtaqueue --max-qsize
5000"` runs five. The `stats` service starts `configd stat run proc cpu vm "io
-x" df io fd allprocs` plus `mysql`, `nginx` and `mtaqueue` when their
`zmstat_*_enabled` key is `TRUE`, and logs to `log/zmstat.out` (it was
`log/zmstat-<name>.out`, one per collector). The process is recognised by
`bin/configd stat`; the `libexec/setup stat` processes and the Perl `zmstat-*`
scripts of earlier releases are stopped when the service stops, and before it
starts, so no collector runs twice after an upgrade.

- Each collector is a goroutine with its own interval (`-i`, else
  `zmstat_interval`, `zmstat_disk_interval` for `df`), its own CSV
  (`-l`) and its own rotation (midnight and SIGHUP), exactly as in a process
  of its own. `-c` is accepted and writes to the shared standard output.
- SIGHUP rotates the log of every collector; SIGTERM and SIGINT stop all of
  them.
- A collector that cannot start (its log cannot be opened, `/sys/block` cannot
  be listed), or that panics, is reported on stderr as `stat <name>: ...` and
  the others keep running; the exit status is then `1`. A sample that fails is
  reported at its tick, as it always was.
- The process has the pid file `stats.pid` (what a service manager
  supervises: `carbonio-stats.service` has `PIDFile=/run/carbonio/stats/stats.pid`)
  and each collector besides has the pid file `<name>.pid` of a process of its
  own (`io-x.pid` for `io -x`), all naming the process. A collector started by
  hand (`zmstat-cpu`) therefore still finds the collector running and leaves
  it alone, and a second `configd stat run` with `stats.pid` of a live process
  ends with status `0`. A collector whose pid file names another live process
  is left to that one. `zmstat-fd stop` of a process that runs several
  collectors stops all of them.
- All collectors run as `zextras`, so none needs a process of its own for its
  privileges: `fd` needs no root (see below) and no collector uses `sudo`.
  `ldap`, which configd does not start, may be given to `run` too. The
  arguments are checked before anything starts (an unknown collector, a bad
  option, a collector given twice, an argument that is not an option: status
  `1`, nothing runs).
- The service manager stops and rotates through the pid files (the process of
  each, once), so processes of the earlier per-collector layout are still
  stopped and rotated after an upgrade.

The numbers come from `/proc` and `/sys` instead of `vmstat`, `iostat`, `ps`
and `df`. Only `mysql` (`bin/mysql`, with the password in `MYSQL_PWD`, not on
the command line) and `mtaqueue` (`postqueue -p`, and `postfix-prometheus` in
the background on every sample) still run another program.

Differences from the scripts:

- Samples are aligned to the first round second (`t0 + k*interval`); the
  scripts slept an interval after each sample and drifted. A sample that
  fails is logged on stderr and skipped. An interval below 1 is the default.
- A log whose last line has no timestamp is not treated as stale. A pid file
  of a live process that is not a zmstat process is replaced. The pid file is
  removed on a clean stop. The syslog marker of long messages is an upper-case
  random UUID.
- `io`: computed from `/sys/block/*/stat` and `/proc/uptime`; the devices are
  those with I/O when the collector starts, sorted by name; one that
  disappears reads zero. The heading follows sysstat 12.5 or later (with
  `kB_dscd`), the `-x` columns sysstat 12.7.4.
- `vm`: the procps 3.3 columns without `gu`; the first reading only sets the
  reference.
- `proc`: `ps` is replaced by `/proc` (the user is the Uid of the process);
  the postfix master pattern bug of the script is not copied; when the
  enabled services cannot be read from LDAP the default process list is
  used; the memory columns use 4 kB pages, as the script did.
- `allprocs`, `nginx`: names with spaces are parsed correctly and the
  processes are sorted by name; no row is written on the first tick, and
  `nginx` writes none while there is no pid file (the script waited 15 s).
- `df`: from the mount table and `statfs`, one row per device, the shortest
  mount point wins; squashfs, udev, devtmpfs, overlay and efivarfs and
  mounts that are not backed by a `/` device are skipped.
- `fd`: one pid file (`fd.pid`); the sudo wrapper and `fd-real.pid` are gone,
  so the sudoers entry for `zmstat-fd` is no longer needed. The script ran
  its sampling loop as root through sudo, but both of its columns are
  readable as `zextras`: `fd_count` is the first number of
  `/proc/sys/fs/file-nr` and `mailboxd_fd_count` the entries of
  `/proc/<pid>/fd` of mailboxd, which runs as `zextras` (the `pgrep -o -f`
  that finds it only reads command lines). configd runs as `zextras`
  (`User=zextras`) and starts the collector as itself, so the columns are the
  ones the script wrote; only a process of another user would be out of
  reach, and the script did not count any. `configd stat fd` still accepts root
  (an operator running `zmstat-fd` by hand); it hands the log to `zextras`
  with mode 0640. `rotate` only signals the running collector; `-c` skips the
  check of the log path.
- `ldap`: go-ldap over `ldapi` instead of `ldapsearch`; no debug output.
