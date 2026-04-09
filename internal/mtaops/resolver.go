// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package mtaops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/state"
)

// resolver implements the OperationResolver interface.
type resolver struct {
	baseDir string
}

// NewResolver creates a new operation resolver.
func NewResolver(baseDir string) OperationResolver {
	return &resolver{
		baseDir: baseDir,
	}
}

// ResolveValue resolves a value based on type (VAR, LOCAL, FILE, MAPLOCAL, or literal).
// This mirrors the lookUpConfig logic from carbonio-jython/jylibs/state.py.
func (r *resolver) ResolveValue(ctx context.Context, valueType, key string, st *state.State) (string, error) {
	ctx = logger.ContextWithComponent(ctx, "mtaops")
	logger.DebugContext(ctx, "Resolving value",
		"type", valueType,
		"key", key)

	switch valueType {
	case "VAR":
		// Look up from GlobalConfig or ServerConfig
		value := st.GlobalConfig.Data[key]
		if value == "" {
			value = st.ServerConfig.Data[key]
		}

		if value == "" {
			logger.DebugContext(ctx, "VAR not found in global or server config",
				"key", key)

			return "", nil // Empty value, not an error
		}

		// Trim whitespace and newlines (LDAP attributes may have trailing whitespace)
		return strings.TrimSpace(value), nil

	case "LOCAL":
		// Look up from LocalConfig
		value := st.LocalConfig.Data[key]
		if value == "" {
			logger.DebugContext(ctx, "LOCAL not found in local config",
				"key", key)

			return "", nil // Empty value, not an error
		}

		// Trim whitespace and newlines (local config may have trailing whitespace)
		return strings.TrimSpace(value), nil

	case "FILE":
		// Read from file and join lines with commas
		filePath := filepath.Join(r.baseDir, "conf", key)

		//nolint:gosec // G304: File path constructed from trusted baseDir and config keys
		data, err := os.ReadFile(filePath)
		if err != nil {
			logger.WarnContext(ctx, "FILE failed to read",
				"key", key,
				"file_path", filePath,
				"error", err)

			return "", nil // Empty value, not an error
		}

		// Transform each line (strip whitespace) and join with commas
		lines := strings.Split(string(data), "\n")

		cleanLines := make([]string, 0, len(lines))

		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" {
				cleanLines = append(cleanLines, line)
			}
		}

		value := strings.Join(cleanLines, ", ")
		logger.DebugContext(ctx, "FILE resolved",
			"key", key,
			"value", value)

		return value, nil

	case "MAPLOCAL":
		// Return file path if file exists
		// zimbraSSLDHParam -> /opt/zextras/conf/dhparam.pem
		mappedFiles := map[string]string{
			"zimbraSSLDHParam": filepath.Join(r.baseDir, "conf", "dhparam.pem"),
		}

		filePath, exists := mappedFiles[key]
		if !exists {
			return "", fmt.Errorf("MAPLOCAL: unknown mapped file key: %s", key)
		}

		if _, err := os.Stat(filePath); err != nil {
			if os.IsNotExist(err) {
				logger.DebugContext(ctx, "MAPLOCAL file does not exist",
					"key", key,
					"file_path", filePath)

				return "", nil // Empty value, file doesn't exist
			}

			return "", fmt.Errorf("MAPLOCAL %s: error checking file %s: %w", key, filePath, err)
		}

		logger.DebugContext(ctx, "MAPLOCAL resolved (file exists)",
			"key", key,
			"file_path", filePath)

		return filePath, nil

	default:
		// If not a recognized type, treat as literal value
		logger.DebugContext(ctx, "Literal value",
			"value", key)

		return key, nil
	}
}

// ResolvePostconfDirective resolves a POSTCONF directive value.
// Format: "POSTCONF key [TYPE value]"
// Examples:
//   - "POSTCONF mynetworks VAR zimbraMtaMyNetworks" -> resolve from VAR
//   - "POSTCONF smtp_banner literal text" -> use literal text
//
//nolint:lll // Function signature naturally exceeds line limit
func (r *resolver) ResolvePostconfDirective(
	ctx context.Context, key, valueType, valueKey string, st *state.State) (PostconfOperation, error) {
	ctx = logger.ContextWithComponent(ctx, "mtaops")
	op := PostconfOperation{Key: key}

	// If no valueType/valueKey, this means clear the parameter (empty value)
	if valueType == "" && valueKey == "" {
		op.Value = ""
		return op, nil
	}

	// Resolve the value
	value, err := r.ResolveValue(ctx, valueType, valueKey, st)
	if err != nil {
		return op, fmt.Errorf("failed to resolve POSTCONF %s: %w", key, err)
	}

	// Convert boolean values to yes/no for Postfix
	switch strings.ToUpper(value) {
	case "TRUE":
		value = "yes"
	case "FALSE":
		value = "no"
	}

	op.Value = value

	return op, nil
}

// ResolvePostconfdDirective resolves a POSTCONFD directive (delete parameter).
// Format: "POSTCONFD key"
func (r *resolver) ResolvePostconfdDirective(key string) PostconfdOperation {
	return PostconfdOperation{Key: key}
}

// ResolveLdapDirective resolves an LDAP directive value.
// Format: "LDAP key TYPE value"
// Example: "LDAP ldap_db_maxsize LOCAL ldap_db_maxsize"
func (r *resolver) ResolveLdapDirective(
	ctx context.Context, key, valueType, valueKey string, st *state.State,
) (LdapOperation, error) {
	ctx = logger.ContextWithComponent(ctx, "mtaops")
	op := LdapOperation{Key: key}

	// Resolve the value
	value, err := r.ResolveValue(ctx, valueType, valueKey, st)
	if err != nil {
		return op, fmt.Errorf("failed to resolve LDAP %s: %w", key, err)
	}

	op.Value = value

	return op, nil
}

// ResolveMapfileDirective resolves a MAPFILE directive.
// Format: "MAPFILE key" or "MAPLOCAL key"
func (r *resolver) ResolveMapfileDirective(key string, isLocal bool, st *state.State) (MapfileOperation, error) {
	op := MapfileOperation{
		Key:     key,
		IsLocal: isLocal,
	}

	// Get mapped file path
	mappedFiles := map[string]string{
		"zimbraSSLDHParam": filepath.Join(r.baseDir, "conf", "dhparam.pem"),
	}

	filePath, exists := mappedFiles[key]
	if !exists {
		return op, fmt.Errorf("unknown MAPFILE key: %s", key)
	}

	op.FilePath = filePath

	if !isLocal {
		// MAPFILE: Read base64-encoded data from LDAP
		base64Data := st.GlobalConfig.Data[key]
		op.Base64Data = base64Data
	}

	return op, nil
}
