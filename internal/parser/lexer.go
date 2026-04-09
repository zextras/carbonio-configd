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
	ctx       context.Context
	reader    *bufio.Reader
	line      int
	column    int
	current   rune
	eof       bool
	peeked    Token // stored peeked token (valid only when hasPeeked is true)
	hasPeeked bool  // true when peeked holds a valid lookahead token
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

// keywords maps upper-case identifiers to their token types.
// Identifiers not in this map are TokenIdentifier.
// LDAP is intentionally absent: it is treated as a plain identifier.
var keywords = map[string]TokenType{
	"SECTION":   TokenSection,
	"REWRITE":   TokenRewrite,
	"VAR":       TokenVar,
	"LOCAL":     TokenLocal,
	"SERVICE":   TokenService,
	"POSTCONF":  TokenPostconf,
	"POSTCONFD": TokenPostconfd,
	"RESTART":   TokenRestart,
	"DEPENDS":   TokenDepends,
	"MAPFILE":   TokenMapfile,
	"MAPLOCAL":  TokenMaplocal,
	"MODE":      TokenMode,
	"FILE":      TokenFile,
	"IF":        TokenIf,
	"FI":        TokenFi,
	"PROXYGEN":  TokenProxygen,
}

// lookupKeyword returns the TokenType for ident (case-insensitive).
func lookupKeyword(ident string) TokenType {
	if tt, ok := keywords[strings.ToUpper(ident)]; ok {
		return tt
	}

	return TokenIdentifier
}

// NextToken returns the next token from the input by value (no heap allocation).
func (l *lexer) NextToken() (Token, error) {
	// Return peeked token if available
	if l.hasPeeked {
		tok := l.peeked
		l.hasPeeked = false

		return tok, nil
	}

	l.skipWhitespace()

	// Save position for token
	line := l.line
	col := l.column

	// EOF
	if l.eof {
		return Token{Type: TokenEOF, Line: line, Column: col}, nil
	}

	// Newline
	if l.current == '\n' {
		l.readChar()
		return Token{Type: TokenNewline, Literal: "\n", Line: line, Column: col}, nil
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

		tok := Token{Line: line, Column: col, Literal: ident, Type: lookupKeyword(ident)}

		return tok, nil
	}

	// Path, string literal, or dot-prefixed path component
	if l.current == '/' || l.current == '"' || unicode.IsDigit(l.current) || l.current == '.' {
		return Token{Type: TokenString, Literal: l.readString(), Line: line, Column: col}, nil
	}

	// Unknown character
	char := l.current
	l.readChar()

	return Token{
		Type:    TokenError,
		Literal: fmt.Sprintf("unexpected character: %q", char),
		Line:    line,
		Column:  col,
	}, fmt.Errorf("unexpected character: %q at line %d, column %d", char, line, col)
}

// Peek returns the next token without consuming it (by value).
func (l *lexer) Peek() (Token, error) {
	if !l.hasPeeked {
		tok, err := l.NextToken()
		if err != nil {
			return Token{}, err
		}

		l.peeked = tok
		l.hasPeeked = true
	}

	return l.peeked, nil
}

// HasMore returns true if there are more tokens.
func (l *lexer) HasMore() bool {
	if l.hasPeeked {
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
