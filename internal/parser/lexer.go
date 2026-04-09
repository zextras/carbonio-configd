// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package parser

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// lexer implements the Lexer interface for tokenizing zmconfigd.cf files.
type lexer struct {
	ctx     context.Context
	reader  *bufio.Reader
	line    int
	column  int
	current rune
	eof     bool
	peeked  *Token
}

// NewLexer creates a new lexer from an io.Reader.
func NewLexer(ctx context.Context, r io.Reader) Lexer {
	ctx = logger.ContextWithComponent(ctx, "parser")
	l := &lexer{
		ctx:    ctx,
		reader: bufio.NewReader(r),
		line:   1,
		column: 0,
	}
	l.readChar() // Initialize first character

	return l
}

// readChar reads the next character from the input.
func (l *lexer) readChar() {
	if l.eof {
		return
	}

	ch, _, err := l.reader.ReadRune()
	if errors.Is(err, io.EOF) {
		l.eof = true
		l.current = 0

		return
	}

	if err != nil {
		l.eof = true
		l.current = 0

		return
	}

	l.current = ch

	l.column++
	if ch == '\n' {
		l.line++
		l.column = 0
	}
}

// peekChar looks at the next character without consuming it.
func (l *lexer) peekChar() rune {
	ch, _, err := l.reader.ReadRune()
	if err != nil {
		return 0
	}

	if err := l.reader.UnreadRune(); err != nil {
		// This should rarely fail, but we'll log it
		logger.WarnContext(l.ctx, "Failed to unread rune",
			"error", err)
	}

	return ch
}

// skipWhitespace skips spaces and tabs but not newlines.
func (l *lexer) skipWhitespace() {
	for l.current == ' ' || l.current == '\t' {
		l.readChar()
	}
}

// skipComment skips until end of line for comments starting with #.
func (l *lexer) skipComment() {
	for l.current != '\n' && !l.eof {
		l.readChar()
	}
}

// readIdentifier reads an identifier (alphanumeric + underscore + hyphen).
func (l *lexer) readIdentifier() string {
	var sb strings.Builder
	for isIdentifierChar(l.current) {
		sb.WriteRune(l.current)
		l.readChar()
	}

	return sb.String()
}

// readString reads a quoted string or unquoted path/value.
func (l *lexer) readString() string {
	var sb strings.Builder

	// Handle quoted strings
	if l.current == '"' {
		l.readChar() // skip opening quote

		for l.current != '"' && !l.eof && l.current != '\n' {
			sb.WriteRune(l.current)
			l.readChar()
		}

		if l.current == '"' {
			l.readChar() // skip closing quote
		}

		return sb.String()
	}

	// Handle unquoted strings (paths, values, etc.)
	for !unicode.IsSpace(l.current) && !l.eof {
		sb.WriteRune(l.current)
		l.readChar()
	}

	return sb.String()
}

// NextToken returns the next token from the input.
//
//nolint:gocyclo,cyclop // Lexer token recognition requires checking many character patterns
func (l *lexer) NextToken() (*Token, error) {
	// Return peeked token if available
	if l.peeked != nil {
		tok := l.peeked
		l.peeked = nil

		return tok, nil
	}

	l.skipWhitespace()

	// Save position for token
	line := l.line
	col := l.column

	// EOF
	if l.eof {
		return &Token{Type: TokenEOF, Line: line, Column: col}, nil
	}

	// Newline
	if l.current == '\n' {
		l.readChar()
		return &Token{Type: TokenNewline, Literal: "\n", Line: line, Column: col}, nil
	}

	// Comment
	if l.current == '#' {
		l.skipComment()
		return l.NextToken() // Skip comment and get next token
	}

	// Identifier or keyword
	if isIdentifierStart(l.current) || l.current == '!' {
		// Handle negation operator
		negated := false
		if l.current == '!' {
			negated = true

			l.readChar()
		}

		ident := l.readIdentifier()

		// Add negation prefix back
		if negated {
			ident = "!" + ident
		}

		// Check for keywords
		tok := &Token{Line: line, Column: col, Literal: ident}
		switch strings.ToUpper(ident) {
		case "SECTION":
			tok.Type = TokenSection
		case "REWRITE":
			tok.Type = TokenRewrite
		case "VAR":
			tok.Type = TokenVar
		case "LOCAL":
			tok.Type = TokenLocal
		case "SERVICE":
			tok.Type = TokenService
		case "POSTCONF":
			tok.Type = TokenPostconf
		case "POSTCONFD":
			tok.Type = TokenPostconfd
		case "RESTART":
			tok.Type = TokenRestart
		case "DEPENDS":
			tok.Type = TokenDepends
		case "MAPFILE":
			tok.Type = TokenMapfile
		case "MAPLOCAL":
			tok.Type = TokenMaplocal
		case "MODE":
			tok.Type = TokenMode
		case "FILE":
			tok.Type = TokenFile
		case "IF":
			tok.Type = TokenIf
		case "FI":
			tok.Type = TokenFi
		case "LDAP":
			// LDAP is treated as an identifier for "LDAP key value" directives
			tok.Type = TokenIdentifier
		case "PROXYGEN":
			tok.Type = TokenProxygen
		default:
			tok.Type = TokenIdentifier
		}

		return tok, nil
	}

	// Path or string literal (/, ., or digit at start)
	if l.current == '/' || l.current == '"' || unicode.IsDigit(l.current) {
		str := l.readString()
		return &Token{Type: TokenString, Literal: str, Line: line, Column: col}, nil
	}

	// Dot - could be start of relative path like ./file or ../file or just a path component
	if l.current == '.' {
		// Check if it looks like a path (./  ../  or .something/)
		next := l.peekChar()
		if next == '/' || next == '.' {
			str := l.readString()
			return &Token{Type: TokenString, Literal: str, Line: line, Column: col}, nil
		}
		// Otherwise treat as part of filename (will be read as string)
		str := l.readString()

		return &Token{Type: TokenString, Literal: str, Line: line, Column: col}, nil
	}

	// Unknown character
	char := l.current
	l.readChar()

	return &Token{
		Type:    TokenError,
		Literal: fmt.Sprintf("unexpected character: %q", char),
		Line:    line,
		Column:  col,
	}, fmt.Errorf("unexpected character: %q at line %d, column %d", char, line, col)
}

// Peek returns the next token without consuming it.
func (l *lexer) Peek() (*Token, error) {
	if l.peeked == nil {
		tok, err := l.NextToken()
		if err != nil {
			return nil, err
		}

		l.peeked = tok
	}

	return l.peeked, nil
}

// HasMore returns true if there are more tokens.
func (l *lexer) HasMore() bool {
	if l.peeked != nil {
		return l.peeked.Type != TokenEOF
	}

	return !l.eof
}

// Helper functions

func isIdentifierStart(ch rune) bool {
	return unicode.IsLetter(ch) || ch == '_'
}

func isIdentifierChar(ch rune) bool {
	return unicode.IsLetter(ch) || unicode.IsDigit(ch) || ch == '_' || ch == '-' || ch == ','
}
