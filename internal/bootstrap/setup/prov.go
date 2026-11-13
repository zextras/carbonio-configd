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
	"sync"

	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/provision"
	"github.com/zextras/carbonio-configd/internal/redact"
)

// Prov is a batch of zmprov commands (cs, cd, ca, aaa, mcf, ms, mc). RunProv
// runs them natively over LDAP, with the checks and the entries of the Java
// provisioning (see internal/provision): no zmprov process, no JVM, and the
// passwords never on a command line.
type Prov struct {
	cmds [][]string
}

// Add appends one zmprov command, e.g. Add("mcf", "zimbraFoo", "bar").
func (p *Prov) Add(args ...string) { p.cmds = append(p.cmds, slices.Clone(args)) }

// Len is the number of commands.
func (p *Prov) Len() int { return len(p.cmds) }

// provWhat is the logged form of a command: the verb and its first argument
// only, as the rest may be a password.
func provWhat(args []string) string {
	if len(args) > 1 {
		return args[0] + " " + args[1]
	}

	return args[0]
}

// ErrProv is returned when a command of a batch failed.
var ErrProv = errors.New("provisioning failed")

// provDirectory is the LDAP access of a batch: the client of the node.
type provDirectory interface {
	provision.Directory
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

// provOpenLDAP opens the directory of the node; tests replace it.
var provOpenLDAP = func(e *Env) (provDirectory, error) {
	c, err := e.LDAP()
	if err != nil {
		return nil, err
	}

	return ldapDirectory{c}, nil
}

// provSchemas keeps the attribute schema each Env read, so a run of many
// commands parses it once.
var provSchemas sync.Map // *Env -> *provision.Schema

// provSchema is the attribute schema of the node, conf/attrs/*.xml.
func (e *Env) provSchema() (*provision.Schema, error) {
	if s, ok := provSchemas.Load(e); ok {
		return s.(*provision.Schema), nil
	}

	s, err := provision.LoadSchema(e.Sys.FS, e.Paths.Path("conf", "attrs"))
	if err != nil {
		return nil, err
	}

	provSchemas.Store(e, s)

	return s, nil
}

// provisioner builds the provisioner of a batch over dir.
func (e *Env) provisioner(dir provision.Directory) (*provision.Provisioner, error) {
	schema, err := e.provSchema()
	if err != nil {
		return nil, err
	}

	host, err := e.LCValue("zimbra_server_hostname")
	if err != nil {
		return nil, err
	}

	return provision.New(provision.Options{
		Dir:            dir,
		Schema:         schema,
		LocalHostname:  host,
		ReadFile:       e.Sys.FS.ReadFile,
		SetLocalConfig: func(key, value string) error { return e.SetLC(map[string]string{key: value}) },
		Warn:           e.Log.Warn,
	}), nil
}

// RunProv runs the batch, in order, against the directory. Like zmprov, which
// goes on after a command failed and exits 2 at the end (ProvUtil
// errorOccursDuringInteraction), RunProv runs every command and the error lists
// the failures. The commands are not logged with their values.
func (e *Env) RunProv(p *Prov) error {
	if p.Len() == 0 {
		return nil
	}

	e.Log.Detail("provisioning batch: %s", provDescribe(p.cmds))

	dir, err := e.provDirectory()
	if err != nil {
		return fmt.Errorf("%w: opening LDAP: %w", ErrProv, err)
	}

	defer func() { _ = dir.Close() }()

	prv, err := e.provisioner(dir)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrProv, err)
	}

	var msgs []string

	for _, cmd := range p.cmds {
		if _, err := prv.Exec(cmd); err != nil {
			msgs = append(msgs, fmt.Sprintf("ERROR: %s: %v", provWhat(cmd), err))
		}
	}

	if len(msgs) == 0 {
		return nil
	}

	masked, _ := redact.Text(strings.Join(msgs, "; "))

	return fmt.Errorf("%w: %s", ErrProv, masked)
}

// provDescribe is the log form of a batch: "cs host; ms host".
func provDescribe(cmds [][]string) string {
	whats := make([]string, len(cmds))
	for i, c := range cmds {
		whats[i] = provWhat(c)
	}

	return strings.Join(whats, "; ")
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
