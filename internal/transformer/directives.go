// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package transformer

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/state"
)

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
	return t.resolveCommentState(ctx, commentRe, "Invalid comment directive", sr, true)
}

// handleUncommentDirective processes uncomment directives.
// Format: %%uncomment VAR:key%% or %%uncomment VAR:key,#%% or %%uncomment VAR:key,#,val1,val2%%
func (t *Transformer) handleUncommentDirective(ctx context.Context, sr string) string {
	return t.resolveCommentState(ctx, uncommentRe, "Invalid uncomment directive", sr, false)
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
	matches := binaryRe.FindStringSubmatch(sr)
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
	matches := truefalseRe.FindStringSubmatch(sr)
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
	matches := rangeRe.FindStringSubmatch(sr)
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
	matches := freqRe.FindStringSubmatch(sr)
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

	valNumStr := freqDigitsRe.ReplaceAllString(lookupValStr, "")
	per := freqNonDigitsRe.ReplaceAllString(lookupValStr, "")

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
	matches := explodeRe.FindStringSubmatch(sr)
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
