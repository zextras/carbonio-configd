// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// isTruthy returns true for "TRUE" (case-insensitive) or "1".
func isTruthy(val string) bool {
	return strings.EqualFold(val, "TRUE") || val == "1"
}

// varOpt is a functional option for configuring a Variable during registration.
type varOpt func(*Variable)

func withAttribute(attr string) varOpt        { return func(v *Variable) { v.Attribute = attr } }
func withValueType(vt ValueType) varOpt       { return func(v *Variable) { v.ValueType = vt } }
func withOverrideType(ot OverrideType) varOpt { return func(v *Variable) { v.OverrideType = ot } }
func withDescription(d string) varOpt         { return func(v *Variable) { v.Description = d } }
func withCustomFormatter(fn func(any) (string, error)) varOpt {
	return func(v *Variable) { v.CustomFormatter = fn }
}
func withCustomResolver(fn func(context.Context) (any, error)) varOpt {
	return func(v *Variable) { v.CustomResolver = fn }
}

// registerVar creates a Variable with Keyword and Value pre-populated from the
// provided keyword and defaultValue, applies any opts, then stores it in g.Variables.
func (g *Generator) registerVar(keyword string, defaultValue any, opts ...varOpt) {
	v := &Variable{
		Keyword:      keyword,
		DefaultValue: defaultValue,
		Value:        defaultValue,
	}
	for _, opt := range opts {
		opt(v)
	}

	g.Variables[keyword] = v
}

// RegisterVariables initializes all proxy configuration variables
func (g *Generator) RegisterVariables(_ context.Context) {
	g.Variables = make(map[string]*Variable)
	g.DomainVariables = make(map[string]*Variable)

	// Core variables
	g.registerCoreVariables()

	// SSL variables
	g.registerSSLVariables()

	// Web variables
	g.registerWebVariables()

	// Mail variables
	g.registerMailVariables()

	// Admin variables
	g.registerAdminVariables()

	// Memcache variables
	g.registerMemcacheVariables()

	// IMAP/POP variables
	g.registerIMAPPOPVariables()

	// SSO variables
	g.registerSSOVariables()

	// Lookup variables
	g.registerLookupVariables()

	// Timeout and performance variables
	g.registerTimeoutVariables()

	// Throttling and rate limit variables
	g.registerThrottlingVariables()

	// XMPP/Bosh variables
	g.registerXMPPVariables()
}

// lookupAttr returns the value of attr from data.
// Returns ("", false) when attr is empty; safe on nil maps.
func lookupAttr(data map[string]string, attr string) (string, bool) {
	if attr == "" {
		return "", false
	}

	v, ok := data[attr]

	return v, ok
}

// LookupValue resolves a variable's value from the appropriate configuration source.
func (g *Generator) LookupValue(ctx context.Context, v *Variable) (any, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")

	if v.CustomResolver != nil {
		return v.CustomResolver(ctx)
	}

	// Extract nil-safe data maps once. Indexing a nil map is safe in Go.
	var globalData, serverData, localData map[string]string
	if g.GlobalConfig != nil {
		globalData = g.GlobalConfig.Data
	}

	if g.ServerConfig != nil {
		serverData = g.ServerConfig.Data
	}

	if g.LocalConfig != nil {
		localData = g.LocalConfig.Data
	}

	switch v.OverrideType {
	case OverrideConfig:
		if val, ok := lookupAttr(globalData, v.Attribute); ok {
			logger.DebugContext(ctx, "LookupValue found in GlobalConfig",
				"keyword", v.Keyword,
				"value", val,
				"attribute", v.Attribute)

			return g.convertValue(ctx, val, v.ValueType)
		}

		logger.DebugContext(ctx, "LookupValue not found in GlobalConfig, using default",
			"keyword", v.Keyword,
			"default_value", v.DefaultValue)

	case OverrideServer:
		if val, ok := lookupAttr(serverData, v.Attribute); ok {
			return g.convertValue(ctx, val, v.ValueType)
		}

		if val, ok := lookupAttr(globalData, v.Attribute); ok {
			return g.convertValue(ctx, val, v.ValueType)
		}

	case OverrideLocalConfig:
		if val, ok := lookupAttr(localData, v.Attribute); ok {
			return g.convertValue(ctx, val, v.ValueType)
		}
	}

	return v.DefaultValue, nil
}

// convertValue converts a string value to the appropriate type.
func (g *Generator) convertValue(ctx context.Context, val string, vt ValueType) (any, error) {
	logger.DebugContext(ctx, "Converting value",
		"value", val,
		"value_type", vt)

	switch vt {
	case ValueTypeInteger:
		return convertToInteger(ctx, val)

	case ValueTypeLong:
		i, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to convert %s to long: %w", val, err)
		}

		return i, nil

	case ValueTypeBoolean:
		return strings.EqualFold(val, "TRUE"), nil

	case ValueTypeEnabler:
		return strings.EqualFold(val, "TRUE"), nil

	case ValueTypeString:
		return val, nil

	case ValueTypeTime:
		// Parse time string or plain seconds to milliseconds.
		return parseDurationToMillis(val, true), nil

	case ValueTypeTimeInSec:
		// Parse time string or plain integer to milliseconds (formatted as seconds later).
		return parseDurationToMillis(val, false), nil

	default:
		return val, nil
	}
}

// convertToInteger parses val as an integer, logging on success.
func convertToInteger(ctx context.Context, val string) (any, error) {
	i, err := strconv.Atoi(val)
	if err != nil {
		return nil, fmt.Errorf("failed to convert %s to integer: %w", val, err)
	}

	logger.DebugContext(ctx, "Converted value to integer",
		"value", val,
		"integer", i)

	return i, nil
}

// parseDurationToMillis converts a time string ("10s", "5m") or a plain numeric value to
// milliseconds. When secondsAsUnit is true, a plain number is treated as seconds and
// multiplied by 1000; otherwise it is kept as-is (already in ms). Returns val unchanged
// when parsing fails.
func parseDurationToMillis(val string, secondsAsUnit bool) any {
	if val == "" {
		return val
	}

	if duration, err := time.ParseDuration(val); err == nil {
		return int(duration.Milliseconds())
	}

	if n, err := strconv.ParseInt(val, 10, 64); err == nil {
		if secondsAsUnit {
			return int(n * 1000)
		}

		return int(n)
	}

	return val
}

// ResolveAllVariables resolves all registered variables
func (g *Generator) ResolveAllVariables(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")
	logger.DebugContext(ctx, "Starting variable resolution",
		"variable_count", len(g.Variables))

	count := 0

	var slowVars []string

	slowThreshold := 5 * time.Millisecond

	for _, v := range g.Variables {
		t := time.Now()
		val, err := g.LookupValue(ctx, v)
		duration := time.Since(t)

		// Log slow variable resolutions
		if duration > slowThreshold {
			slowVars = append(slowVars, fmt.Sprintf("%s (%.2fms)", v.Keyword, duration.Seconds()*1000))
		}

		if err != nil {
			logger.ErrorContext(ctx, "Failed to resolve variable",
				"keyword", v.Keyword,
				"error", err)

			return fmt.Errorf("failed to resolve variable %s: %w", v.Keyword, err)
		}

		v.Value = val

		count++
		if count%20 == 0 {
			logger.DebugContext(ctx, "Variable resolution progress",
				"resolved", count,
				"total", len(g.Variables))
		}
	}

	if len(slowVars) > 0 {
		logger.WarnContext(ctx, "Slow variable resolutions detected",
			"slow_count", len(slowVars),
			"slow_variables", slowVars)
	}

	logger.DebugContext(ctx, "Variable resolution completed",
		"variable_count", count)

	return nil
}

// GetVariable retrieves a variable by keyword
func (g *Generator) GetVariable(keyword string) (*Variable, error) {
	v, ok := g.Variables[keyword]
	if !ok {
		return nil, fmt.Errorf("variable %s not found", keyword)
	}

	return v, nil
}
