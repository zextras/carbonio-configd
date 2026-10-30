// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package template

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/transformer"
)

// A line the legacy transform() raises on (here @@SPLIT key@@ with a key missing
// from localconfig) aborts the whole rewrite: the error surfaces and the target
// keeps its previous content.
func TestRewriter_TransformErrorAbortsRewrite(t *testing.T) {
	base := t.TempDir()
	rewriter := NewRewriter(base, newMockLookup(), sys.Real())

	require.NoError(t, os.WriteFile(filepath.Join(base, "a.in"), []byte("ok\nx=@@SPLIT unset_key@@\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(base, "a.conf"), []byte("previous\n"), 0o644))

	err := rewriter.RewriteConfig(context.Background(), "a.in", "a.conf", "")
	require.ErrorIs(t, err, transformer.ErrTransform)

	got, readErr := os.ReadFile(filepath.Join(base, "a.conf"))
	require.NoError(t, readErr)
	assert.Equal(t, "previous\n", string(got))

	var out bytes.Buffer

	err = rewriter.RewriteReader(context.Background(), strings.NewReader("x=@@SPLIT unset_key@@\n"), &out)
	require.ErrorIs(t, err, transformer.ErrTransform)
	assert.Empty(t, out.String())
}
