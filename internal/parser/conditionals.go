// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package parser

import (
	"fmt"
	"strings"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// parseConditional parses a top-level if/fi block and appends it to the section.
func (p *parser) parseConditional(section *config.MtaConfigSection) error {
	condType, condKey, negated, err := p.parseConditionalHeader()
	if err != nil {
		return err
	}

	cond := config.Conditional{
		Type:         condType,
		Key:          condKey,
		Negated:      negated,
		Postconf:     make(map[string]string),
		Postconfd:    make(map[string]string),
		RequiredVars: make(map[string]string),
	}

	p.skipToNewline()

	if err := p.parseConditionalBody(&cond); err != nil {
		return err
	}

	section.Conditionals = append(section.Conditionals, cond)

	return nil
}

// parseConditionalBody mirrors the legacy `while not re.match(r"fi", line)`
// loop of jylibs/mtaconfig.py: only POSTCONF, POSTCONFD (VAR|LOCAL|FILE
// lookups, no MAPLOCAL) and VAR/LOCAL lines are honoured; every other line,
// including a nested if header, is logged and ignored, and the block ends at
// the FIRST fi. Nesting is therefore not supported: the body of an inner if is
// folded into the outer block and the outer fi is left over at section level.
func (p *parser) parseConditionalBody(cond *config.Conditional) error {
	for {
		switch p.current.Type {
		case TokenNewline:
			p.advance()
		case TokenFi:
			p.skipToNewline()

			return nil
		case TokenEOF, TokenError, TokenSection:
			// Legacy runs off the end of the file (IndexError).
			return fmt.Errorf("unterminated if block %s %s: missing fi before line %d", cond.Type, cond.Key, p.current.Line)
		default:
			if err := p.parseConditionalLine(cond); err != nil {
				return err
			}
		}
	}
}

// parseConditionalLine handles one body line of a conditional.
func (p *parser) parseConditionalLine(cond *config.Conditional) error {
	switch p.current.Type {
	case TokenPostconf:
		key, value, lk, err := p.parsePostconfLine(ConfigTypePOSTCONF, false)
		if err != nil {
			return err
		}

		cond.Postconf[key] = value
		lk.register(cond.RequiredVars)
	case TokenPostconfd:
		key, _, lk, err := p.parsePostconfLine(ConfigTypePOSTCONFD, false)
		if err != nil {
			return err
		}

		cond.Postconfd[key] = ""
		lk.register(cond.RequiredVars)
	case TokenVar, TokenLocal:
		return p.parseConditionalRequiredVar(cond)
	default:
		logger.WarnContext(p.ctx, "Ignoring unsupported directive inside if block",
			"directive", p.current.Literal, "line", p.current.Line)
	}

	p.skipToNewline()

	return nil
}

// parseConditionalRequiredVar parses a VAR/LOCAL line inside a conditional.
func (p *parser) parseConditionalRequiredVar(cond *config.Conditional) error {
	cfgType := ConfigTypeVAR
	if p.current.Type == TokenLocal {
		cfgType = ConfigTypeLOCAL
	}

	line := p.current.Line

	p.advance()

	if p.current.Type != TokenIdentifier {
		return fmt.Errorf("expected variable name after %s at line %d", cfgType, line)
	}

	cond.RequiredVars[p.current.Literal] = cfgType

	return nil
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
