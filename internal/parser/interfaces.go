// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package parser provides interfaces and types for parsing zmconfigd.cf configuration files.
// It defines the Parser and Lexer interfaces along with token types for lexical analysis
// of configuration directives, sections, and conditionals.
package parser

import (
	"context"

	"github.com/zextras/carbonio-configd/internal/config"
)

// Parser interface defines methods for parsing zmconfigd.cf files.
type Parser interface {
	// Parse reads and parses a zmconfigd.cf file
	Parse(ctx context.Context, filepath string) (*config.MtaConfig, error)

	// ParseString parses zmconfigd.cf content from a string
	ParseString(ctx context.Context, content string) (*config.MtaConfig, error)
}

// Lexer interface defines methods for tokenizing input.
type Lexer interface {
	// NextToken returns the next token from the input by value, avoiding a heap allocation.
	NextToken() (Token, error)

	// Peek returns the next token without consuming it by value.
	Peek() (Token, error)

	// HasMore returns true if there are more tokens
	HasMore() bool
}

// TokenType represents the type of token.
type TokenType int

const (
	// TokenEOF represents end of file.
	TokenEOF TokenType = iota
	// TokenError represents a lexical error.
	TokenError
	// TokenSection represents a SECTION directive.
	TokenSection
	// TokenRewrite represents a REWRITE directive.
	TokenRewrite
	// TokenVar represents a VAR directive.
	TokenVar
	// TokenLocal represents a LOCAL directive.
	TokenLocal
	// TokenService represents a SERVICE directive.
	TokenService
	// TokenPostconf represents a POSTCONF directive.
	TokenPostconf
	// TokenPostconfd represents a POSTCONFD directive.
	TokenPostconfd
	// TokenRestart represents a RESTART directive.
	TokenRestart
	// TokenDepends represents a DEPENDS directive.
	TokenDepends
	// TokenMapfile represents a MAPFILE directive.
	TokenMapfile
	// TokenMaplocal represents a MAPLOCAL directive.
	TokenMaplocal
	// TokenMode represents a MODE directive.
	TokenMode
	// TokenFile represents a FILE directive.
	TokenFile
	// TokenIf represents an IF directive.
	TokenIf
	// TokenFi represents an FI directive.
	TokenFi
	// TokenLdap represents an LDAP directive.
	TokenLdap
	// TokenProxygen represents a PROXYGEN directive.
	TokenProxygen
	// TokenNot represents a NOT operator.
	TokenNot
	// TokenIdentifier represents an identifier token.
	TokenIdentifier
	// TokenString represents a string literal.
	TokenString
	// TokenNewline represents a newline character.
	TokenNewline
	// TokenComment represents a comment.
	TokenComment
)

// Token represents a lexical token.
type Token struct {
	Type    TokenType
	Literal string
	Line    int
	Column  int
}

// tokenNames maps each TokenType iota to its string representation.
// Array lookup is O(1) with no branches.
var tokenNames = [TokenComment + 1]string{
	TokenEOF:        "EOF",
	TokenError:      "ERROR",
	TokenSection:    "SECTION",
	TokenRewrite:    "REWRITE",
	TokenVar:        "VAR",
	TokenLocal:      "LOCAL",
	TokenService:    "SERVICE",
	TokenPostconf:   "POSTCONF",
	TokenPostconfd:  "POSTCONFD",
	TokenRestart:    "RESTART",
	TokenDepends:    "DEPENDS",
	TokenMapfile:    "MAPFILE",
	TokenMaplocal:   "MAPLOCAL",
	TokenMode:       "MODE",
	TokenFile:       "FILE",
	TokenIf:         "IF",
	TokenFi:         "FI",
	TokenLdap:       "LDAP",
	TokenProxygen:   "PROXYGEN",
	TokenNot:        "NOT",
	TokenIdentifier: "IDENTIFIER",
	TokenString:     "STRING",
	TokenNewline:    "NEWLINE",
	TokenComment:    "COMMENT",
}

// String returns a string representation of the token type.
func (t TokenType) String() string {
	if t >= 0 && int(t) < len(tokenNames) {
		return tokenNames[t]
	}

	return "UNKNOWN"
}
