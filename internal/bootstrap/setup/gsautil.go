// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/zextras/carbonio-configd/internal/provision"
	"github.com/zextras/carbonio-configd/internal/zxadmin"
)

// gsautilUsage is the usage text of com.zimbra.cs.util.GalSyncAccountUtil,
// printed to the standard output.
const gsautilUsage = "zmgsautil: {command}\n" +
	"\tcreateAccount " + gsautilCreateArgs + " -s {server}" + gsautilFolderPolling + "\n" +
	"\taddDataSource " + gsautilCreateArgs + gsautilFolderPolling + "\n" +
	"\tdeleteAccount [-a {account-name} | -i {account-id}]\n" +
	"\ttrickleSync " + gsautilSyncArgs + "\n" +
	"\tfullSync " + gsautilSyncArgs + "\n" +
	"\tforceSync " + gsautilSyncArgs + "\n"

// The parts the lines of the usage text share.
const (
	gsautilCreateArgs    = "-a {account-name} -n {datasource-name} --domain {domain-name} -t zimbra|ldap"
	gsautilSyncArgs      = "[-a {account-name} | -i {account-id}] [-d {datasource-id}] [-n {datasource-name}]"
	gsautilFolderPolling = " [-f {folder-name}] [-p {polling-interval}]"
)

// The options of zmgsautil (short name, long name): every one takes an
// argument, "-h" included.
var gsautilOptions = [...][2]string{
	{optAccount, "account"}, {optID, "id"}, {optName, gsautilLongName}, {optDID, "did"}, {optDomain, "domain"},
	{optFolder, "folder"}, {optPolling, "polling"}, {optType, "type"}, {optServer, "server"}, {optHelp, "help"},
}

// The short names of the options the commands read.
const (
	optAccount = "a"
	optID      = "i"
	optName    = "n"
	optDID     = "d"
	optDomain  = "x"
	optFolder  = "f"
	optPolling = "p"
	optType    = "t"
	optServer  = "s"
	optHelp    = "h"

	gsautilLongName = "name"

	// The types of GAL a data source can synchronise.
	gsautilZimbra = "zimbra"
	gsautilLDAP   = "ldap"
)

// gsautilOpts are the options of one command line: the first value of each
// option given.
type gsautilOpts map[string]string

func (o gsautilOpts) has(opt string) bool {
	_, ok := o[opt]

	return ok
}

// gsautilKey is the short name of the option a token names (with or without
// its leading hyphens, as commons-cli Options.hasOption).
func gsautilKey(token string) (string, bool) {
	name := strings.TrimPrefix(token, "--")
	if name == token {
		name = strings.TrimPrefix(token, "-")
	}

	for _, o := range gsautilOptions {
		if name == o[0] || name == o[1] {
			return o[0], true
		}
	}

	return "", false
}

// isGsautilOption is a token that names an option: it starts with a hyphen.
func isGsautilOption(token string) bool {
	_, ok := gsautilKey(token)

	return ok && strings.HasPrefix(token, "-")
}

// gsautilFlatten splits the "--name=value" and "-xvalue" forms into the option
// and its value, as commons-cli GnuParser.flatten does.
func gsautilFlatten(args []string) []string {
	tokens := make([]string, 0, len(args))

	for i, arg := range args {
		switch {
		case arg == "--":
			return append(tokens, args[i:]...)
		case arg == "-" || !strings.HasPrefix(arg, "-"):
			tokens = append(tokens, arg)
		default:
			tokens = append(tokens, gsautilSplitOption(arg)...)
		}
	}

	return tokens
}

// gsautilSplitOption is the tokens of an argument that starts with a hyphen.
func gsautilSplitOption(arg string) []string {
	if _, ok := gsautilKey(arg); ok {
		return []string{arg}
	}

	if name, value, found := strings.Cut(arg, "="); found {
		if _, ok := gsautilKey(name); ok {
			return []string{name, value}
		}
	}

	if _, ok := gsautilKey(arg[:2]); ok {
		return []string{arg[:2], arg[2:]}
	}

	return []string{arg}
}

// stripQuotes removes the double quotes around a value that has no other
// (commons-cli Util.stripLeadingAndTrailingQuotes).
func stripQuotes(s string) string {
	if n := len(s); n > 1 && s[0] == '"' && s[n-1] == '"' && !strings.Contains(s[1:n-1], `"`) {
		return s[1 : n-1]
	}

	return s
}

// parseGsautilArgs parses the command line like commons-cli's GnuParser: each
// option takes the following token as its value unless that is an option
// itself, the first occurrence of an option wins, and anything that is not an
// option is skipped. The error is the commons-cli ParseException message.
func parseGsautilArgs(args []string) (gsautilOpts, error) {
	tokens := gsautilFlatten(args)
	opts := gsautilOpts{}

	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if token == "--" {
			break
		}

		if !strings.HasPrefix(token, "-") || token == "-" {
			continue
		}

		key, ok := gsautilKey(token)
		if !ok {
			return nil, fmt.Errorf("Unrecognized option: %s", token) //nolint:staticcheck // the commons-cli message
		}

		if i+1 >= len(tokens) || isGsautilOption(tokens[i+1]) {
			return nil, fmt.Errorf("Missing argument for option: %s", key) //nolint:staticcheck // the commons-cli message
		}

		i++

		if !opts.has(key) {
			opts[key] = stripQuotes(tokens[i])
		}
	}

	return opts, nil
}

// galSyncUtil is one run of zmgsautil.
type galSyncUtil struct {
	e      *Env
	stdout io.Writer
	stderr io.Writer
}

// RunGalSyncUtil runs "setup gsautil": the port of
// com.zimbra.cs.util.GalSyncAccountUtil, which carbonio-core's zmgsautil
// calls. args is the command and its options (see gsautilUsage); the output and
// the exit status are the tool's: 1 after the usage text, 0 after success or
// after an error the server or the directory reported ("Error: <message>").
func RunGalSyncUtil(e *Env, args []string, stdout, stderr io.Writer) int {
	g := &galSyncUtil{e: e, stdout: stdout, stderr: stderr}

	if len(args) < 1 {
		return g.usage()
	}

	opts, err := parseGsautilArgs(args)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "error: %s\n", err)

		return g.usage()
	}

	if opts.has(optHelp) {
		return g.usage()
	}

	switch strings.ToLower(args[0]) {
	case "tricklesync":
		return g.sync(opts, false, false)
	case "fullsync":
		return g.sync(opts, true, false)
	case "forcesync":
		return g.sync(opts, true, true)
	case "createaccount":
		return g.createOrAdd(opts, true)
	case "adddatasource":
		return g.createOrAdd(opts, false)
	case "deleteaccount":
		return g.deleteAccount(opts)
	default:
		return g.usage()
	}
}

func (g *galSyncUtil) usage() int {
	_, _ = io.WriteString(g.stdout, gsautilUsage)

	return 1
}

// serviceError reports an error of the server or of the directory the way
// the tool does ("Error: <message>", status 0), any other (the server cannot
// be reached) on the error output with status 1.
func (g *galSyncUtil) serviceError(err error) int {
	if fault, ok := errors.AsType[*zxadmin.Fault](err); ok {
		_, _ = fmt.Fprintf(g.stdout, "Error: %s\n", fault.Reason)

		return 0
	}

	if perr, ok := errors.AsType[*provision.Error](err); ok {
		_, _ = fmt.Fprintf(g.stdout, "Error: %s\n", perr.Msg)

		return 0
	}

	_, _ = fmt.Fprintf(g.stderr, "zmgsautil: %v\n", err)

	return 1
}

// accountID is the id of the account named name, looked up in the directory.
func (g *galSyncUtil) accountID(name string) (string, error) {
	dir, err := g.e.provDirectory()
	if err != nil {
		return "", fmt.Errorf("opening LDAP: %w", err)
	}

	defer func() { _ = dir.Close() }()

	prv, err := g.e.provisioner(dir)
	if err != nil {
		return "", err
	}

	return prv.AccountID(name)
}

// sync is trickleSync, fullSync and forceSync: the data source of the account,
// by id else by name.
func (g *galSyncUtil) sync(opts gsautilOpts, full, reset bool) int {
	accountID := opts[optID]
	hasAccount := opts.has(optID)

	if opts.has(optAccount) {
		id, err := g.accountID(opts[optAccount])
		if err != nil {
			return g.directoryError(err)
		}

		accountID, hasAccount = id, true
	}

	if !hasAccount || (!opts.has(optDID) && !opts.has(optName)) {
		return g.usage()
	}

	admin, err := svcOpenGAL(g.e)
	if err != nil {
		return g.serviceError(err)
	}

	err = admin.SyncGalAccount(g.e.Ctx, &zxadmin.GalSyncRequest{
		AccountID: accountID, DataSourceID: opts[optDID], DataSourceName: opts[optName],
		Full: full, Reset: reset,
	})
	if err != nil {
		return g.serviceError(err)
	}

	return 0
}

// directoryError is an error of the account lookup: always reported as
// "Error: <message>" with status 0.
func (g *galSyncUtil) directoryError(err error) int {
	msg := err.Error()
	if perr, ok := errors.AsType[*provision.Error](err); ok {
		msg = perr.Msg
	}

	_, _ = fmt.Fprintf(g.stdout, "Error: %s\n", msg)

	return 0
}

// createOrAdd is createAccount (create: the account is created on the server)
// and addDataSource: both print the name and id of the accounts of the
// answer.
func (g *galSyncUtil) createOrAdd(opts gsautilOpts, create bool) int {
	required := []string{optAccount, optName, optDomain, optType}
	if create {
		required = append(required, optServer)
	}

	for _, o := range required {
		if !opts.has(o) {
			return g.usage()
		}
	}

	if t := opts[optType]; t != gsautilZimbra && t != gsautilLDAP {
		return g.usage()
	}

	admin, err := svcOpenGAL(g.e)
	if err != nil {
		return g.serviceError(err)
	}

	req := &zxadmin.GalSyncAccountRequest{
		Account: opts[optAccount], Name: opts[optName], Domain: opts[optDomain], Type: opts[optType],
		Folder: opts[optFolder], PollingInterval: opts[optPolling],
	}

	var accounts []zxadmin.GalSyncAccount

	if create {
		req.Server = opts[optServer]

		var acct zxadmin.GalSyncAccount

		acct, err = admin.CreateGalSyncAccount(g.e.Ctx, req)
		accounts = []zxadmin.GalSyncAccount{acct}
	} else {
		accounts, err = admin.AddGalSyncDataSource(g.e.Ctx, req)
	}

	if err != nil {
		return g.serviceError(err)
	}

	for _, a := range accounts {
		_, _ = fmt.Fprintf(g.stdout, "%s\t%s\n", a.Name, a.ID)
	}

	return 0
}

// deleteAccount deletes the GAL sync account named or identified.
func (g *galSyncUtil) deleteAccount(opts gsautilOpts) int {
	name, byName := opts[optAccount]
	id, byID := opts[optID]

	if !byName && !byID {
		return g.usage()
	}

	admin, err := svcOpenGAL(g.e)
	if err != nil {
		return g.serviceError(err)
	}

	// The account is sent by name when there is one, by id otherwise.
	account, useID := name, false
	if !byName {
		account, useID = id, true
	}

	if err := admin.DeleteGalSyncAccount(g.e.Ctx, account, useID); err != nil {
		return g.serviceError(err)
	}

	return 0
}
