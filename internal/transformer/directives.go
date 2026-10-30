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

// resolveCommentState implements the shared logic for comment/uncomment directives,
// mirroring jylibs/state.py xformConfig. The operand is "TYPE:key[,commentStr[,v1,v2...]]":
//
//   - without a value set the line is commented when the looked-up value is true
//     (comment) or false (uncomment);
//   - with a value set it is commented when the value is missing or listed
//     (comment), or when it is present and not listed (uncomment).
//
// A "!" key prefix turns the lookup into a boolean (legacy checkConditional), which
// is never "missing" and never equal to a listed string.
func (t *Transformer) resolveCommentState(
	ctx context.Context,
	re *regexp.Regexp,
	warnMsg string,
	sr string,
	isComment bool,
) string {
	matches := re.FindStringSubmatch(sr)
	if len(matches) < 3 {
		logger.WarnContext(ctx, warnMsg, "directive", sr)
		return "" // Invalid format
	}

	cmd := matches[1]
	parts := strings.SplitN(matches[2], ",", 3)
	key := parts[0]
	commentStr := "#"

	if len(parts) > 1 {
		commentStr = parts[1]
	}

	negate := strings.HasPrefix(key, "!")

	lookupVal, err := t.ConfigLookup.LookUpConfig(ctx, cmd, strings.TrimPrefix(key, "!"))
	if err != nil {
		lookupVal = "" // legacy None: false for the boolean forms
	}

	var hit bool

	switch {
	case len(parts) > 2:
		missing := err != nil && !negate
		listed := !negate && err == nil && slices.Contains(strings.Split(parts[2], ","), lookupVal)
		hit = missing || listed
	case negate:
		hit = state.IsFalseValue(lookupVal)
	default:
		hit = state.IsTrueValue(lookupVal)
	}

	if hit == isComment {
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

// lookupTruth reports whether the value behind TYPE:key is true for the
// binary/truefalse directives. A "!" key prefix negates the test (legacy
// checkConditional); a failed lookup is the legacy None, i.e. false.
func (t *Transformer) lookupTruth(ctx context.Context, cmd, key string) bool {
	lookupVal, err := t.ConfigLookup.LookUpConfig(ctx, cmd, strings.TrimPrefix(key, "!"))
	if err != nil {
		lookupVal = ""
	}

	if strings.HasPrefix(key, "!") {
		return state.IsFalseValue(lookupVal)
	}

	return state.IsTrueValue(lookupVal)
}

// handleBinaryDirective processes binary directives.
// Format: %%binary VAR:key%%
func (t *Transformer) handleBinaryDirective(ctx context.Context, sr string) string {
	matches := binaryRe.FindStringSubmatch(sr)
	if len(matches) < 3 {
		logger.WarnContext(ctx, "Invalid binary directive", "directive", sr)
		return "" // Invalid format
	}

	if t.lookupTruth(ctx, matches[1], matches[2]) {
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

	if t.lookupTruth(ctx, matches[1], matches[2]) {
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

	lookupVal, errVal := atoiPy(lookupValStr)
	lo, errLo := atoiPy(loStr)
	hi, errHi := atoiPy(hiStr)

	if errVal != nil || errLo != nil || errHi != nil {
		logger.WarnContext(ctx, "Invalid integer in range directive",
			"value", lookupValStr, "lo", loStr, "hi", hiStr)

		return "" // Legacy int() raises and aborts the rewrite
	}

	// ((val/100.00) * (hi - lo)) + lo with Python float semantics. The explicit
	// conversion stops the compiler from fusing the multiply-add (FMA), which
	// would change the last bit on some architectures.
	scaled := float64(float64(lookupVal) / 100.00 * float64(hi-lo))

	return pyStrFloat(scaled + float64(lo))
}

// atoiPy parses an integer like Python 2's int(str): surrounding whitespace is ignored.
func atoiPy(s string) (int, error) {
	return strconv.Atoi(strings.Trim(s, pyWhitespace))
}

// pyStrFloat formats f like Python 2's str(float): 12 significant digits with
// trailing zeros removed, and ".0" appended to an integral value, so 50 renders
// as "50.0", 6.6 as "6.6" and 1e16 as "1e+16".
func pyStrFloat(f float64) string {
	s := strconv.FormatFloat(f, 'g', 12, 64)
	if strings.ContainsAny(s, ".eIN") {
		return s
	}

	return s + ".0"
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

// resolveSearchReplaceDirective implements the shared logic for contains/exact directives.
// Format: %%<directive> VAR:key searchString^replacement^altReplacement%%
// The match func receives (lookupVal, searchString) and returns whether the value matches.
//
// As in jylibs/state.py, replacement and altReplacement are stripped, and an
// empty replacement falls back to searchString. A missing key is Python's None:
// contains searches in str(None) == "None", exact (searchInNone false) yields "".
func (t *Transformer) resolveSearchReplaceDirective(
	ctx context.Context,
	directiveName string,
	sr string,
	searchInNone bool,
	match func(lookupVal, searchString string) bool,
) string {
	parts := strings.SplitN(sr, " ", 3)
	if len(parts) < 3 {
		logger.WarnContext(ctx, "Invalid "+directiveName+" directive", "directive", sr)
		return ""
	}

	cfgTypeParts := strings.SplitN(parts[1], ":", 2)
	if len(cfgTypeParts) != 2 {
		logger.WarnContext(ctx, "Invalid "+directiveName+" config type:key format", "config", parts[1])
		return ""
	}

	lookupVal, err := t.ConfigLookup.LookUpConfig(ctx, cfgTypeParts[0], cfgTypeParts[1])
	if err != nil {
		logger.WarnContext(ctx, "Error looking up config for "+directiveName,
			"config_type", cfgTypeParts[0],
			"key", cfgTypeParts[1],
			"error", err)

		if !searchInNone {
			return ""
		}

		lookupVal = "None"
	}

	splitReplace := strings.SplitN(parts[2], "^", 3)
	searchString := splitReplace[0]
	replacement := ""
	altReplacement := ""

	if len(splitReplace) > 1 {
		replacement = strings.Trim(splitReplace[1], pyWhitespace)
	}

	if len(splitReplace) > 2 {
		altReplacement = strings.Trim(splitReplace[2], pyWhitespace)
	}

	if match(lookupVal, searchString) {
		if replacement == "" {
			return searchString
		}

		return replacement
	}

	return altReplacement
}

// handleContainsDirective processes contains directives.
// Format: %%contains VAR:key string^replacement^altreplacement%%
func (t *Transformer) handleContainsDirective(ctx context.Context, sr string) string {
	return t.resolveSearchReplaceDirective(ctx, "contains", sr, true, strings.Contains)
}

// handleExactDirective processes exact directives.
// Format: %%exact VAR:key string^replacement^altreplacement%%
func (t *Transformer) handleExactDirective(ctx context.Context, sr string) string {
	return t.resolveSearchReplaceDirective(ctx, "exact", sr, false,
		func(lookupVal, searchString string) bool {
			return slices.Contains(strings.Fields(lookupVal), searchString)
		})
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

	// Legacy: per = re.sub(r"\d+", "", val); val = int(re.sub(r"\D", "", val)).
	// An empty digit string raises ValueError in Jython (the rewrite aborts);
	// here it counts as 0, which falls through to total.
	per := freqDigitRe.ReplaceAllString(lookupValStr, "")
	valNum, _ := strconv.Atoi(freqNonDigitRe.ReplaceAllString(lookupValStr, ""))
	total, _ := strconv.Atoi(totalStr)

	// Python 2 integer division floors.
	switch per {
	case "m":
		valNum = floorDiv(valNum, 60)
	case "s":
		valNum = floorDiv(valNum, 3600)
	case "d":
		valNum *= 24
	}

	val := total
	if valNum != 0 {
		val = floorDiv(total, valNum)
	}

	if val < 1 && total > 1 {
		val = 1
	}

	return strconv.Itoa(val)
}

// floorDiv is Python 2's integer a / b (rounds toward negative infinity); b != 0.
func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}

	return q
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
