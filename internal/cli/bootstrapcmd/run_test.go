// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrapcmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/bootstrap/setup"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

var runTestEnviron = []string{
	"CARBONIO_BOOTSTRAP_ADMIN_PASSWORD=Sup3rSecret!",
	"CARBONIO_BOOTSTRAP_STORE_SMTP_HOST=smtp.example.com",
}

// runTestEnv is a setup.Env on a fake new node: carbonio-core and the
// directory server installed, no install history.
func runTestEnv(t *testing.T) (*setup.Env, *systest.Fakes) {
	t.Helper()

	t.Setenv("CARBONIO_BASE_DIR", t.TempDir())

	f := systest.New()
	f.FS.Put("/etc/os-release", []byte("ID=rocky\n"), 0o644)
	f.Exec.OnPath("rpm", systest.Reply(
		"carbonio-core install ok installed\ncarbonio-directory-server install ok installed\n"))

	sysEnv := f.System()

	return &setup.Env{
		Ctx:   context.Background(),
		Sys:   sysEnv,
		Log:   setup.NewLog(&strings.Builder{}, nil),
		Ctl:   services.NewController(sysEnv),
		Paths: setup.DefaultPaths(),
	}, f
}

func runTestSteps(applied *[]string) []setup.Step {
	step := func(name string, err error) setup.Step {
		return setup.Step{Title: name, Apply: func(*setup.Env) error {
			*applied = append(*applied, name)

			return err
		}}
	}

	return []setup.Step{step("first", nil), step("second", nil)}
}

func runTestOpts() *RunOptions {
	return &RunOptions{Options: Options{Wizard: WizardMissing, NonInteractive: true, Environ: runTestEnviron}}
}

func TestRunSetup_AppliesStepsOnNewInstall(t *testing.T) {
	e, _ := runTestEnv(t)

	var applied []string

	var out, errb strings.Builder

	status := runSetup(e, strings.NewReader(""), &out, &errb, runTestOpts(), Deps{Host: host{}}, runTestSteps(&applied))

	require.Equal(t, 0, status, errb.String())
	assert.Equal(t, []string{"first", "second"}, applied)
	assert.True(t, e.Facts.NewInstall)
	assert.Equal(t, "mail.example.com", e.Cfg.Host.Hostname)
	assert.Contains(t, e.Facts.EnabledRoles(), "directory-server")
	assert.NotContains(t, out.String()+errb.String(), "Sup3rSecret!", "the password never reaches the output")
}

func TestRunSetup_StepFailureStopsAndExplains(t *testing.T) {
	e, _ := runTestEnv(t)

	var applied []string

	steps := runTestSteps(&applied)
	steps[0].Apply = func(*setup.Env) error { return errors.New("zmprov exploded") }

	var out, errb strings.Builder

	status := runSetup(e, strings.NewReader(""), &out, &errb, runTestOpts(), Deps{Host: host{}}, steps)

	assert.Equal(t, 1, status)
	assert.Contains(t, errb.String(), "zmprov exploded")
	assert.Contains(t, errb.String(), "run carbonio-bootstrap again")
	assert.Empty(t, applied, "the steps after the failure do not run")
	assert.NotContains(t, out.String(), "Configuration complete")
}

func TestRunSetup_DryRunPrintsThePlanAndChangesNothing(t *testing.T) {
	e, f := runTestEnv(t)

	var applied []string

	opts := runTestOpts()
	opts.DryRun = true

	var out, errb strings.Builder

	status := runSetup(e, strings.NewReader(""), &out, &errb, opts, Deps{Host: host{}}, runTestSteps(&applied))

	require.Equal(t, 0, status, errb.String())
	assert.Empty(t, applied)
	assert.Contains(t, out.String(), "STEP")
	assert.Regexp(t, `first\s+would apply`, out.String())
	assert.Regexp(t, `second\s+would apply`, out.String())
	assert.NotContains(t, out.String(), "Configuration complete")
	assert.False(t, f.FS.Exists(e.Paths.History), "a dry run writes no install history")
}

func TestRunSetup_DryRunPlanShowsWhyAStepCannotBeChecked(t *testing.T) {
	e, _ := runTestEnv(t)

	opts := runTestOpts()
	opts.DryRun = true

	steps := []setup.Step{{
		Title: "needs ldap",
		Done:  func(*setup.Env) (bool, error) { return false, errors.New("LDAP not up") },
		Apply: func(*setup.Env) error { return nil },
	}}

	var out, errb strings.Builder

	status := runSetup(e, strings.NewReader(""), &out, &errb, opts, Deps{Host: host{}}, steps)

	require.Equal(t, 0, status, errb.String())
	assert.Contains(t, out.String(), "would apply: cannot check yet: LDAP not up")
}

func TestRunSetup_InvalidConfigurationRunsNothing(t *testing.T) {
	e, _ := runTestEnv(t)

	var applied []string

	opts := runTestOpts()
	opts.Environ = append(opts.Environ, "CARBONIO_BOOTSTRAP_ROLES=bogus-role")

	var out, errb strings.Builder

	status := runSetup(e, strings.NewReader(""), &out, &errb, opts, Deps{Host: host{}}, runTestSteps(&applied))

	assert.Equal(t, 1, status)
	assert.Contains(t, errb.String(), "cannot build the bootstrap configuration")
	assert.Contains(t, errb.String(), "bogus-role")
	assert.Empty(t, applied)
}

func TestRunSetup_OutputSavesWithoutPasswords(t *testing.T) {
	e, f := runTestEnv(t)

	var applied []string

	opts := runTestOpts()
	opts.Output = filepath.Join(t.TempDir(), "bootstrap.yaml")

	var out, errb strings.Builder

	status := runSetup(e, strings.NewReader(""), &out, &errb, opts, Deps{Host: host{}}, runTestSteps(&applied))

	require.Equal(t, 0, status, errb.String())
	assert.Equal(t, []string{"first", "second"}, applied, "--output saves and still applies")

	data, ok := f.FS.Data(opts.Output)
	require.True(t, ok, "saved through the node filesystem")
	assert.Contains(t, string(data), "hostname: mail.example.com")
	assert.NotContains(t, string(data), "Sup3rSecret!")
}

func TestRunSetup_OutputWriteFailureStopsBeforeTheSteps(t *testing.T) {
	e, f := runTestEnv(t)

	var applied []string

	opts := runTestOpts()
	opts.Output = "/readonly/bootstrap.yaml"
	f.FS.Fail = func(op, path string) error {
		if op == "WriteFileAtomic" && path == opts.Output {
			return errors.New("read-only file system")
		}

		return nil
	}

	var out, errb strings.Builder

	status := runSetup(e, strings.NewReader(""), &out, &errb, opts, Deps{Host: host{}}, runTestSteps(&applied))

	assert.Equal(t, 1, status)
	assert.Contains(t, errb.String(), "read-only file system")
	assert.Empty(t, applied)
}

func TestRunSetup_DetectFactsErrorIsReported(t *testing.T) {
	e, f := runTestEnv(t)
	// An existing install (history ends in CONFIGURED END) needs the
	// localconfig, which is missing.
	f.FS.Put(e.Paths.History, []byte("1: INSTALLED carbonio-core\n2: CONFIGURED END\n"), 0o644)

	var applied []string

	var out, errb strings.Builder

	status := runSetup(e, strings.NewReader(""), &out, &errb, runTestOpts(), Deps{Host: host{}}, runTestSteps(&applied))

	assert.Equal(t, 1, status)
	assert.Contains(t, errb.String(), "Error: reading the node:")
	assert.Empty(t, applied)
}

func TestRunSetup_UnreadableHistoryIsReported(t *testing.T) {
	e, f := runTestEnv(t)
	f.FS.Fail = func(op, path string) error {
		if op == "ReadFile" && path == e.Paths.History {
			return errors.New("input/output error")
		}

		return nil
	}

	var applied []string

	var errb strings.Builder

	status := runSetup(e, strings.NewReader(""), &strings.Builder{}, &errb, runTestOpts(), Deps{Host: host{}},
		runTestSteps(&applied))

	assert.Equal(t, 1, status)
	assert.Contains(t, errb.String(), "reading the node: input/output error")
	assert.Empty(t, applied, "an unreadable history must not be taken for a new install")
}

func TestRunSetup_LegacyAndConfigConflictIsReported(t *testing.T) {
	e, _ := runTestEnv(t)

	opts := runTestOpts()
	opts.ConfigPath, opts.LegacyPath = "a.yaml", "b.conf"

	var errb strings.Builder

	status := runSetup(e, strings.NewReader(""), &strings.Builder{}, &errb, opts, Deps{Host: host{}}, nil)

	assert.Equal(t, 1, status)
	assert.Contains(t, errb.String(), "mutually exclusive")
}

func TestRunSetup_EnabledHostReportsTheEnabledPackages(t *testing.T) {
	h := enabledHost{Host: host{}, roles: []string{"mta"}}

	assert.Equal(t, []string{"mta"}, h.InstalledRoles())
	assert.Equal(t, "mail.example.com", h.FQDN(), "the other answers come from the node")
}

func TestRunSetup_RunSetupRefusesNonRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root")
	}

	f := systest.New()

	var errb strings.Builder

	status := RunSetup(context.Background(), f.System(), strings.NewReader(""), &strings.Builder{}, &errb,
		&RunOptions{})

	assert.Equal(t, 1, status)
	assert.Contains(t, errb.String(), "must run as root")
	assert.Empty(t, f.Exec.Lines(), "nothing runs before the refusal")
}

func TestRunSetup_DryRunWithoutRootLogsNothingToDisk(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CARBONIO_BASE_DIR", base)

	f := systest.New()
	f.FS.Put("/etc/os-release", []byte("ID=rocky\n"), 0o644)
	f.Exec.OnPath("rpm", systest.Reply(
		"carbonio-core install ok installed\ncarbonio-directory-server install ok installed\n"))
	f.Exec.OnPath("hostname", systest.Reply("mail.example.com\n"))

	var out, errb strings.Builder

	opts := &RunOptions{DryRun: true, Options: Options{
		Wizard: WizardMissing, NonInteractive: true, Environ: runTestEnviron,
		Sets: []string{"ldap.host=mail.example.com", "ldap.admin_password=ldapSecret1"},
	}}

	status := RunSetup(context.Background(), f.System(), strings.NewReader(""), &out, &errb, opts)

	require.Equal(t, 0, status, errb.String())
	assert.Contains(t, out.String(), "STEP")
	assert.NotContains(t, out.String(), "Logging to")

	entries, err := os.ReadDir(base)
	require.NoError(t, err)
	assert.Empty(t, entries, "a dry run creates no log file")
}
