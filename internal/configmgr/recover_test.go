// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"sync"
	"testing"
)

func TestRunRecovered_ConvertsPanicToError(t *testing.T) {
	t.Parallel()

	err := runRecovered(context.Background(), "x", func() error { panic("boom") })
	if err == nil {
		t.Fatal("expected panic to become an error")
	}
}

func TestRunStep_PanicIsReportedNotPropagated(t *testing.T) {
	t.Parallel()

	cm := realFSConfigManager()
	errChan := make(chan error, 1)

	var wg sync.WaitGroup

	cm.runStep(context.Background(), &wg, errChan, func(context.Context) error { panic("boom") })
	wg.Wait()

	if len(errChan) != 1 {
		t.Fatalf("expected one error on errChan, got %d", len(errChan))
	}
}
