// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/user"
	"reflect"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/security"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// genRun runs "proxy gen" with fn as the in-process generator.
func genRun(t *testing.T, gen *GenOptions, fn GenFunc) (code int, stdout, stderr string) {
	t.Helper()

	var out, errOut bytes.Buffer

	code = Run(context.Background(), systest.New().System(), &out, &errOut, &Options{
		Action:   ActionGen,
		Gen:      gen,
		Generate: fn,
	})

	return code, out.String(), errOut.String()
}

func TestRunGen_PassesFlagsAndSucceeds(t *testing.T) {
	var got *GenOptions

	want := &GenOptions{DryRun: true, WorkDir: "/w", Server: "mbox", Overrides: []string{"a=b"}}

	code, _, _ := genRun(t, want, func(_ context.Context, _ io.Writer, o *GenOptions) error {
		got = o

		return nil
	})

	if code != 0 {
		t.Fatalf("code=%d, want 0", code)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("options = %+v, want %+v", got, want)
	}
}

func TestRunGen_ErrorExitsOne(t *testing.T) {
	code, _, stderr := genRun(t, &GenOptions{DryRun: true}, func(context.Context, io.Writer, *GenOptions) error {
		return errors.New("boom")
	})

	if code != 1 || !strings.Contains(stderr, "Error: boom") {
		t.Errorf("code=%d stderr=%q, want 1 and the error", code, stderr)
	}
}

func TestRunGen_StrayArgsPrintUsageExitZero(t *testing.T) {
	code, stdout, _ := genRun(t, &GenOptions{DryRun: true, Args: []string{"x"}},
		func(context.Context, io.Writer, *GenOptions) error {
			t.Error("generator must not run")

			return nil
		})

	if code != 0 || !strings.Contains(stdout, "ProxyConfGen generates the NGINX Proxy configuration files") {
		t.Errorf("code=%d stdout=%q, want 0 and usage", code, stdout)
	}
}

func TestRunGen_RequiresZextrasToWrite(t *testing.T) {
	if current, err := user.Current(); err != nil || current.Username == security.RequiredUser {
		t.Skip("runs as the zextras user")
	}

	code, _, stderr := genRun(t, &GenOptions{}, func(context.Context, io.Writer, *GenOptions) error {
		t.Error("generator must not run")

		return nil
	})

	if code != 1 || !strings.Contains(stderr, "zextras") {
		t.Errorf("code=%d stderr=%q, want 1 and the zextras message", code, stderr)
	}
}

func TestSplitOverrides(t *testing.T) {
	all, rest := SplitOverrides([]string{"a=b"}, []string{"c=d", "stray", "e=f=g"})

	if want := []string{"a=b", "c=d", "e=f=g"}; !reflect.DeepEqual(all, want) {
		t.Errorf("overrides = %v, want %v", all, want)
	}

	if want := []string{"stray"}; !reflect.DeepEqual(rest, want) {
		t.Errorf("rest = %v, want %v", rest, want)
	}
}
