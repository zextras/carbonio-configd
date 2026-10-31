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

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
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
	sr string,
	isComment bool,
) (string, error) {
	matches, err := findOne(re, sr)
	if err != nil {
		return "", err
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
		hit = config.IsFalseValue(lookupVal)
	default:
		hit = config.IsTrueValue(lookupVal)
	}

	if hit == isComment {
		return commentStr, nil
	}

	return "", nil
}

// handleCommentDirective processes comment directives.
// Format: %%comment VAR:key%% or %%comment VAR:!key%% (negated)
// or %%comment VAR:key,#%% or %%comment VAR:key,#,val1,val2%%
//
// When the key is prefixed with `!`, the logic is inverted: comment the line
// when the value is FALSE/empty (instead of when TRUE). This mirrors the
// legacy Python configd behavior used in amavisd.conf.in templates.
func (t *Transformer) handleCommentDirective(ctx context.Context, sr string) (string, error) {
	return t.resolveCommentState(ctx, commentRe, sr, true)
}

// handleUncommentDirective processes uncomment directives.
// Format: %%uncomment VAR:key%% or %%uncomment VAR:key,#%% or %%uncomment VAR:key,#,val1,val2%%
func (t *Transformer) handleUncommentDirective(ctx context.Context, sr string) (string, error) {
	return t.resolveCommentState(ctx, uncommentRe, sr, false)
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
		return config.IsFalseValue(lookupVal)
	}

	return config.IsTrueValue(lookupVal)
}

// transformErr builds an ErrTransform for a directive the legacy code would
// raise on.
func transformErr(directive, format string, args ...any) error {
	return fmt.Errorf("%w: %%%%%s%%%%: %s", ErrTransform, directive, fmt.Sprintf(format, args...))
}

// findOne mirrors the legacy `[(a, b, ...)] = re.findall(re, sr)`: the directive
// must match its pattern exactly once, otherwise the unpack raises ValueError.
func findOne(re *regexp.Regexp, sr string) ([]string, error) {
	all := re.FindAllStringSubmatch(sr, -1)
	if len(all) != 1 {
		return nil, transformErr(sr, "does not match %s exactly once", re)
	}

	return all[0], nil
}

// lookupRequired resolves TYPE:key where the legacy code feeds the result to
// int()/re.sub(), so a missing key (None) raises TypeError.
func (t *Transformer) lookupRequired(ctx context.Context, sr, cmd, key string) (string, error) {
	val, err := t.ConfigLookup.LookUpConfig(ctx, cmd, key)
	if err != nil {
		return "", transformErr(sr, "%s:%s is not set: %v", cmd, key, err)
	}

	return val, nil
}

// handleBinaryDirective processes binary directives.
// Format: %%binary VAR:key%%
func (t *Transformer) handleBinaryDirective(ctx context.Context, sr string) (string, error) {
	matches, err := findOne(binaryRe, sr)
	if err != nil {
		return "", err
	}

	if t.lookupTruth(ctx, matches[1], matches[2]) {
		return "1", nil
	}

	return "0", nil
}

// handleTrueFalseDirective processes truefalse directives.
// Format: %%truefalse VAR:key%%
func (t *Transformer) handleTrueFalseDirective(ctx context.Context, sr string) (string, error) {
	matches, err := findOne(truefalseRe, sr)
	if err != nil {
		return "", err
	}

	if t.lookupTruth(ctx, matches[1], matches[2]) {
		return "true", nil
	}

	return "false", nil
}

// handleRangeDirective processes range directives.
// Format: %%range VAR:key lo hi%%
//
// Legacy int(lookUpConfig(...)), int(hi) and int(lo) raise on a missing key or
// a non-integer, which aborts the rewrite.
func (t *Transformer) handleRangeDirective(ctx context.Context, sr string) (string, error) {
	matches, err := findOne(rangeRe, sr)
	if err != nil {
		return "", err
	}

	lookupValStr, err := t.lookupRequired(ctx, sr, matches[1], matches[2])
	if err != nil {
		return "", err
	}

	lookupVal, errVal := atoiPy(lookupValStr)
	lo, errLo := atoiPy(matches[3])
	hi, errHi := atoiPy(matches[4])

	if errVal != nil || errLo != nil || errHi != nil {
		return "", transformErr(sr, "value %q, lo %q, hi %q must be integers", lookupValStr, matches[3], matches[4])
	}

	// ((val/100.00) * (hi - lo)) + lo with Python float semantics. The explicit
	// conversion stops the compiler from fusing the multiply-add (FMA), which
	// would change the last bit on some architectures.
	scaled := float64(float64(lookupVal) / 100.00 * float64(hi-lo))

	return pyStrFloat(scaled + float64(lo)), nil
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

// splitTypeKey is the legacy `(type, key) = field.split(':')`: exactly one
// colon, otherwise the unpack raises ValueError.
func splitTypeKey(sr, field string) (cfgType, key string, err error) {
	parts := strings.Split(field, ":")
	if len(parts) != 2 {
		return "", "", transformErr(sr, "%q is not TYPE:key", field)
	}

	return parts[0], parts[1], nil
}

// handleListDirective processes list directives.
// Format: %%list VAR:key separator%%
//
// Legacy fields = sr.split(' ', 2): fields[1] must exist, and fields[2] only
// when the value is non-empty (an empty or missing value renders empty).
func (t *Transformer) handleListDirective(ctx context.Context, sr string) (string, error) {
	fields := strings.SplitN(sr, " ", 3)
	if len(fields) < 2 {
		return "", transformErr(sr, "missing TYPE:key")
	}

	cfgType, key, err := splitTypeKey(sr, fields[1])
	if err != nil {
		return "", err
	}

	lookupVal, err := t.ConfigLookup.LookUpConfig(ctx, cfgType, key)
	if err != nil {
		lookupVal = "" // legacy None
	}

	if lookupVal == "" {
		return "", nil
	}

	if len(fields) < 3 {
		return "", transformErr(sr, "missing separator")
	}

	return strings.Join(strings.Fields(lookupVal), fields[2]), nil
}

// resolveSearchReplaceDirective implements the shared logic for contains/exact directives.
// Format: %%<directive> VAR:key searchString^replacement^altReplacement%%
// The match func receives (lookupVal, searchString) and returns whether the value matches.
//
// As in jylibs/state.py, replacement and altReplacement are stripped, and an
// empty replacement falls back to searchString. A missing key is Python's None:
// contains searches in str(None) == "None", exact (searchInNone false) yields "".
// fields[1] (TYPE:key) and fields[2] (the search string) are indexed without a
// guard, so a missing one raises and aborts the rewrite.
func (t *Transformer) resolveSearchReplaceDirective(
	ctx context.Context,
	sr string,
	searchInNone bool,
	match func(lookupVal, searchString string) bool,
) (string, error) {
	f := strings.SplitN(sr, "^", 3)

	var replacement, altReplacement string

	if len(f) > 1 {
		replacement = strings.Trim(f[1], pyWhitespace)
	}

	if len(f) > 2 {
		altReplacement = strings.Trim(f[2], pyWhitespace)
	}

	fields := strings.SplitN(f[0], " ", 3)
	if len(fields) < 2 {
		return "", transformErr(sr, "missing TYPE:key")
	}

	cfgType, key, err := splitTypeKey(sr, fields[1])
	if err != nil {
		return "", err
	}

	lookupVal, err := t.ConfigLookup.LookUpConfig(ctx, cfgType, key)
	if err != nil {
		if !searchInNone {
			return "", nil
		}

		lookupVal = "None"
	}

	if len(fields) < 3 {
		return "", transformErr(sr, "missing search string")
	}

	searchString := fields[2]

	if match(lookupVal, searchString) {
		if replacement == "" {
			return searchString, nil
		}

		return replacement, nil
	}

	return altReplacement, nil
}

// handleContainsDirective processes contains directives.
// Format: %%contains VAR:key string^replacement^altreplacement%%
func (t *Transformer) handleContainsDirective(ctx context.Context, sr string) (string, error) {
	return t.resolveSearchReplaceDirective(ctx, sr, true, strings.Contains)
}

// handleExactDirective processes exact directives.
// Format: %%exact VAR:key string^replacement^altreplacement%%
func (t *Transformer) handleExactDirective(ctx context.Context, sr string) (string, error) {
	return t.resolveSearchReplaceDirective(ctx, sr, false,
		func(lookupVal, searchString string) bool {
			return slices.Contains(strings.Fields(lookupVal), searchString)
		})
}

// handleFreqDirective processes freq directives.
// Format: %%freq VAR:key total%%
//
// Legacy re.sub on a missing value, int("") of a value without digits and
// int(total) of a non-integer all raise and abort the rewrite.
func (t *Transformer) handleFreqDirective(ctx context.Context, sr string) (string, error) {
	matches, err := findOne(freqRe, sr)
	if err != nil {
		return "", err
	}

	lookupValStr, err := t.lookupRequired(ctx, sr, matches[1], matches[2])
	if err != nil {
		return "", err
	}

	// Legacy: per = re.sub(r"\d+", "", val); val = int(re.sub(r"\D", "", val)).
	per := freqDigitRe.ReplaceAllString(lookupValStr, "")

	valNum, errVal := strconv.Atoi(freqNonDigitRe.ReplaceAllString(lookupValStr, ""))
	total, errTotal := atoiPy(matches[3])

	if errVal != nil || errTotal != nil {
		return "", transformErr(sr, "value %q and total %q must hold integers", lookupValStr, matches[3])
	}

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

	return strconv.Itoa(val), nil
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
func (t *Transformer) handleExplodeDirective(ctx context.Context, sr string) (string, error) {
	matches, err := findOne(explodeRe, sr)
	if err != nil {
		return "", err
	}

	lookupValsStr, err := t.ConfigLookup.LookUpConfig(ctx, matches[2], matches[3])
	if err != nil {
		lookupValsStr = "" // legacy None: `if vals:` is false
	}

	explodedLines := make([]string, 0, 4)

	for v := range strings.FieldsSeq(lookupValsStr) {
		explodedLines = append(explodedLines, matches[1]+" "+v)
	}

	return strings.Join(explodedLines, "\n"), nil
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
