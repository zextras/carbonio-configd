// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package transformer

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// mustTransform runs Transform and fails the test on a transform error.
func mustTransform(t testing.TB, tr *Transformer, ctx context.Context, line string) string {
	t.Helper()

	out, err := tr.Transform(ctx, line)
	require.NoError(t, err)

	return out
}

// BenchmarkNewTransformer_Allocs confirms that constructing many Transformer
// instances no longer triggers per-instance regex compilation. The allocation
// count reported by b.ReportAllocs / -benchmem must stay tiny: we expect one
// allocation for the Transformer struct itself and nothing else.
func BenchmarkNewTransformer_Allocs(b *testing.B) {
	lookup := newMockLookup()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = NewTransformer(lookup)
	}
}

// TestTransform_Concurrent exercises the shared package-level regex patterns
// under high concurrency with -race. Each worker runs the full Transform
// pipeline over a set of inputs that hit every regex we share.
func TestTransform_Concurrent(t *testing.T) {
	t.Parallel()

	const (
		workers   = 32
		perWorker = 1000
	)

	lookup := newMockLookup()

	inputs := []string{
		"plain text without directives",
		"LocalConfig @@zimbra_server_hostname@@",
		"Variable %%VAR:zimbraServerHostname%%",
		"Mixed @@zimbra_home@@ and %%LOCAL:zimbra_user%%",
		"%%comment VAR:zimbraReverseProxyMailMode%%server_name default;",
		"%%explode  ,  VAR:zimbraMtaSmtpdTlsKeyFile%%",
	}

	var wg sync.WaitGroup

	wg.Add(workers)

	for range workers {
		go func() {
			defer wg.Done()

			tx := NewTransformer(lookup)
			ctx := context.Background()

			for range perWorker {
				for _, in := range inputs {
					if _, err := tx.Transform(ctx, in); err != nil {
						t.Errorf("Transform(%q): %v", in, err)
					}
				}
			}
		}()
	}

	wg.Wait()
}
