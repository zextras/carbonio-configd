// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package transformer

import (
	"context"
	"sync"
	"testing"
)

// BenchmarkNewTransformer_Allocs confirms that constructing many Transformer
// instances no longer triggers per-instance regex compilation. The allocation
// count reported by b.ReportAllocs / -benchmem must stay tiny: we expect one
// allocation for the Transformer struct itself and nothing else.
func BenchmarkNewTransformer_Allocs(b *testing.B) {
	lookup := newMockLookup()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = NewTransformer(lookup, nil)
	}
}

// TestTransform_Concurrent exercises the shared package-level regex patterns
// under high concurrency with -race. Each worker runs the full Transform
// pipeline over a set of inputs that hit every regex we share.
func TestTransform_Concurrent(t *testing.T) {
	t.Parallel()

	const (
		workers  = 32
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

			tx := NewTransformer(lookup, nil)
			ctx := context.Background()

			for range perWorker {
				for _, in := range inputs {
					_ = tx.Transform(ctx, in)
				}
			}
		}()
	}

	wg.Wait()
}
