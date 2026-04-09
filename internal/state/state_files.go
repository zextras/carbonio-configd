// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package state

import (
	"context"
	"crypto/md5" //nolint:gosec // MD5 used for non-cryptographic checksumming only
	"encoding/hex"
	"io"
	"os"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// MAPPEDFILES mirrors the MAPPEDFILES in jylibs/state.py
var MAPPEDFILES = map[string]string{
	"zimbraSSLDHParam": "conf/dhparam.pem",
}

// --- MD5 Hashing and Change Detection Methods ---

// ComputeFileMD5 computes the MD5 hash of a file's contents.
// Returns the hex-encoded MD5 hash string or an error if file cannot be read.
func ComputeFileMD5(ctx context.Context, filepath string) (string, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "state")
	//nolint:gosec // G304: File path comes from trusted configuration
	file, err := os.Open(filepath)
	if err != nil {
		return "", err
	}

	defer func() {
		if cerr := file.Close(); cerr != nil {
			logger.WarnContext(ctx, "Failed to close file",
				"filepath", filepath,
				"error", cerr)
		}
	}()

	hash := md5.New() //nolint:gosec // MD5 used for non-cryptographic checksumming only
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// ComputeStringMD5 computes the MD5 hash of a string.
// Returns the hex-encoded MD5 hash string.
func ComputeStringMD5(content string) string {
	hash := md5.New() //nolint:gosec // MD5 used for non-cryptographic checksumming only
	hash.Write([]byte(content))

	return hex.EncodeToString(hash.Sum(nil))
}

// GetFileMD5 retrieves the cached MD5 hash for a file path.
// Returns empty string if no cached hash exists.
func (s *State) GetFileMD5(filepath string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.FileMD5Cache[filepath]
}

// SetFileMD5 stores the MD5 hash for a file path in the cache.
func (s *State) SetFileMD5(filepath, md5hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.FileMD5Cache[filepath] = md5hash
}

// FileHasChanged checks if a file's current MD5 differs from cached MD5.
// If no cached MD5 exists, returns true (file is considered new/changed).
// If file doesn't exist or can't be read, returns true (triggering rewrite).
func (s *State) FileHasChanged(ctx context.Context, filepath string) bool {
	ctx = logger.ContextWithComponentOnce(ctx, "state")
	cachedMD5 := s.GetFileMD5(filepath)

	currentMD5, err := ComputeFileMD5(ctx, filepath)
	if err != nil {
		// File doesn't exist or can't be read - consider it changed
		logger.DebugContext(ctx, "File cannot be read, treating as changed",
			"filepath", filepath,
			"error", err)

		return true
	}

	if cachedMD5 == "" {
		// No cached hash - file is new
		logger.DebugContext(ctx, "File has no cached MD5, treating as changed",
			"filepath", filepath)

		return true
	}

	changed := cachedMD5 != currentMD5
	if changed {
		logger.InfoContext(ctx, "File MD5 changed",
			"filepath", filepath,
			"old_md5", cachedMD5,
			"new_md5", currentMD5)
	}

	return changed
}

// UpdateFileMD5 recomputes and updates the cached MD5 for a file.
// Returns error if file cannot be read.
func (s *State) UpdateFileMD5(ctx context.Context, filepath string) error {
	ctx = logger.ContextWithComponentOnce(ctx, "state")

	md5hash, err := ComputeFileMD5(ctx, filepath)
	if err != nil {
		return err
	}

	s.SetFileMD5(filepath, md5hash)

	logger.DebugContext(ctx, "Updated MD5 cache",
		"filepath", filepath,
		"md5", md5hash)

	return nil
}

// ShouldRewriteSection determines if a section should be rewritten based on:
// 1. FirstRun flag (always rewrite on first run)
// 2. Section.changed flag (configuration variables changed)
// 3. ForcedConfig map (section explicitly forced via command-line or network)
// 4. RequestedConfig map (section explicitly requested via network command)
func (s *State) ShouldRewriteSection(ctx context.Context, sectionName string, section *config.MtaConfigSection) bool {
	ctx = logger.ContextWithComponentOnce(ctx, "state")

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.FirstRun {
		logger.DebugContext(ctx, "Section rewrite required (first run)",
			"section", sectionName)

		return true
	}

	if section != nil && section.Changed {
		logger.DebugContext(ctx, "Section rewrite required (configuration changed)",
			"section", sectionName)

		return true
	}

	if _, forced := s.ForcedConfig[sectionName]; forced {
		logger.DebugContext(ctx, "Section rewrite required (forced)",
			"section", sectionName)

		return true
	}

	if _, requested := s.RequestedConfig[sectionName]; requested {
		logger.DebugContext(ctx, "Section rewrite required (requested)",
			"section", sectionName)

		return true
	}

	logger.DebugContext(ctx, "Section no rewrite needed",
		"section", sectionName)

	return false
}

// ClearFileCache clears the FILE type lookup cache.
// This is called at the start of each configuration fetch cycle.
func (s *State) ClearFileCache(ctx context.Context) {
	ctx = logger.ContextWithComponentOnce(ctx, "state")

	s.mu.Lock()
	defer s.mu.Unlock()

	clear(s.FileCache)

	logger.DebugContext(ctx, "Cleared FILE lookup cache")
}
