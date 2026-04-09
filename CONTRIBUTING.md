<!--
SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>

SPDX-License-Identifier: AGPL-3.0-only
-->

# Contributing

Thank you for your interest in contributing to Carbonio!

To get started, please visit: <https://zextras.com/carbonio-ce-contribute>

## Development Setup

### Pre-commit Hooks

This project uses pre-commit hooks to ensure code quality. The hooks run automatically before each commit and include:

- **gofmt**: Formats Go code
- **golangci-lint**: Runs linters to catch common issues
- **fast-tests**: Runs unit tests for affected packages (< 5s)

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
```
