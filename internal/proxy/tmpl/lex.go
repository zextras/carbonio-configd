// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tmpl

import "strings"

// TokenKind is the kind of a Token.
type TokenKind int

// The token kinds of a template line.
const (
	// Text is literal text, whitespace included.
	Text TokenKind = iota
	// Enabler is a ${name} enabler prefix: at the start of the line, or after
	// another prefix, and followed by more text.
	Enabler
	// Var is a ${name} substitution anywhere else.
	Var
)

// Token is a piece of a template line. Text holds the literal text, or the
// variable name for an Enabler or a Var. Offset is the byte offset of the
// token in the line.
type Token struct {
	Kind   TokenKind
	Text   string
	Offset int
}

// Tokenize splits a template line into tokens: the leading whitespace as
// Text, then the chain of Enabler prefixes, then the rest of the line as Text
// and Var tokens. Whether an Enabler really is one depends on the variable
// (see Resolver.Enabler); the lexer only knows the position. Writing each Text token as is and each
// Enabler or Var token as ${Text} gives the line back.
func Tokenize(line string) []Token {
	var toks []Token

	pos := 0

	for {
		m := EnablerPattern.FindStringSubmatchIndex(line[pos:])
		if m == nil {
			break
		}

		// m: [0 end, 1..] group 1 = indent, group 2 = name, group 3 = rest.
		if m[3] > 0 {
			toks = append(toks, Token{Kind: Text, Text: line[pos : pos+m[3]], Offset: pos})
		}

		toks = append(toks, Token{Kind: Enabler, Text: line[pos+m[4] : pos+m[5]], Offset: pos + m[3]})
		pos += m[5] + 1 // past the closing brace
	}

	return appendVarTokens(toks, line[pos:], pos)
}

// appendVarTokens splits text into Text and Var tokens.
func appendVarTokens(toks []Token, text string, base int) []Token {
	last := 0

	for _, m := range VarPattern.FindAllStringSubmatchIndex(text, -1) {
		if m[0] > last {
			toks = append(toks, Token{Kind: Text, Text: text[last:m[0]], Offset: base + last})
		}

		toks = append(toks, Token{Kind: Var, Text: text[m[2]:m[3]], Offset: base + m[0]})
		last = m[1]
	}

	if last < len(text) {
		toks = append(toks, Token{Kind: Text, Text: text[last:], Offset: base + last})
	}

	return toks
}

// varEnd returns the index after the ${name} that starts at text[i], or -1
// when text[i:] does not start a variable.
func varEnd(text string, i int) int {
	if !strings.HasPrefix(text[i:], "${") {
		return -1
	}

	for j := i + 2; j < len(text); j++ {
		c := text[j]

		switch {
		case c == '}':
			if j == i+2 {
				return -1
			}

			return j + 1
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == ':':
		default:
			return -1
		}
	}

	return -1
}

// scanner is the nginx-aware scan of template text: quotes, comments and
// ${var} references are opaque to the braces. A quote left open at the end of
// a line carries into the next one.
type scanner struct {
	quote byte // the open quote character, 0 when none
}

// lineScan is the result of scanning one line.
type lineScan struct {
	opens, closes int
	// leadClose counts the '}' before any other code on the line.
	leadClose int
	// code is the line without its trailing comment.
	code string
	// continued is true when the line began inside a quote left open by an
	// earlier line.
	continued bool
	// firstClose is the offset of the first closing brace (-1 when absent).
	firstClose int
}

func (s *scanner) scan(text string) lineScan {
	ls := lineScan{code: text, firstClose: -1}
	i := 0

	if s.quote != 0 {
		ls.continued = true
		i = s.skipQuote(text, 0)

		if s.quote != 0 {
			return ls
		}
	}

	leading := !ls.continued
	tokenStart := true

	for i < len(text) {
		if next, ok := s.skipOpaque(text, i, tokenStart); ok {
			i, leading, tokenStart = next, false, false

			continue
		}

		c := text[i]
		if c == '#' && tokenStart {
			ls.code = text[:i]

			return ls
		}

		ls.count(c, i, &leading)

		tokenStart = c == ' ' || c == '\t' || c == ';' || c == '{' || c == '}'
		i++
	}

	return ls
}

// skipOpaque skips the token at text[i] whose content is not syntax: a
// ${variable}, an escaped character or a quoted string (opened where a token
// starts). ok is false when there is none.
func (s *scanner) skipOpaque(text string, i int, tokenStart bool) (next int, ok bool) {
	switch c := text[i]; {
	case c == '$':
		if end := varEnd(text, i); end > 0 {
			return end, true
		}
	case c == '\\':
		return i + 2, true
	case (c == '"' || c == '\'') && tokenStart:
		s.quote = c

		return s.skipQuote(text, i+1), true
	}

	return i, false
}

// count counts the brace c found at offset i; leading tracks whether only
// closing braces and whitespace came before it on the line.
func (ls *lineScan) count(c byte, i int, leading *bool) {
	switch c {
	case '{':
		ls.opens++
		*leading = false
	case '}':
		ls.closes++

		if ls.firstClose < 0 {
			ls.firstClose = i
		}

		if *leading {
			ls.leadClose++
		}
	case ' ', '\t':
	default:
		*leading = false
	}
}

// skipQuote scans from i inside the open quote s.quote and returns the index
// after the closing quote, closing the quote state; at the end of the text
// the quote stays open.
func (s *scanner) skipQuote(text string, i int) int {
	for i < len(text) {
		switch text[i] {
		case '\\':
			i += 2

			continue
		case s.quote:
			s.quote = 0

			return i + 1
		}

		i++
	}

	return len(text)
}
