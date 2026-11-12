// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/zextras/carbonio-configd/internal/getopt"
)

// certProg is the name zmcertmgr gives itself in its messages (basename of
// $0 in the script), whatever name the wrapper that runs it has.
const certProg = "zmcertmgr"

// The options, by their names.
const (
	certOptNew      = "new"
	certOptNewkey   = "newkey"
	certOptKeysize  = "keysize"
	certOptDigest   = "digest"
	certOptSubject  = "subject"
	certOptSANs     = "subjectaltnames"
	certOptDefSAN   = "defaultsubjectaltname"
	certOptDays     = "days"
	certOptAllsvrs  = "allservers"
	certOptLocal    = "localonly"
	certOptDeploy   = "deploy"
	certOptHelp     = "help"
	certOptMan      = "man"
	certOptDebug    = "debug"
	certServicesRef = "[all|ldap|mailboxd|mta|proxy]"
)

// certOptKinds are the kinds of every option of the script (Getopt::Long specs
// "name", "name!", "name=i", "name=s", "name=s@", "name:1").
var certOptKinds = map[string]getopt.Kind{
	certOptNew: getopt.Flag, certOptNewkey: getopt.Flag, certOptKeysize: getopt.Int, certOptDigest: getopt.String,
	certOptSubject: getopt.String, certOptSANs: getopt.StringList, certOptDefSAN: getopt.NegFlag,
	certOptDays: getopt.Int, certOptAllsvrs: getopt.Flag, certOptLocal: getopt.Flag,
	certOptDeploy: getopt.StringList, certOptHelp: getopt.Flag, certOptMan: getopt.Flag, certOptDebug: getopt.OptInt,
}

// certCommand is a command of the script: the options it takes besides -help,
// -man and -debug, whether it takes a type, how many arguments may follow
// and what to tell when they do not fit.
type certCommand struct {
	opts     []string
	needType bool
	xargs    []int
	xinfo    string
}

// certCreateOpts are the options of the commands that create (the script's
// @create_opts).
var certCreateOpts = []string{certOptNew, certOptKeysize, certOptDigest, certOptSubject}

// certCommands is %commands of Opts::_process_options.
var certCommands = map[string]*certCommand{
	"addcacert":     {xargs: []int{1}, xinfo: "<certfile>"},
	certCmdCreateCA: {opts: append(slices.Clone(certCreateOpts), certOptNewkey, certOptSANs)},
	certCmdCheckExpiry: {
		opts: []string{certOptDays}, xargs: []int{0, 1}, xinfo: certServicesRef,
	},
	"createcsr": {needType: true, opts: append(slices.Clone(certCreateOpts), certOptSANs, certOptDefSAN)},
	certCmdCreateCRT: {
		opts: append(slices.Clone(certCreateOpts), certOptSANs, certOptDays, certOptAllsvrs, certOptDefSAN),
	},
	"deployca": {opts: []string{certOptLocal}},
	"deploycrt": {
		needType: true, opts: []string{certOptAllsvrs, certOptDeploy, certOptLocal}, xargs: []int{0, 2},
		xinfo: "<<self>|<comm [certfile ca_chain_file]>> [-localonly] [-allservers] [[-deploy " +
			certServicesRef + "] ...]",
	},
	"getcrt":         {needType: true, opts: []string{certOptAllsvrs}},
	"savecrt":        {needType: true, opts: []string{certOptAllsvrs}},
	certCmdVerifyCRT: {needType: true, xargs: []int{0, 1, 2, 3}, xinfo: "[[[priv_key] [certfile]] [ca_chain_file]]"},
	"verifycrtchain": {
		xargs: []int{2}, xinfo: "<ca_chain_file> <certfile>",
	},
	"verifycrtkey":    {xargs: []int{2}, xinfo: "<priv_key> <certfile>"},
	"viewcsr":         {needType: true, xargs: []int{0, 1}, xinfo: "<csr_file>"},
	certCmdViewStaged: {needType: true, xargs: []int{0, 1}, xinfo: "<crt_file>"},
	"viewdeployedcrt": {xargs: []int{0, 1}, xinfo: certServicesRef},
	"migrate":         {},
}

// certCommandAliases are the names that run another command. "viewcrt" is
// listed in the script as viewstagedcrt, but its method does not exist
// there; it runs viewstagedcrt here.
var certCommandAliases = map[string]string{"viewcrt": certCmdViewStaged}

// certOpts are the command and options of a run (the script's %opt).
type certOpts struct {
	Command string
	Type    string

	Help, Man bool
	// Debug is the -debug level, 0 when off.
	Debug int

	New, Newkey, Allservers, Localonly bool
	// NoDefaultSAN is -noDefaultSubjectAltName.
	NoDefaultSAN bool

	// Keysize, Days, Digest and Subject are what the options gave; the Has
	// flags tell whether the option was there at all (-days 0 was).
	Keysize, Days           int
	HasKeysize, HasDays     bool
	Digest, Subject         string
	HasDigest, HasSubject   bool
	SubjectAltNames, Deploy []string

	// Args are the arguments that remain after the command, the type and the
	// options.
	Args []string
}

// certParse is the result of reading the command line.
type certParse struct {
	opts *certOpts
	// errs are what the script reports before its usage (exit 2).
	errs []string
	// optsOK is false when Getopt::Long complained (its messages are on
	// stderr and the short usage is printed, exit 1).
	optsOK bool
}

// certParseArgs reads the command line as the script does: the command first,
// then the type for the commands that take one, then options mixed with
// arguments (Getopt::Long with permutation). Getopt::Long's complaints are
// written to stderr.
func certParseArgs(args []string, stderr io.Writer) *certParse {
	p := &certParse{opts: &certOpts{}, optsOK: true}
	o := p.opts

	cmd := certCommand{xargs: []int{0}}

	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		p.errs = append(p.errs, "a command must be specified")
	} else if c, ok := certLookupCommand(args[0]); ok {
		cmd = *c
		o.Command, args = certCanonCommand(args[0]), args[1:]

		args = p.takeType(&cmd, args)
	}

	names := append(slices.Clone(cmd.opts), certOptHelp, certOptMan, certOptDebug)

	rest := p.getopt(args, names, stderr)
	o.Args = rest

	p.check(&cmd, rest)

	return p
}

// certLookupCommand finds a command, or the one an alias stands for.
func certLookupCommand(name string) (*certCommand, bool) {
	c, ok := certCommands[certCanonCommand(name)]

	return c, ok
}

// certCanonCommand is the name of the command an alias stands for.
func certCanonCommand(name string) string {
	if to, ok := certCommandAliases[name]; ok {
		return to
	}

	return name
}

// takeType reads the type that follows a command that needs one: only the
// argument right after the command counts.
func (p *certParse) takeType(cmd *certCommand, args []string) []string {
	if !cmd.needType {
		return args
	}

	if len(args) > 0 && (args[0] == certTypeSelf || args[0] == certTypeComm) {
		p.opts.Type = args[0]

		return args[1:]
	}

	p.errs = append(p.errs, p.opts.Command+": type 'self' or 'comm' must be specified")

	return args
}

// getopt is GetOptions with the default configuration of Getopt::Long: it
// reads the options, reports the complaints on stderr and returns the
// arguments that are not options.
func (p *certParse) getopt(args, names []string, stderr io.Writer) []string {
	specs := make([]getopt.Spec, len(names))
	for i, n := range names {
		specs[i] = getopt.Spec{Names: []string{n}, Kind: certOptKinds[n]}
	}

	r := getopt.Parse(args, specs, stderr)
	p.optsOK = r.AllGood
	p.opts.fill(r)

	return r.Rest
}

// fill records the options that were read.
func (o *certOpts) fill(r *getopt.Result) {
	o.New, o.Newkey = r.Has(certOptNew), r.Has(certOptNewkey)
	o.Allservers, o.Localonly = r.Has(certOptAllsvrs), r.Has(certOptLocal)
	o.NoDefaultSAN = r.Negated[certOptDefSAN]
	o.Help, o.Man = r.Has(certOptHelp), r.Has(certOptMan)
	o.Keysize, o.HasKeysize = r.Ints[certOptKeysize], r.Has(certOptKeysize)
	o.Days, o.HasDays = r.Ints[certOptDays], r.Has(certOptDays)
	o.Digest, o.HasDigest = r.Strs[certOptDigest], r.Has(certOptDigest)
	o.Subject, o.HasSubject = r.Strs[certOptSubject], r.Has(certOptSubject)
	o.SubjectAltNames = certSplitAll(r.Lists[certOptSANs])
	o.Deploy = certSplitAll(r.Lists[certOptDeploy])

	if r.Has(certOptDebug) {
		o.Debug = 1
		if !r.Bare[certOptDebug] {
			o.Debug = r.Ints[certOptDebug]
		}
	}
}

// certSplitAll splits each value of a list option at its commas.
func certSplitAll(values []string) []string {
	var out []string

	for _, v := range values {
		out = append(out, certSplitCSV(v)...)
	}

	return out
}

var certCSVRE = regexp.MustCompile(`\s*,\s*`)

// certSplitCSV is split( /\s*,\s*/, $_ ): Perl drops the empty fields at the
// end.
func certSplitCSV(s string) []string {
	parts := certCSVRE.Split(s, -1)

	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}

	return parts
}

// check adds what the script checks once the options are read: the number of
// arguments and -newkey with -new.
func (p *certParse) check(cmd *certCommand, rest []string) {
	o := p.opts

	if !slices.Contains(cmd.xargs, len(rest)) {
		if cmd.xinfo != "" {
			p.errs = append(p.errs, o.Command+" "+cmd.xinfo)
		}

		if len(rest) > 0 {
			p.errs = append(p.errs, "unexpected argument(s): "+strings.Join(rest, " "))
		}
	}

	if o.Newkey && !o.New {
		p.errs = append(p.errs, o.Command+": -newkey can only be used with -new")
	}
}
