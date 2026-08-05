// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package transformer provides line-by-line transformation of configuration content.
// It handles variable substitution, conditional evaluation, and rewrite directive
// processing for configuration files. The transformer is used by the executor to
// expand templates and configuration values before writing to disk.
package transformer

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/lookup"
	"github.com/zextras/carbonio-configd/internal/state"
)

// Transformer holds the necessary dependencies for transforming lines.
// Regex patterns are compiled once at package init (see the var block below)
// and shared across every Transformer instance, so constructing a new
// Transformer is allocation-free.
type Transformer struct {
	ConfigLookup lookup.ConfigLookup
	State        *state.State
}

const (
	// configTypeVAR is the VAR config type for variable substitution.
	configTypeVAR = "VAR"

	// configTypeLOCAL is the LOCAL config type for variable substitution.
	configTypeLOCAL = "LOCAL"

	// configTypeSERVICE is the SERVICE config type for variable substitution.
	configTypeSERVICE = "SERVICE"
)

// Compiled once at package init. `*regexp.Regexp` is safe for concurrent use.
var (
	localConfigRe   = regexp.MustCompile(`@@([^@]+)@@`)
	configVarRe     = regexp.MustCompile(`%%(VAR|LOCAL|SERVICE):([^%]+)%%`)
	plainVarRe      = regexp.MustCompile(`%%([^%:]+)%%`)
	commentRe       = regexp.MustCompile(`comment ([^:]+):([^,\s]+)(?:,([^,]+))?(?:,([^,]+))?`)
	uncommentRe     = regexp.MustCompile(`uncomment ([^:]+):([^,\s]+)(?:,([^,]+))?(?:,([^,]+))?`)
	binaryRe        = regexp.MustCompile(`binary ([^:]+):(\S+)`)
	truefalseRe     = regexp.MustCompile(`truefalse ([^:]+):(\S+)`)
	rangeRe         = regexp.MustCompile(`range ([^:]+):(\S+)\s+(\S+)\s+(\S+)`)
	freqRe          = regexp.MustCompile(`freq ([^:]+):(\S+)\s+(\S+)`)
	freqDigitsRe    = regexp.MustCompile(`\D`)
	freqNonDigitsRe = regexp.MustCompile(`\d`)
	explodeRe       = regexp.MustCompile(`explode\s+(.*)\s+([^:]+):(\w+)`)
	ldapURLRe       = regexp.MustCompile(`ldap.?://(\S+):\d+`)
	inlineDirectRe  = regexp.MustCompile(`%%([^%]+)%%`)
)

// NewTransformer creates a new Transformer instance.
func NewTransformer(cl lookup.ConfigLookup, st *state.State) *Transformer {
	return &Transformer{
		ConfigLookup: cl,
		State:        st,
	}
}

// Transform applies variable substitutions and conditional processing to a line.
func (t *Transformer) Transform(ctx context.Context, line string) string {
	ctx = logger.ContextWithComponentOnce(ctx, "transformer")
	// Check for early exit if no special characters are present
	if !strings.Contains(line, "@") && !strings.Contains(line, "%") {
		return line // Return as-is without adding newline
	}

	// Apply @@localconfig_key@@ substitutions
	line = localConfigRe.ReplaceAllStringFunc(line, func(match string) string {
		return t.xformLocalConfig(ctx, match)
	})

	// Trim trailing whitespace before processing %% directives
	line = strings.TrimRight(line, " \t\r\n")

	// Handle PREFIX directives like %%comment VAR:key%%rest_of_line
	// These directives appear at the start of a line and affect the rest of the line
	if processedLine, handled := t.processPrefixDirective(ctx, line); handled {
		line = processedLine
		// Continue processing - don't return early to handle VAR/LOCAL substitutions
	}

	// Handle WRAPPING directives - lines that start and end with %%
	// Only for wrapping directives (contains, exact, list, binary, truefalse, etc.)
	if result, handled := t.processWrappingDirective(ctx, line); handled {
		return result // Return immediately for wrapping directives
	}

	// Process inline directives within the line
	// Pattern: "text = %%directive args%%" -> "text = result"
	line = t.processInlineDirectives(ctx, line)

	// Apply %%VAR:key%%, %%LOCAL:key%%, %%SERVICE:key%% substitutions
	// Must happen before xformConfig to handle these patterns correctly
	line = configVarRe.ReplaceAllStringFunc(line, func(match string) string {
		return t.xformConfigVariable(ctx, match)
	})

	// Apply %%config_variable%% substitutions (for complex directives)
	line = plainVarRe.ReplaceAllStringFunc(line, func(match string) string {
		return t.xformConfig(ctx, match)
	})

	return line + "\n" // Add newline back as it was removed for processing
}

// isPrefixDirective checks if the directive content is a prefix directive (comment/uncomment).
func isPrefixDirective(directiveContent string) bool {
	return strings.HasPrefix(directiveContent, "comment ") ||
		strings.HasPrefix(directiveContent, "uncomment ")
}

// isWrappingDirective checks if the directive content is a wrapping directive.
func isWrappingDirective(directiveContent string) bool {
	return strings.HasPrefix(directiveContent, "contains ") ||
		strings.HasPrefix(directiveContent, "exact ") ||
		strings.HasPrefix(directiveContent, "list ") ||
		strings.HasPrefix(directiveContent, "binary ") ||
		strings.HasPrefix(directiveContent, "truefalse ") ||
		strings.HasPrefix(directiveContent, "range ") ||
		strings.HasPrefix(directiveContent, "freq ") ||
		strings.HasPrefix(directiveContent, "explode ")
}

// knownDirectives lists every wrapping-directive keyword the transformer
// understands. Declared at package scope so the backing array is allocated
// once at init and every isKnownDirective call is a pure slice walk.
var knownDirectives = []string{"binary", "truefalse", "range", "freq", "list", "contains", "exact"}

// isKnownDirective checks if the directive name is a known directive type.
func isKnownDirective(directiveName string) bool {
	return slices.Contains(knownDirectives, directiveName)
}

// processPrefixDirective handles prefix directives like %%comment VAR:key%%rest_of_line.
func (t *Transformer) processPrefixDirective(ctx context.Context, line string) (string, bool) {
	// Strip leading whitespace — %%comment/%%uncomment directives in config
	// templates (e.g. amavisd.conf.in) are often indented. Preserve the
	// original indentation so the output aligns with the rest of the file.
	trimmed := strings.TrimLeft(line, " \t")
	indent := line[:len(line)-len(trimmed)]

	if !strings.HasPrefix(trimmed, "%%") {
		return line, false
	}

	// Find the closing %% of the directive
	endIdx := strings.Index(trimmed[2:], "%%")
	if endIdx == -1 {
		return line, false
	}

	endIdx += 2 // Adjust for the offset
	directiveContent := trimmed[2:endIdx]
	restOfLine := trimmed[endIdx+2:]

	// Check if this is a known prefix directive
	if !isPrefixDirective(directiveContent) {
		return line, false
	}

	// Process the directive and prepend its result to the rest of the line
	directiveResult := t.xformConfig(ctx, "%%"+directiveContent+"%%")
	// Remove the newline that xformConfig adds since we're prepending to a line
	directiveResult = strings.TrimSuffix(directiveResult, "\n")

	return indent + directiveResult + restOfLine, true
}

// processWrappingDirective handles wrapping directives like %%list VAR:key separator%%.
func (t *Transformer) processWrappingDirective(ctx context.Context, line string) (string, bool) {
	if !strings.HasPrefix(line, "%%") || !strings.HasSuffix(line, "%%") || strings.Count(line, "%") < 4 {
		return line, false
	}

	// Check if this is a wrapping directive vs other patterns
	innerContent := strings.TrimPrefix(strings.TrimSuffix(line, "%%"), "%%")
	if !isWrappingDirective(innerContent) {
		return line, false
	}

	// For %%contains...%% lines, pre-substitute any embedded variables before
	// evaluating the directive, mirroring jylibs/state.py transform() which
	// strips the outer %% and runs xformConfigVariable over the inner content of
	// a %%contains...%% line prior to the contains evaluation. Legacy matched
	// the bare %%key%% form (VAR then LOCAL fallback); the typed %%VAR:key%%
	// form is a Go extension. Other wrapping directives are passed through
	// unchanged (their handlers parse VAR:key themselves).
	if strings.HasPrefix(innerContent, "contains ") {
		substituted := configVarRe.ReplaceAllStringFunc(innerContent, func(match string) string {
			return t.xformConfigVariable(ctx, match)
		})
		substituted = plainVarRe.ReplaceAllStringFunc(substituted, func(match string) string {
			return t.xformConfig(ctx, match)
		})
		line = "%%" + substituted + "%%"
	}

	// This is a directive line - process it directly without VAR substitution
	// The directive handlers will parse VAR:key patterns themselves
	result := t.xformConfig(ctx, line)
	// xformConfig returns the value without newline
	return result + "\n", true
}

// processInlineDirectives handles inline directives within a line like "text = %%directive%%".
func (t *Transformer) processInlineDirectives(ctx context.Context, line string) string {
	if !strings.Contains(line, "%%") || strings.Count(line, "%") < 4 {
		return line
	}

	return inlineDirectRe.ReplaceAllStringFunc(line, func(match string) string {
		innerContent := strings.Trim(match, "%")
		// Check if this looks like a directive (has a space and known directive name)
		parts := strings.SplitN(innerContent, " ", 2)
		if len(parts) >= 2 {
			directiveName := parts[0]
			if isKnownDirective(directiveName) {
				// This is a directive - process it
				return t.xformConfig(ctx, match)
			}
		}
		// Not a directive - return as-is for later processing
		return match
	})
}

// xformLocalConfig handles @@localconfig_key@@ substitutions.
func (t *Transformer) xformLocalConfig(ctx context.Context, match string) string {
	key := strings.Trim(match, "@")
	parts := strings.Fields(key)
	funcName := ""
	lookupKey := key

	if len(parts) > 1 {
		funcName = parts[0]
		lookupKey = strings.Join(parts[1:], " ")
	}

	val, err := t.ConfigLookup.LookUpConfig(ctx, "LOCAL", lookupKey)
	if err != nil {
		logger.WarnContext(ctx, "Could not look up local config key",
			"key", lookupKey,
			"error", err)

		return "" // Return empty string if not found
	}

	switch funcName {
	case "SPLIT":
		// Jython's SPLIT is val.split(' ', 1)[0]: the substring before the FIRST
		// space only. Tabs (or any other whitespace) are preserved, unlike
		// strings.Fields which splits on all whitespace.
		before, _, _ := strings.Cut(val, " ")

		return before
	case "PERDITION_LDAP_SPLIT":
		// This is a complex legacy transformation, simplifying for now
		// It extracts hostnames from LDAP URLs
		hostnames := []string{}

		for _, match := range ldapURLRe.FindAllStringSubmatch(val, -1) {
			if len(match) > 1 {
				hostnames = append(hostnames, match[1])
			}
		}

		if len(hostnames) > 0 {
			return strings.Fields(val)[0] + " " + strings.Join(hostnames, " ")
		}

		return strings.Fields(val)[0]
	default:
		return val
	}
}

// xformConfigVariable handles %%VAR:key%%, %%LOCAL:key%%, and %%SERVICE:key%% substitutions.
func (t *Transformer) xformConfigVariable(ctx context.Context, match string) string {
	content := strings.Trim(match, "%")

	parts := strings.SplitN(content, ":", 2)
	if len(parts) != 2 {
		logger.WarnContext(ctx, "Invalid config variable format",
			"match", match)

		return "" // Invalid format
	}

	cfgType := parts[0]
	key := parts[1]

	// Validate config type
	if cfgType != configTypeVAR && cfgType != configTypeLOCAL && cfgType != configTypeSERVICE {
		logger.WarnContext(ctx, "Invalid config type in variable",
			"config_type", cfgType)

		return ""
	}

	val, err := t.ConfigLookup.LookUpConfig(ctx, cfgType, key)
	if err != nil {
		logger.WarnContext(ctx, "Could not look up config variable",
			"config_type", cfgType,
			"key", key,
			"error", err)

		return "" // Return empty string if not found
	}

	return val
}

// xformConfig handles complex %% directives like comment, binary, range, etc.
// It dispatches to specialized handler methods based on the directive type.
func (t *Transformer) xformConfig(ctx context.Context, match string) string {
	sr := strings.Trim(match, "%")

	// Dispatch to appropriate handler based on directive prefix
	switch {
	case strings.HasPrefix(sr, "comment"):
		return t.handleCommentDirective(ctx, sr)
	case strings.HasPrefix(sr, "uncomment"):
		return t.handleUncommentDirective(ctx, sr)
	case strings.HasPrefix(sr, "binary"):
		return t.handleBinaryDirective(ctx, sr)
	case strings.HasPrefix(sr, "truefalse"):
		return t.handleTrueFalseDirective(ctx, sr)
	case strings.HasPrefix(sr, "range"):
		return t.handleRangeDirective(ctx, sr)
	case strings.HasPrefix(sr, "freq"):
		return t.handleFreqDirective(ctx, sr)
	case strings.HasPrefix(sr, "list"):
		return t.handleListDirective(ctx, sr)
	case strings.HasPrefix(sr, "contains"):
		return t.handleContainsDirective(ctx, sr)
	case strings.HasPrefix(sr, "exact"):
		return t.handleExactDirective(ctx, sr)
	case strings.HasPrefix(sr, "explode"):
		return t.handleExplodeDirective(ctx, sr)
	default:
		return t.handleDefaultLookup(ctx, sr)
	}
}
