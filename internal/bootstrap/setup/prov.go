// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/redact"
)

// Prov is a batch of zmprov commands, run by one zmprov process (one JVM)
// that reads them on stdin: setup.pl started one zmprov per command, with the
// passwords on its command line.
type Prov struct {
	lines []string
	// what are the logged forms: the verb and its first argument only, as the
	// rest may be a password.
	what []string
}

// Add appends one zmprov command, e.g. Add("mcf", "zimbraFoo", "bar").
func (p *Prov) Add(args ...string) {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = provQuote(a)
	}

	p.lines = append(p.lines, strings.Join(quoted, " "))

	what := args[0]
	if len(args) > 1 {
		what += " " + args[1]
	}

	p.what = append(p.what, what)
}

// Len is the number of commands.
func (p *Prov) Len() int { return len(p.lines) }

// provQuote quotes s for zmprov's line parser (StringUtil.parseLine): double
// quotes, with backslash and double quote escaped and control characters
// spelled \n \r \t.
func provQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)

	return `"` + r.Replace(s) + `"`
}

// ErrProv is returned when a zmprov command of a batch failed.
var ErrProv = errors.New("zmprov failed")

// RunProv runs the batch with "zmprov -r -m -l" as zextras. The batch ends
// with "exit", so zmprov exits 2 when any command failed (ProvUtil
// errorOccursDuringInteraction); the error then lists zmprov's ERROR lines.
// The commands are not logged with their values.
func (e *Env) RunProv(p *Prov) error {
	if p.Len() == 0 {
		return nil
	}

	e.Log.Detail("zmprov batch: %s", strings.Join(p.what, "; "))

	input := strings.Join(p.lines, "\n") + "\nexit\n"

	_, err := e.Output(&Cmd{
		AsZextras: true,
		Path:      e.Paths.Path("bin", "zmprov"),
		Args:      []string{"-r", "-m", "-l"},
		Stdin:     strings.NewReader(input),
		Quiet:     true,
	})
	if err == nil {
		return nil
	}

	var cmdErr *CmdError
	if !errors.As(err, &cmdErr) {
		return fmt.Errorf("%w: %w", ErrProv, err)
	}

	var msgs []string

	for line := range strings.SplitSeq(cmdErr.Output, "\n") {
		if i := strings.Index(line, "ERROR: "); i >= 0 {
			msgs = append(msgs, line[i:])
		}
	}

	if len(msgs) == 0 {
		return fmt.Errorf("%w: exit status %d", ErrProv, cmdErr.Code)
	}

	masked, _ := redact.Text(strings.Join(msgs, "; "))

	return fmt.Errorf("%w: %s", ErrProv, masked)
}

// Prov runs the single zmprov command args (see RunProv).
func (e *Env) Prov(args ...string) error {
	var p Prov

	p.Add(args...)

	return e.RunProv(&p)
}

// LC returns the effective localconfig (defaults, localconfig.xml,
// CONFIGD_LC_* environment), reloaded after every SetLC.
func (e *Env) LC() (map[string]string, error) {
	if e.lc == nil {
		lc, err := localconfig.LoadEffectiveFrom(e.Paths.LocalConfig, nil)
		if err != nil {
			return nil, err
		}

		e.lc = lc
	}

	return e.lc, nil
}

// LCValue returns one key of the effective localconfig ("" when unset).
func (e *Env) LCValue(key string) (string, error) {
	lc, err := e.LC()
	if err != nil {
		return "", err
	}

	return lc[key], nil
}

// SetLC writes key/value pairs to localconfig.xml in one write, skipping
// the keys that already hold the value. Values are not logged when the key
// is a secret.
func (e *Env) SetLC(pairs map[string]string) error {
	current, err := localconfig.LoadLocalConfigFromFile(e.Paths.LocalConfig)
	if err != nil {
		current = map[string]string{}
	}

	var keys []localconfig.Key

	for _, k := range sortedKeys(pairs) {
		if v, ok := current[k]; ok && v == pairs[k] {
			continue
		}

		keys = append(keys, localconfig.Key{Name: k, Value: pairs[k]})
		e.Log.Detail("Setting local config %s", k)
	}

	if len(keys) == 0 {
		return nil
	}

	e.lc = nil

	return localconfig.SetKeys(e.Paths.LocalConfig, keys)
}

// UnsetLC removes keys from localconfig.xml (absent keys are ignored).
func (e *Env) UnsetLC(keys ...string) error {
	e.lc = nil

	err := localconfig.RemoveKeys(e.Paths.LocalConfig, keys)

	if _, ok := errors.AsType[*localconfig.KeyNotSetError](err); ok {
		return nil
	}

	return err
}

// LDAP opens a client to the LDAP master with the credentials of the
// effective localconfig. The caller closes it.
func (e *Env) LDAP() (*ldap.Client, error) {
	lc, err := e.LC()
	if err != nil {
		return nil, err
	}

	params, err := ldap.ParamsFrom(e.Ctx, maps.Clone(lc), ldap.PurposeWrite, nil)
	if err != nil {
		return nil, fmt.Errorf("LDAP parameters: %w", err)
	}

	return ldap.NewClient(params)
}

func sortedKeys(m map[string]string) []string {
	return slices.Sorted(maps.Keys(m))
}
