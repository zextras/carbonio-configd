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
	ConfigTypeSECTION   = "SECTION"
	ConfigTypeREWRITE   = "REWRITE"
	ConfigTypeVAR       = "VAR"
	ConfigTypeLOCAL     = "LOCAL"
	ConfigTypeSERVICE   = "SERVICE"
	ConfigTypePOSTCONF  = "POSTCONF"
	ConfigTypePOSTCONFD = "POSTCONFD"
	ConfigTypeRESTART   = "RESTART"
	ConfigTypeDEPENDS   = "DEPENDS"
	ConfigTypeMAPFILE   = "MAPFILE"
	ConfigTypeMAPLOCAL  = "MAPLOCAL"
	ConfigTypeMODE      = "MODE"
	ConfigTypeFILE      = "FILE"
	ConfigTypePROXYGEN  = "PROXYGEN"
	errExpectedVarName  = "expected variable name at line %d"
)

// parser implements the Parser interface for parsing zmconfigd.cf files.
type parser struct {
	lexer   Lexer
	current Token
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
	// Mirror jylibs/mtaconfig.py load(): an empty config file is an error rather
	// than a silently accepted zero-section config.
	if content == "" {
		return nil, fmt.Errorf("empty config file")
	}

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

// parseRequiredVarDirective parses a directive that declares a single
// required variable name (VAR, LOCAL, MAPFILE, MAPLOCAL), recording it as
// section.RequiredVars[name] = configType. errMsg is the "expected ... at
// line %d" format string reported when the directive name is missing.
func (p *parser) parseRequiredVarDirective(section *config.MtaConfigSection, configType, errMsg string) error {
	p.advance() // skip directive keyword

	if p.current.Type != TokenIdentifier {
		return fmt.Errorf(errMsg, p.current.Line)
	}

	varName := p.current.Literal
	section.RequiredVars[varName] = configType

	p.advance()

	p.skipToNewline()

	return nil
}

// parseVar parses a VAR directive.
func (p *parser) parseVar(section *config.MtaConfigSection) error {
	return p.parseRequiredVarDirective(section, ConfigTypeVAR, errExpectedVarName)
}

// parseLocal parses a LOCAL directive.
func (p *parser) parseLocal(section *config.MtaConfigSection) error {
	return p.parseRequiredVarDirective(section, ConfigTypeLOCAL, "expected local variable name at line %d")
}

// parseMapfile parses a MAPFILE directive.
func (p *parser) parseMapfile(section *config.MtaConfigSection) error {
	return p.parseRequiredVarDirective(section, ConfigTypeMAPFILE, errExpectedVarName)
}

// parseMaplocal parses a MAPLOCAL directive.
func (p *parser) parseMaplocal(section *config.MtaConfigSection) error {
	return p.parseRequiredVarDirective(section, ConfigTypeMAPLOCAL, errExpectedVarName)
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

// parseLdapDirective consumes an LDAP directive (`LDAP <key> <value>`) and
// returns the key and value when the value is a LOCAL/MAPLOCAL lookup. Mirroring
// jylibs/mtaconfig.py parseLdap, a directive of any other type (VAR, FILE,
// literal) is consumed but reported as not recordable (ok=false). Shared by the
// section and conditional LDAP parsers.
func (p *parser) parseLdapDirective() (key, value string, ok bool, err error) {
	p.advance() // skip LDAP

	// Get key
	if p.current.Type != TokenIdentifier {
		return "", "", false, fmt.Errorf("expected LDAP key at line %d", p.current.Line)
	}

	key = p.current.Literal
	p.advance()

	// Get value
	value = p.parseValue()
	p.skipToNewline()

	return key, value, isLdapLocalValue(value), nil
}

// isLdapLocalValue reports whether an LDAP directive value is a LOCAL or
// MAPLOCAL lookup — the only types jylibs/mtaconfig.py records for LDAP writes.
func isLdapLocalValue(value string) bool {
	return strings.HasPrefix(value, ConfigTypeLOCAL+":") ||
		strings.HasPrefix(value, ConfigTypeMAPLOCAL+":")
}

// parseLdap parses an LDAP directive within a section body.
func (p *parser) parseLdap(section *config.MtaConfigSection) error {
	key, value, ok, err := p.parseLdapDirective()
	if err != nil {
		return err
	}

	if ok {
		section.Ldap[key] = value
	}

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
