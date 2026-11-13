// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrapcmd

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zextras/carbonio-configd/internal/bootstrap/setup"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// stubRC replaces the zmrc client until the test ends.
func stubRC(t *testing.T, fn func(*setup.Env, []string, io.Reader, io.Writer, io.Writer) int) {
	t.Helper()

	old := runRC
	runRC = fn

	t.Cleanup(func() { runRC = old })
}

func TestRunRC_PassesTheArgumentsAndStdinAndTheClientsStatus(t *testing.T) {
	for _, root := range []bool{true, false} {
		h := newToolHarness(t, root)
		zextrasUID = func() int { return 1000 }

		var gotArgs []string

		var gotStdin string

		stubRC(t, func(e *setup.Env, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
			h.env, gotArgs = e, args

			in, _ := io.ReadAll(stdin)
			gotStdin = string(in)

			_, _ = io.WriteString(stdout, "out\n")
			_, _ = io.WriteString(stderr, "err\n")

			return 1
		})

		var out, errb bytes.Buffer

		status := RunRC(context.Background(), systest.New().System(), strings.NewReader("HOST:h status\n"), &out, &errb,
			[]string{"mta.example.com"})

		assert.Equal(t, 1, status, "the status of the client, root %v", root)
		assert.Equal(t, []string{"mta.example.com"}, gotArgs)
		assert.Equal(t, "HOST:h status\n", gotStdin)
		assert.Equal(t, "out\n", out.String())
		assert.Equal(t, "err\n", errb.String())
		assert.Equal(t, !root, h.env.IsZextras)
	}
}

func TestRunRC_RefusesOtherUsers(t *testing.T) {
	h := newToolHarness(t, false)

	called := false
	stubRC(t, func(*setup.Env, []string, io.Reader, io.Writer, io.Writer) int { called = true; return 0 })

	var out, errb bytes.Buffer

	status := RunRC(context.Background(), systest.New().System(), strings.NewReader(""), &out, &errb, []string{"h"})

	assert.Equal(t, 1, status)
	assert.Equal(t, "Error: carbonio-bootstrap rc must run as root or zextras\n", errb.String())
	assert.False(t, called)
	assert.Empty(t, h.calls)
}
