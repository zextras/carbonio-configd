// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package mtaops

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/postfixconf"
	pfclient "github.com/zextras/carbonio-configd/internal/postfixconf/client"
	"github.com/zextras/carbonio-configd/internal/redact"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/tracing"
)

// postconfKeyRe matches Postfix parameter names. Every key in the legacy
// configd.cf is [A-Za-z0-9_]+ (mixed case occurs, e.g. lmtp_tls_CAfile).
var postconfKeyRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// executor implements the Executor interface.
type executor struct {
	baseDir      string
	postconfPath string
	ldapManager  ldap.AttributeModifier
	mappedFiles  map[string]string // key -> file path mapping
	env          sys.System
	postfixConf  *pfclient.Client // the only writer of common/conf (CO-4621)
}

// Option customizes NewExecutor.
type Option func(*executor)

// WithSystemdMode tells the executor how to find out whether configd runs in
// systemd mode, which selects how the root helper carbonio-postfix-conf is
// triggered (see postfixconf/client). Without it the executor is not in
// systemd mode.
func WithSystemdMode(systemd func() bool) Option {
	return func(e *executor) {
		e.postfixConf = pfclient.NewClient(e.baseDir, e.env, systemd)
	}
}

// NewExecutor creates a new MTA operations executor. All host access (postconf,
// mapped-file reads and writes, umask) goes through env. Postfix configuration
// is never written here: postconf -e/-X changes are staged and applied by the
// root helper carbonio-postfix-conf.
func NewExecutor(baseDir string, ldapManager ldap.AttributeModifier, env sys.System, opts ...Option) Executor {
	e := &executor{
		baseDir:      baseDir,
		postconfPath: filepath.Join(baseDir, "common", "sbin", "postconf"),
		ldapManager:  ldapManager,
		mappedFiles:  mappedFilePaths(baseDir),
		env:          env,
		postfixConf:  pfclient.NewClient(baseDir, env, nil),
	}

	for _, opt := range opts {
		opt(e)
	}

	return e
}

// mappedFilePaths resolves state.MAPPEDFILES (the canonical key -> relative
// path table) to absolute paths under baseDir, so a new mapped-file key only
// needs to be added in one place.
func mappedFilePaths(baseDir string) map[string]string {
	paths := make(map[string]string, len(state.MAPPEDFILES))
	for key, relPath := range state.MAPPEDFILES {
		paths[key] = filepath.Join(baseDir, relPath)
	}

	return paths
}

// NormalizePostconfValue returns the value `postconf -e` is given for value:
// a multi-value attribute (newline-separated) is split on newlines, trimmed,
// stripped of empty entries and rejoined with ", ". Values without a newline
// are returned unchanged. The read-only plan uses it to predict main.cf.
func NormalizePostconfValue(value string) string {
	if !strings.Contains(value, "\n") {
		return value
	}

	var clean []string

	for line := range strings.SplitSeq(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			clean = append(clean, line)
		}
	}

	return strings.Join(clean, ", ")
}

// ExecutePostconf executes a postconf -e operation.
func (e *executor) ExecutePostconf(ctx context.Context, op PostconfOperation) error {
	ctx = logger.ContextWithComponentOnce(ctx, "mtaops")
	return e.ExecutePostconfBatch(ctx, []PostconfOperation{op})
}

// ExecutePostconfBatch applies multiple postconf -e operations in a single helper run
// (staged, then applied as root by carbonio-postfix-conf).
// Values containing newlines (e.g. multi-value LDAP attributes) are normalized in
// place: split on newlines, trimmed, and rejoined with ", " so they stay part of
// the same batch invocation instead of postconf's multi-line syntax.
func (e *executor) ExecutePostconfBatch(ctx context.Context, ops []PostconfOperation) error {
	ctx = logger.ContextWithComponentOnce(ctx, "mtaops")

	if len(ops) == 0 {
		return nil
	}

	ctx, span := tracing.StartSpanContext(ctx, "MTA.PostconfBatch")
	if span != nil {
		span.AddMetadata("count", fmt.Sprintf("%d", len(ops)))
	}
	defer tracing.EndSpan(span)

	logger.DebugContext(ctx, "Executing postconf batch",
		"operation_count", len(ops))

	// Normalize all operations: convert newlines to comma-separated format
	// LDAP multi-value attributes may come as newline-separated
	for i := range ops {
		if !strings.Contains(ops[i].Value, "\n") {
			continue
		}

		ops[i].Value = NormalizePostconfValue(ops[i].Value)
		logger.DebugContext(ctx, "Normalized multi-line value",
			"key", ops[i].Key,
			"value", redact.ForLog(ops[i].Key, ops[i].Value))
	}

	change := postfixconf.Change{Set: make([]postfixconf.KV, 0, len(ops))}

	for _, op := range ops {
		if !postconfKeyRe.MatchString(op.Key) {
			logger.WarnContext(ctx, "Skipping postconf operation with invalid key",
				"key", op.Key)

			continue
		}

		if !postfixconf.IsAllowedKey(op.Key) {
			logger.WarnContext(ctx, "Not changing a Postfix parameter that carbonio-postfix-conf does not allow",
				"key", op.Key)

			continue
		}

		change.Set = append(change.Set, postfixconf.KV{Key: op.Key, Value: op.Value})

		logger.DebugContext(ctx, "Queued postconf operation",
			"key", op.Key,
			"value", redact.ForLog(op.Key, op.Value))
	}

	if len(change.Set) == 0 {
		return nil
	}

	if err := e.postfixConf.Apply(ctx, change); err != nil {
		logger.ErrorContext(ctx, "Postconf batch failed", "error", err)

		return fmt.Errorf("postconf batch failed: %w", err)
	}

	logger.DebugContext(ctx, "Postconf batch completed",
		"operations_set", len(ops))

	return nil
}

// ExecutePostconfd executes a postconf -X operation (delete parameter).
func (e *executor) ExecutePostconfd(ctx context.Context, op PostconfdOperation) error {
	ctx = logger.ContextWithComponentOnce(ctx, "mtaops")
	return e.ExecutePostconfdBatch(ctx, []PostconfdOperation{op})
}

// ExecutePostconfdBatch executes multiple postconf -X operations in a single call.
func (e *executor) ExecutePostconfdBatch(ctx context.Context, ops []PostconfdOperation) error {
	ctx = logger.ContextWithComponentOnce(ctx, "mtaops")

	if len(ops) == 0 {
		return nil
	}

	ctx, span := tracing.StartSpanContext(ctx, "MTA.PostconfdBatch")
	if span != nil {
		span.AddMetadata("count", fmt.Sprintf("%d", len(ops)))
	}
	defer tracing.EndSpan(span)

	logger.DebugContext(ctx, "Executing postconf -X batch",
		"operation_count", len(ops))

	change := postfixconf.Change{Unset: make([]string, 0, len(ops))}

	for _, op := range ops {
		if !postfixconf.IsAllowedKey(op.Key) {
			logger.WarnContext(ctx, "Not removing a Postfix parameter that carbonio-postfix-conf does not allow",
				"key", op.Key)

			continue
		}

		change.Unset = append(change.Unset, op.Key)
		logger.DebugContext(ctx, "Queued postconf -X",
			"key", op.Key)
	}

	if len(change.Unset) == 0 {
		return nil
	}

	// The helper tolerates keys main.cf does not define.
	if err := e.postfixConf.Apply(ctx, change); err != nil {
		logger.ErrorContext(ctx, "Postconf -X batch failed", "error", err)

		return fmt.Errorf("postconf -X batch failed: %w", err)
	}

	logger.DebugContext(ctx, "Postconf -X batch completed",
		"operations_deleted", len(ops))

	return nil
}

// ReadPostconf reads one Postfix parameter without modifying anything.
//
// With explicitOnly false it runs `postconf -h <key>` and returns the effective
// value (defaults included); set is always true on success. With explicitOnly
// true it runs `postconf -n <key>` (no -h, so an explicitly-empty parameter is
// told apart from an unset one) and returns set=false when main.cf does not
// define the key. A non-zero exit (unknown parameter, ...) is an error.
func (e *executor) ReadPostconf(
	ctx context.Context, key string, explicitOnly bool,
) (value string, set bool, err error) {
	ctx = logger.ContextWithComponentOnce(ctx, "mtaops")

	if !postconfKeyRe.MatchString(key) {
		return "", false, fmt.Errorf("%w: %q", ErrInvalidPostconfKey, key)
	}

	if !explicitOnly {
		out, runErr := sys.Output(ctx, e.env.Exec, e.postconfPath, "-h", key)
		if runErr != nil {
			return "", false, fmt.Errorf("postconf -h %s failed: %w", key, runErr)
		}

		return unwrapPostconfValue(strings.Split(string(out), "\n")), true, nil
	}

	out, runErr := sys.Output(ctx, e.env.Exec, e.postconfPath, "-n", key)
	if runErr != nil {
		return "", false, fmt.Errorf("postconf -n %s failed: %w", key, runErr)
	}

	value, set = parsePostconfEntry(string(out), key)

	return value, set, nil
}

// parsePostconfEntry extracts the value of key from `postconf -n key` output
// ("key = value", long values wrapped onto indented continuation lines). set is
// false when no line defines key.
func parsePostconfEntry(out, key string) (string, bool) {
	lines := strings.Split(out, "\n")

	for i, line := range lines {
		rest, found := strings.CutPrefix(line, key)
		if !found {
			continue
		}

		rest = strings.TrimLeft(rest, " \t")

		rest, found = strings.CutPrefix(rest, "=")
		if !found {
			continue
		}

		first := strings.TrimLeft(rest, " \t")

		return unwrapPostconfValue(append([]string{first}, continuationLines(lines[i+1:])...)), true
	}

	return "", false
}

// continuationLines returns the leading run of indented lines, which postconf
// uses to wrap long values.
func continuationLines(lines []string) []string {
	n := 0

	for n < len(lines) && lines[n] != "" && (lines[n][0] == ' ' || lines[n][0] == '\t') {
		n++
	}

	return lines[:n]
}

// unwrapPostconfValue joins a value postconf wrapped over several lines (the
// continuation lines are indented) back into a single trimmed string.
func unwrapPostconfValue(lines []string) string {
	parts := make([]string, 0, len(lines))

	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}

	return strings.Join(parts, " ")
}

// handleEmptyMapfileData mirrors jylibs/state.py:408-410: when the MAPFILE VAR
// is empty, an existing mapped file is removed so a cleared value (e.g.
// zimbraSSLDHParam -> conf/dhparam.pem) does not leave a stale file behind. A
// missing file is a no-op.
func (e *executor) handleEmptyMapfileData(ctx context.Context, op MapfileOperation, filePath string) error {
	if _, err := e.env.FS.Stat(filePath); err != nil {
		if os.IsNotExist(err) {
			logger.DebugContext(ctx, "MAPFILE no data in LDAP and no existing file",
				"key", op.Key,
				"file_path", filePath)

			return nil
		}

		return fmt.Errorf("MAPFILE %s: error checking file %s: %w", op.Key, filePath, err)
	}

	if err := e.env.FS.Remove(filePath); err != nil {
		return fmt.Errorf("MAPFILE %s: failed to remove stale file %s: %w", op.Key, filePath, err)
	}

	logger.InfoContext(ctx, "MAPFILE removed (no data in LDAP)",
		"key", op.Key,
		"file_path", filePath)

	return nil
}

// ExecuteMapfile executes a MAPFILE/MAPLOCAL operation.
func (e *executor) ExecuteMapfile(ctx context.Context, op MapfileOperation) error {
	ctx = logger.ContextWithComponentOnce(ctx, "mtaops")

	ctx, span := tracing.StartSpanContext(ctx, "MTA.Mapfile")
	if span != nil {
		span.AddMetadata("key", op.Key)

		if op.IsLocal {
			span.AddMetadata("type", "MAPLOCAL")
		} else {
			span.AddMetadata("type", "MAPFILE")
		}
	}
	defer tracing.EndSpan(span)

	// Get the mapped file path
	filePath, exists := e.mappedFiles[op.Key]
	if !exists {
		return fmt.Errorf("unmapped MAPFILE key: %s", op.Key)
	}

	if op.IsLocal {
		return e.executeMapLocal(ctx, op, filePath)
	}

	return e.executeMapFile(ctx, op, filePath)
}

// executeMapLocal handles MAPLOCAL operations: checks whether the mapped file exists.
func (e *executor) executeMapLocal(ctx context.Context, op MapfileOperation, filePath string) error {
	if _, err := e.env.FS.Stat(filePath); err != nil {
		if os.IsNotExist(err) {
			logger.DebugContext(ctx, "MAPLOCAL file does not exist",
				"key", op.Key,
				"file_path", filePath)

			return nil
		}

		return fmt.Errorf("MAPLOCAL %s: error checking file %s: %w", op.Key, filePath, err)
	}

	logger.DebugContext(ctx, "MAPLOCAL file exists",
		"key", op.Key,
		"file_path", filePath)

	return nil
}

// executeMapFile handles MAPFILE operations: base64-decodes and writes to file when content changed.
func (e *executor) executeMapFile(ctx context.Context, op MapfileOperation, filePath string) error {
	if op.Base64Data == "" {
		return e.handleEmptyMapfileData(ctx, op, filePath)
	}

	data, err := base64.StdEncoding.DecodeString(op.Base64Data)
	if err != nil {
		return fmt.Errorf("MAPFILE %s: failed to decode base64 data: %w", op.Key, err)
	}

	// Skip write when content is unchanged.
	//nolint:gosec // G304: File path comes from trusted MAPPEDFILES map
	existingData, err := e.env.FS.ReadFile(filePath)
	if err == nil && bytes.Equal(existingData, data) {
		logger.DebugContext(ctx, "MAPFILE already has correct content",
			"key", op.Key,
			"file_path", filePath)

		return nil
	}

	if err := e.writeMapfile(filePath, data); err != nil {
		return fmt.Errorf("MAPFILE %s: failed to write file %s: %w", op.Key, filePath, err)
	}

	logger.InfoContext(ctx, "MAPFILE wrote data to file",
		"key", op.Key,
		"bytes_written", len(data),
		"file_path", filePath)

	return nil
}

// writeMapfile keeps the file-mode semantics of jylibs/state.py:401-406
// (open(mapfile, "w")): an existing regular file keeps its mode, a new file
// gets 0666 &^ umask (0640 under the zextras 0027 umask). Unlike legacy it
// writes atomically (temp + rename) so a reader such as nginx never sees a
// truncated file and no planted symlink is followed.
func (e *executor) writeMapfile(path string, data []byte) error {
	mode := os.FileMode(0o666) &^ e.env.Proc.Umask()

	if fi, err := e.env.FS.Lstat(path); err == nil && fi.Mode().IsRegular() {
		mode = fi.Mode().Perm()
	}

	if err := e.env.FS.WriteFileAtomic(path, data, mode); err != nil {
		return fmt.Errorf("atomic write: %w", err)
	}

	return nil
}

// ExecuteLdapWrite executes an LDAP cn=config write operation.
func (e *executor) ExecuteLdapWrite(ctx context.Context, op LdapOperation) error {
	ctx = logger.ContextWithComponentOnce(ctx, "mtaops")

	ctx, span := tracing.StartSpanContext(ctx, "MTA.LdapWrite")
	if span != nil {
		span.AddMetadata("key", op.Key)
	}
	defer tracing.EndSpan(span)

	logger.DebugContext(ctx, "Executing LDAP write",
		"key", op.Key,
		"value", redact.ForLog(op.Key, op.Value))

	// Use the existing LDAP manager's ModifyAttribute method
	// The ldap package already has the keymap and handles DN/attr mapping internally
	if err := e.ldapManager.ModifyAttribute(ctx, op.Key, op.Value); err != nil {
		return fmt.Errorf("LDAP write failed for %s: %w", op.Key, err)
	}

	logger.InfoContext(ctx, "LDAP attribute set",
		"key", op.Key,
		"value", redact.ForLog(op.Key, op.Value))

	return nil
}
