// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package release

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func TestInstalledPackageNames(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		osRelease string
		path      string
		out       string
		want      []string
	}{
		"deb keeps the name before the version": {
			"ID=ubuntu\n", "dpkg-query", "carbonio-core 1.2-3\ncarbonio-mta 2.0\n",
			[]string{"carbonio-core", "carbonio-mta"},
		},
		"rpm drops the arch suffix": {
			"ID=\"rocky\"\n", "rpm", "carbonio-mta.x86_64 1:2.0-1\ncarbonio-core.noarch 1.2-3\n",
			[]string{"carbonio-core", "carbonio-mta"},
		},
		"no os-release is an rpm node": {
			"", "rpm", "carbonio-proxy.x86_64 1-1\n", []string{"carbonio-proxy"},
		},
		"nothing installed": {"ID=debian\n", "dpkg-query", "", nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := systest.New()
			if tc.osRelease != "" {
				f.FS.Put("/etc/os-release", []byte(tc.osRelease), 0o644)
			}

			f.Exec.OnPath(tc.path, systest.Reply(tc.out))

			assert.Equal(t, tc.want, InstalledPackageNames(context.Background(), f.System()))
		})
	}
}

func TestInstalledPackageNames_PackageManagerFails(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.FS.Put("/etc/os-release", []byte("ID=ubuntu\n"), 0o644)
	f.Exec.FailStart(func(*sys.Cmd) bool { return true }, errors.New("not found"))

	assert.Empty(t, InstalledPackageNames(context.Background(), f.System()))
}
