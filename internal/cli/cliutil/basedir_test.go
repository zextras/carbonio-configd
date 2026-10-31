// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package cliutil

import (
	"bytes"
	"os/user"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/security"
)

func TestBasePath(t *testing.T) {
	t.Setenv("CARBONIO_BASE_DIR", "")

	if got := BaseDir(); got != defaultBaseDir {
		t.Errorf("BaseDir() = %q, want %q", got, defaultBaseDir)
	}

	t.Setenv("CARBONIO_BASE_DIR", "/tmp/cb/")

	if got := BasePath("conf", "../conf", "localconfig.xml"); got != "/tmp/cb/conf/localconfig.xml" {
		t.Errorf("BasePath = %q, want a cleaned path under CARBONIO_BASE_DIR", got)
	}
}

func TestRequireZextras(t *testing.T) {
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer

	got := RequireZextras(&stderr)

	if current.Username == security.RequiredUser {
		if !got || stderr.Len() != 0 {
			t.Errorf("RequireZextras = %v, stderr %q, want true and no output for the zextras user", got, stderr.String())
		}

		return
	}

	want := "Please run this program as the '" + security.RequiredUser + "' user.\n"
	if got || stderr.String() != want {
		t.Errorf("RequireZextras = %v, stderr %q, want false and %q", got, stderr.String(), want)
	}

	if strings.Contains(stderr.String(), "Error:") {
		t.Errorf("the permission failure must not add an Error: line, got %q", stderr.String())
	}
}
