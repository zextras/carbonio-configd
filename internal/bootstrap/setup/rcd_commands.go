// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"maps"
	"regexp"
	"slices"
	"strings"
)

// The commands zmrcd runs for a caller that holds the SSH key of the
// installation: a closed table, never a shell. A command is either a SIMPLE
// one (the line "<command> <args>" must be exactly the key; nothing of the
// line reaches the process) or a CHECKED one (the arguments must match a
// conservative regular expression and become argv after the fixed program).
// The program, the flags and the pipelines are the table's: input never
// supplies a path or a program.

// rcdSimple is a command whose line must equal its key.
type rcdSimple struct {
	// run is the command as the legacy zmrcd ran it. It is both what STARTCMD
	// and ENDCMD show and, split into words (and on " | " into the stages of
	// a pipeline), what is run.
	run string
	// quiet leaves out the STARTCMD and ENDCMD lines (bulk output).
	quiet bool
	// secret keeps the output out of the log file: it holds directory
	// entries, keys or configuration.
	secret bool
}

// rcdChecked is a command whose arguments are checked by a regular expression.
type rcdChecked struct {
	// re must match the whole argument string (leading and trailing spaces
	// included).
	re *regexp.Regexp
	// help is how the legacy script printed the expression on "?".
	help string
	// program is the fixed part of the command line: words, no input.
	program string
	quiet   bool
}

// Paths of the binaries of the table.
const (
	rcdBin     = "/opt/zextras/bin/"
	rcdLibexec = "/opt/zextras/libexec/"
	rcdSbin    = "/opt/zextras/common/sbin/"
	rcdSudo    = "sudo "
	rcdSlapcat = rcdSbin + "slapcat -F /opt/zextras/data/ldap/config -b ''"
	rcdZmqstat = rcdSudo + rcdLibexec + "zmqstat"
	rcdGzip    = " | gzip -cf"
)

// rcdSimpleCommands is SIMPLE_COMMANDS of the legacy script.
var rcdSimpleCommands = map[string]rcdSimple{
	"start amavis":     {run: rcdBin + "zmamavisdctl start"},
	"start freshclam":  {run: rcdBin + "zmfreshclamctl start"},
	"start mailbox":    {run: rcdBin + "zmstorectl start"},
	"start ldap":       {run: rcdBin + "ldap start"},
	"start mta":        {run: rcdBin + "zmmtactl start"},
	"start antispam":   {run: rcdBin + "zmantispamctl start"},
	"start antivirus":  {run: rcdBin + "zmantivirusctl start"},
	"stop amavis":      {run: rcdBin + "zmamavisdctl stop"},
	"stop freshclam":   {run: rcdBin + "zmfreshclamctl stop"},
	"stop mailbox":     {run: rcdBin + "zmstorectl stop"},
	"stop ldap":        {run: rcdBin + "ldap stop"},
	"stop mta":         {run: rcdBin + "zmmtactl stop"},
	"stop antispam":    {run: rcdBin + "zmantispamctl stop"},
	"stop antivirus":   {run: rcdBin + "zmantivirusctl stop"},
	"status":           {run: rcdBin + "zmcontrol status"},
	"startup":          {run: rcdBin + "zmcontrol startup"},
	"shutdown":         {run: rcdBin + "zmcontrol shutdown"},
	"msgtrace":         {run: rcdBin + "zmmsgtrace"},
	"flushqueue":       {run: rcdSbin + "postqueue -f"},
	"showqueue":        {run: rcdSbin + "postqueue -p"},
	"zmserverips":      {run: rcdLibexec + "zmserverips"},
	"zmupdateauthkeys": {run: rcdBin + "zmupdateauthkeys"},
	"slapcat":          {run: rcdSlapcat, secret: true},
	"zmqstat all":      {run: rcdZmqstat, quiet: true},
	"zmqstat incoming": {run: rcdZmqstat + " incoming", quiet: true},
	"zmqstat hold":     {run: rcdZmqstat + " hold", quiet: true},
	"zmqstat active":   {run: rcdZmqstat + " active", quiet: true},
	"zmqstat deferred": {run: rcdZmqstat + " deferred", quiet: true},
	"zmqstat corrupt":  {run: rcdZmqstat + " corrupt", quiet: true},

	"zmcollectconfigfiles": {run: "tar cv /opt/zextras/conf" + rcdGzip, quiet: true, secret: true},
	"zmcollectldapzimbra":  {run: rcdSlapcat + " -s cn=zimbra" + rcdGzip, quiet: true, secret: true},
	"zmproxyconfgen":       {run: rcdBin + "zmproxyconfgen"},
	"zmproxyctl reload":    {run: rcdBin + "zmproxyctl reload"},
	"zmaltermimeconfig":    {run: rcdLibexec + "zmaltermimeconfig"},
	"downloadcsr":          {run: "cat /opt/zextras/ssl/carbonio/commercial/commercial.csr"},
}

// Argument alphabets of the table (the Perl character classes, which are the
// whole security of a CHECKED command: no shell metacharacter in any of them).
const (
	// rcdPathArgs is alphanumerics, slash, dot, dash, underscore, colon, at
	// and space: what a destination path or a host needs.
	rcdPathArgs = `^[a-zA-Z0-9/.\-_:@ ]*$`
	// rcdPathHelp is the same class as the script printed it.
	rcdPathHelp = `[a-zA-Z0-9\/\.\-_:@ ]*`
	// rcdCertArgs adds the comma, equals, quotes, asterisk and backslash of
	// subjects and alternative names. The quotes and the backslash are shell
	// quoting for the words (see rcdWords); the asterisk is literal.
	rcdCertArgs = `[a-zA-Z0-9/.\-\\_:@,='"* ]*$`
	// rcdQID is a Postfix queue id, short or long format.
	rcdQID = `(?:[A-F0-9]{6,}|(?i:[0-9BCDFGHJKLMNPQRSTVWXYZ]){10,}z[0-9BCDFGHJKLMNPQRSTVWXYZbcdfghjklmnpqrstvwxy]+)`
)

// rcdCheckedCommands is REGEX_CHECKED_COMMANDS of the legacy script. help is
// the text the script printed for the expression (the Perl source form or
// its stringified qr), kept for "?".
var rcdCheckedCommands = map[string]rcdChecked{
	"zmqaction": {
		re: regexp.MustCompile(`^(hold|release|requeue|delete)\s+(incoming|deferred|corrupt|active|maildrop|hold)\s+` +
			`(?:(?:` + rcdQID + `,)*` + rcdQID + `|ALL)$`),
		help: `(?^:(hold|release|requeue|delete)\s+(incoming|deferred|corrupt|active|maildrop|hold)\s+` +
			`(?:(?:(?^:(?:(?^:[A-F0-9]){6,}+|(?^i:[0-9BCDFGHJKLMNPQRSTVWXYZ]){10,}z` +
			`(?^:[0-9BCDFGHJKLMNPQRSTVWXYZbcdfghjklmnpqrstvwxy])++)),)*+` +
			`(?^:(?:(?^:[A-F0-9]){6,}+|(?^i:[0-9BCDFGHJKLMNPQRSTVWXYZ]){10,}z` +
			`(?^:[0-9BCDFGHJKLMNPQRSTVWXYZbcdfghjklmnpqrstvwxy])++))|ALL))`,
		program: rcdLibexec + "zmqaction",
	},
	"zmbackupldap": {
		re: regexp.MustCompile(rcdPathArgs), help: rcdPathHelp,
		program: rcdLibexec + "zmbackupldap",
	},
	"zmcertmgr": {
		re: regexp.MustCompile(`^(viewdeployedcrt|viewstagedcrt|createcsr|createcrt|getcrt|deploycrt|viewcsr|` +
			`verifycrt|verifycrtkey|verifycrtchain)\s?` + rcdCertArgs),
		help: `(viewdeployedcrt|viewstagedcrt|createcsr|createcrt|getcrt|deploycrt|viewcsr|verifycrt|` +
			`verifycrtkey|verifycrtchain)\s?` + rcdCertHelp,
		program: rcdBin + "zmcertmgr",
	},
	"clusvcadm": {
		re: regexp.MustCompile(rcdPathArgs), help: rcdPathHelp,
		program: rcdSudo + "/usr/sbin/clusvcadm", quiet: true,
	},
	"zmclustat": {
		re: regexp.MustCompile(rcdPathArgs), help: rcdPathHelp,
		program: rcdSudo + "/opt/cluster/bin/zmclustat", quiet: true,
	},
	"zmloggerhostmap": {
		re: regexp.MustCompile(rcdPathArgs), help: rcdPathHelp,
		program: rcdBin + "zmloggerhostmap",
	},
	rcdPlainRsync: {
		re: regexp.MustCompile(rcdRsyncArgs), help: `['"a-zA-Z0-9\/\.\-_:@= ]*`,
		program: rcdPlainRsync,
	},
	"certbot": {
		re:      regexp.MustCompile(`^(certonly)\s?` + rcdCertArgs),
		help:    `(certonly)\s?` + rcdCertHelp,
		program: rcdLibexec + "certbot",
	},
	"dkimutil": {
		// The legacy class was [a|r], which let a "|" through to the shell.
		re:      regexp.MustCompile(`^(-[ar])?\s?(-d\s(([\w-]+\.)+[\w-]{2,4}))?$`),
		help:    `(-[a|r])?\s?(-d\s(([\w-]+\.)+[\w-]{2,4}))?`,
		program: rcdLibexec + "zmdkimkeyutil",
	},
}

const (
	// rcdCertHelp is the argument class of zmcertmgr and certbot as "?" shows it.
	rcdCertHelp = `[a-zA-Z0-9\/\.\-\\_:@\,\=\'\"\* ]*`
	// rcdRsyncArgs is what a rsync command line may be made of, the program
	// word included (the SSH_ORIGINAL_COMMAND of an rsync over ssh).
	rcdRsyncArgs = `^['"a-zA-Z0-9/.\-_:@= ]*$`
)

// rcdPlainRsync and rcdZextrasRsync are the only programs a SSH_ORIGINAL_COMMAND
// ending in "rsync" may name.
const (
	rcdPlainRsync   = "rsync"
	rcdZextrasRsync = "/opt/zextras/common/bin/rsync"
)

// rcdIsSpace reports the characters Perl's split ' ' and the shell treat as
// separators.
func rcdIsSpace(c byte) bool {
	return c == ' ' || (c >= '\t' && c <= '\r')
}

// rcdWords splits s into words the way a POSIX shell would for a line without
// expansions: blanks separate words, single quotes keep everything, double
// quotes keep everything but a backslash before a quote or a backslash, a
// backslash outside quotes escapes the next character. It returns false for
// an unterminated quote. No variable, command, tilde or glob expansion
// happens: "$(x)" and "*" stay literal.
func rcdWords(s string) ([]string, bool) {
	var w rcdSplitter

	for i := 0; i < len(s); i++ {
		i += w.feed(s, i)
	}

	return w.done()
}

// rcdSplitter is the state of rcdWords.
type rcdSplitter struct {
	words  []string
	cur    []byte
	inWord bool
	quote  byte // 0, '\'' or '"'
}

// feed consumes s[i] and returns how many bytes after it it consumed too.
func (w *rcdSplitter) feed(s string, i int) int {
	switch w.quote {
	case '\'':
		w.single(s[i])

		return 0
	case '"':
		return w.double(s, i)
	}

	return w.bare(s, i)
}

func (w *rcdSplitter) single(c byte) {
	if c == '\'' {
		w.quote = 0

		return
	}

	w.cur = append(w.cur, c)
}

func (w *rcdSplitter) double(s string, i int) int {
	c := s[i]

	switch {
	case c == '"':
		w.quote = 0
	case c == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\'):
		w.cur = append(w.cur, s[i+1])

		return 1
	default:
		w.cur = append(w.cur, c)
	}

	return 0
}

func (w *rcdSplitter) bare(s string, i int) int {
	c := s[i]

	switch {
	case c == '\'' || c == '"':
		w.quote, w.inWord = c, true
	case c == '\\' && i+1 < len(s):
		w.cur, w.inWord = append(w.cur, s[i+1]), true

		return 1
	case rcdIsSpace(c):
		if w.inWord {
			w.words, w.cur, w.inWord = append(w.words, string(w.cur)), nil, false
		}
	default:
		w.cur, w.inWord = append(w.cur, c), true
	}

	return 0
}

func (w *rcdSplitter) done() ([]string, bool) {
	if w.quote != 0 {
		return nil, false
	}

	if w.inWord {
		w.words = append(w.words, string(w.cur))
	}

	return w.words, true
}

// rcdStages splits a table command into the argv of each stage of its
// pipeline. The table is fixed: a command that does not split is a
// programming error that the tests of every entry catch.
func rcdStages(run string) [][]string {
	parts := strings.Split(run, " | ")
	stages := make([][]string, 0, len(parts))

	for _, p := range parts {
		words, _ := rcdWords(p)
		stages = append(stages, words)
	}

	return stages
}

// rcdHelpLines is the "?" listing: the simple commands, then the checked
// ones, each sorted by name.
func rcdHelpLines() []string {
	var lines []string

	for _, name := range slices.Sorted(maps.Keys(rcdSimpleCommands)) {
		lines = append(lines, name+" -> "+rcdSimpleCommands[name].run+"\n")
	}

	for _, name := range slices.Sorted(maps.Keys(rcdCheckedCommands)) {
		c := rcdCheckedCommands[name]
		lines = append(lines, name+" "+c.help+" -> "+c.program+" <arg>\n")
	}

	return lines
}
