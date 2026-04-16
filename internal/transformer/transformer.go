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
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/lookup"
	"github.com/zextras/carbonio-configd/internal/state"
)

// Transformer holds the necessary dependencies for transforming lines.
type Transformer struct {
	ConfigLookup lookup.ConfigLookup
	State        *state.State

	// Compiled regex patterns (cached for performance)
	localConfigRe   *regexp.Regexp
	configVarRe     *regexp.Regexp
	plainVarRe      *regexp.Regexp
	commentRe       *regexp.Regexp
	uncommentRe     *regexp.Regexp
	binaryRe        *regexp.Regexp
	truefalseRe     *regexp.Regexp
	rangeRe         *regexp.Regexp
	freqRe          *regexp.Regexp
	freqDigitsRe    *regexp.Regexp
	freqNonDigitsRe *regexp.Regexp
	explodeRe       *regexp.Regexp
	ldapURLRe       *regexp.Regexp
	inlineDirectRe  *regexp.Regexp // compiled once, used in processInlineDirectives
}

// NewTransformer creates a new Transformer instance.
func NewTransformer(cl lookup.ConfigLookup, st *state.State) *Transformer {
	return &Transformer{
		ConfigLookup: cl,
		State:        st,

		// Compile all regex patterns once at initialization
		localConfigRe:   regexp.MustCompile(`@@([^@]+)@@`),
		configVarRe:     regexp.MustCompile(`%%(VAR|LOCAL|SERVICE):([^%]+)%%`),
		plainVarRe:      regexp.MustCompile(`%%([^%:]+)%%`),
		commentRe:       regexp.MustCompile(`comment ([^:]+):([^,\s]+)(?:,([^,]+))?(?:,([^,]+))?`),
		uncommentRe:     regexp.MustCompile(`uncomment ([^:]+):([^,\s]+)(?:,([^,]+))?(?:,([^,]+))?`),
		binaryRe:        regexp.MustCompile(`binary ([^:]+):(\S+)`),
		truefalseRe:     regexp.MustCompile(`truefalse ([^:]+):(\S+)`),
		rangeRe:         regexp.MustCompile(`range ([^:]+):(\S+)\s+(\S+)\s+(\S+)`),
		freqRe:          regexp.MustCompile(`freq ([^:]+):(\S+)\s+(\S+)`),
		freqDigitsRe:    regexp.MustCompile(`\D`),
		freqNonDigitsRe: regexp.MustCompile(`\d`),
		explodeRe:       regexp.MustCompile(`explode\s+(.*)\s+([^:]+):(\w+)`),
		ldapURLRe:       regexp.MustCompile(`ldap.?://(\S+):\d+`),
		inlineDirectRe:  regexp.MustCompile(`%%([^%]+)%%`),
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
	line = t.localConfigRe.ReplaceAllStringFunc(line, func(match string) string {
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
	line = t.configVarRe.ReplaceAllStringFunc(line, func(match string) string {
		return t.xformConfigVariable(ctx, match)
	})

	// Apply %%config_variable%% substitutions (for complex directives)
	line = t.plainVarRe.ReplaceAllStringFunc(line, func(match string) string {
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

// isKnownDirective checks if the directive name is a known directive type.
func isKnownDirective(directiveName string) bool {
	knownDirectives := []string{"binary", "truefalse", "range", "freq", "list", "contains", "exact"}
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

	return t.inlineDirectRe.ReplaceAllStringFunc(line, func(match string) string {
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
		// Jython's SPLIT takes the first part of a space-separated string
		return strings.Fields(val)[0]
	case "PERDITION_LDAP_SPLIT":
		// This is a complex legacy transformation, simplifying for now
		// It extracts hostnames from LDAP URLs
		hostnames := []string{}

		for _, match := range t.ldapURLRe.FindAllStringSubmatch(val, -1) {
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
	if cfgType != "VAR" && cfgType != "LOCAL" && cfgType != "SERVICE" {
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

// resolveCommentState implements the shared logic for comment/uncomment directives.
// trueIsComment controls the no-valSet branch: true = comment when value is TRUE (comment
// directive), false = comment when value is FALSE (uncomment directive).
func (t *Transformer) resolveCommentState(
	ctx context.Context,
	re *regexp.Regexp,
	warnMsg string,
	sr string,
	trueIsComment bool,
) string {
	matches := re.FindStringSubmatch(sr)
	if len(matches) < 3 {
		logger.WarnContext(ctx, warnMsg, "directive", sr)
		return "" // Invalid format
	}

	cmd := matches[1]
	key := matches[2]

	negate := strings.HasPrefix(key, "!")
	if negate {
		key = key[1:]
	}

	commentStr := "#"
	if len(matches) > 3 && matches[3] != "" {
		commentStr = matches[3]
	}

	valSet := []string{}
	if len(matches) > 4 && matches[4] != "" {
		valSet = strings.Split(matches[4], ",")
	}

	lookupVal, err := t.ConfigLookup.LookUpConfig(ctx, cmd, key)
	if err != nil {
		lookupVal = "" // Treat lookup error as empty
	}

	var shouldComment bool

	switch {
	case len(valSet) > 0:
		shouldComment = slices.Contains(valSet, lookupVal)
	case trueIsComment:
		shouldComment = state.IsTrueValue(lookupVal)
	default:
		shouldComment = !state.IsTrueValue(lookupVal)
	}

	if negate {
		shouldComment = !shouldComment
	}

	if shouldComment {
		return commentStr
	}

	return ""
}

// handleCommentDirective processes comment directives.
// Format: %%comment VAR:key%% or %%comment VAR:!key%% (negated)
// or %%comment VAR:key,#%% or %%comment VAR:key,#,val1,val2%%
//
// When the key is prefixed with `!`, the logic is inverted: comment the line
// when the value is FALSE/empty (instead of when TRUE). This mirrors the
// legacy Python configd behavior used in amavisd.conf.in templates.
func (t *Transformer) handleCommentDirective(ctx context.Context, sr string) string {
	return t.resolveCommentState(ctx, t.commentRe, "Invalid comment directive", sr, true)
}

// handleUncommentDirective processes uncomment directives.
// Format: %%uncomment VAR:key%% or %%uncomment VAR:key,#%% or %%uncomment VAR:key,#,val1,val2%%
func (t *Transformer) handleUncommentDirective(ctx context.Context, sr string) string {
	return t.resolveCommentState(ctx, t.uncommentRe, "Invalid uncomment directive", sr, false)
}

// lookupBooleanValue is a helper to look up a boolean config value.
// Returns the string value (treating errors as empty string).
func (t *Transformer) lookupBooleanValue(ctx context.Context, cmd, key string) string {
	lookupVal, err := t.ConfigLookup.LookUpConfig(ctx, cmd, key)
	if err != nil {
		return "" // Treat lookup error as empty
	}

	return lookupVal
}

// handleBinaryDirective processes binary directives.
// Format: %%binary VAR:key%%
func (t *Transformer) handleBinaryDirective(ctx context.Context, sr string) string {
	matches := t.binaryRe.FindStringSubmatch(sr)
	if len(matches) < 3 {
		logger.WarnContext(ctx, "Invalid binary directive", "directive", sr)
		return "" // Invalid format
	}

	lookupVal := t.lookupBooleanValue(ctx, matches[1], matches[2])

	if state.IsTrueValue(lookupVal) {
		return "1"
	}

	return "0"
}

// handleTrueFalseDirective processes truefalse directives.
// Format: %%truefalse VAR:key%%
func (t *Transformer) handleTrueFalseDirective(ctx context.Context, sr string) string {
	matches := t.truefalseRe.FindStringSubmatch(sr)
	if len(matches) < 3 {
		logger.WarnContext(ctx, "Invalid truefalse directive", "directive", sr)
		return "" // Invalid format
	}

	lookupVal := t.lookupBooleanValue(ctx, matches[1], matches[2])

	if state.IsTrueValue(lookupVal) {
		return "true"
	}

	return "false"
}

// handleRangeDirective processes range directives.
// Format: %%range VAR:key lo hi%%
func (t *Transformer) handleRangeDirective(ctx context.Context, sr string) string {
	matches := t.rangeRe.FindStringSubmatch(sr)
	if len(matches) < 5 {
		logger.WarnContext(ctx, "Invalid range directive", "directive", sr)
		return "" // Invalid format
	}

	cmd := matches[1]
	key := matches[2]
	loStr := matches[3]
	hiStr := matches[4]

	lookupValStr, err := t.ConfigLookup.LookUpConfig(ctx, cmd, key)
	if err != nil {
		logger.WarnContext(ctx, "Error looking up config for range",
			"command", cmd,
			"key", key,
			"error", err)

		return "" // Return empty string on error
	}

	lookupVal, err := strconv.Atoi(lookupValStr)
	if err != nil {
		logger.WarnContext(ctx, "Invalid integer value for range lookup", "value", lookupValStr)
		return "" // Return empty string on error
	}

	lo, _ := strconv.Atoi(loStr)
	hi, _ := strconv.Atoi(hiStr)

	calculatedVal := ((float64(lookupVal) / 100.00) * float64(hi-lo)) + float64(lo)

	return fmt.Sprintf("%.0f", calculatedVal)
}

// handleListDirective processes list directives.
// Format: %%list VAR:key separator%%
func (t *Transformer) handleListDirective(ctx context.Context, sr string) string {
	parts := strings.SplitN(sr, " ", 3)
	if len(parts) < 3 {
		logger.WarnContext(ctx, "Invalid list directive", "directive", sr)
		return "" // Invalid format
	}

	cfgTypeKey := parts[1]
	separator := parts[2]

	cfgTypeParts := strings.SplitN(cfgTypeKey, ":", 2)
	if len(cfgTypeParts) != 2 {
		logger.WarnContext(ctx, "Invalid list config type:key format", "config", cfgTypeKey)
		return "" // Invalid format
	}

	cfgType := cfgTypeParts[0]
	key := cfgTypeParts[1]

	lookupVal, err := t.ConfigLookup.LookUpConfig(ctx, cfgType, key)
	if err != nil {
		logger.WarnContext(ctx, "Error looking up config for list",
			"config_type", cfgType,
			"key", key,
			"error", err)

		return "" // Return empty string on error
	}

	if lookupVal != "" {
		return strings.Join(strings.Fields(lookupVal), separator)
	}

	return ""
}

// handleContainsDirective processes contains directives.
// Format: %%contains VAR:key string^replacement^altreplacement%%
func (t *Transformer) handleContainsDirective(ctx context.Context, sr string) string {
	parts := strings.SplitN(sr, " ", 3)
	if len(parts) < 3 {
		logger.WarnContext(ctx, "Invalid contains directive", "directive", sr)
		return "" // Invalid format
	}

	cfgTypeKey := parts[1]
	searchAndReplace := parts[2]

	cfgTypeParts := strings.SplitN(cfgTypeKey, ":", 2)
	if len(cfgTypeParts) != 2 {
		logger.WarnContext(ctx, "Invalid contains config type:key format", "config", cfgTypeKey)
		return "" // Invalid format
	}

	cfgType := cfgTypeParts[0]
	key := cfgTypeParts[1]

	lookupVal, err := t.ConfigLookup.LookUpConfig(ctx, cfgType, key)
	if err != nil {
		logger.WarnContext(ctx, "Error looking up config for contains",
			"config_type", cfgType,
			"key", key,
			"error", err)

		return "" // Return empty string on error
	}

	splitReplace := strings.SplitN(searchAndReplace, "^", 3)
	searchString := splitReplace[0]
	replacement := searchString
	altReplacement := ""

	if len(splitReplace) > 1 {
		replacement = splitReplace[1]
	}

	if len(splitReplace) > 2 {
		altReplacement = splitReplace[2]
	}

	if strings.Contains(lookupVal, searchString) {
		return replacement
	}

	return altReplacement
}

// handleExactDirective processes exact directives.
// Format: %%exact VAR:key string^replacement^altreplacement%%
func (t *Transformer) handleExactDirective(ctx context.Context, sr string) string {
	parts := strings.SplitN(sr, " ", 3)
	if len(parts) < 3 {
		logger.WarnContext(ctx, "Invalid exact directive", "directive", sr)
		return "" // Invalid format
	}

	cfgTypeKey := parts[1]
	searchAndReplace := parts[2]

	cfgTypeParts := strings.SplitN(cfgTypeKey, ":", 2)
	if len(cfgTypeParts) != 2 {
		logger.WarnContext(ctx, "Invalid exact config type:key format", "config", cfgTypeKey)
		return "" // Invalid format
	}

	cfgType := cfgTypeParts[0]
	key := cfgTypeParts[1]

	lookupVal, err := t.ConfigLookup.LookUpConfig(ctx, cfgType, key)
	if err != nil {
		logger.WarnContext(ctx, "Error looking up config for exact",
			"config_type", cfgType,
			"key", key,
			"error", err)

		return "" // Return empty string on error
	}

	splitReplace := strings.SplitN(searchAndReplace, "^", 3)
	searchString := splitReplace[0]
	replacement := searchString
	altReplacement := ""

	if len(splitReplace) > 1 {
		replacement = splitReplace[1]
	}

	if len(splitReplace) > 2 {
		altReplacement = splitReplace[2]
	}

	// For exact, we need to split the lookupVal into fields and check for exact match
	foundExact := slices.Contains(strings.Fields(lookupVal), searchString)

	if foundExact {
		return replacement
	}

	return altReplacement
}

// handleFreqDirective processes freq directives.
// Format: %%freq VAR:key total%%
func (t *Transformer) handleFreqDirective(ctx context.Context, sr string) string {
	matches := t.freqRe.FindStringSubmatch(sr)
	if len(matches) < 4 {
		logger.WarnContext(ctx, "Invalid freq directive", "directive", sr)
		return "" // Invalid format
	}

	cmd := matches[1]
	key := matches[2]
	totalStr := matches[3]

	lookupValStr, err := t.ConfigLookup.LookUpConfig(ctx, cmd, key)
	if err != nil {
		logger.WarnContext(ctx, "Error looking up config for freq",
			"command", cmd,
			"key", key,
			"error", err)

		return "" // Return empty string on error
	}

	valNumStr := t.freqDigitsRe.ReplaceAllString(lookupValStr, "")
	per := t.freqNonDigitsRe.ReplaceAllString(lookupValStr, "")

	valNum, _ := strconv.Atoi(valNumStr)
	total, _ := strconv.Atoi(totalStr)

	switch per {
	case "m":
		valNum /= 60
	case "s":
		valNum /= 3600
	case "d":
		valNum *= 24
	}

	var val string
	if valNum != 0 {
		val = fmt.Sprintf("%d", total/valNum)
	} else {
		val = fmt.Sprintf("%d", total)
	}

	if valInt, _ := strconv.Atoi(val); valInt < 1 && total > 1 {
		val = "1"
	}

	return val
}

// handleExplodeDirective processes explode directives.
// Format: %%explode base_string VAR:key%%
func (t *Transformer) handleExplodeDirective(ctx context.Context, sr string) string {
	matches := t.explodeRe.FindStringSubmatch(sr)
	if len(matches) < 4 {
		logger.WarnContext(ctx, "Invalid explode directive", "directive", sr)
		return "" // Invalid format
	}

	baseString := matches[1]
	cmd := matches[2]
	key := matches[3]

	lookupValsStr, err := t.ConfigLookup.LookUpConfig(ctx, cmd, key)
	if err != nil {
		logger.WarnContext(ctx, "Error looking up config for explode",
			"command", cmd,
			"key", key,
			"error", err)

		return "" // Return empty string on error
	}

	var explodedLines []string

	if lookupValsStr != "" {
		for v := range strings.FieldsSeq(lookupValsStr) {
			explodedLines = append(explodedLines, fmt.Sprintf("%s %s", baseString, v))
		}
	}

	return strings.Join(explodedLines, "\n")
}

// handleDefaultLookup handles default variable lookups when no directive matches.
// It tries VAR first, then LOCAL as a fallback.
func (t *Transformer) handleDefaultLookup(ctx context.Context, sr string) string {
	lookupVal, err := t.ConfigLookup.LookUpConfig(ctx, "VAR", sr)
	if err != nil {
		lookupVal, err = t.ConfigLookup.LookUpConfig(ctx, "LOCAL", sr)
	}

	if err == nil {
		return lookupVal
	}

	logger.WarnContext(ctx, "Unknown directive or config key", "directive", sr)

	return "" // Return empty string if not found
}
