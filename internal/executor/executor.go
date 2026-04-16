// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package executor provides execution logic for MTA configuration sections.
// It handles conditional evaluation (IF/FI blocks), LDAP operations,
// postconf/postconfd directives, and service restarts. The executor bridges
// configuration parsing and actual system changes.
package executor

import (
	"context"
	"maps"
	"strings"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/lookup"
	"github.com/zextras/carbonio-configd/internal/postfix"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/state"
)

// SectionExecutor handles the execution of MTA configuration sections
// including evaluation of conditional IF/FI blocks.
type SectionExecutor struct {
	ConfigLookup lookup.ConfigLookup
	State        *state.State
	PostfixMgr   postfix.Manager
	ServiceMgr   services.Manager
}

// NewSectionExecutor creates a new SectionExecutor instance.
func NewSectionExecutor(cl lookup.ConfigLookup,
	st *state.State, pm postfix.Manager, sm services.Manager) *SectionExecutor {
	return &SectionExecutor{
		ConfigLookup: cl,
		State:        st,
		PostfixMgr:   pm,
		ServiceMgr:   sm,
	}
}

// EvaluateConditional evaluates a conditional block and returns whether it should be executed.
// Returns true if the condition is met, false otherwise.
// It delegates to ConfigLookup for value resolution, then applies truthy/negation logic.
func (e *SectionExecutor) EvaluateConditional(ctx context.Context, cond *config.Conditional) bool {
	ctx = logger.ContextWithComponentOnce(ctx, "executor")
	logger.DebugContext(ctx, "Evaluating conditional",
		"type", cond.Type,
		"key", cond.Key,
		"negated", cond.Negated)

	val, err := e.ConfigLookup.LookUpConfig(ctx, cond.Type, cond.Key)

	var result bool

	if err != nil {
		logger.DebugContext(ctx, "Conditional lookup failed, treating as false",
			"type", cond.Type,
			"key", cond.Key)

		result = false
	} else {
		result = !state.IsFalseValue(val)
	}

	if cond.Negated {
		result = !result
	}

	logger.DebugContext(ctx, "Conditional evaluation result",
		"result", result)

	return result
}

// ExecuteSection processes a section and returns the directives to be executed
// based on conditional evaluation. Returns effective postconf, postconfd, ldap maps, and restarts.
func (e *SectionExecutor) ExecuteSection(ctx context.Context, section *config.MtaConfigSection) (
	postconf map[string]string,
	postconfd map[string]string,
	ldap map[string]string,
	restarts map[string]bool) {
	ctx = logger.ContextWithComponentOnce(ctx, "executor")
	logger.DebugContext(ctx, "Executing section",
		"section", section.Name)

	// Initialize result maps with base section directives
	postconf = make(map[string]string)
	postconfd = make(map[string]string)
	ldap = make(map[string]string)
	restarts = make(map[string]bool)

	// Copy base directives (unconditional)
	maps.Copy(postconf, section.Postconf)
	maps.Copy(postconfd, section.Postconfd)
	maps.Copy(ldap, section.Ldap)
	maps.Copy(restarts, section.Restarts)

	// Process conditionals (single evaluation path shared with nested levels)
	e.applyConditionals(ctx, section.Conditionals, postconf, postconfd, ldap, restarts, true)

	logger.DebugContext(ctx, "Section execution complete",
		"postconf_count", len(postconf),
		"postconfd_count", len(postconfd),
		"ldap_count", len(ldap),
		"restarts_count", len(restarts))

	return postconf, postconfd, ldap, restarts
}

// applyConditionals evaluates a list of conditionals and merges the directives
// of the branches whose predicates evaluate true into the provided result maps.
// It recurses into nested conditionals, acting as the single source of truth
// for conditional evaluation across both top-level and nested levels.
//
// The topLevel flag controls whether "skipping directives" debug lines are
// emitted (they are only interesting at the outer level to preserve existing
// log semantics).
func (e *SectionExecutor) applyConditionals(
	ctx context.Context,
	conditionals []config.Conditional,
	postconf, postconfd, ldap map[string]string,
	restarts map[string]bool,
	topLevel bool,
) {
	for i := range conditionals {
		cond := &conditionals[i]
		if !e.EvaluateConditional(ctx, cond) {
			if topLevel {
				logger.DebugContext(ctx, "Conditional not matched, skipping directives")
			}

			continue
		}

		if topLevel {
			logger.DebugContext(ctx, "Conditional matched, adding directives")
		}

		maps.Copy(postconf, cond.Postconf)
		maps.Copy(postconfd, cond.Postconfd)
		maps.Copy(ldap, cond.Ldap)
		maps.Copy(restarts, cond.Restarts)

		// Recurse into nested conditionals via the same evaluation path.
		e.applyConditionals(ctx, cond.Nested, postconf, postconfd, ldap, restarts, false)
	}
}

// GetSectionDependencies returns a list of section names that the given section depends on.
func (e *SectionExecutor) GetSectionDependencies(section *config.MtaConfigSection) []string {
	deps := make([]string, 0, len(section.Depends))
	for dep := range section.Depends {
		deps = append(deps, dep)
	}

	return deps
}

// CheckRequiredVars checks if all required variables for a section are present.
// Returns true if all required vars are present, false otherwise.
func (e *SectionExecutor) CheckRequiredVars(ctx context.Context, section *config.MtaConfigSection) bool {
	ctx = logger.ContextWithComponentOnce(ctx, "executor")
	logger.DebugContext(ctx, "Checking required vars for section",
		"section", section.Name)

	allPresent := true

	for varName, varType := range section.RequiredVars {
		_, err := e.ConfigLookup.LookUpConfig(ctx, varType, varName)
		if err != nil {
			logger.WarnContext(ctx, "Required variable not found for section",
				"variable", varName,
				"var_type", varType,
				"section", section.Name)

			allPresent = false
		} else {
			logger.DebugContext(ctx, "Required variable found",
				"variable", varName,
				"var_type", varType)
		}
	}

	return allPresent
}

// ExpandValue expands a value that may contain FILE, VAR, LOCAL, MAPLOCAL references.
// Examples:
//   - "FILE zmconfigd/foo.cf" -> reads and expands the file content
//   - "VAR:zimbraFoo" -> looks up the variable
//   - "literal value" -> returns as-is
//   - "prefix VAR:foo suffix" -> "prefix <value> suffix"
func (e *SectionExecutor) ExpandValue(ctx context.Context, value string) (string, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "executor")

	if value == "" {
		return "", nil
	}

	// Check for FILE directive (format: "FILE path")
	if after, ok := strings.CutPrefix(value, "FILE "); ok {
		filePath := after

		expandedValue, err := e.ConfigLookup.LookUpConfig(ctx, "FILE", filePath)
		if err != nil {
			logger.WarnContext(ctx, "Failed to expand FILE",
				"file_path", filePath,
				"error", err)

			return "", err
		}

		return expandedValue, nil
	}

	// Check for VAR: prefix
	if after, ok := strings.CutPrefix(value, "VAR:"); ok {
		varName := after

		expandedValue, err := e.ConfigLookup.LookUpConfig(ctx, "VAR", varName)
		if err != nil {
			logger.WarnContext(ctx, "Failed to expand VAR",
				"var_name", varName,
				"error", err)

			return "", err
		}

		return expandedValue, nil
	}

	// Check for LOCAL: prefix
	if after, ok := strings.CutPrefix(value, "LOCAL:"); ok {
		localName := after

		expandedValue, err := e.ConfigLookup.LookUpConfig(ctx, "LOCAL", localName)
		if err != nil {
			logger.WarnContext(ctx, "Failed to expand LOCAL",
				"local_name", localName,
				"error", err)

			return "", err
		}

		return expandedValue, nil
	}

	// Check for MAPLOCAL: prefix
	if after, ok := strings.CutPrefix(value, "MAPLOCAL:"); ok {
		maplocalName := after

		expandedValue, err := e.ConfigLookup.LookUpConfig(ctx, "MAPLOCAL", maplocalName)
		if err != nil {
			logger.WarnContext(ctx, "Failed to expand MAPLOCAL",
				"maplocal_name", maplocalName,
				"error", err)

			return "", err
		}

		return expandedValue, nil
	}

	// If no special prefix, return the value as-is (literal)
	return value, nil
}

// ApplyPostfixDirectives applies postconf and postconfd directives using the PostfixManager.
// Takes maps returned from ExecuteSection and queues them for execution.
// Values are expanded to resolve FILE, VAR, LOCAL, and MAPLOCAL references.
func (e *SectionExecutor) ApplyPostfixDirectives(ctx context.Context, postconf, postconfd map[string]string) error {
	ctx = logger.ContextWithComponentOnce(ctx, "executor")
	logger.DebugContext(ctx, "Applying postfix directives",
		"postconf_count", len(postconf),
		"postconfd_count", len(postconfd))

	// Queue postconf changes (expand values first)
	for key, value := range postconf {
		expandedValue, err := e.ExpandValue(ctx, value)
		if err != nil {
			logger.ErrorContext(ctx, "Failed to expand value for postconf",
				"key", key,
				"error", err)

			return err
		}

		if err := e.PostfixMgr.AddPostconf(ctx, key, expandedValue); err != nil {
			logger.ErrorContext(ctx, "Failed to add postconf",
				"key", key,
				"error", err)

			return err
		}
	}

	// Queue postconfd deletions
	for key := range postconfd {
		if err := e.PostfixMgr.AddPostconfd(ctx, key); err != nil {
			logger.ErrorContext(ctx, "Failed to add postconfd",
				"key", key,
				"error", err)

			return err
		}
	}

	logger.DebugContext(ctx, "Postfix directives queued successfully")

	return nil
}

// FlushPostfixChanges flushes all accumulated postfix changes.
func (e *SectionExecutor) FlushPostfixChanges(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "executor")
	logger.DebugContext(ctx, "Flushing postfix changes")

	// Flush postconf changes
	if err := e.PostfixMgr.FlushPostconf(ctx); err != nil {
		logger.ErrorContext(ctx, "Failed to flush postconf",
			"error", err)

		return err
	}

	// Flush postconfd deletions
	if err := e.PostfixMgr.FlushPostconfd(ctx); err != nil {
		logger.ErrorContext(ctx, "Failed to flush postconfd",
			"error", err)

		return err
	}

	logger.DebugContext(ctx, "Postfix changes flushed successfully")

	return nil
}

// ProcessAllSections processes all sections in an MtaConfig and applies their postfix directives.
// It evaluates conditionals for each section and queues all postconf/postconfd changes.
// Call FlushPostfixChanges() after this method to execute all queued changes.
func (e *SectionExecutor) ProcessAllSections(ctx context.Context, mtaConfig *config.MtaConfig) error {
	ctx = logger.ContextWithComponentOnce(ctx, "executor")
	logger.DebugContext(ctx, "Processing all sections for postfix changes")

	processedCount := 0

	for sectionName, section := range mtaConfig.Sections {
		// Check if section has required vars
		if !e.CheckRequiredVars(ctx, section) {
			logger.WarnContext(ctx, "Section missing required vars, skipping",
				"section", sectionName)

			continue
		}

		// Execute section to get effective postconf/postconfd directives
		postconf, postconfd, _, restarts := e.ExecuteSection(ctx, section)

		// Queue service restarts
		for service := range restarts {
			logger.DebugContext(ctx, "Queuing restart for service",
				"service", service)

			if err := e.ServiceMgr.AddRestart(ctx, service); err != nil {
				logger.WarnContext(ctx, "Failed to queue restart for service",
					"service", service,
					"error", err)
			}
		}

		// Apply directives to postfix manager
		if err := e.ApplyPostfixDirectives(ctx, postconf, postconfd); err != nil {
			logger.ErrorContext(ctx, "Failed to apply postfix directives for section",
				"section", sectionName,
				"error", err)

			return err
		}

		processedCount++

		logger.DebugContext(ctx, "Processed section",
			"section", sectionName,
			"postconf_count", len(postconf),
			"postconfd_count", len(postconfd))
	}

	logger.DebugContext(ctx, "Processed sections successfully",
		"processed_count", processedCount)

	return nil
}
