// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package parser

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/zextras/carbonio-configd/internal/config"
)

// Config type constants
const (
	ConfigTypeVAR      = "VAR"
	ConfigTypeLOCAL    = "LOCAL"
	ConfigTypeMAPFILE  = "MAPFILE"
	ConfigTypeMAPLOCAL = "MAPLOCAL"
	ConfigTypeREWRITE  = "REWRITE"
	errExpectedVarName = "expected variable name at line %d"
)

// parser implements the Parser interface for parsing zmconfigd.cf files.
type parser struct {
	lexer   Lexer
	current *Token
	errors  []error
}

// NewParser creates a new parser instance.
func NewParser(lexer Lexer) Parser {
	p := &parser{
		lexer:  lexer,
		errors: []error{},
	}
	p.advance() // Initialize first token

	return p
}

// Parse reads and parses a zmconfigd.cf file.
func (p *parser) Parse(ctx context.Context, filepath string) (*config.MtaConfig, error) {
	//nolint:gosec // G304: File path comes from trusted configuration
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filepath, err)
	}

	return p.ParseString(ctx, string(data))
}

// ParseString parses zmconfigd.cf content from a string.
func (p *parser) ParseString(ctx context.Context, content string) (*config.MtaConfig, error) {
	// Create a new lexer from content
	lexer := NewLexer(ctx, strings.NewReader(content))
	p.lexer = lexer
	p.advance() // Initialize first token

	mtaConfig := &config.MtaConfig{
		Sections: make(map[string]*config.MtaConfigSection),
	}

	for !p.isAtEnd() {
		if err := p.parseSection(mtaConfig); err != nil {
			p.errors = append(p.errors, err)
			// Try to recover by finding next SECTION
			p.skipToNextSection()
		}
	}

	if len(p.errors) > 0 {
		return mtaConfig, fmt.Errorf("parse errors: %v", p.errors)
	}

	return mtaConfig, nil
}

// parseSection parses a single SECTION block.
func (p *parser) parseSection(mtaConfig *config.MtaConfig) error {
	// Skip newlines
	for p.current.Type == TokenNewline {
		p.advance()
	}

	if p.isAtEnd() {
		return nil
	}

	// Expect SECTION keyword
	if p.current.Type != TokenSection {
		return fmt.Errorf("expected SECTION at line %d, got %s", p.current.Line, p.current.Type)
	}

	p.advance()

	// Get section name
	if p.current.Type != TokenIdentifier {
		return fmt.Errorf("expected section name at line %d", p.current.Line)
	}

	sectionName := p.current.Literal
	p.advance()

	// Create new section
	section := &config.MtaConfigSection{
		Name:         sectionName,
		Changed:      false,
		Depends:      make(map[string]bool),
		Rewrites:     make(map[string]config.RewriteEntry),
		Restarts:     make(map[string]bool),
		RequiredVars: make(map[string]string),
		Postconf:     make(map[string]string),
		Postconfd:    make(map[string]string),
		Ldap:         make(map[string]string),
	}

	// Check for DEPENDS clause
	if p.current.Type == TokenDepends {
		p.advance()
		// Get dependency names
		for p.current.Type == TokenIdentifier {
			section.Depends[p.current.Literal] = true
			p.advance()
		}
	}

	// Expect newline after section header
	if p.current.Type != TokenNewline {
		return fmt.Errorf("expected newline after section header at line %d", p.current.Line)
	}

	p.advance()

	// Parse section body (indented directives)
	for !p.isAtEnd() && !p.isNextSection() {
		if p.current.Type == TokenNewline {
			p.advance()
			continue
		}

		// Parse directive
		if err := p.parseDirective(section); err != nil {
			return err
		}
	}

	// Add section to config
	mtaConfig.Sections[sectionName] = section

	return nil
}

// parseDirective parses a single directive within a section.
func (p *parser) parseDirective(section *config.MtaConfigSection) error {
	switch p.current.Type {
	case TokenRewrite:
		return p.parseRewrite(section)
	case TokenVar:
		return p.parseVar(section)
	case TokenLocal:
		return p.parseLocal(section)
	case TokenMapfile:
		return p.parseMapfile(section)
	case TokenMaplocal:
		return p.parseMaplocal(section)
	case TokenPostconf:
		return p.parsePostconf(section)
	case TokenPostconfd:
		return p.parsePostconfd(section)
	case TokenRestart:
		return p.parseRestart(section)
	case TokenProxygen:
		return p.parseProxygen(section)
	case TokenIf:
		return p.parseConditional(section)
	case TokenIdentifier:
		// Handle special identifiers like LDAP
		if strings.EqualFold(p.current.Literal, "LDAP") {
			return p.parseLdap(section)
		}

		return fmt.Errorf("unknown directive %s at line %d", p.current.Literal, p.current.Line)
	default:
		return fmt.Errorf("unexpected token %s at line %d", p.current.Type, p.current.Line)
	}
}

// parseRewrite parses a REWRITE directive.
func (p *parser) parseRewrite(section *config.MtaConfigSection) error {
	p.advance() // skip REWRITE

	// Get source file (may be split across multiple tokens like "conf" + "/test.in")
	source := p.readPath()
	if source == "" {
		return fmt.Errorf("expected source file at line %d", p.current.Line)
	}

	// Get destination file
	dest := p.readPath()
	if dest == "" {
		return fmt.Errorf("expected destination file at line %d", p.current.Line)
	}

	// Optional MODE
	mode := ""

	if p.current.Type == TokenMode {
		p.advance()

		if p.current.Type != TokenString && p.current.Type != TokenIdentifier {
			return fmt.Errorf("expected mode value at line %d", p.current.Line)
		}

		mode = p.current.Literal
		p.advance()
	}

	section.Rewrites[source] = config.RewriteEntry{
		Value: dest,
		Mode:  mode,
	}

	p.skipToNewline()

	return nil
}

// parseVar parses a VAR directive.
func (p *parser) parseVar(section *config.MtaConfigSection) error {
	p.advance() // skip VAR

	if p.current.Type != TokenIdentifier {
		return fmt.Errorf(errExpectedVarName, p.current.Line)
	}

	varName := p.current.Literal
	section.RequiredVars[varName] = ConfigTypeVAR

	p.advance()

	p.skipToNewline()

	return nil
}

// parseLocal parses a LOCAL directive.
func (p *parser) parseLocal(section *config.MtaConfigSection) error {
	p.advance() // skip LOCAL

	if p.current.Type != TokenIdentifier {
		return fmt.Errorf("expected local variable name at line %d", p.current.Line)
	}

	varName := p.current.Literal
	section.RequiredVars[varName] = ConfigTypeLOCAL

	p.advance()

	p.skipToNewline()

	return nil
}

// parseMapfile parses a MAPFILE directive.
func (p *parser) parseMapfile(section *config.MtaConfigSection) error {
	p.advance() // skip MAPFILE

	if p.current.Type != TokenIdentifier {
		return fmt.Errorf(errExpectedVarName, p.current.Line)
	}

	varName := p.current.Literal
	section.RequiredVars[varName] = ConfigTypeMAPFILE

	p.advance()

	p.skipToNewline()

	return nil
}

// parseMaplocal parses a MAPLOCAL directive.
func (p *parser) parseMaplocal(section *config.MtaConfigSection) error {
	p.advance() // skip MAPLOCAL

	if p.current.Type != TokenIdentifier {
		return fmt.Errorf(errExpectedVarName, p.current.Line)
	}

	varName := p.current.Literal
	section.RequiredVars[varName] = ConfigTypeMAPLOCAL

	p.advance()

	p.skipToNewline()

	return nil
}

// parsePostconf parses a POSTCONF directive.
func (p *parser) parsePostconf(section *config.MtaConfigSection) error {
	p.advance() // skip POSTCONF

	if p.current.Type != TokenIdentifier {
		return fmt.Errorf("expected postfix parameter name at line %d", p.current.Line)
	}

	key := p.current.Literal
	p.advance()

	// Parse value (can be VAR, LOCAL, FILE, or literal)
	value := ""
	if !p.isAtNewline() && !p.isAtEnd() {
		value = p.parseValue()
	}

	section.Postconf[key] = value

	p.skipToNewline()

	return nil
}

// parsePostconfd parses a POSTCONFD directive (delete postfix parameter).
func (p *parser) parsePostconfd(section *config.MtaConfigSection) error {
	p.advance() // skip POSTCONFD

	if p.current.Type != TokenIdentifier {
		return fmt.Errorf("expected postfix parameter name at line %d", p.current.Line)
	}

	key := p.current.Literal
	section.Postconfd[key] = ""

	p.advance()

	p.skipToNewline()

	return nil
}

// parseRestart parses a RESTART directive.
func (p *parser) parseRestart(section *config.MtaConfigSection) error {
	p.advance() // skip RESTART

	// Parse service names
	for p.current.Type == TokenIdentifier {
		section.Restarts[p.current.Literal] = true
		p.advance()
	}

	p.skipToNewline()

	return nil
}

// parseLdap parses an LDAP directive.
func (p *parser) parseLdap(section *config.MtaConfigSection) error {
	p.advance() // skip LDAP

	// Get key
	if p.current.Type != TokenIdentifier {
		return fmt.Errorf("expected LDAP key at line %d", p.current.Line)
	}

	key := p.current.Literal
	p.advance()

	// Get value
	value := p.parseValue()
	section.Ldap[key] = value

	p.skipToNewline()

	return nil
}

// parseProxygen parses a PROXYGEN directive.
func (p *parser) parseProxygen(section *config.MtaConfigSection) error {
	p.advance() // skip PROXYGEN

	// Mark section as requiring proxy generation
	section.Proxygen = true

	p.skipToNewline()

	return nil
}

// parseValue parses a value which can be VAR, LOCAL, FILE, MAPLOCAL, or literal.
func (p *parser) parseValue() string {
	var parts []string

	for !p.isAtNewline() && !p.isAtEnd() {
		switch p.current.Type {
		case TokenVar:
			p.advance()

			if p.current.Type == TokenIdentifier {
				parts = append(parts, "VAR:"+p.current.Literal)
				p.advance()
			}
		case TokenLocal:
			p.advance()

			if p.current.Type == TokenIdentifier {
				parts = append(parts, "LOCAL:"+p.current.Literal)
				p.advance()
			}
		case TokenFile:
			p.advance()
			path := p.readPath()
			parts = append(parts, "FILE "+path)
		case TokenMaplocal:
			p.advance()

			if p.current.Type == TokenIdentifier {
				parts = append(parts, "MAPLOCAL:"+p.current.Literal)
				p.advance()
			}
		case TokenIdentifier, TokenString:
			parts = append(parts, p.current.Literal)
			p.advance()
		default:
			return strings.Join(parts, " ")
		}
	}

	return strings.Join(parts, " ")
}

// parseConditional parses a top-level if/fi block and appends it to the section.
func (p *parser) parseConditional(section *config.MtaConfigSection) error {
	cond, err := p.parseConditionalBlock()
	if err != nil {
		return err
	}

	section.Conditionals = append(section.Conditionals, cond)

	return nil
}

// parseNestedConditional parses a nested if/fi block and appends it to the parent conditional.
func (p *parser) parseNestedConditional(parent *config.Conditional) error {
	cond, err := p.parseConditionalBlock()
	if err != nil {
		return err
	}

	parent.Nested = append(parent.Nested, cond)

	return nil
}

// parseConditionalBlock parses a complete IF ... FI block (header + body) and
// returns the populated Conditional. It is the single source of truth for
// conditional parsing and is used by both top-level and nested callers.
func (p *parser) parseConditionalBlock() (config.Conditional, error) {
	condType, condKey, negated, err := p.parseConditionalHeader()
	if err != nil {
		return config.Conditional{}, err
	}

	cond := config.Conditional{
		Type:      condType,
		Key:       condKey,
		Negated:   negated,
		Postconf:  make(map[string]string),
		Postconfd: make(map[string]string),
		Ldap:      make(map[string]string),
		Nested:    []config.Conditional{},
	}

	p.skipToNewline()

	if err := p.parseConditionalBody(&cond); err != nil {
		return cond, err
	}

	return cond, nil
}

// parseConditionalBody advances through tokens until the matching FI token,
// dispatching each token type to the appropriate handler.
func (p *parser) parseConditionalBody(cond *config.Conditional) error {
	depth := 1

	for !p.isAtEnd() && depth > 0 {
		done, err := p.parseConditionalToken(cond, &depth)
		if err != nil {
			return err
		}

		if done {
			break
		}
	}

	return nil
}

// parseConditionalToken processes the current token inside a conditional body.
// It returns (true, nil) when the matching FI has been consumed, (false, nil) to
// continue, or (false, err) on a parse error.
func (p *parser) parseConditionalToken(cond *config.Conditional, depth *int) (done bool, err error) {
	switch p.current.Type {
	case TokenIf:
		return false, p.parseNestedConditional(cond)

	case TokenFi:
		*depth--
		if *depth == 0 {
			p.advance()
			return true, nil
		}

		p.advance()

	case TokenPostconf:
		return false, p.parseConditionalPostconf(cond)

	case TokenPostconfd:
		return false, p.parseConditionalPostconfd(cond)

	case TokenIdentifier:
		if strings.EqualFold(p.current.Literal, "LDAP") {
			return false, p.parseConditionalLdap(cond)
		}

		p.advance()

	default:
		p.advance()
	}

	return false, nil
}

// parseConditionalHeader parses the "IF SERVICE|VAR [!]key" header and
// returns (type, key, negated, error).
func (p *parser) parseConditionalHeader() (condType, condKey string, negated bool, err error) {
	p.advance() // skip IF

	// Parse condition type (SERVICE or VAR)
	if p.current.Type != TokenService && p.current.Type != TokenVar {
		return "", "", false,
			fmt.Errorf("expected SERVICE or VAR after IF at line %d, got %v", p.current.Line, p.current.Type)
	}

	condType = strings.ToUpper(p.current.Literal)
	p.advance()

	// Parse condition key (may have ! prefix for negation)
	if p.current.Type != TokenIdentifier {
		return "", "", false, fmt.Errorf("expected condition key at line %d", p.current.Line)
	}

	condKey = p.current.Literal

	if strings.HasPrefix(condKey, "!") {
		negated = true
		condKey = condKey[1:]
	}

	p.advance()

	return condType, condKey, negated, nil
}

// parseConditionalPostconf parses a POSTCONF directive within a conditional.
func (p *parser) parseConditionalPostconf(cond *config.Conditional) error {
	p.advance() // skip POSTCONF

	// Get key
	if p.current.Type != TokenIdentifier {
		return fmt.Errorf("expected POSTCONF key at line %d", p.current.Line)
	}

	key := p.current.Literal
	p.advance()

	// Get value
	value := p.parseValue()
	cond.Postconf[key] = value

	p.skipToNewline()

	return nil
}

// parseConditionalPostconfd parses a POSTCONFD directive within a conditional.
func (p *parser) parseConditionalPostconfd(cond *config.Conditional) error {
	p.advance() // skip POSTCONFD

	// Get key
	if p.current.Type != TokenIdentifier {
		return fmt.Errorf("expected POSTCONFD key at line %d", p.current.Line)
	}

	key := p.current.Literal
	p.advance()

	// Get value
	value := p.parseValue()
	cond.Postconfd[key] = value

	p.skipToNewline()

	return nil
}

// parseConditionalLdap parses an LDAP directive within a conditional.
func (p *parser) parseConditionalLdap(cond *config.Conditional) error {
	p.advance() // skip LDAP

	// Get key
	if p.current.Type != TokenIdentifier {
		return fmt.Errorf("expected LDAP key at line %d", p.current.Line)
	}

	key := p.current.Literal
	p.advance()

	// Get value
	value := p.parseValue()
	cond.Ldap[key] = value

	p.skipToNewline()

	return nil
}

// Helper methods

func (p *parser) readPath() string {
	// Read a file path which may be composed of an identifier followed by a string starting with /
	// Examples:
	//   "conf" + "/test.in" = "conf/test.in"
	//   "/absolute/path" = "/absolute/path"
	//   "relative.file" = "relative.file"
	if p.isAtNewline() || p.isAtEnd() {
		return ""
	}

	// Case 1: Starts with / (absolute or continuation like /test.in)
	if p.current.Type == TokenString && p.current.Literal != "" && p.current.Literal[0] == '/' {
		path := p.current.Literal
		p.advance()

		return path
	}

	// Case 2: Identifier potentially followed by /something
	if p.current.Type == TokenIdentifier {
		part1 := p.current.Literal
		p.advance()

		// Check if next token is a string starting with /
		if !p.isAtNewline() && !p.isAtEnd() &&
			p.current.Type == TokenString &&
			p.current.Literal != "" &&
			p.current.Literal[0] == '/' {
			part2 := p.current.Literal
			p.advance()

			return part1 + part2
		}

		// Just the identifier (relative path like "data")
		return part1
	}

	// Case 3: Plain string
	if p.current.Type == TokenString {
		path := p.current.Literal
		p.advance()

		return path
	}

	return ""
}

func (p *parser) advance() {
	tok, err := p.lexer.NextToken()
	if err != nil {
		p.errors = append(p.errors, err)
		// Create an error token
		p.current = &Token{Type: TokenError, Literal: err.Error()}

		return
	}

	p.current = tok
}

func (p *parser) isAtEnd() bool {
	return p.current.Type == TokenEOF || p.current.Type == TokenError
}

func (p *parser) isAtNewline() bool {
	return p.current.Type == TokenNewline
}

func (p *parser) isNextSection() bool {
	return p.current.Type == TokenSection
}

func (p *parser) skipToNewline() {
	for !p.isAtNewline() && !p.isAtEnd() {
		p.advance()
	}
}

func (p *parser) skipToNextSection() {
	for !p.isNextSection() && !p.isAtEnd() {
		p.advance()
	}
}
