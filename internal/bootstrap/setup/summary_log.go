// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"errors"
	"time"
)

// LogSummary closes the log of a run: the totals, every warning with its
// step, and the result, so the log alone tells how the run ended.
func LogSummary(e *Env, results []Result, err error, elapsed time.Duration) {
	var applied, already, warned int

	for _, r := range results {
		switch r.Outcome {
		case Applied:
			applied++
		case AlreadyDone:
			already++
		case Warned:
			warned++
		case Skipped, WouldApply, Failed:
		}

		for _, w := range r.Warnings {
			e.Log.Detail("Warning: %s: %s", r.Step.Title, w)
		}

		if r.Outcome == Warned && r.Err != nil {
			e.Log.Detail("Warning: %s: %v", r.Step.Title, r.Err)
		}
	}

	e.Log.Detail("Summary: %d applied, %d already done, %d with warnings, in %s",
		applied, already, warned, logDuration(elapsed))

	switch {
	case errors.Is(err, ErrStopped):
		e.Log.Detail("Result: stopped by the operator; run carbonio-bootstrap again to go on")
	case err != nil:
		e.Log.Detail("Result: failed: %v", err)
	default:
		e.Log.Detail("Configuration complete.")
	}
}
