<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Performance

## LDAP error classification

Source: `internal/ldap/client.go` (`executeWithRetry`, `isLDAPErrorRetryable`)

LDAP errors are split into two classes that determine retry behaviour. The timeouts and the
TLS rules of the same client are in
[architecture.md](architecture.md#ldap-transport-security-and-timeouts).

**Permanent errors — fail immediately, no retry:**

| LDAP result code | Meaning |
|---|---|
| `NoSuchObject` | DN does not exist in the directory |
| `InvalidDNSyntax` | Malformed distinguished name |
| `InvalidCredentials` | Bind password is wrong |
| `InsufficientAccessRights` | Bound user lacks permission |
| `ObjectClassViolation` | Schema violation |
| `AttributeOrValueExists`, `NoSuchAttribute` | The modification contradicts the entry's state; repeating it cannot change that (`ModifyValues` treats both as converged) |

These represent conditions that will not resolve by waiting. Retrying them would only
stall the config cycle for `MaxRetries × delay` seconds. A query that targets an optional
or conditionally-present DN must not rely on retry behaviour to absorb a `NoSuchObject`
error — it will fail on the first attempt.

**Transient errors — retry with exponential backoff:**

| LDAP result code | Meaning |
|---|---|
| `ServerDown` | Connection refused or lost |
| `Timeout` | Operation timed out |
| `Busy` | Server temporarily unable to handle request |
| `Unavailable` | Server in maintenance or shutdown |
| `UnwillingToPerform` | Server declined the operation temporarily |
| _(any other LDAP result code)_ | Unknown: retried to be safe |
| _(non-LDAP network error)_ | TCP-level failure |

Default retry parameters: the first try plus up to **3 retries** (`MaxRetries`), initial delay
**100 ms** (`RetryDelay`), max delay **5 s** (`MaxRetryDelay`), with exponential backoff between
attempts. These can be overridden via `ldap.ClientConfig` at construction time. A retry that
fails to connect or hits a transport error discards the connection instead of returning it to
the pool, and closing the client abandons a pending backoff wait. Each attempt is bounded by
the 10 s dial, 10 s StartTLS+bind and 30 s per-request timeouts, so a dead server costs at most
a few of those, not an unbounded stall. The config load adds its own 60 s wait (see
[architecture.md](architecture.md#config-loading-pipeline)).

## Parallel config rewrites

Source: `internal/configmgr/manager_actions_rewrite.go` (`doRewrites`, `processRewrite`)

`ConfigManager.Execute` runs the template rewrites concurrently with proxygen, postconf, postconfd
and the LDAP writes; the mapped files run first and serially because the other steps read them
(see [architecture.md](architecture.md#cycle-model-load-snapshot-plan-execute-apply-restarts)).
Inside the rewrite step, the files run concurrently to shorten the phase when many need updating,
through a buffered-channel semaphore:

```go
const maxConcurrent = 4 // Tuned for balance between parallelism and I/O contention

semaphore := make(chan struct{}, maxConcurrent)

for i, entry := range entries {
    if cancelled(ctx) { break }   // shutdown stops launching new rewrites

    wg.Add(1)
    semaphore <- struct{}{}       // blocks when 4 goroutines are already running

    go func() {
        defer wg.Done()
        defer func() { <-semaphore }()
        record(entry.Source, cm.rewriteWorker(ctx, entry.Source, re, i+1, totalFiles))
    }()
}

wg.Wait()
```

Files that failed are returned as pending and re-enter the next plan with a `retry` reason; the
step reports the joined errors, which makes the cycle back off (see the main loop phases).

**Why 4?** The comment in the source reads: "Tuned for balance between parallelism and
I/O contention." Higher values increase throughput on SSDs but risk saturating the disk
queue on spinning media or network-mounted filesystems. Lower values leave parallelism on
the table. If you want to change `maxConcurrent`, benchmark on the target hardware first, with a
profiling build (see below) running one cycle.

**Atomic write pattern**: a rewrite renders the template into a temporary file
(`sys.FS.CreateTemp("", "zmconfigd-rewrite-")`, in the system temp directory), sets the file mode
(the section's mode, default `0440`) and then `rename`s it over the destination, so readers
never observe a partially-written file. When the temp directory is on another filesystem the
rename fails and the commit falls back to copy (`FS.CopyFile`, itself written to a `.configd-copy-*.tmp`
sibling and renamed) plus delete, which is atomic for the destination but not a single syscall.
If the transform or commit fails, the temp file is removed and the original is untouched. The
other writers of the daemon (mapped files, the metrics textfile through `sys.FS.WriteFileAtomic`)
use `fileutil.AtomicWrite`: a `.configd-*.tmp` file created in the destination's own
directory, then `rename`, so the rename is always atomic at the VFS level.

## Skipping what is already current

Source: `internal/configmgr/manager_actions_rewrite.go` (`targetCurrent`),
`internal/configmgr/manager_actions_mta.go` (`dropUnchangedPostconf`, `dropAbsentPostconfd`),
`internal/proxy/generator.go` (`isUnchanged`, `appendConfGenResult`)

A cycle that finds the host already in the planned state does no disk or Postfix work for it. What is
skipped never changes which services restart: restarts come from the plan (a changed section), not
from what Execute wrote.

- **Proxy files**: a generated file that is a regular file with the generated mode and the rendered
  bytes is not rewritten. `nginx.conf` ends with a result marker (`#__SUCCESS__` or
  `#__CONF_GEN_ERROR__:...`) that `zmnginxctl restart` reads; the check ignores one trailing marker, and
  after the run the file always ends with the marker of the current run (appended to a fresh file, replaced
  when only the marker changed, left alone when it is the same).
- **REWRITE targets**: the template is rendered in memory and the target is left alone when it is a regular
  file (a symlink is replaced) with the same bytes and the same permission bits. Targets under `common/conf`
  (installed by the root helper) also need `root:root` ownership and the helper's mode `0644`; when nothing
  of the batch differs, nothing is staged and the helper is not started. A hand-edited file differs, so an
  explicit `REWRITE <section>` still restores it.
- **postconf**: one `postconf -n` per Execute (shared by the postconf and the postconf -X step) gives the
  explicit main.cf settings. A `-e` operation is dropped only when main.cf defines the key with the same
  value, whitespace ignored. A key that main.cf does not define (default or master.cf only) is still set, and
  a failing read applies every operation. A `-X` operation is dropped when main.cf does not define the key.
  An empty batch runs nothing.

## Profiling tooling

Source: `cmd/configd/args_profiling.go`, `cmd/configd/profiling.go`, `cmd/configd/args_tracing.go`,
`Makefile`

Profiling and tracing are compiled in only when the binary is built with the matching build tag.
A default binary (`make build`, and the packaged one) has none of the flags below: the command line rejects them
as unknown, and the no-op stubs (`profiling_noop.go`, `args_profiling_noop.go`) keep the code paths
out of the build.

| Make target | Build tags | Adds |
|---|---|---|
| `make build` | none | nothing |
| `make build-profiling` | `profiling` | `--once`, `--cpuprofile`, `--memprofile`, `--trace`, `--profile-duration` |
| `make build-tracing` | `tracing` | `--enable-tracing`, `--tracing-output` (span-based tracing, `internal/tracing`) |
| `make build-full` | `profiling tracing` | all of the above |
| `make benchmark` | none | runs the Go benchmarks (`go test -bench=. -benchmem ./...`) |

The binary lands in `bin/configd`. `make test-coverage` and `make deadcode` also exercise the tag
combinations. Without make:

```bash
go build -tags profiling ./cmd/configd
```

Flags of a `-tags profiling` build:

| Flag | Effect |
|---|---|
| `--once` | Run one config cycle and exit (the listener is not started and the LDAP watcher is off; combine with profiling flags to profile a single cycle) |
| `--cpuprofile <file>` | Write CPU profile in pprof format to `<file>` |
| `--memprofile <file>` | Write heap profile in pprof format to `<file>` |
| `--trace <file>` | Write execution trace to `<file>` |
| `--profile-duration <s>` | Stop profiling after `s` seconds; `0` (default) means profile the entire run |

`configd shadow` refuses `--cpuprofile`, `--memprofile`, `--trace` and `--enable-tracing`, because
their output files would be side effects of a run that must not write outside `--shadow-dir`.

**Typical workflow for profiling a single config cycle:**

```bash
# Build with profiling support
make build-profiling            # or: go build -tags profiling -o configd-prof ./cmd/configd

# Run one cycle and write profiles (must run as the zextras user; stop the daemon or use a QA node)
sudo -u zextras bin/configd --once --cpuprofile cpu.prof --memprofile mem.prof

# Analyse CPU profile
go tool pprof -http=:8080 cpu.prof

# Analyse heap profile
go tool pprof -http=:8081 mem.prof
```

`--once` runs the real cycle, including Execute and restarts, so use a node where that is
acceptable. To only measure planning without side effects, use `configd shadow --single-cycle`
(without profiling flags) and the per-phase timings of the `Loop completed` / `Cycle summary` log lines
and the `configd_cycle_phase_duration_seconds` metrics (see
[architecture.md](architecture.md#observability)).

**Execution trace analysis:**

```bash
sudo -u zextras bin/configd --once --trace trace.out
go tool trace trace.out
```

The trace viewer shows goroutine scheduling, GC pauses, and blocking on the LDAP client or the
rewrite semaphore, useful for identifying where wall-clock time is spent when total cycle time
regresses.
