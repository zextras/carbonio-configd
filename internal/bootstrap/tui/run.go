// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tui

import (
	"context"
	"fmt"
	"io"

	tea "charm.land/bubbletea/v2"
)

// Configure runs the configuration screen on in and out and returns what the
// operator chose; opts.Resolved holds the answers.
func Configure(ctx context.Context, in io.Reader, out io.Writer, opts *ConfigureOptions) (Action, error) {
	final, err := tea.NewProgram(NewConfigure(opts), tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out)).Run()
	if err != nil {
		return ActionQuit, fmt.Errorf("terminal interface: %w", err)
	}

	return final.(*Model).Result(), nil
}

// Apply runs the apply screen: the steps run while it shows their progress.
// It returns the error that ended the run (nil: complete).
func Apply(ctx context.Context, in io.Reader, out io.Writer, opts *ApplyOptions) error {
	final, err := tea.NewProgram(NewApply(opts), tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out)).Run()
	if err != nil {
		return fmt.Errorf("terminal interface: %w", err)
	}

	finished, runErr := final.(*ApplyModel).Result()
	if !finished {
		return fmt.Errorf("the run did not finish")
	}

	return runErr
}
