// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrapcmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func covFakes() *systest.Fakes {
	f := systest.New()
	f.FS.Put("/etc/os-release", []byte("ID=rocky\n"), 0o644)
	f.Exec.OnPath("rpm", systest.Reply("carbonio-core install ok installed\n"+
		"carbonio-directory-server install ok installed\ncarbonio-appserver install ok installed\n"))
	f.Exec.OnPath("hostname", systest.Reply("mail.example.com\n"))

	return f
}

func covRun(t *testing.T, f *systest.Fakes, opts *Options, deps Deps, stdin string) (int, string, string) {
	t.Helper()

	var out, errb strings.Builder

	status := run(context.Background(), f.System(), strings.NewReader(stdin), &out, &errb, opts, &deps)

	return status, out.String(), errb.String()
}

func TestRun_PublicEntryUsesTheNodeAsHost(t *testing.T) {
	f := covFakes()

	var out, errb strings.Builder

	opts := &Options{Wizard: WizardMissing, Environ: runTestEnviron}
	status := Run(context.Background(), f.System(), strings.NewReader(""), &out, &errb, opts)

	require.Equal(t, 0, status, errb.String())
	assert.Contains(t, out.String(), "hostname: mail.example.com", "the FQDN comes from the node")
	assert.Contains(t, out.String(), "directory-server", "the roles come from the installed packages")
	assert.NotContains(t, out.String(), "Sup3rSecret!")
}

func TestRealDeps_NoTerminalOnPipes(t *testing.T) {
	d := realDeps(context.Background(), systest.New().System(), strings.NewReader(""), &strings.Builder{})

	assert.False(t, d.Terminal, "a pipe is not a terminal: no wizard")
	assert.Nil(t, d.ReadSecret)
	assert.NotNil(t, d.Host)
	assert.NotNil(t, d.Resolver)
}

func TestRun_FileErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string
		opts  Options
		want  []string
	}{
		"missing YAML file": {
			opts: Options{ConfigPath: "/nope.yaml"},
			want: []string{"cannot build the bootstrap configuration", "/nope.yaml"},
		},
		"missing legacy file": {
			opts: Options{LegacyPath: "/nope.conf"},
			want: []string{"/nope.conf"},
		},
		"invalid YAML names the file": {
			files: map[string]string{"/bad.yaml": "hostname: [unclosed\n"},
			opts:  Options{ConfigPath: "/bad.yaml"},
			want:  []string{"/bad.yaml:"},
		},
		"unknown YAML key names the file": {
			files: map[string]string{"/typo.yaml": "no_such_key: 1\n"},
			opts:  Options{ConfigPath: "/typo.yaml"},
			want:  []string{"/typo.yaml:", "no_such_key"},
		},
		"invalid legacy file names the file": {
			files: map[string]string{"/bad.conf": "no key-value separator here\n"},
			opts:  Options{LegacyPath: "/bad.conf"},
			want:  []string{"/bad.conf"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := covFakes()
			for p, c := range tc.files {
				f.FS.Put(p, []byte(c), 0o644)
			}

			tc.opts.Environ = runTestEnviron

			status, out, errb := covRun(t, f, &tc.opts, Deps{Host: host{}}, "")

			assert.Equal(t, 1, status)
			assert.Empty(t, out, "nothing is printed for a configuration that cannot be built")

			for _, w := range tc.want {
				assert.Contains(t, errb, w)
			}
		})
	}
}

func TestInFile_PrefixesEveryJoinedError(t *testing.T) {
	err := inFile("a.yaml", errors.Join(errors.New("one"), errors.New("two")))

	assert.Equal(t, "a.yaml: one\na.yaml: two", err.Error())

	single := errors.New("boom")
	got := inFile("b.yaml", single)

	assert.Equal(t, "b.yaml: boom", got.Error())
	assert.ErrorIs(t, got, single)
}

func TestRun_YAMLFileIsApplied(t *testing.T) {
	f := covFakes()
	f.FS.Put("/bootstrap.yaml", []byte("host:\n  hostname: yaml.example.org\n"), 0o600)

	status, out, errb := covRun(t, f, &Options{ConfigPath: "/bootstrap.yaml", Environ: runTestEnviron}, Deps{Host: host{}}, "")

	require.Equal(t, 0, status, errb)
	assert.Contains(t, out, "hostname: yaml.example.org", "the file wins over the node default")
}

func TestRun_OutputGoesThroughTheNodeFS(t *testing.T) {
	f := covFakes()

	status, out, errb := covRun(t, f, &Options{Output: "/out/bootstrap.yaml", Environ: runTestEnviron}, Deps{Host: host{}}, "")

	require.Equal(t, 0, status, errb)
	assert.Contains(t, out, "Configuration saved to /out/bootstrap.yaml")

	data, ok := f.FS.Data("/out/bootstrap.yaml")
	require.True(t, ok)
	assert.NotContains(t, string(data), "Sup3rSecret!")

	mode, _ := f.FS.Mode("/out/bootstrap.yaml")
	assert.Equal(t, saveMode, int(mode.Perm()))
}

func TestRun_OutputWriteFailure(t *testing.T) {
	f := covFakes()
	f.FS.Fail = func(op, _ string) error {
		if op == "WriteFileAtomic" {
			return errors.New("disk full")
		}

		return nil
	}

	status, out, errb := covRun(t, f, &Options{Output: "/out.yaml", Environ: runTestEnviron}, Deps{Host: host{}}, "")

	assert.Equal(t, 1, status)
	assert.NotContains(t, out, "Configuration saved")
	assert.Contains(t, errb, "disk full")
}

func TestRun_WizardAllOnTerminalAsksEveryQuestionAndOffersToSave(t *testing.T) {
	f := covFakes()

	// Accept every default; the admin password is already set (env), so the
	// answers are Enter for each question, then no save path.
	status, out, errb := covRun(t, f, &Options{Wizard: WizardAll, Environ: runTestEnviron},
		Deps{Host: host{}, Terminal: true}, strings.Repeat("\n", 60))

	require.Equal(t, 0, status, errb+out)
	assert.Contains(t, out, "Hostname of this node")
	assert.Contains(t, out, "Save these answers to a file")
	assert.NotContains(t, out, "Configuration saved to", "an empty path skips the save")
}

func TestRun_WizardWithOutputDoesNotOfferToSave(t *testing.T) {
	f := covFakes()

	status, out, errb := covRun(t, f, &Options{Wizard: WizardAll, Output: "/w.yaml", Environ: runTestEnviron},
		Deps{Host: host{}, Terminal: true}, strings.Repeat("\n", 60))

	require.Equal(t, 0, status, errb+out)
	assert.NotContains(t, out, "Save these answers to a file")
	assert.True(t, f.FS.Exists("/w.yaml"))
}

func TestRun_WizardSaveFailureIsReported(t *testing.T) {
	f := covFakes()
	f.FS.Fail = func(op, _ string) error {
		if op == "WriteFileAtomic" {
			return errors.New("read-only file system")
		}

		return nil
	}

	// As the wizard test of bootstrapcmd_test.go: 11 pre-filled questions, the
	// admin password twice, the SMTP host, start services, accept, save path.
	answers := strings.Repeat("\n", 11) + "secret1\nsecret1\nsmtp.example.com\n\ny\n/save.yaml\n"

	status, out, errb := covRun(t, f, &Options{Wizard: WizardMissing}, Deps{Host: host{}, Terminal: true}, answers)

	assert.Equal(t, 1, status, out)
	assert.Contains(t, errb, "read-only file system")
	assert.NotContains(t, out, "Configuration saved to")
	assert.False(t, f.FS.Exists("/save.yaml"))
}
