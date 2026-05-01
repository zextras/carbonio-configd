// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package parser

import (
	"fmt"
	"strings"

	"github.com/zextras/carbonio-configd/internal/config"
)

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
		p.current = Token{Type: TokenError, Literal: err.Error()}

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
