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
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/lookup"
)

// Transformer holds the necessary dependencies for transforming lines.
// Regex patterns are compiled once at package init (see the var block below)
// and shared across every Transformer instance, so constructing a new
// Transformer is allocation-free.
type Transformer struct {
	ConfigLookup lookup.ConfigLookup
}

// ErrTransform marks a line the legacy jylibs/state.py transform() cannot render:
// the Jython code raises (int() of None or garbage, a failed
// `[(a, b)] = re.findall(...)` unpack, an out-of-range fields[n], None.split())
// and rewriteConfig abandons the whole file without touching its target.
// Callers must discard any output produced for the file so far.
var ErrTransform = errors.New("transform failed")

const (
	// configTypeVAR is the VAR config type for variable substitution.
	configTypeVAR = "VAR"

	// configTypeLOCAL is the LOCAL config type for variable substitution.
	configTypeLOCAL = "LOCAL"

	// configTypeSERVICE is the SERVICE config type for variable substitution.
	configTypeSERVICE = "SERVICE"

	// pyWhitespace is what Python 2's str.strip()/rstrip() remove from a byte string.
	pyWhitespace = " \t\n\r\v\f"
)

// Compiled once at package init. `*regexp.Regexp` is safe for concurrent use.
var (
	localConfigRe  = regexp.MustCompile(`@@([^@]+)@@`)
	configVarRe    = regexp.MustCompile(`%%(VAR|LOCAL|SERVICE):([^%]+)%%`)
	plainVarRe     = regexp.MustCompile(`%%([^%:]+)%%`)
	commentRe      = regexp.MustCompile(`comment ([^:]+):(\S+)`)
	uncommentRe    = regexp.MustCompile(`uncomment ([^:]+):(\S+)`)
	binaryRe       = regexp.MustCompile(`binary ([^:]+):(\S+)`)
	truefalseRe    = regexp.MustCompile(`truefalse ([^:]+):(\S+)`)
	rangeRe        = regexp.MustCompile(`range ([^:]+):(\S+)\s+(\S+)\s+(\S+)`)
	freqRe         = regexp.MustCompile(`freq ([^:]+):(\S+)\s+(\S+)`)
	freqNonDigitRe = regexp.MustCompile(`\D`)
	freqDigitRe    = regexp.MustCompile(`\d`)
	explodeRe      = regexp.MustCompile(`explode (.*) ([^:]+):(\w+)`)
	ldapURLRe      = regexp.MustCompile(`ldap.?://(\S*):\d+`)
	inlineDirectRe = regexp.MustCompile(`%%([^%]+)%%`)
)

// NewTransformer creates a new Transformer instance.
func NewTransformer(cl lookup.ConfigLookup) *Transformer {
	return &Transformer{
		ConfigLookup: cl,
	}
}

// Transform applies variable substitutions and conditional processing to a line.
//
// It returns an error wrapping ErrTransform where legacy transform() raises
// (see handlers). The first failing directive stops the line, as a raise aborts
// the surrounding re.sub; the caller must discard the whole file being rendered.
func (t *Transformer) Transform(ctx context.Context, line string) (string, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "transformer")
	// jylibs/state.py transform(): a line with fewer than two '@' and fewer than
	// two '%' cannot hold a @@key@@ or %%key%% marker and is returned untouched,
	// including its trailing whitespace and the missing newline of a last line.
	if strings.Count(line, "@") < 2 && strings.Count(line, "%") < 2 {
		return line, nil
	}

	// Apply @@localconfig_key@@ substitutions
	line, err := replaceAll(localConfigRe, line, func(match string) (string, error) {
		return t.xformLocalConfig(ctx, match)
	})
	if err != nil {
		return "", err
	}

	// Trim trailing whitespace before processing %% directives (Python's rstrip()).
	line = strings.TrimRight(line, pyWhitespace)

	// Handle PREFIX directives like %%comment VAR:key%%rest_of_line
	// These directives appear at the start of a line and affect the rest of the line
	line, err = t.processPrefixDirective(ctx, line)
	if err != nil {
		return "", err
	}

	// Handle WRAPPING directives - lines that start and end with %%
	// Only for wrapping directives (contains, exact, list, binary, truefalse, etc.)
	result, handled, err := t.processWrappingDirective(ctx, line)
	if handled || err != nil {
		return result, err // Return immediately for wrapping directives
	}

	// Process inline directives within the line
	// Pattern: "text = %%directive args%%" -> "text = result"
	line, err = t.processInlineDirectives(ctx, line)
	if err != nil {
		return "", err
	}

	// Apply %%VAR:key%%, %%LOCAL:key%%, %%SERVICE:key%% substitutions
	// Must happen before xformConfig to handle these patterns correctly
	src := line
	line = configVarRe.ReplaceAllStringFunc(line, func(match string) string {
		return keepOnOneLine(src, match, t.xformConfigVariable(ctx, match))
	})

	// Apply %%config_variable%% substitutions (for complex directives)
	src = line

	line, err = replaceAll(plainVarRe, line, func(match string) (string, error) {
		value, err := t.xformConfig(ctx, match)

		return keepOnOneLine(src, match, value), err
	})
	if err != nil {
		return "", err
	}

	return line + "\n", nil // Add newline back as it was removed for processing
}

// replaceAll is ReplaceAllStringFunc for callbacks that can fail. The first
// error stops evaluation of the remaining matches, like a Jython raise inside
// re.sub; the returned text is then meaningless.
func replaceAll(re *regexp.Regexp, src string, fn func(match string) (string, error)) (string, error) {
	var firstErr error

	out := re.ReplaceAllStringFunc(src, func(match string) string {
		if firstErr != nil {
			return match
		}

		replaced, err := fn(match)
		if err != nil {
			firstErr = err

			return match
		}

		return replaced
	})

	return out, firstErr
}

// keepOnOneLine collapses the newlines that join a multi-valued LDAP attribute
// (see ldap.Client: multi-valued attributes are newline-joined) into spaces when
// the substituted directive is only part of a line. Emitting the newlines there
// would split the directive across two lines and corrupt the generated file —
// e.g. "mech_list: %%zimbraMtaSaslSmtpdMechList%%" in conf/sasl2/smtpd.conf.in
// became "mech_list: LOGIN\nPLAIN", which made smtpd fail SASL initialization.
// The legacy Jython implementation could not hit this: jylibs/serverconfig.py
// built its config with a dict comprehension over the zmprov output, so repeated
// attributes collapsed to a single value.
//
// A directive occupying the whole line keeps its newlines, because some keys are
// deliberately newline-joined to render multi-line blocks — zimbraMtaMyNetworksPerLine
// and the *XML variants built in configmgr.
func keepOnOneLine(line, match, value string) string {
	if strings.TrimSpace(line) == match {
		return value
	}

	return strings.ReplaceAll(value, "\n", " ")
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
var knownDirectives = []string{"binary", "truefalse", "range", "freq", "list", "contains", "exact", "explode"}

// isKnownDirective checks if the directive name is a known directive type.
func isKnownDirective(directiveName string) bool {
	return slices.Contains(knownDirectives, directiveName)
}

// processPrefixDirective handles prefix directives like %%comment VAR:key%%rest_of_line.
// A line that does not start with one is returned unchanged.
func (t *Transformer) processPrefixDirective(ctx context.Context, line string) (string, error) {
	// Strip leading whitespace — %%comment/%%uncomment directives in config
	// templates (e.g. amavisd.conf.in) are often indented. Preserve the
	// original indentation so the output aligns with the rest of the file.
	trimmed := strings.TrimLeft(line, " \t")
	indent := line[:len(line)-len(trimmed)]

	if !strings.HasPrefix(trimmed, "%%") {
		return line, nil
	}

	// Find the closing %% of the directive
	endIdx := strings.Index(trimmed[2:], "%%")
	if endIdx == -1 {
		return line, nil
	}

	endIdx += 2 // Adjust for the offset
	directiveContent := trimmed[2:endIdx]
	restOfLine := trimmed[endIdx+2:]

	// Check if this is a known prefix directive
	if !isPrefixDirective(directiveContent) {
		return line, nil
	}

	// Process the directive and prepend its result to the rest of the line
	directiveResult, err := t.xformConfig(ctx, "%%"+directiveContent+"%%")
	if err != nil {
		return "", err
	}

	// Remove the newline that xformConfig adds since we're prepending to a line
	directiveResult = strings.TrimSuffix(directiveResult, "\n")

	return indent + directiveResult + restOfLine, nil
}

// processWrappingDirective handles wrapping directives like %%list VAR:key separator%%.
// handled reports whether line was such a directive (the result is final then).
func (t *Transformer) processWrappingDirective(
	ctx context.Context,
	line string,
) (result string, handled bool, err error) {
	if !strings.HasPrefix(line, "%%") || !strings.HasSuffix(line, "%%") || strings.Count(line, "%") < 4 {
		return line, false, nil
	}

	// Check if this is a wrapping directive vs other patterns
	innerContent := strings.TrimPrefix(strings.TrimSuffix(line, "%%"), "%%")
	if !isWrappingDirective(innerContent) {
		return line, false, nil
	}

	// For %%contains...%%/%%exact...%% lines, pre-substitute any embedded
	// variables before evaluating the directive, mirroring jylibs/state.py
	// transform() which strips the outer %% and runs xformConfigVariable over
	// the inner content of a %%contains...%% line prior to the contains
	// evaluation. exact shares the same resolveSearchReplaceDirective grammar
	// as contains, so it can equally embed variables. Legacy matched the bare
	// %%key%% form (VAR then LOCAL fallback); the typed %%VAR:key%% form is a
	// Go extension. Other wrapping directives are passed through unchanged
	// (their handlers parse VAR:key themselves).
	if strings.HasPrefix(innerContent, "contains ") || strings.HasPrefix(innerContent, "exact ") {
		substituted := configVarRe.ReplaceAllStringFunc(innerContent, func(match string) string {
			// Always embedded in surrounding directive text, so never allowed to
			// introduce a newline. See keepOnOneLine.
			return strings.ReplaceAll(t.xformConfigVariable(ctx, match), "\n", " ")
		})

		substituted, err = replaceAll(plainVarRe, substituted, func(match string) (string, error) {
			return t.xformConfig(ctx, match)
		})
		if err != nil {
			return "", true, err
		}

		line = "%%" + substituted + "%%"
	}

	// This is a directive line - process it directly without VAR substitution
	// The directive handlers will parse VAR:key patterns themselves
	value, err := t.xformConfig(ctx, line)
	if err != nil {
		return "", true, err
	}

	// xformConfig returns the value without newline
	return value + "\n", true, nil
}

// processInlineDirectives handles inline directives within a line like "text = %%directive%%".
func (t *Transformer) processInlineDirectives(ctx context.Context, line string) (string, error) {
	if !strings.Contains(line, "%%") || strings.Count(line, "%") < 4 {
		return line, nil
	}

	return replaceAll(inlineDirectRe, line, func(match string) (string, error) {
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
		return match, nil
	})
}

// xformLocalConfig handles @@localconfig_key@@ substitutions.
//
// Legacy xformLocalConfig raises (AttributeError/IndexError, aborting the
// rewrite) when SPLIT or PERDITION_LDAP_SPLIT is applied to a key missing from
// localconfig (None) and PERDITION_LDAP_SPLIT also on an empty value
// (val.split()[0]). A plain @@key@@ of a missing key renders empty.
func (t *Transformer) xformLocalConfig(ctx context.Context, match string) (string, error) {
	// jylibs/state.py xformLocalConfig: "FUNC key" is split on the first space
	// only; without a space the whole text is the key.
	lookupKey := strings.Trim(match, "@")
	funcName := ""

	if fn, rest, found := strings.Cut(lookupKey, " "); found {
		funcName, lookupKey = fn, rest
	}

	val, err := t.ConfigLookup.LookUpConfig(ctx, "LOCAL", lookupKey)
	if err != nil {
		if funcName == "SPLIT" || funcName == "PERDITION_LDAP_SPLIT" {
			return "", fmt.Errorf("%w: @@%s %s@@: key is not set in localconfig", ErrTransform, funcName, lookupKey)
		}

		logger.WarnContext(ctx, "Could not look up local config key",
			"key", lookupKey,
			"error", err)

		return "", nil // Return empty string if not found
	}

	switch funcName {
	case "SPLIT":
		// Jython's SPLIT is val.split(' ', 1)[0]: the substring before the FIRST
		// space only. Tabs (or any other whitespace) are preserved, unlike
		// strings.Fields which splits on all whitespace.
		before, _, _ := strings.Cut(val, " ")

		return before, nil
	case "PERDITION_LDAP_SPLIT":
		// Legacy: ' '.join((val.split()[0], ' '.join(re.findall(ldapURLRe, val)))):
		// the first word, a space, then the host of every ldap URL, with the space
		// kept when no URL matches.
		first := strings.Fields(val)
		if len(first) == 0 {
			return "", fmt.Errorf("%w: @@PERDITION_LDAP_SPLIT %s@@: empty value", ErrTransform, lookupKey)
		}

		hostnames := make([]string, 0, 4)

		for _, m := range ldapURLRe.FindAllStringSubmatch(val, -1) {
			hostnames = append(hostnames, m[1])
		}

		return first[0] + " " + strings.Join(hostnames, " "), nil
	default:
		return val, nil
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
// It dispatches to specialized handler methods based on the directive prefix,
// matching legacy re.match(r"<name>", sr) (a prefix, not a word match).
func (t *Transformer) xformConfig(ctx context.Context, match string) (string, error) {
	sr := strings.Trim(match, "%")

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
		return t.handleDefaultLookup(ctx, sr), nil
	}
}
