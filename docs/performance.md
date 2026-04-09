<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Performance

## LDAP error classification

Source: `internal/ldap/client.go` (`executeWithRetry`)

LDAP errors are split into two classes that determine retry behaviour.

**Permanent errors — fail immediately, no retry:**

| LDAP result code | Meaning |
|---|---|
| `NoSuchObject` | DN does not exist in the directory |
| `InvalidDNSyntax` | Malformed distinguished name |
| `InvalidCredentials` | Bind password is wrong |
| `InsufficientAccessRights` | Bound user lacks permission |

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
| _(non-LDAP network error)_ | TCP-level failure |

Default retry parameters: **3 attempts**, initial delay **100 ms**, max delay **5 s**,
with exponential backoff between attempts. These can be overridden via `ldap.Config` at
construction time.

## Parallel config rewrites

Source: `internal/configmgr/manager_actions.go` (`doRewrites`)

Config file rewrites run concurrently to shorten the rewrite phase when many files need
updating. The implementation uses a buffered-channel semaphore:

```go
maxConcurrent := 4  // Tuned for balance between parallelism and I/O contention
semaphore := make(chan struct{}, maxConcurrent)

for filePath, rewriteEntry := range rewrites {
    semaphore <- struct{}{}  // blocks when 4 goroutines are already running
    go func(...) {
        defer func() { <-semaphore }()
        cm.processRewrite(ctx, fp, re)
    }(...)
}
wg.Wait()
```

**Why 4?** The comment in the source reads: "Tuned for balance between parallelism and
I/O contention." Higher values increase throughput on SSDs but risk saturating the disk
queue on spinning media or network-mounted filesystems. Lower values leave parallelism on
the table. If you want to change `maxConcurrent`, benchmark with `--cpuprofile` and
`--trace` on the target hardware first.

**Atomic write pattern**: every file is written via `os.CreateTemp` in the same directory
as the destination, followed by `os.Rename`. The temp file has a `.configd-*.tmp`
prefix. Because temp file and destination are on the same filesystem, the rename is atomic
at the VFS level — readers never observe a partially-written file. If the write fails, the
temp file is removed and the original is untouched.

## Profiling tooling

Source: `cmd/configd/args_profiling.go`, `cmd/configd/profiling.go`

Profiling flags are compiled in only when the binary is built with `-tags profiling`:

```bash
go build -tags profiling ./cmd/configd
```

Available flags:

| Flag | Effect |
|---|---|
| `--once` | Run one config cycle and exit (combine with profiling flags to profile a single cycle) |
| `--cpuprofile <file>` | Write CPU profile in pprof format to `<file>` |
| `--memprofile <file>` | Write heap profile in pprof format to `<file>` |
| `--trace <file>` | Write execution trace to `<file>` |
| `--profile-duration <s>` | Stop profiling after `s` seconds; `0` means profile the entire run |

**Typical workflow for profiling a single config cycle:**

```bash
# Build with profiling support
go build -tags profiling -o configd-prof ./cmd/configd

# Run one cycle and write profiles
sudo -u zextras ./configd-prof --once --cpuprofile cpu.prof --memprofile mem.prof

# Analyse CPU profile
go tool pprof -http=:8080 cpu.prof

# Analyse heap profile
go tool pprof -http=:8081 mem.prof
```

**Execution trace analysis:**

```bash
sudo -u zextras ./configd-prof --once --trace trace.out
go tool trace trace.out
```

The trace viewer shows goroutine scheduling, GC pauses, and blocking on the LDAP semaphore
or the rewrite semaphore — useful for identifying where wall-clock time is spent when
total cycle time regresses.
