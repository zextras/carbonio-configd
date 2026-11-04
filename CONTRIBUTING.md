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

The project needs the Go version in `go.mod` (Go 1.26).

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
# (needs a local carbonio-mailbox clone, see MAILBOX_REPO in the Makefile)
make generate-check
```

Integration tests are described in [test/README.md](test/README.md).
