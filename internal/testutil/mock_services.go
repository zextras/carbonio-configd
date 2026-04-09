// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package testutil provides reusable mock types for testing.
package testutil

import (
	"context"

	"github.com/zextras/carbonio-configd/internal/services"
)

// Compile-time interface assertion.
var _ services.Manager = (*MockServiceManager)(nil)

// MockServiceManager is a test double for services.Manager.
// Override individual methods by setting the corresponding function field.
// Unset fields fall back to harmless zero-value returns.
type MockServiceManager struct {
	ControlProcessFn       func(ctx context.Context, service string, action services.ServiceAction) error
	IsRunningFn            func(ctx context.Context, service string) (bool, error)
	AddRestartFn           func(ctx context.Context, service string) error
	ProcessRestartsFn      func(ctx context.Context, configLookup func(string) string) error
	ClearRestartsFn        func(ctx context.Context)
	GetPendingRestartsFn   func() []string
	SetDependenciesFn      func(ctx context.Context, deps map[string][]string)
	AddDependencyRestartsFn func(ctx context.Context, sectionName string, configLookup func(string) string)
	HasCommandFn           func(service string) bool
	SetUseSystemdFn        func(enabled bool)
}

// ControlProcess delegates to ControlProcessFn or returns nil.
func (m *MockServiceManager) ControlProcess(ctx context.Context, service string, action services.ServiceAction) error {
	if m.ControlProcessFn != nil {
		return m.ControlProcessFn(ctx, service, action)
	}

	return nil
}

// IsRunning delegates to IsRunningFn or returns (false, nil).
func (m *MockServiceManager) IsRunning(ctx context.Context, service string) (bool, error) {
	if m.IsRunningFn != nil {
		return m.IsRunningFn(ctx, service)
	}

	return false, nil
}

// AddRestart delegates to AddRestartFn or returns nil.
func (m *MockServiceManager) AddRestart(ctx context.Context, service string) error {
	if m.AddRestartFn != nil {
		return m.AddRestartFn(ctx, service)
	}

	return nil
}

// ProcessRestarts delegates to ProcessRestartsFn or returns nil.
func (m *MockServiceManager) ProcessRestarts(ctx context.Context, configLookup func(string) string) error {
	if m.ProcessRestartsFn != nil {
		return m.ProcessRestartsFn(ctx, configLookup)
	}

	return nil
}

// ClearRestarts delegates to ClearRestartsFn or does nothing.
func (m *MockServiceManager) ClearRestarts(ctx context.Context) {
	if m.ClearRestartsFn != nil {
		m.ClearRestartsFn(ctx)
	}
}

// GetPendingRestarts delegates to GetPendingRestartsFn or returns nil.
func (m *MockServiceManager) GetPendingRestarts() []string {
	if m.GetPendingRestartsFn != nil {
		return m.GetPendingRestartsFn()
	}

	return nil
}

// SetDependencies delegates to SetDependenciesFn or does nothing.
func (m *MockServiceManager) SetDependencies(ctx context.Context, deps map[string][]string) {
	if m.SetDependenciesFn != nil {
		m.SetDependenciesFn(ctx, deps)
	}
}

// AddDependencyRestarts delegates to AddDependencyRestartsFn or does nothing.
func (m *MockServiceManager) AddDependencyRestarts(
	ctx context.Context, sectionName string, configLookup func(string) string,
) {
	if m.AddDependencyRestartsFn != nil {
		m.AddDependencyRestartsFn(ctx, sectionName, configLookup)
	}
}

// HasCommand delegates to HasCommandFn or returns false.
func (m *MockServiceManager) HasCommand(service string) bool {
	if m.HasCommandFn != nil {
		return m.HasCommandFn(service)
	}

	return false
}

// SetUseSystemd delegates to SetUseSystemdFn or does nothing.
func (m *MockServiceManager) SetUseSystemd(enabled bool) {
	if m.SetUseSystemdFn != nil {
		m.SetUseSystemdFn(enabled)
	}
}
