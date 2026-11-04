<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Contributing

Thank you for your interest in contributing to Carbonio!

To get started, please visit: <https://zextras.com/carbonio-ce-contribute>

## Development Setup

### Pre-commit Hooks

The hooks in `.pre-commit-config.yaml` run before each commit:

- **pre-commit-hooks**: trailing whitespace, end-of-file newline, YAML syntax, large files
- **go-fmt** (`-w`) and **go-mod-tidy**
- **golangci-lint** (`--timeout=5m`)
- **fast-tests** (`scripts/fast-tests.sh`): `go test -short` with a 5 s timeout on the packages of the staged Go files
- **generate-thirdparties**: regenerates `THIRDPARTIES` as the Jenkins verify stage does

#### Installation

Install pre-commit hooks:

```bash
pre-commit install
```

#### Bypassing Hooks

For emergency commits, you can bypass hooks with:

```bash
git commit --no-verify
```

**Note:** Use `--no-verify` sparingly and only when necessary.

The project needs the Go version in `go.mod` (Go 1.27).

### Running Tests

```bash
# Run all tests
make test

# Run tests with coverage
make test-coverage

# Run linter
make lint

# Run tests with race detector
make test-race

# Local quality gate: lint, test-race, deadcode, dup
make check

# REUSE license compliance (same gate as the Jenkins 'REUSE Lint' stage)
make reuse

# Verify generated localconfig docs/defaults are up to date
# (hermetic: regenerates from the carbonio-mailbox sources vendored in scripts/lc-source)
make generate-check

# vet / race-test every build-tag combination (default, integration, tracing, profiling)
make vet-tags test-tags

# 10 s per fuzz target, and the dependency vulnerability scan
make fuzz-smoke security
```

### What Jenkins runs

`make lint` fails when `golangci-lint` is not installed (it never skips silently). The pipeline runs, in order: REUSE
lint, THIRDPARTIES, **Static checks** (`make vet-tags deadcode dup generate-check`), **Tests** (`gotestsum` with the
race detector for the default, `tracing`, `profiling` and `tracing profiling` tags), **Fuzz smoke**
(`make fuzz-smoke`), **Vulnerability scan** (`make security`), **Lint and SonarQube analysis** (one `golangci-lint`
run: its checkstyle report goes to Sonar and its exit status fails the build), the packages and the docker image.
`make check` reproduces the network-free part locally.

`gotestsum`, `govulncheck`, `deadcode` and `dupl` are pinned in `tools/go.mod` and run through
`go tool -modfile=tools/go.mod`; bump them there (`go get -modfile=tools/go.mod -tool <pkg>@<version>`), never with
`@latest` in the Jenkinsfile.

The `LDAP integration` stage (`make test-integration-ldap`) needs a container runtime that the default `golang-v1` pod
does not have. It runs on a `docker-build-v1` pod on the nightly build of `main`, or when a build is started with
`RUN_LDAP_INTEGRATION=true`; see [test/README.md](test/README.md#in-jenkins).

Integration tests are described in [test/README.md](test/README.md).
