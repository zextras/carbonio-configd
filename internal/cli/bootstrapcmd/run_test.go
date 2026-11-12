// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrapcmd

import (
	"context"
	"errors"
	"io"
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

	status := runSetup(e, strings.NewReader(""), &out, &errb, runTestOpts(), &Deps{Host: host{}}, runTestSteps(&applied))

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

	status := runSetup(e, strings.NewReader(""), &out, &errb, runTestOpts(), &Deps{Host: host{}}, steps)

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

	status := runSetup(e, strings.NewReader(""), &out, &errb, opts, &Deps{Host: host{}}, runTestSteps(&applied))

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

	status := runSetup(e, strings.NewReader(""), &out, &errb, opts, &Deps{Host: host{}}, steps)

	require.Equal(t, 0, status, errb.String())
	assert.Contains(t, out.String(), "would apply: cannot check yet: LDAP not up")
}

func TestRunSetup_InvalidConfigurationRunsNothing(t *testing.T) {
	e, _ := runTestEnv(t)

	var applied []string

	opts := runTestOpts()
	opts.Environ = append(opts.Environ, "CARBONIO_BOOTSTRAP_ROLES=bogus-role")

	var out, errb strings.Builder

	status := runSetup(e, strings.NewReader(""), &out, &errb, opts, &Deps{Host: host{}}, runTestSteps(&applied))

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

	status := runSetup(e, strings.NewReader(""), &out, &errb, opts, &Deps{Host: host{}}, runTestSteps(&applied))

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

	status := runSetup(e, strings.NewReader(""), &out, &errb, opts, &Deps{Host: host{}}, runTestSteps(&applied))

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

	status := runSetup(e, strings.NewReader(""), &out, &errb, runTestOpts(), &Deps{Host: host{}}, runTestSteps(&applied))

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

	status := runSetup(e, strings.NewReader(""), &strings.Builder{}, &errb, runTestOpts(), &Deps{Host: host{}},
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

	status := runSetup(e, strings.NewReader(""), &strings.Builder{}, &errb, opts, &Deps{Host: host{}}, nil)

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

func TestRunSetup_PlainOutputIsAHeaderStepLinesAndASummary(t *testing.T) {
	e, _ := runTestEnv(t)
	f := e.Sys.FS

	require.NoError(t, f.WriteFileAtomic(e.Paths.Path(".version"), []byte("26.12.0\n"), 0o644))

	steps := []setup.Step{
		{Title: "Create SSL certificates", Apply: func(e *setup.Env) error {
			e.Log.Info("carbonio-appserver certificate created")

			return nil
		}},
		{Title: "Check port conflicts", Done: func(*setup.Env) (bool, error) { return true, nil }},
		{Title: "Create GAL sync account", Apply: func(e *setup.Env) error {
			e.Log.Warn("skipped: services not started")

			return nil
		}},
		{Title: "Not applicable", When: func(*setup.Env) bool { return false }},
	}

	var out, errb strings.Builder

	status := runSetup(e, strings.NewReader(""), &out, &errb, runTestOpts(), &Deps{Host: host{}, LogPath: "/opt/zextras/log/b.log"},
		steps)

	require.Equal(t, 0, status, errb.String())

	got := out.String()
	assert.Contains(t, got, "Carbonio Bootstrap 26.12.0 · mail.example.com · new install\n")
	assert.Contains(t, got, "Log:     /opt/zextras/log/b.log\n")
	assert.Regexp(t, `\[1/3\] Create SSL certificates +ok +\d`, got)
	assert.Contains(t, got, "      · carbonio-appserver certificate created\n")
	assert.Regexp(t, `\[2/3\] Check port conflicts +skip +already done\n`, got)
	assert.Regexp(t, `\[3/3\] Create GAL sync account +warn +skipped: services not started\n`, got)
	assert.NotContains(t, got, "Not applicable", "a step that does not apply is not listed")
	assert.Regexp(t, `Done in [\d.:]+s? · 2 applied · 1 already done · 1 warning\n`, got)
	assert.Contains(t, got, "Warnings:\n  · Create GAL sync account: skipped: services not started\n")
	assert.NotContains(t, got, "Admin console", "no proxy on this node")
	assert.NotContains(t, got, "\x1b", "piped output has no colour")
	assert.NotContains(t, got, "\r")
	assert.NotContains(t, got+errb.String(), "Sup3rSecret!")
}

func TestRunSetup_ServicesNotStartedTellsHowToStartThem(t *testing.T) {
	e, _ := runTestEnv(t)

	var applied []string

	opts := runTestOpts()
	opts.Sets = []string{"start_services=false"}

	var out, errb strings.Builder

	status := runSetup(e, strings.NewReader(""), &out, &errb, opts, &Deps{Host: host{}}, runTestSteps(&applied))

	require.Equal(t, 0, status, errb.String())
	assert.Contains(t, out.String(), "Services not started (start_services: no). Start them with: ")
}

func TestRunSetup_FailureLineAndSummary(t *testing.T) {
	e, _ := runTestEnv(t)

	var applied []string

	steps := runTestSteps(&applied)
	steps[1].Apply = func(*setup.Env) error { return errors.New("zmprov exploded") }

	var out, errb strings.Builder

	status := runSetup(e, strings.NewReader(""), &out, &errb, runTestOpts(), &Deps{Host: host{}}, steps)

	assert.Equal(t, 1, status)
	assert.Regexp(t, `\[2/2\] second +FAIL +[\d.]+s\n +· zmprov exploded\n`, out.String())
	assert.Contains(t, errb.String(), "Failed at step 2/2: second\n  zmprov exploded\n")
	assert.Contains(t, errb.String(), "Fix the cause and run carbonio-bootstrap again: the steps already done are skipped.")
	assert.NotContains(t, out.String(), "Done in")
}

func TestRunSetup_TerminalOutputIsColoured(t *testing.T) {
	e, _ := runTestEnv(t)

	var applied []string

	var out, errb strings.Builder

	status := runSetup(e, strings.NewReader(""), &out, &errb, runTestOpts(),
		&Deps{Host: host{}, Display: Display{Color: true}}, runTestSteps(&applied))

	require.Equal(t, 0, status, errb.String())
	assert.Contains(t, out.String(), "\x1b[38;")
	assert.NotContains(t, out.String(), "\r", "no live line without Live")
}

func TestRunSetup_LegacyKeysNotImportedAreCountedAndListedWithVerbose(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		e, fakes := runTestEnv(t)

		legacy := "/opt/product.config"
		fakes.FS.Put(legacy, []byte("EXPANDMENU=\"no\"\nSMTPHOST=\"smtp.example.org\"\n"), 0o600)

		var applied []string

		opts := runTestOpts()
		opts.LegacyPath, opts.Verbose = legacy, verbose

		var out, errb strings.Builder

		log := &strings.Builder{}
		e.Log = setup.NewLog(io.Discard, log)

		status := runSetup(e, strings.NewReader(""), &out, &errb, opts,
			&Deps{Host: host{}, Drops: &LegacyDrops{}, LogPath: "/l.log"}, runTestSteps(&applied))

		require.Equal(t, 0, status, errb.String())
		assert.Contains(t, out.String(), "Config:  "+legacy+" (legacy) · ")
		assert.Contains(t, log.String(), "legacy key EXPANDMENU not imported: obsolete", "the log file lists them always")
		assert.Empty(t, errb.String(), "nothing goes to stderr")

		if verbose {
			assert.Contains(t, out.String(), "1 key not imported:\n         · EXPANDMENU: obsolete\n")
		} else {
			assert.Contains(t, out.String(), "1 key not imported, see log\n")
			assert.NotContains(t, out.String(), "EXPANDMENU")
		}
	}
}

func TestDisplayOf(t *testing.T) {
	assert.Equal(t, Display{}, displayOf(&strings.Builder{}, nil), "not a terminal: plain lines")

	f, err := os.Open(os.DevNull)
	require.NoError(t, err)

	defer func() { _ = f.Close() }()

	assert.Equal(t, Display{}, displayOf(f, nil), "a file is not a terminal")
}
