// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/redact"
)

// Prov is a batch of zmprov commands. RunProv writes the modifications of
// allowlisted attributes (provAttrList) to LDAP itself and sends everything
// else to zmprov, which reads its commands on stdin: one JVM for a run of
// commands, where setup.pl started one zmprov per command, with the passwords
// on its command line.
type Prov struct {
	cmds [][]string
}

// Add appends one zmprov command, e.g. Add("mcf", "zimbraFoo", "bar").
func (p *Prov) Add(args ...string) { p.cmds = append(p.cmds, slices.Clone(args)) }

// Len is the number of commands.
func (p *Prov) Len() int { return len(p.cmds) }

// provQuote quotes s for zmprov's line parser (StringUtil.parseLine): double
// quotes, with backslash and double quote escaped and control characters
// spelled \n \r \t.
func provQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)

	return `"` + r.Replace(s) + `"`
}

// provLine is the zmprov input line of one command.
func provLine(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = provQuote(a)
	}

	return strings.Join(quoted, " ")
}

// provWhat is the logged form of a command: the verb and its first argument
// only, as the rest may be a password.
func provWhat(args []string) string {
	if len(args) > 1 {
		return args[0] + " " + args[1]
	}

	return args[0]
}

// ErrProv is returned when a command of a batch failed.
var ErrProv = errors.New("zmprov failed")

// provDirectory is the LDAP access of a batch: the client of the node.
type provDirectory interface {
	BaseDN() string
	ModifyValues(dn string, add, del, replace map[string][]string) error
	Close() error
}

// provDirectory opens the directory for RunProv: the Env's own opener when
// set (tests), else provOpenLDAP.
func (e *Env) provDirectory() (provDirectory, error) {
	if e.provOpen != nil {
		return e.provOpen(e)
	}

	return provOpenLDAP(e)
}

// provOpenLDAP opens the directory for the commands that RunProv does not
// give to zmprov; tests replace it.
var provOpenLDAP = func(e *Env) (provDirectory, error) {
	c, err := e.LDAP()
	if err != nil {
		return nil, err
	}

	return c, nil
}

// provRun is a run of consecutive commands that go the same way: all to LDAP
// (changes holds them, parsed) or all to zmprov (cmds).
type provRun struct {
	cmds    [][]string
	changes []provChange
}

func (r provRun) viaLDAP() bool { return len(r.changes) > 0 }

// provPlan splits the batch, in order, into runs: the commands that modify
// allowlisted attributes go to LDAP, the others to zmprov.
func (e *Env) provPlan(p *Prov) []provRun {
	var runs []provRun

	for _, cmd := range p.cmds {
		change, ok := e.provParse(cmd)

		last := len(runs) - 1
		if last < 0 || runs[last].viaLDAP() != ok {
			runs = append(runs, provRun{})
			last++
		}

		runs[last].cmds = append(runs[last].cmds, cmd)

		if ok {
			runs[last].changes = append(runs[last].changes, change)
		}
	}

	return runs
}

// provDescribe is the log form of a plan: "ldap: mcf zimbraFoo | zmprov: cs host".
func provDescribe(runs []provRun) string {
	parts := make([]string, len(runs))

	for i, r := range runs {
		whats := make([]string, len(r.cmds))
		for j, c := range r.cmds {
			whats[j] = provWhat(c)
		}

		via := "zmprov"
		if r.viaLDAP() {
			via = "ldap"
		}

		parts[i] = via + ": " + strings.Join(whats, "; ")
	}

	return strings.Join(parts, " | ")
}

// RunProv runs the batch, in order. The commands that modify only allowlisted
// attributes (mcf, ms and mc, see provAttrList) are written to LDAP; the
// others, the creation commands among them, are run by "zmprov -r -m -l" as
// zextras, one process per run of consecutive commands. Like zmprov, which
// goes on after a command failed and exits 2 at the end (ProvUtil
// errorOccursDuringInteraction), RunProv runs every command and the error
// lists the failures. The commands are not logged with their values.
func (e *Env) RunProv(p *Prov) error {
	if p.Len() == 0 {
		return nil
	}

	runs := e.provPlan(p)
	e.Log.Detail("zmprov batch: %s", provDescribe(runs))

	var (
		msgs []string
		dir  provDirectory
	)

	defer func() {
		if dir != nil {
			_ = dir.Close()
		}
	}()

	for _, r := range runs {
		if !r.viaLDAP() {
			m, err := e.provZmprov(r.cmds)
			if err != nil {
				return err
			}

			msgs = append(msgs, m...)

			continue
		}

		if dir == nil {
			var err error

			if dir, err = e.provDirectory(); err != nil {
				msgs = append(msgs, fmt.Sprintf("ERROR: opening LDAP: %v", err))

				continue
			}
		}

		msgs = append(msgs, provModify(dir, r)...)
	}

	if len(msgs) == 0 {
		return nil
	}

	masked, _ := redact.Text(strings.Join(msgs, "; "))

	return fmt.Errorf("%w: %s", ErrProv, masked)
}

// provModify writes the changes of a run and returns the failures.
func provModify(dir provDirectory, r provRun) []string {
	var msgs []string

	for i, c := range r.changes {
		if err := dir.ModifyValues(c.dn(dir.BaseDN()), c.add, c.del, c.replace); err != nil {
			msgs = append(msgs, fmt.Sprintf("ERROR: ldap: %s: %v", provWhat(r.cmds[i]), err))
		}
	}

	return msgs
}

// provZmprov runs the commands with one zmprov process and returns its ERROR
// lines (or its exit status when it printed none). The input ends with
// "exit", so zmprov exits 2 when any command failed.
func (e *Env) provZmprov(cmds [][]string) ([]string, error) {
	lines := make([]string, len(cmds))
	for i, c := range cmds {
		lines[i] = provLine(c)
	}

	_, err := e.Output(&Cmd{
		AsZextras: true,
		Path:      e.Paths.Path("bin", "zmprov"),
		Args:      []string{"-r", "-m", "-l"},
		Stdin:     strings.NewReader(strings.Join(lines, "\n") + "\nexit\n"),
		Quiet:     true,
	})
	if err == nil {
		return nil, nil
	}

	var cmdErr *CmdError
	if !errors.As(err, &cmdErr) {
		return nil, fmt.Errorf("%w: %w", ErrProv, err)
	}

	var msgs []string

	for line := range strings.SplitSeq(cmdErr.Output, "\n") {
		if i := strings.Index(line, "ERROR: "); i >= 0 {
			msgs = append(msgs, line[i:])
		}
	}

	if len(msgs) == 0 {
		msgs = []string{fmt.Sprintf("exit status %d", cmdErr.Code)}
	}

	return msgs, nil
}

// Prov runs the single command args (see RunProv).
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

// LDAP opens a client to the LDAP master with the credentials of the
// effective localconfig. The caller closes it.
func (e *Env) LDAP() (*ldap.Client, error) {
	return e.ldapClient(context.Background())
}

// ldapClient is LDAP with ctx as the lifecycle context of the client, which
// also carries the logger of its connection warnings.
func (e *Env) ldapClient(ctx context.Context) (*ldap.Client, error) {
	lc, err := e.LC()
	if err != nil {
		return nil, err
	}

	params, err := ldap.ParamsFrom(e.Ctx, maps.Clone(lc), ldap.PurposeWrite, nil)
	if err != nil {
		return nil, fmt.Errorf("LDAP parameters: %w", err)
	}

	params.Context = ctx

	return ldap.NewClient(params)
}

func sortedKeys(m map[string]string) []string {
	return slices.Sorted(maps.Keys(m))
}
