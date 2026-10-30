// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package testutil

import (
	"context"

	"github.com/zextras/carbonio-configd/internal/mtaops"
	"github.com/zextras/carbonio-configd/internal/snapshot"
)

var (
	_ mtaops.Executor          = (*MockMtaExecutor)(nil)
	_ mtaops.OperationResolver = (*MockMtaResolver)(nil)
)

// MockMtaExecutor is a test double for mtaops.Executor.
// Set function fields for per-test behavior; defaults return nil.
type MockMtaExecutor struct {
	ExecutePostconfFn       func(ctx context.Context, op mtaops.PostconfOperation) error
	ExecutePostconfBatchFn  func(ctx context.Context, ops []mtaops.PostconfOperation) error
	ExecutePostconfdFn      func(ctx context.Context, op mtaops.PostconfdOperation) error
	ExecutePostconfdBatchFn func(ctx context.Context, ops []mtaops.PostconfdOperation) error
	ExecuteMapfileFn        func(ctx context.Context, op mtaops.MapfileOperation) error
	ExecuteLdapWriteFn      func(ctx context.Context, op mtaops.LdapOperation) error
	ReadPostconfFn          func(ctx context.Context, key string, explicitOnly bool) (string, bool, error)
}

// ReadPostconf delegates to ReadPostconfFn or returns ("", false, nil).
func (m *MockMtaExecutor) ReadPostconf(
	ctx context.Context, key string, explicitOnly bool,
) (value string, set bool, err error) {
	if m.ReadPostconfFn != nil {
		return m.ReadPostconfFn(ctx, key, explicitOnly)
	}

	return "", false, nil
}

// ExecutePostconf delegates to ExecutePostconfFn or returns nil.
func (m *MockMtaExecutor) ExecutePostconf(ctx context.Context, op mtaops.PostconfOperation) error {
	if m.ExecutePostconfFn != nil {
		return m.ExecutePostconfFn(ctx, op)
	}

	return nil
}

// ExecutePostconfBatch delegates to ExecutePostconfBatchFn or returns nil.
func (m *MockMtaExecutor) ExecutePostconfBatch(ctx context.Context, ops []mtaops.PostconfOperation) error {
	if m.ExecutePostconfBatchFn != nil {
		return m.ExecutePostconfBatchFn(ctx, ops)
	}

	return nil
}

// ExecutePostconfd delegates to ExecutePostconfdFn or returns nil.
func (m *MockMtaExecutor) ExecutePostconfd(ctx context.Context, op mtaops.PostconfdOperation) error {
	if m.ExecutePostconfdFn != nil {
		return m.ExecutePostconfdFn(ctx, op)
	}

	return nil
}

// ExecutePostconfdBatch delegates to ExecutePostconfdBatchFn or returns nil.
func (m *MockMtaExecutor) ExecutePostconfdBatch(ctx context.Context, ops []mtaops.PostconfdOperation) error {
	if m.ExecutePostconfdBatchFn != nil {
		return m.ExecutePostconfdBatchFn(ctx, ops)
	}

	return nil
}

// ExecuteMapfile delegates to ExecuteMapfileFn or returns nil.
func (m *MockMtaExecutor) ExecuteMapfile(ctx context.Context, op mtaops.MapfileOperation) error {
	if m.ExecuteMapfileFn != nil {
		return m.ExecuteMapfileFn(ctx, op)
	}

	return nil
}

// ExecuteLdapWrite delegates to ExecuteLdapWriteFn or returns nil.
func (m *MockMtaExecutor) ExecuteLdapWrite(ctx context.Context, op mtaops.LdapOperation) error {
	if m.ExecuteLdapWriteFn != nil {
		return m.ExecuteLdapWriteFn(ctx, op)
	}

	return nil
}

// MockMtaResolver is a test double for mtaops.OperationResolver.
// Set ResolveValueFn for per-test behavior; default returns ("", nil).
type MockMtaResolver struct {
	ResolveValueFn func(ctx context.Context, valueType, key string, snap *snapshot.Snapshot) (string, error)
}

// ResolveValue delegates to ResolveValueFn or returns ("", nil).
func (m *MockMtaResolver) ResolveValue(
	ctx context.Context, valueType, key string, snap *snapshot.Snapshot,
) (string, error) {
	if m.ResolveValueFn != nil {
		return m.ResolveValueFn(ctx, valueType, key, snap)
	}

	return "", nil
}
