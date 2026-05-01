// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package transformer

import (
	"context"
	"fmt"
)

// errorConfigLookup always returns an error for any lookup.
type errorConfigLookup struct{}

func (e *errorConfigLookup) LookUpConfig(_ context.Context, _, _ string) (string, error) {
	return "", fmt.Errorf("lookup error")
}
