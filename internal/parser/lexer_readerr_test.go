// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package parser

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestLexer_ReadErrorIsSurfaced(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	l := NewLexer(context.Background(), io.MultiReader(strings.NewReader("SECTION"), failingReader{boom}))

	var err error

	for range 10 {
		var tok Token
		if tok, err = l.NextToken(); err != nil || tok.Type == TokenEOF {
			break
		}
	}

	if !errors.Is(err, boom) {
		t.Fatalf("expected read error to surface, got %v", err)
	}
}
