// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ldapPasswdOptions are the options of bin/ldappasswd.pl (zmldappasswd),
// parsed by Getopt::Std with 'arcblnph'.
type ldapPasswdOptions struct {
	help bool // -h
	// confirm is -c: the replication password on a replica; it needs -l.
	confirm bool
	// account is the account the options select, the admin one when none.
	account ldapAccount
	// accounts counts the account options (-r -l -n -a -b -p): one at most.
	accounts int
	// password is the argument after the options; hasPassword tells it was
	// given, so that "" is not mistaken for no password.
	password    string
	hasPassword bool
	// extra are the arguments after the password, which the script ignored.
	extra []string
}

// ldapPasswdAccounts are the options that select an account, by letter.
var ldapPasswdAccounts = map[byte]ldapAccount{
	'r': ldapAcctRoot,
	'l': ldapAcctReplica,
	'p': ldapAcctPostfix,
	'a': ldapAcctAmavis,
	'n': ldapAcctNginx,
	'b': ldapAcctBES,
}

// ldapPasswdOptionLetters are the letters of the script's getopts string.
const ldapPasswdOptionLetters = "arcblnph"

// parseLDAPPasswdArgs parses args as Getopt::Std did: options may be bundled
// (-cl), "--" ends the options and so does the first argument that is not
// one. Unknown options are reported on stderr like Getopt::Std; ok is false
// when there was one.
func parseLDAPPasswdArgs(args []string, stderr io.Writer) (o *ldapPasswdOptions, ok bool) {
	o = &ldapPasswdOptions{account: ldapAcctAdmin}
	ok = true

	i := 0
	for i < len(args) && len(args[i]) > 1 && args[i][0] == '-' {
		cluster := args[i][1:]
		i++

		if cluster == "-" {
			break
		}

		for pos := range len(cluster) {
			if !o.set(cluster[pos], stderr) {
				ok = false
			}
		}
	}

	if i < len(args) {
		o.password, o.hasPassword = args[i], true
		o.extra = args[i+1:]
	}

	return o, ok
}

// set applies one option letter; false for a letter that is not one.
func (o *ldapPasswdOptions) set(letter byte, stderr io.Writer) bool {
	switch {
	case letter == 'h':
		o.help = true
	case letter == 'c':
		o.confirm = true
	case strings.IndexByte(ldapPasswdOptionLetters, letter) < 0:
		_, _ = fmt.Fprintf(stderr, "Unknown option: %c\n", letter)

		return false
	default:
		o.account = ldapPasswdAccounts[letter]
		o.accounts++
	}

	return true
}

// usageNeeded tells whether the script refused the options with its usage
// text: -h, more than one account, -c without -l or on the master.
func (o *ldapPasswdOptions) usageNeeded(isMaster bool) bool {
	switch {
	case o.help, o.accounts > 1:
		return true
	case o.confirm && (o.account.DN != ldapDNReplica || isMaster):
		return true
	}

	return false
}

// LDAPPasswdUsage is the text zmldappasswd printed for -h and for refused
// options; program is its path. The script wanted the password as an
// argument; without it, it is read from stdin, so that it need not be on a
// command line.
func LDAPPasswdUsage(program string) string {
	return "Usage: " + program + " [-h] [-r] [-p] [[-c]-l] [newpassword]\n" +
		"\t-h: display this help message\n" +
		"\t-a: change ldap_amavis_password\n" +
		"\t-b: change ldap_bes_searcher_password\n" +
		"\t-l: change ldap_replication_password\n" +
		"\t-c: Update ldap_replication_password on replica. Requires -l\n" +
		"\t-n: change ldap_nginx_password\n" +
		"\t-p: change ldap_postfix_password\n" +
		"\t-r: change ldap_root_passwd\n" +
		"\tOnly one of a, l, n, p, or r may be specified\n" +
		"\tWithout options zimbra_ldap_password is changed\n\n" +
		"\tOption -c requires -l and must be run on a replica after\n" +
		"\tchanging the password on the master (using -l by itself).\n\n" +
		"\tWithout newpassword the first line of stdin is the password: it is\n" +
		"\tnot then on a command line, where every user can read it.\n\n"
}

// ldapPasswdFromStdin is the first line of r without its line end.
func ldapPasswdFromStdin(r io.Reader) (string, error) {
	if r == nil {
		return "", nil
	}

	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("reading the password from stdin: %w", err)
	}

	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

// RunLDAPPasswd is zmldappasswd: it parses the options of the script in args,
// changes the password of the account they select in localconfig, the
// directory and conf/carbonio.ldif, and returns the exit status. The password
// is the argument after the options or, without one, the first line of stdin.
// The script's output and exit statuses are kept: 1 for the usage text (on
// stdout), 255 for options Getopt::Std refuses; a failure is "Error: ..." on
// stderr and 1 (the script mostly ignored the LDAP errors and died on a
// connection failure).
func RunLDAPPasswd(e *Env, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runLDAPPasswd(e, ldapNativeKit(), args, stdin, stdout, stderr)
}

// ldapPasswdErrFmt is how a failure is reported on stderr.
const ldapPasswdErrFmt = "Error: %v\n"

func runLDAPPasswd(e *Env, k *ldapKit, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	o, ok := parseLDAPPasswdArgs(args, stderr)
	if !ok {
		_, _ = fmt.Fprintln(stderr, "Unable to set options")

		return 255
	}

	usage := func() int {
		_, _ = io.WriteString(stdout, LDAPPasswdUsage(e.Paths.Path("bin", "zmldappasswd")))

		return 1
	}

	isMaster, err := ldapIsMaster(e)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, ldapPasswdErrFmt, err)

		return 1
	}

	if o.usageNeeded(isMaster) {
		return usage()
	}

	if !o.hasPassword {
		if o.password, err = ldapPasswdFromStdin(stdin); err != nil {
			_, _ = fmt.Fprintf(stderr, ldapPasswdErrFmt, err)

			return 1
		}
	}

	if o.password == "" || len(o.extra) > 0 {
		// The script ignored what followed the password, which would
		// silently truncate one that contains spaces.
		return usage()
	}

	_, _ = fmt.Fprintln(stdout, "Updating local config and LDAP")

	if err := ldapChangePassword(e, k, o.account, o.password, o.confirm); err != nil {
		_, _ = fmt.Fprintf(stderr, ldapPasswdErrFmt, err)

		return 1
	}

	return 0
}

// ldapIsMaster is whether this node is the LDAP master: localconfig
// ldap_is_master, "true" in any case.
func ldapIsMaster(e *Env) (bool, error) {
	v, err := e.LCValue("ldap_is_master")
	if err != nil {
		return false, err
	}

	return strings.EqualFold(strings.TrimSpace(v), "true"), nil
}
