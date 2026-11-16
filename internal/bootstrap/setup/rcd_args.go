// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

// The argument alphabets of rcd_commands.go keep shell syntax out; they do not
// keep options out, and rsync, certbot and zmcertmgr have options that run
// programs (-e, --deploy-hook, ...). The checks of this file are the second
// layer: the words of the arguments must be exactly what the real callers send
// (rsync's server side, the mailbox's RemoteCertbot, GenerateCSR, InstallCert
// and zmcertmgr's own getcrt/deploycrt of the other servers), with no option
// that is not listed and no path outside the installation.

// rcdHome is the only tree the paths of the arguments may name.
const rcdHome = "/opt/zextras"

// rcdUnderHome reports whether p is inside rcdHome. A relative path is
// relative to the home of the zextras user, which is where a forced command
// starts; ".." is refused outright rather than resolved, and so is a word that
// would be read as an option.
func rcdUnderHome(p string) bool {
	if p == "" || strings.HasPrefix(p, "-") || slices.Contains(strings.Split(p, "/"), "..") {
		return false
	}

	if !path.IsAbs(p) {
		p = path.Join(rcdHome, p)
	}

	p = path.Clean(p)

	return p == rcdHome || strings.HasPrefix(p, rcdHome+"/")
}

// rcdOptKind is what an option takes.
type rcdOptKind int

const (
	rcdOptFlag   rcdOptKind = iota // no value
	rcdOptInt                      // digits
	rcdOptWord                     // letters and digits
	rcdOptName                     // a host name, a certificate name
	rcdOptEmail                    // an e-mail address
	rcdOptPath                     // a path inside rcdHome
	rcdOptText                     // anything the alphabet of the command let through
	rcdOptNumber                   // a size or a count, with the suffix rsync takes
)

var (
	rcdIntRE    = regexp.MustCompile(`^\d{1,9}$`)
	rcdWordRE   = regexp.MustCompile(`^[A-Za-z0-9]+$`)
	rcdNameRE   = regexp.MustCompile(`^[A-Za-z0-9*][A-Za-z0-9*._-]*$`)
	rcdEmailRE  = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+$`)
	rcdNumberRE = regexp.MustCompile(`^\d+[KkMmGgBb]?$`)
)

// valid reports whether v is acceptable for the kind.
func (k rcdOptKind) valid(v string) bool {
	switch k {
	case rcdOptInt:
		return rcdIntRE.MatchString(v)
	case rcdOptWord:
		return rcdWordRE.MatchString(v)
	case rcdOptName:
		return rcdNameRE.MatchString(v)
	case rcdOptEmail:
		return rcdEmailRE.MatchString(v)
	case rcdOptPath:
		return rcdUnderHome(v)
	case rcdOptNumber:
		return rcdNumberRE.MatchString(v)
	case rcdOptText:
		return true
	case rcdOptFlag:
		return false
	}

	return false
}

// rcdOptions reads the options out of words: a word that starts with "-" must
// be a listed option (key as norm gives it) with a valid value, which is the
// next word or, for "--name=value", the rest of the word; any other word is
// returned as a positional argument. ok is false for the first thing that does
// not fit, including "--" and "-".
func rcdOptions(words []string, spec map[string]rcdOptKind, norm func(string) string) (rest []string, ok bool) {
	for i := 0; i < len(words); i++ {
		if !strings.HasPrefix(words[i], "-") {
			rest = append(rest, words[i])

			continue
		}

		if i, ok = rcdOption(words, i, spec, norm); !ok {
			return nil, false
		}
	}

	return rest, true
}

// rcdOption checks the option at words[i] (see rcdOptions) and returns the
// index of its last word: i itself, or i+1 when the value is the next word.
func rcdOption(words []string, i int, spec map[string]rcdOptKind, norm func(string) string) (last int, ok bool) {
	name, value, inline := words[i], "", false
	if strings.HasPrefix(name, "--") {
		name, value, inline = strings.Cut(name, "=")
	}

	kind, listed := spec[norm(name)]

	switch {
	case !listed:
		return i, false
	case kind == rcdOptFlag:
		return i, !inline
	case inline:
		return i, kind.valid(value)
	case i+1 >= len(words):
		return i, false
	default:
		return i + 1, kind.valid(words[i+1])
	}
}

func rcdExact(name string) string { return name }

// rcdOptKey is how zmcertmgr's Getopt::Long sees an option name: one or two
// dashes, no case.
func rcdOptKey(name string) string { return strings.ToLower(strings.TrimLeft(name, "-")) }

// --- rsync ---

// rcdRsyncShort is a bundle of the short options an rsync client passes to its
// server: the letters that take no value, optionally ending in the "e." of
// the protocol compatibility flags (which is not -e <rsh>: the server never
// starts a shell, and "e" without the dot is refused).
var rcdRsyncShort = regexp.MustCompile(`^-[vqlLkKHpoOgDtJUNuarRdSWnizcxIyEAXPm]*(?:e\.[A-Za-z0-9.]*)?$`)

// rcdRsyncLong are the long options a client sends to the server, with what
// each takes. Nothing that names a program, a daemon configuration, a log or
// a batch file is here.
var rcdRsyncLong = map[string]rcdOptKind{
	"--delete": rcdOptFlag, "--delete-before": rcdOptFlag, "--delete-during": rcdOptFlag,
	"--delete-after": rcdOptFlag, "--delete-delay": rcdOptFlag, "--delete-excluded": rcdOptFlag,
	"--delete-missing-args": rcdOptFlag, "--ignore-missing-args": rcdOptFlag, "--ignore-errors": rcdOptFlag,
	"--force": rcdOptFlag, "--numeric-ids": rcdOptFlag, "--partial": rcdOptFlag, "--inplace": rcdOptFlag,
	"--append": rcdOptFlag, "--append-verify": rcdOptFlag, "--size-only": rcdOptFlag, "--ignore-times": rcdOptFlag,
	"--super": rcdOptFlag, "--fake-super": rcdOptFlag, "--existing": rcdOptFlag, "--ignore-existing": rcdOptFlag,
	"--omit-dir-times": rcdOptFlag, "--omit-link-times": rcdOptFlag, "--safe-links": rcdOptFlag,
	"--copy-unsafe-links": rcdOptFlag, "--munge-links": rcdOptFlag, "--no-implied-dirs": rcdOptFlag,
	"--whole-file": rcdOptFlag, "--no-whole-file": rcdOptFlag, "--preallocate": rcdOptFlag,
	"--fsync": rcdOptFlag, "--remove-source-files": rcdOptFlag, "--prune-empty-dirs": rcdOptFlag,
	"--stats": rcdOptFlag,

	"--timeout": rcdOptInt, "--block-size": rcdOptInt, "--checksum-seed": rcdOptInt,
	"--compress-level": rcdOptInt, "--modify-window": rcdOptInt, "--max-delete": rcdOptInt,
	"--bwlimit": rcdOptNumber, "--max-size": rcdOptNumber, "--min-size": rcdOptNumber,
	"--chmod": rcdOptText, "--log-format": rcdOptText, "--skip-compress": rcdOptText,
	"--checksum-choice": rcdOptWord, "--compress-choice": rcdOptWord, "--suffix": rcdOptText,
	"--exclude": rcdOptText, "--include": rcdOptText,
	"--partial-dir": rcdOptPath, "--temp-dir": rcdOptPath, "--backup-dir": rcdOptPath,
	"--link-dest": rcdOptPath, "--compare-dest": rcdOptPath, "--copy-dest": rcdOptPath,
}

// rcdRsyncValid checks the arguments of an rsync, the program word left out:
// the server side of an rsync over ssh, which is what a client sends:
// "--server [--sender] <options> . <path>...", the paths inside rcdHome.
func rcdRsyncValid(args []string) bool {
	if len(args) < 3 || args[0] != "--server" {
		return false
	}

	i := 1
	if args[i] == "--sender" {
		i++
	}

	for ; i < len(args) && args[i] != "."; i++ {
		if !rcdRsyncOption(args[i]) {
			return false
		}
	}

	paths := args[min(i+1, len(args)):]
	if len(paths) == 0 {
		return false
	}

	for _, p := range paths {
		if !rcdUnderHome(p) {
			return false
		}
	}

	return true
}

// rcdRsyncOption checks one word before the "." of an rsync server command line.
func rcdRsyncOption(w string) bool {
	if !strings.HasPrefix(w, "--") {
		return len(w) > 1 && rcdRsyncShort.MatchString(w)
	}

	name, value, inline := strings.Cut(w, "=")

	kind, ok := rcdRsyncLong[name]
	if !ok {
		return false
	}

	if kind == rcdOptFlag {
		return !inline
	}

	return inline && kind.valid(value)
}

// --- certbot ---

// rcdCertbotOpts are the options of the certbot command line of the mailbox
// (RemoteCertbot): "certonly --agree-tos --email <mail> -n --keep --webroot -w
// <path> --cert-name <name> -d <host>...", and what its dry run adds.
var rcdCertbotOpts = map[string]rcdOptKind{
	"--agree-tos": rcdOptFlag, "-n": rcdOptFlag, "--non-interactive": rcdOptFlag, "--keep": rcdOptFlag,
	"--keep-until-expiring": rcdOptFlag, "--expand": rcdOptFlag, "--dry-run": rcdOptFlag,
	"--no-eff-email": rcdOptFlag, "--webroot": rcdOptFlag,
	"-w": rcdOptPath, "--webroot-path": rcdOptPath,
	"-m": rcdOptEmail, "--email": rcdOptEmail,
	"--cert-name": rcdOptName, "-d": rcdOptName, "--domain": rcdOptName, "--domains": rcdOptName,
}

// rcdCertbotValid checks the arguments of certbot (the program is the fixed
// libexec/certbot wrapper): certonly and the listed options, nothing else:
// no hook, no configuration, working or logs directory.
func rcdCertbotValid(args []string) bool {
	if len(args) == 0 || args[0] != "certonly" {
		return false
	}

	rest, ok := rcdOptions(args[1:], rcdCertbotOpts, rcdExact)

	return ok && len(rest) == 0
}

// --- zmcertmgr ---

var (
	rcdCertTypes    = []string{"self", "comm"}
	rcdCertServices = []string{certSvcAll, certSvcLDAP, certSvcMailboxd, certSvcMTA, certSvcProxy}
)

// rcdCertOpts are the options of zmcertmgr that the callers use, by the name
// Getopt::Long reads them (the mailbox abbreviates -allservers to -allserver).
var rcdCertOpts = map[string]rcdOptKind{
	certOptNew: rcdOptFlag, certOptKeysize: rcdOptInt, certOptDigest: rcdOptWord, certOptSubject: rcdOptText,
	certOptSANs: rcdOptText, certOptNoDefSAN: rcdOptFlag, certOptDays: rcdOptInt,
	certOptAllsvrs: rcdOptFlag, certOptAllsvr: rcdOptFlag,
}

// rcdCertCmd is a zmcertmgr command of the table: whether the type follows it,
// its options, and its positional arguments (paths, or one of services).
type rcdCertCmd struct {
	typed    bool
	opts     []string
	services bool
	minPaths int
	maxPaths int
}

var (
	rcdCertCreateOpts = []string{certOptNew, certOptKeysize, certOptDigest, certOptSubject, certOptSANs, certOptNoDefSAN}
	rcdCertAllOpts    = []string{certOptAllsvrs, certOptAllsvr}
)

var rcdCertCmds = map[string]rcdCertCmd{
	certCmdViewDeployed: {services: true},
	"viewstagedcrt":     {typed: true, maxPaths: 1},
	certCmdViewCSR:      {typed: true, maxPaths: 1},
	certCmdCreateCSR:    {typed: true, opts: rcdCertCreateOpts},
	certCmdCreateCRT:    {opts: append(slices.Clone(rcdCertCreateOpts), certOptDays, certOptAllsvrs, certOptAllsvr)},
	certCmdGetCRT:       {typed: true, opts: rcdCertAllOpts},
	certCmdDeployCRT:    {typed: true, opts: rcdCertAllOpts, maxPaths: 2},
	"verifycrt":         {typed: true, maxPaths: 3},
	certCmdVerifyChain:  {minPaths: 2, maxPaths: 2},
	certCmdVerifyKey:    {minPaths: 2, maxPaths: 2},
}

// rcdCertmgrValid checks the arguments of zmcertmgr: a command of the table,
// its type, only its options with valid values, and paths inside rcdHome.
func rcdCertmgrValid(args []string) bool {
	if len(args) == 0 {
		return false
	}

	c, ok := rcdCertCmds[args[0]]
	if !ok {
		return false
	}

	rest := args[1:]

	if c.typed {
		if len(rest) == 0 || !slices.Contains(rcdCertTypes, rest[0]) {
			return false
		}

		rest = rest[1:]
	}

	spec := make(map[string]rcdOptKind, len(c.opts))
	for _, o := range c.opts {
		spec[o] = rcdCertOpts[o]
	}

	pos, ok := rcdOptions(rest, spec, rcdOptKey)
	if !ok {
		return false
	}

	if c.services {
		return len(pos) <= 1 && (len(pos) == 0 || slices.Contains(rcdCertServices, pos[0]))
	}

	if len(pos) < c.minPaths || len(pos) > c.maxPaths {
		return false
	}

	return !slices.ContainsFunc(pos, func(p string) bool { return !rcdUnderHome(p) })
}
