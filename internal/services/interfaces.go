// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package services provides interfaces for managing Carbonio system services.
// It defines the Manager interface for service control operations including start, stop,
// restart, status checks, and dependency-based cascade restarts.
package services

import "context"

// ServiceAction represents the action to perform on a service.
type ServiceAction int

const (
	// ActionRestart restarts the service (or reload for MTA)
	ActionRestart ServiceAction = -1
	// ActionStop stops the service
	ActionStop ServiceAction = 0
	// ActionStart starts the service
	ActionStart ServiceAction = 1
	// ActionStatus checks the service status
	ActionStatus ServiceAction = 2
)

const actionUnknown = "unknown"

// String returns the string representation of a ServiceAction.
func (a ServiceAction) String() string {
	switch a {
	case ActionRestart:
		return "restart"
	case ActionStop:
		return "stop"
	case ActionStart:
		return "start"
	case ActionStatus:
		return "status"
	default:
		return actionUnknown
	}
}

// Manager defines the interface for service management operations.
type Manager interface {
	// ControlProcess performs an action on a service.
	// Returns an error if the operation fails.
	ControlProcess(ctx context.Context, service string, action ServiceAction) error

	// IsRunning checks if a service is currently running.
	IsRunning(ctx context.Context, service string) (bool, error)

	// AddRestart queues a service for restart.
	AddRestart(ctx context.Context, service string) error

	// ProcessRestarts processes all queued service restarts with dependency cascading.
	// Handles dependency ordering and retry logic.
	// The configLookup function is used to check if dependent services are enabled (SERVICE_* keys).
	ProcessRestarts(ctx context.Context, configLookup func(string) string) error

	// ClearRestarts clears all queued restarts.
	ClearRestarts(ctx context.Context)

	// GetPendingRestarts returns the list of services pending restart.
	GetPendingRestarts() []string

	// SetDependencies sets the dependency map for cascade restarts.
	// The map key is the section name, and the value is a list of services it depends on.
	SetDependencies(ctx context.Context, deps map[string][]string)

	// AddDependencyRestarts queues dependent services for restart based on a section name.
	// Used after a service is successfully started/stopped/restarted.
	// The configLookup function is used to check if dependent services are enabled (SERVICE_* keys).
	AddDependencyRestarts(ctx context.Context, sectionName string, configLookup func(string) string)

	// HasCommand checks if a service has a control command defined.
	HasCommand(service string) bool

	// SetUseSystemd enables or disables systemctl for service control.
	SetUseSystemd(enabled bool)
}
