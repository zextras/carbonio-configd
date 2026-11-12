// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrapcmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/bootstrap/setup"
)

// The log alone must tell what ran, with which configuration, and how it
// ended: a previous run that put mailboxd on port 80 left no trace of where
// that value came from.
func TestRunSetup_LogTellsInputsConfigTimingsAndResult(t *testing.T) {
	e, _ := runTestEnv(t)

	var file, out, errb strings.Builder

	e.Log = setup.NewLog(&out, &file)

	var applied []string

	status := runSetup(e, strings.NewReader(""), &out, &errb, runTestOpts(), &Deps{Host: host{}}, runTestSteps(&applied))
	require.Equal(t, 0, status, errb.String())

	log := file.String()
	assert.Contains(t, log, "carbonio-bootstrap ")
	assert.Contains(t, log, "Input: 2 environment keys, no wizard")
	assert.Contains(t, log, "Node: mail.example.com, new install")
	assert.Contains(t, log, "Enabled roles: directory-server")
	assert.Regexp(t, `  http: 8080 # default`, log, "every resolved key is logged with its source")
	assert.NotContains(t, log, "Sup3rSecret!", "passwords are masked")
	assert.Regexp(t, `first: done in \d`, log)
	assert.Contains(t, log, "Summary: 2 applied, 0 already done, 0 with warnings, in ")
	assert.Contains(t, log, "Configuration complete.")
}

func TestRunSetup_LogEndsWithTheFailure(t *testing.T) {
	e, _ := runTestEnv(t)

	var file, out, errb strings.Builder

	e.Log = setup.NewLog(&out, &file)

	var applied []string

	steps := runTestSteps(&applied)
	steps[1].Apply = func(*setup.Env) error { return errors.New("mailboxd did not open localhost:80") }

	require.Equal(t, 1, runSetup(e, strings.NewReader(""), &out, &errb, runTestOpts(), &Deps{Host: host{}}, steps))

	log := file.String()
	assert.Regexp(t, `second: failed after \d.*: mailboxd did not open`, log)
	assert.Contains(t, log, "Summary: 1 applied")
	assert.Contains(t, log, "Result: failed: ")
	assert.NotContains(t, log, "Configuration complete.")
}

func TestInputsOf(t *testing.T) {
	opts := &RunOptions{Options: Options{
		LegacyPath: "/opt/product.config", ConfigPath: "/root/node.yaml", Sets: []string{"a=b"},
		Environ: []string{"CARBONIO_BOOTSTRAP_X=1", "PATH=/bin"}, Wizard: WizardAll,
	}}

	assert.Equal(t, []string{"legacy file /opt/product.config", "file /root/node.yaml", "1 environment key",
		"1 --set value", "wizard (every key)"}, inputsOf(opts, &Deps{Terminal: true}))

	opts.Wizard = WizardMissing
	assert.Equal(t, "wizard (missing keys)", inputsOf(opts, &Deps{Terminal: true})[4])
	assert.Equal(t, "no wizard", inputsOf(opts, &Deps{})[4], "no terminal, no wizard")
}
