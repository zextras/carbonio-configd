// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package testutil

import (
	"context"
	"fmt"

	"github.com/zextras/carbonio-configd/internal/lookup"
)

var _ lookup.ConfigLookup = (*MockConfigLookup)(nil)

// MockConfigLookup is a test double for lookup.ConfigLookup.
// Set LookUpConfigFn for per-test behavior; default returns ("", nil).
type MockConfigLookup struct {
	LookUpConfigFn func(ctx context.Context, cfgType, key string) (string, error)
}

// LookUpConfig delegates to LookUpConfigFn or returns ("", nil).
func (m *MockConfigLookup) LookUpConfig(ctx context.Context, cfgType, key string) (string, error) {
	if m.LookUpConfigFn != nil {
		return m.LookUpConfigFn(ctx, cfgType, key)
	}

	return "", nil
}

// NewMockConfigLookupWithData returns a MockConfigLookup backed by a nested
// map[cfgType]map[key]value. LookUpConfig returns the value when found, or
// fmt.Errorf("key not found: %s:%s", cfgType, key) when missing.
func NewMockConfigLookupWithData(data map[string]map[string]string) *MockConfigLookup {
	return &MockConfigLookup{
		LookUpConfigFn: func(_ context.Context, cfgType, key string) (string, error) {
			if typeData, ok := data[cfgType]; ok {
				if val, ok := typeData[key]; ok {
					return val, nil
				}
			}

			return "", fmt.Errorf("key not found: %s:%s", cfgType, key)
		},
	}
}
