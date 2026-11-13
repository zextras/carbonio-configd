// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const (
	rcdTestHost = "mta.example.com"
	rcdTestLog  = "/opt/zextras/log/zmrcd.log"
	rcdBinDir   = "/opt/zextras/bin/"
)

// rcdRig is a node that runs zmrcd: its commands are fakes that record their
// argv, and the "directory" and the process replacement are seams.
type rcdRig struct {
	t     *testing.T
	e     *Env
	fakes *systest.Fakes

	original string
	services map[string][]string
	svcErr   error
	execve   [][]string
	execErr  error
}

func newRCDRig(t *testing.T) *rcdRig {
	t.Helper()

	e, fakes, _ := testEnv(t)
	e.lc = map[string]string{rcdHostKey: rcdTestHost, rcdLoggingKey: "false"}

	// Replacing the process never returns but with an error.
	r := &rcdRig{t: t, e: e, fakes: fakes, execErr: syscall.EACCES}
	e.rcd = &rcdSeam{
		getenv: func(k string) string {
			assert.Equal(t, rcdOriginalCommand, k)

			return r.original
		},
		execve: func(path string, argv []string) error {
			assert.Equal(t, argv[0], path)

			r.execve = append(r.execve, argv)

			return r.execErr
		},
		services: func(*Env) (map[string][]string, error) { return r.services, r.svcErr },
	}

	return r
}

// run feeds stdin to zmrcd and returns its exit status and what it printed.
func (r *rcdRig) run(stdin string) (status int, out, errs string) {
	r.t.Helper()

	var o, e bytes.Buffer

	status = RunRCD(r.e, nil, strings.NewReader(stdin), &o, &e)

	return status, o.String(), e.String()
}

// line is a command line for this host.
func rcdLine(command string) string { return "HOST:" + rcdTestHost + " " + command + "\n" }

// reply makes the program print out (and err on stderr) and exit with code.
func (r *rcdRig) reply(path, out, errs string, code int) {
	r.fakes.Exec.OnPath(path, func(*sys.Cmd) (sys.Result, error) {
		res := sys.Result{Stdout: []byte(out), Stderr: []byte(errs), ExitCode: code}
		if code != 0 {
			return res, &sys.ExitError{Code: code}
		}

		return res, nil
	})
}

// argvs are the commands that were run, program first.
func (r *rcdRig) argvs() [][]string {
	var out [][]string

	for _, c := range r.fakes.Exec.Calls() {
		out = append(out, c.Argv())
	}

	return out
}

func TestRunRCD_RunsTheCommandsOfTheTable(t *testing.T) {
	const (
		bin     = rcdBinDir
		libexec = "/opt/zextras/libexec/"
		sbin    = "/opt/zextras/common/sbin/"
	)

	slapcat := []string{sbin + "slapcat", "-F", "/opt/zextras/data/ldap/config", "-b", ""}

	for _, tc := range []struct {
		line string
		argv []string
		// shown is the command line of STARTCMD and ENDCMD, "" for the
		// commands that print neither.
		shown string
	}{
		{"start amavis", []string{bin + "zmamavisdctl", "start"}, bin + "zmamavisdctl start"},
		{"start freshclam", []string{bin + "zmfreshclamctl", "start"}, bin + "zmfreshclamctl start"},
		{"start mailbox", []string{bin + "zmstorectl", "start"}, bin + "zmstorectl start"},
		{"start ldap", []string{bin + "ldap", "start"}, bin + "ldap start"},
		{"start mta", []string{bin + "zmmtactl", "start"}, bin + "zmmtactl start"},
		{"start antispam", []string{bin + "zmantispamctl", "start"}, bin + "zmantispamctl start"},
		{"start antivirus", []string{bin + "zmantivirusctl", "start"}, bin + "zmantivirusctl start"},
		{"stop amavis", []string{bin + "zmamavisdctl", "stop"}, bin + "zmamavisdctl stop"},
		{"stop freshclam", []string{bin + "zmfreshclamctl", "stop"}, bin + "zmfreshclamctl stop"},
		{"stop mailbox", []string{bin + "zmstorectl", "stop"}, bin + "zmstorectl stop"},
		{"stop ldap", []string{bin + "ldap", "stop"}, bin + "ldap stop"},
		{"stop mta", []string{bin + "zmmtactl", "stop"}, bin + "zmmtactl stop"},
		{"stop antispam", []string{bin + "zmantispamctl", "stop"}, bin + "zmantispamctl stop"},
		{"stop antivirus", []string{bin + "zmantivirusctl", "stop"}, bin + "zmantivirusctl stop"},
		{"status", []string{bin + "zmcontrol", "status"}, bin + "zmcontrol status"},
		{"startup", []string{bin + "zmcontrol", "startup"}, bin + "zmcontrol startup"},
		{"shutdown", []string{bin + "zmcontrol", "shutdown"}, bin + "zmcontrol shutdown"},
		{"msgtrace", []string{bin + "zmmsgtrace"}, bin + "zmmsgtrace"},
		{"flushqueue", []string{sbin + "postqueue", "-f"}, sbin + "postqueue -f"},
		{"showqueue", []string{sbin + "postqueue", "-p"}, sbin + "postqueue -p"},
		{"zmserverips", []string{libexec + "zmserverips"}, libexec + "zmserverips"},
		{"zmupdateauthkeys", []string{bin + "zmupdateauthkeys"}, bin + "zmupdateauthkeys"},
		{"slapcat", slapcat, sbin + "slapcat -F /opt/zextras/data/ldap/config -b ''"},
		{"zmqstat all", []string{"sudo", libexec + "zmqstat"}, ""},
		{"zmqstat incoming", []string{"sudo", libexec + "zmqstat", "incoming"}, ""},
		{"zmqstat hold", []string{"sudo", libexec + "zmqstat", "hold"}, ""},
		{"zmqstat active", []string{"sudo", libexec + "zmqstat", "active"}, ""},
		{"zmqstat deferred", []string{"sudo", libexec + "zmqstat", "deferred"}, ""},
		{"zmqstat corrupt", []string{"sudo", libexec + "zmqstat", "corrupt"}, ""},
		{"zmproxyconfgen", []string{bin + "zmproxyconfgen"}, bin + "zmproxyconfgen"},
		{"zmproxyctl reload", []string{bin + "zmproxyctl", "reload"}, bin + "zmproxyctl reload"},
		{"zmaltermimeconfig", []string{libexec + "zmaltermimeconfig"}, libexec + "zmaltermimeconfig"},
		{"downloadcsr", []string{"cat", "/opt/zextras/ssl/carbonio/commercial/commercial.csr"},
			"cat /opt/zextras/ssl/carbonio/commercial/commercial.csr"},
	} {
		t.Run(tc.line, func(t *testing.T) {
			r := newRCDRig(t)
			r.reply(tc.argv[0], "the output\n", "", 0)

			status, out, errs := r.run(rcdLine(tc.line))

			assert.Equal(t, 0, status)
			assert.Empty(t, errs)
			assert.Equal(t, [][]string{tc.argv}, r.argvs(), "the program and the arguments of the table, nothing else")

			if tc.shown == "" {
				assert.Equal(t, "the output\n", out)

				return
			}

			assert.Equal(t, "STARTCMD: "+rcdTestHost+" "+tc.shown+"\nthe output\nENDCMD: "+rcdTestHost+" "+tc.shown+"\n", out)
		})
	}
}

func TestRunRCD_PipelinesRunTheirStagesAndAnswerWithTheLast(t *testing.T) {
	const slapcat = "/opt/zextras/common/sbin/slapcat"

	for _, tc := range []struct {
		line   string
		first  []string
		source string
	}{
		{"zmcollectconfigfiles", []string{"tar", "cv", "/opt/zextras/conf"}, "tar"},
		{"zmcollectldapzimbra", []string{slapcat, "-F", "/opt/zextras/data/ldap/config", "-b", "", "-s", "cn=zimbra"}, slapcat},
	} {
		t.Run(tc.line, func(t *testing.T) {
			r := newRCDRig(t)
			r.reply(tc.first[0], "raw-bytes", "tar: removing leading /\n", 1) // a warning of the first stage: not the status
			r.fakes.Exec.OnPath("gzip", func(c *sys.Cmd) (sys.Result, error) {
				in, err := io.ReadAll(c.Stdin)
				require.NoError(t, err)

				return sys.Result{Stdout: []byte("gz(" + string(in) + ")")}, nil
			})

			status, out, errs := r.run(rcdLine(tc.line))

			assert.Equal(t, 0, status, "the status of a pipeline is the one of its last stage")
			assert.Equal(t, "gz(raw-bytes)\n", out, "no STARTCMD or ENDCMD for the bulk commands; the last line gets its newline")
			assert.Equal(t, "tar: removing leading /\n", errs)
			assert.ElementsMatch(t, [][]string{tc.first, {"gzip", "-cf"}}, r.argvs())
		})
	}
}

func TestRunRCD_RelaysTheOutputAFailureEndsWithItsStatus(t *testing.T) {
	r := newRCDRig(t)
	r.reply(rcdBinDir+"zmcontrol", "partial\nlast line without newline", "boom\n", 3)

	status, out, errs := r.run(rcdLine("status") + rcdLine("start mta"))

	assert.Equal(t, 3, status, "the status of the command is the status of zmrcd")
	assert.Equal(t,
		"STARTCMD: mta.example.com /opt/zextras/bin/zmcontrol status\npartial\nlast line without newline\n"+
			"ENDCMD: mta.example.com /opt/zextras/bin/zmcontrol status\n", out)
	assert.Equal(t, "boom\n", errs, "stderr of the command is the stderr of zmrcd")
	assert.Len(t, r.argvs(), 1, "only the first line is a command")
}

func TestRunRCD_AFailureStopsTheHostsThatFollow(t *testing.T) {
	r := newRCDRig(t)
	r.reply(rcdBinDir+"zmmtactl", "", "", 2)

	// "mta.example.com" runs (and fails) before "zzz" is announced; "aaa" came first.
	status, out, _ := r.run("HOST:zzz,HOST:" + rcdTestHost + ",HOST:aaa start mta\n")

	assert.Equal(t, 2, status)
	assert.Equal(t, "Remote: HOST:aaa start mta\n"+
		"STARTCMD: mta.example.com /opt/zextras/bin/zmmtactl start\nENDCMD: mta.example.com /opt/zextras/bin/zmmtactl start\n", out)
}

func TestRunRCD_QuietCommandsStillFailWithTheirStatus(t *testing.T) {
	r := newRCDRig(t)
	r.reply("sudo", "queue\n", "", 5)

	status, out, _ := r.run(rcdLine("zmqstat all"))

	assert.Equal(t, 5, status)
	assert.Equal(t, "queue\n", out)
}

func TestRunRCD_ACommandThatDoesNotStartIsAnErrorWithoutCommandLines(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&fs.PathError{Op: "fork/exec", Path: "x", Err: syscall.ENOENT}, "No such file or directory"},
		{&fs.PathError{Op: "fork/exec", Path: "x", Err: syscall.EACCES}, "Permission denied"},
		{errors.New("no way"), "no way"},
	} {
		r := newRCDRig(t)
		r.fakes.Exec.FailStart(func(c *sys.Cmd) bool { return c.Path == rcdBinDir+"zmmtactl" }, tc.err)

		status, out, errs := r.run(rcdLine("start mta"))

		assert.Equal(t, 1, status)
		assert.Empty(t, out, "STARTCMD is printed once the command ran")
		assert.Equal(t, "ERROR: Can't run /opt/zextras/bin/zmmtactl start: "+tc.want+"\n", errs)
	}
}

func TestRunRCD_AFirstStageThatDoesNotStartFailsThePipeline(t *testing.T) {
	r := newRCDRig(t)
	r.fakes.Exec.FailStart(func(c *sys.Cmd) bool { return c.Path == "tar" }, &fs.PathError{Err: syscall.ENOENT})

	status, out, errs := r.run(rcdLine("zmcollectconfigfiles"))

	assert.Equal(t, 1, status)
	assert.Empty(t, out)
	assert.Equal(t, "ERROR: Can't run tar cv /opt/zextras/conf | gzip -cf: No such file or directory\n", errs)
}

func TestRunRCD_RefusesWhatIsNotInTheTable(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{"bogus", `Unknown command: "bogus"`},
		{"bogus a b", `Unknown command: "bogus"`},
		{"start", `Unknown command: "start"`},
		{"start snmp", `Unknown command: "start"`},
		{"START MTA", `Unknown command: "START"`},
		{"start  mta ", `Unknown command: "start"`},
		{"status extra", `Unknown command: "status"`},
		{"zmqstat bogus", `Unknown command: "zmqstat"`},
		{"", `Unknown command: ""`},
		// What a shell would run, a table lookup does not.
		{"status; id", `Unknown command: "status;"`},
		{"status && id", `Unknown command: "status"`},
		{"status|id", `Unknown command: "status|id"`},
		{"$(id)", `Unknown command: "$(id)"`},
		{"`id`", "Unknown command: \"`id`\""},
		{"/bin/sh -c id", `Unknown command: "/bin/sh"`},
		{"/opt/zextras/bin/zmcontrol status", `Unknown command: "/opt/zextras/bin/zmcontrol"`},
		{"../../bin/sh", `Unknown command: "../../bin/sh"`},
		{"zmprov gacf", `Unknown command: "zmprov"`},
		{"sudo id", `Unknown command: "sudo"`},
		{"slapcat -b ''", `Unknown command: "slapcat"`},
		{"tar cv /etc", `Unknown command: "tar"`},
	} {
		r := newRCDRig(t)

		status, out, errs := r.run(rcdLine(tc.line))

		assert.Equal(t, 1, status, tc.line)
		assert.Empty(t, out, tc.line)
		assert.Equal(t, "ERROR: "+tc.want+"\n", errs, tc.line)
		assert.Empty(t, r.argvs(), "nothing was run for %q", tc.line)
	}
}

func TestRunRCD_CheckedCommandsPassTheirArgumentsAsArgv(t *testing.T) {
	const (
		libexec = "/opt/zextras/libexec/"
		qid     = "0123456789zBCDFGH"
	)

	for _, tc := range []struct {
		line string
		argv []string
	}{
		{"zmqaction hold incoming ABCDEF", []string{libexec + "zmqaction", "hold", "incoming", "ABCDEF"}},
		{"zmqaction release deferred ABCDEF,012345", []string{libexec + "zmqaction", "release", "deferred", "ABCDEF,012345"}},
		{"zmqaction delete  active   ALL", []string{libexec + "zmqaction", "delete", "active", "ALL"}},
		{"zmqaction requeue hold " + qid + ",ABCDEF", []string{libexec + "zmqaction", "requeue", "hold", qid + ",ABCDEF"}},
		{"zmbackupldap /opt/zextras/backup/ldap:1@h-2_3", []string{libexec + "zmbackupldap", "/opt/zextras/backup/ldap:1@h-2_3"}},
		{"zmbackupldap", []string{libexec + "zmbackupldap"}},
		{"zmloggerhostmap -a b", []string{rcdBinDir + "zmloggerhostmap", "-a", "b"}},
		{"clusvcadm -e svc", []string{"sudo", "/usr/sbin/clusvcadm", "-e", "svc"}},
		{"zmclustat", []string{"sudo", "/opt/cluster/bin/zmclustat"}},
		{"zmcertmgr viewdeployedcrt", []string{rcdBinDir + "zmcertmgr", "viewdeployedcrt"}},
		{`zmcertmgr createcsr self -new -subject "/C=IT/CN=a b" -subjectAltNames 'x.example.com,y.example.com'`,
			[]string{rcdBinDir + "zmcertmgr", "createcsr", "self", "-new", "-subject", "/C=IT/CN=a b", "-subjectAltNames", "x.example.com,y.example.com"}},
		{`zmcertmgr createcsr -subjectAltNames '*.example.com'`, []string{rcdBinDir + "zmcertmgr", "createcsr", "-subjectAltNames", "*.example.com"}},
		{`zmcertmgr createcsr a\ b ''`, []string{rcdBinDir + "zmcertmgr", "createcsr", "a b", ""}},
		{`zmcertmgr createcsr "a\"b" "c\d"`, []string{rcdBinDir + "zmcertmgr", "createcsr", `a"b`, `c\d`}},
		{"zmcertmgr verifycrtchain /ca.pem /c.pem", []string{rcdBinDir + "zmcertmgr", "verifycrtchain", "/ca.pem", "/c.pem"}},
		{"certbot certonly --agree-tos -n -d a.example.com", []string{libexec + "certbot", "certonly", "--agree-tos", "-n", "-d", "a.example.com"}},
		{"dkimutil", []string{libexec + "zmdkimkeyutil"}},
		{"dkimutil -a", []string{libexec + "zmdkimkeyutil", "-a"}},
		{"dkimutil -r -d a.b.example.org", []string{libexec + "zmdkimkeyutil", "-r", "-d", "a.b.example.org"}},
		{"rsync -av src:/x dst", []string{"rsync", "-av", "src:/x", "dst"}},
	} {
		t.Run(tc.line, func(t *testing.T) {
			r := newRCDRig(t)

			status, _, errs := r.run(rcdLine(tc.line))

			assert.Equal(t, 0, status)
			assert.Empty(t, errs)
			assert.Equal(t, [][]string{tc.argv}, r.argvs())
		})
	}
}

func TestRunRCD_CheckedCommandsShowTheLineAsTheScriptDid(t *testing.T) {
	r := newRCDRig(t)

	_, out, _ := r.run(rcdLine("dkimutil") + "ignored\n")

	assert.Equal(t,
		"STARTCMD: mta.example.com /opt/zextras/libexec/zmdkimkeyutil \nENDCMD: mta.example.com /opt/zextras/libexec/zmdkimkeyutil \n", out,
		"the program, a space and the (empty) arguments")

	r = newRCDRig(t)
	_, out, _ = r.run(rcdLine("zmbackupldap /backup "))

	assert.Equal(t,
		"STARTCMD: mta.example.com /opt/zextras/libexec/zmbackupldap /backup \n"+
			"ENDCMD: mta.example.com /opt/zextras/libexec/zmbackupldap /backup \n", out, "trailing blanks of the arguments are kept")
}

// Everything a shell would take as syntax, and everything outside the
// alphabets of the table, is refused before anything runs.
func TestRunRCD_RefusesArgumentsOutsideTheirAlphabet(t *testing.T) {
	for _, tc := range []struct{ command, args string }{
		{"zmbackupldap", "/x;id"},
		{"zmbackupldap", "/x && id"},
		{"zmbackupldap", "/x|id"},
		{"zmbackupldap", "$(id)"},
		{"zmbackupldap", "`id`"},
		{"zmbackupldap", "/x>/etc/passwd"},
		{"zmbackupldap", "'/x'"},
		{"zmbackupldap", `"/x"`},
		{"zmbackupldap", `a\b`},
		{"zmbackupldap", "a*"},
		{"zmbackupldap", "a=b"},
		{"zmbackupldap", "a,b"},
		{"zmbackupldap", "a\tb"},
		{"zmbackupldap", "a\rb"},
		{"zmbackupldap", "\x00"},
		{"zmbackupldap", "é"},
		{"clusvcadm", "-e svc;id"},
		{"zmclustat", "$HOME"},
		{"zmloggerhostmap", "a b|c"},
		{"zmcertmgr", "createcsr $(id)"},
		{"zmcertmgr", "createcsr `id`"},
		{"zmcertmgr", "createcsr a;id"},
		{"zmcertmgr", "createcsr a|b"},
		{"zmcertmgr", "createcsr a&b"},
		{"zmcertmgr", "createcsr a>b"},
		{"zmcertmgr", "createcsr a<b"},
		{"zmcertmgr", "createcsr a(b)"},
		{"zmcertmgr", "createcsr $HOME"},
		{"zmcertmgr", "createcsr a\tb"},
		{"zmcertmgr", "bogus"},
		{"zmcertmgr", "VIEWCSR"},
		{"zmcertmgr", ""},
		{"zmcertmgr", `createcsr "unterminated`},
		{"zmcertmgr", "createcsr 'unterminated"},
		{"certbot", "renew"},
		{"certbot", "certonly; id"},
		{"dkimutil", "-|"},
		{"dkimutil", "-x"},
		{"dkimutil", "-d example"},
		{"dkimutil", "-d a.b.abcde"},
		{"dkimutil", "-a -d example.com;id"},
		{"dkimutil", "-d a.b.c|d"},
		{"rsync", "a;id"},
		{"rsync", "$(x)"},
		{"rsync", `a\b`},
		{"rsync", "a*b"},
		{"zmqaction", ""},
		{"zmqaction", "hold incoming"},
		{"zmqaction", "hold nowhere ABCDEF"},
		{"zmqaction", "HOLD incoming ALL"},
		{"zmqaction", "hold incoming ABCDE"},
		{"zmqaction", "hold incoming abcdef"},
		{"zmqaction", "hold incoming ABCDEFG"},
		{"zmqaction", "hold incoming ABCDEF,"},
		{"zmqaction", "hold incoming ALL,ABCDEF"},
		{"zmqaction", "hold incoming ABCDEF;id"},
		{"zmqaction", "hold incoming ABCDEF$(id)"},
		{"zmqaction", "hold incoming 012345678zabc"},
		{"zmqaction", "hold incoming 0123456789Zabc"},
	} {
		r := newRCDRig(t)

		status, out, errs := r.run(rcdLine(tc.command + " " + tc.args))

		assert.Equal(t, 1, status, "%s %q", tc.command, tc.args)
		assert.Empty(t, out)
		assert.Equal(t, "ERROR: args '"+strings.TrimSuffix(tc.args, "\n")+"' not allowed for command '"+tc.command+"'\n", errs,
			"%s %q", tc.command, tc.args)
		assert.Empty(t, r.argvs(), "nothing was run for %s %q", tc.command, tc.args)
	}
}

func TestRunRCD_ALongQueueIdOfEitherFormatIsAccepted(t *testing.T) {
	for _, id := range []string{"ABCDEF", "0123456789zBCD", "bcdfghjklmzbcd", "BCDFGHJKLMzbcd", "0123456789zBCDFGHJKLMNPQRSTVWXYZbcdfghjkl"} {
		r := newRCDRig(t)

		status, _, errs := r.run(rcdLine("zmqaction hold incoming " + id))

		assert.Equal(t, 0, status, id)
		assert.Empty(t, errs, id)
		assert.Len(t, r.argvs(), 1, id)
	}
}

func TestRunRCD_OtherHostsAreOnlyAnnounced(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{"HOST:other.example.com status\n", "Remote: HOST:other.example.com status \n"},
		{"HOST:other.example.com zmqstat   all  \n", "Remote: HOST:other.example.com zmqstat all  \n"},
		{"HOST:other.example.com rm -rf /\n", "Remote: HOST:other.example.com rm -rf /\n"}, // not checked: not run
		{"HOST:other.example.com\n", "Remote: HOST:other.example.com  \n"},
		{"HOST:b,HOST:a,HOST:b status\n", "Remote: HOST:a status \nRemote: HOST:b status \n"},
		{"HOST: status\n", "Remote: HOST: status \n"},
	} {
		r := newRCDRig(t)

		status, out, errs := r.run(tc.line)

		assert.Equal(t, 0, status, tc.line)
		assert.Equal(t, tc.want, out, tc.line)
		assert.Empty(t, errs)
		assert.Empty(t, r.argvs())
	}
}

func TestRunRCD_TheHostIsComparedWithoutCase(t *testing.T) {
	r := newRCDRig(t)

	status, out, _ := r.run("HOST:MTA.Example.COM status\n")

	assert.Equal(t, 0, status)
	assert.Equal(t, "STARTCMD: MTA.Example.COM /opt/zextras/bin/zmcontrol status\nENDCMD: MTA.Example.COM /opt/zextras/bin/zmcontrol status\n", out,
		"the host is shown as it was sent")
	assert.Len(t, r.argvs(), 1)
}

func TestRunRCD_HostListsResolveServicesThroughTheDirectory(t *testing.T) {
	services := map[string][]string{
		"mbox2.example.com": {"mailbox", "proxy"},
		"mbox1.example.com": {"mailbox"},
		"mta.example.com":   {"mta", "antivirus"},
		"bare.example.com":  nil,
	}

	for _, tc := range []struct{ field, want string }{
		{"SERVICE:mailbox", "Remote: HOST:mbox1.example.com status \nRemote: HOST:mbox2.example.com status \n"},
		{"SERVICE:proxy,HOST:zzz.example.com,SERVICE:mailbox", "Remote: HOST:mbox1.example.com status \nRemote: HOST:mbox2.example.com status \nRemote: HOST:zzz.example.com status \n"},
		{"SERVICE:nothing", ""},
		{"SERVICE:ALL", "Remote: HOST:mbox1.example.com status \nRemote: HOST:mbox2.example.com status \nSTARTCMD"},
		{"HOST:ALL,HOST:ignored", "Remote: HOST:mbox1.example.com status \nRemote: HOST:mbox2.example.com status \nSTARTCMD"},
		{"HOST:mbox2.example.com,SERVICE:mailbox", "Remote: HOST:mbox1.example.com status \nRemote: HOST:mbox2.example.com status \n"},
	} {
		r := newRCDRig(t)
		r.services = services

		status, out, errs := r.run(tc.field + " status\n")

		assert.Equal(t, 0, status, tc.field)
		assert.Empty(t, errs, tc.field)
		assert.True(t, strings.HasPrefix(out, tc.want), "%s: %q", tc.field, out)
	}
}

func TestRunRCD_TheDirectoryIsReadOnceAndOnlyForServices(t *testing.T) {
	r := newRCDRig(t)

	reads := 0
	r.e.rcd.services = func(*Env) (map[string][]string, error) { reads++; return map[string][]string{"a": {"mta"}}, nil }

	_, _, _ = r.run("HOST:b status\n")
	assert.Zero(t, reads, "HOST: needs no directory")

	_, _, _ = r.run("SERVICE:mta,SERVICE:mta,SERVICE:mailbox status\n")
	assert.Equal(t, 1, reads)
}

func TestRunRCD_AnUnreadableDirectoryIsAnError(t *testing.T) {
	r := newRCDRig(t)
	r.svcErr = errors.New("ldap down")

	status, out, errs := r.run("SERVICE:mta status\n")

	assert.Equal(t, 1, status)
	assert.Empty(t, out)
	assert.Equal(t, "ERROR: listing the servers: ldap down\n", errs)
}

func TestRunRCD_RefusesAnInvalidHostList(t *testing.T) {
	for _, field := range []string{"FOO:x", "host:x", ":x", ":", "x", "HOST:a,,HOST:b", "HOST:a,x", "ALL", ",HOST:a"} {
		r := newRCDRig(t)

		status, out, errs := r.run(field + " status\n")

		assert.Equal(t, 1, status, field)
		assert.Empty(t, out, field)
		assert.Equal(t, "ERROR: Invalid hostlist\n", errs, field)
		assert.Empty(t, r.argvs())
	}
}

func TestRunRCD_ABlankLineOrNoInputDoesNothing(t *testing.T) {
	for _, in := range []string{"", "\n", "   \n", "\t\r\n"} {
		r := newRCDRig(t)

		status, out, errs := r.run(in)

		assert.Equal(t, 0, status, "%q", in)
		assert.Empty(t, out)
		assert.Empty(t, errs)
		assert.Empty(t, r.argvs())
	}
}

// Perl's split(' ', $_, 3): blanks of any kind separate the words, the
// arguments are the rest of the line.
func TestRunRCD_SplitsTheLineLikeTheScript(t *testing.T) {
	for _, tc := range []struct{ in, host, command, args string }{
		{"HOST:h status\n", "HOST:h", "status", ""},
		{"HOST:h status", "HOST:h", "status", ""},
		{"  HOST:h \t status \n", "HOST:h", "status", ""},
		{"HOST:h zmqstat   all\n", "HOST:h", "zmqstat", "all"},
		{"HOST:h zmqstat all  \n", "HOST:h", "zmqstat", "all  "},
		{"HOST:h a b  c d\n", "HOST:h", "a", "b  c d"},
		{"HOST:h status\r\n", "HOST:h", "status", ""},
		{"HOST:h zmqstat all\r\n", "HOST:h", "zmqstat", "all\r"},
		{"HOST:h\n", "HOST:h", "", ""},
		{"HOST:h", "HOST:h", "", ""},
		{"\n", "", "", ""},
		{"", "", "", ""},
	} {
		host, command, args := rcdSplitLine(tc.in)

		assert.Equal(t, []string{tc.host, tc.command, tc.args}, []string{host, command, args}, "%q", tc.in)
	}
}

func TestRunRCD_OnlyTheFirstCommandLineRuns(t *testing.T) {
	r := newRCDRig(t)

	status, _, _ := r.run(rcdLine("status") + rcdLine("shutdown") + "garbage\n")

	assert.Equal(t, 0, status)
	assert.Equal(t, [][]string{{rcdBinDir + "zmcontrol", "status"}}, r.argvs())
}

func TestRunRCD_ALineLongerThanTheLimitIsRefused(t *testing.T) {
	r := newRCDRig(t)

	status, out, errs := r.run(rcdLine("zmbackupldap " + strings.Repeat("a", rcdMaxLine)))

	assert.Equal(t, 1, status)
	assert.Empty(t, out)
	assert.Equal(t, "ERROR: line too long\n", errs)
	assert.Empty(t, r.argvs())

	r = newRCDRig(t)
	status, _, errs = r.run(rcdLine("zmbackupldap " + strings.Repeat("a", rcdMaxLine/2)))

	assert.Equal(t, 0, status, "a long line below the limit is a command: %s", errs)
}

func TestRunRCD_HelpListsTheTableAndReadsOn(t *testing.T) {
	r := newRCDRig(t)
	r.reply(rcdBinDir+"zmcontrol", "up\n", "", 0)

	status, out, errs := r.run("?\n? and more\n" + rcdLine("status"))

	assert.Equal(t, 0, status)
	assert.Empty(t, errs)
	assert.Equal(t, [][]string{{rcdBinDir + "zmcontrol", "status"}}, r.argvs(), "the line after the help is the command")

	help, command, found := strings.Cut(out, "STARTCMD")
	require.True(t, found, out)
	assert.Equal(t, "STARTCMD: mta.example.com /opt/zextras/bin/zmcontrol status\nup\nENDCMD: mta.example.com /opt/zextras/bin/zmcontrol status\n",
		"STARTCMD"+command)

	lines := strings.Split(strings.TrimSuffix(help, "\n"), "\n")
	half := len(lines) / 2

	assert.Equal(t, lines[:half], lines[half:], "every help line is printed once per request")
	assert.Equal(t, "downloadcsr -> cat /opt/zextras/ssl/carbonio/commercial/commercial.csr", lines[0])
	assert.Contains(t, lines, "slapcat -> /opt/zextras/common/sbin/slapcat -F /opt/zextras/data/ldap/config -b ''")
	assert.Contains(t, lines, "zmcollectconfigfiles -> tar cv /opt/zextras/conf | gzip -cf")
	assert.Contains(t, lines, `dkimutil (-[a|r])?\s?(-d\s(([\w-]+\.)+[\w-]{2,4}))? -> /opt/zextras/libexec/zmdkimkeyutil <arg>`)
	assert.Contains(t, lines, `rsync ['"a-zA-Z0-9\/\.\-_:@= ]* -> rsync <arg>`)
	assert.True(t, strings.HasPrefix(lines[half-1], "zmqaction (?^:(hold|release|requeue|delete)"), lines[half-1])
}

func TestRunRCD_HelpAtTheEndOfInputEndsTheRun(t *testing.T) {
	r := newRCDRig(t)

	status, out, _ := r.run("?")

	assert.Equal(t, 0, status)
	assert.NotEmpty(t, out)
	assert.Empty(t, r.argvs())
}

func TestRunRCD_TakesNoArguments(t *testing.T) {
	r := newRCDRig(t)

	var out, errb bytes.Buffer

	status := RunRCD(r.e, []string{"/etc/passwd"}, strings.NewReader(rcdLine("status")), &out, &errb)

	assert.Equal(t, 1, status)
	assert.Empty(t, out.String())
	assert.Equal(t, RCDUsage, errb.String())
	assert.Empty(t, r.argvs())
}

func TestRunRCD_AnUnreadableLocalConfigIsAnError(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "localconfig.xml")
	require.NoError(t, os.WriteFile(bad, []byte("<localconfig><key"), 0o600))

	r := newRCDRig(t)
	r.e.lc = nil
	r.e.Paths.LocalConfig = bad

	status, out, errs := r.run(rcdLine("status"))

	assert.Equal(t, 1, status)
	assert.Empty(t, out)
	assert.Contains(t, errs, "ERROR: reading the local configuration: ")
	assert.Empty(t, r.argvs())
}

func TestRunRCD_ReadsTheRealEnvironmentAndLocalConfigByDefault(t *testing.T) {
	t.Setenv(rcdOriginalCommand, "")

	r := newRCDRig(t)
	r.e.rcd = nil

	status, out, errs := r.run("HOST:elsewhere status\n")

	assert.Equal(t, 0, status)
	assert.Equal(t, "Remote: HOST:elsewhere status \n", out)
	assert.Empty(t, errs)
}

func rcdLog(t *testing.T, r *rcdRig) string {
	t.Helper()

	data, ok := r.fakes.FS.Data(rcdTestLog)
	require.True(t, ok, "the log file exists")

	return string(data)
}

func TestRunRCD_LogsWhatItPrintsWhenEnabled(t *testing.T) {
	r := newRCDRig(t)
	r.e.lc[rcdLoggingKey] = "True" // the script compared uc()
	r.fakes.FS.PutDir("/opt/zextras/log", 0o755)
	r.reply(rcdBinDir+"zmcontrol", "Host mta\n\tservice up\nlast", "", 0)

	status, _, _ := r.run("HOST:other status\n")
	require.Equal(t, 0, status)

	status, _, _ = r.run(rcdLine("status"))
	require.Equal(t, 0, status)

	status, _, _ = r.run(rcdLine("bogus"))
	require.Equal(t, 1, status)

	assert.Equal(t, "Remote: HOST:other status \n"+
		"STARTCMD: mta.example.com /opt/zextras/bin/zmcontrol status\nHost mta\n\tservice up\nlast\n"+
		"ENDCMD: mta.example.com /opt/zextras/bin/zmcontrol status\n"+
		"Unknown command: \"bogus\"\n", rcdLog(t, r), "what the script logged: the lines it printed, errors without the prefix")

	mode, _ := r.fakes.FS.Mode(rcdTestLog)
	assert.Equal(t, fs.FileMode(0o640), mode.Perm())
}

func TestRunRCD_DoesNotLogTheOutputOfTheCommandsThatCarryDataOrKeys(t *testing.T) {
	r := newRCDRig(t)
	r.e.lc[rcdLoggingKey] = "TRUE"
	r.fakes.FS.PutDir("/opt/zextras/log", 0o755)
	r.reply("/opt/zextras/common/sbin/slapcat", "userPassword: {SSHA}secret\n", "", 0)
	r.reply("gzip", "conf bytes with a key", "", 0)

	_, out, _ := r.run(rcdLine("slapcat"))
	assert.Contains(t, out, "userPassword: {SSHA}secret\n", "the caller still gets the output")

	_, out, _ = r.run(rcdLine("zmcollectconfigfiles"))
	assert.Contains(t, out, "conf bytes with a key")

	log := rcdLog(t, r)

	assert.NotContains(t, log, "secret")
	assert.NotContains(t, log, "conf bytes")
	assert.Contains(t, log, "STARTCMD: mta.example.com /opt/zextras/common/sbin/slapcat -F /opt/zextras/data/ldap/config -b ''\n")
	assert.Contains(t, log, "ENDCMD: mta.example.com /opt/zextras/common/sbin/slapcat -F /opt/zextras/data/ldap/config -b ''\n")
}

func TestRunRCD_WritesNoLogWhenDisabledOrWhenItCannotBeOpened(t *testing.T) {
	for _, logging := range []string{"false", "", "yes"} {
		r := newRCDRig(t)
		r.e.lc[rcdLoggingKey] = logging
		r.fakes.FS.PutDir("/opt/zextras/log", 0o755)

		status, _, _ := r.run(rcdLine("status"))

		assert.Equal(t, 0, status)
		assert.False(t, r.fakes.FS.Exists(rcdTestLog), "logging %q", logging)
	}

	// No log directory: the run goes on, like the script's open without die.
	r := newRCDRig(t)
	r.e.lc[rcdLoggingKey] = "TRUE"
	r.fakes.FS.Put("/opt/zextras/log", []byte("a file, not a directory"), 0o644)

	status, out, _ := r.run(rcdLine("status"))

	assert.Equal(t, 0, status)
	assert.Contains(t, out, "STARTCMD")
}

func TestRunRCD_RunsAnRsyncOfSSHInPlaceOfTheLoop(t *testing.T) {
	for _, tc := range []struct {
		original string
		argv     []string
	}{
		{"rsync --server -vlogDtpre.iLsfxCIvu . /opt/zextras/backup",
			[]string{"rsync", "--server", "-vlogDtpre.iLsfxCIvu", ".", "/opt/zextras/backup"}},
		{"/opt/zextras/common/bin/rsync --server -av . /dst", []string{"/opt/zextras/common/bin/rsync", "--server", "-av", ".", "/dst"}},
		{"rsync --server --exclude='a b' . /dst", []string{"rsync", "--server", "--exclude=a b", ".", "/dst"}},
		{`rsync --server -e"x y" "a b"`, []string{"rsync", "--server", "-ex y", "a b"}},
	} {
		r := newRCDRig(t)
		r.original = tc.original

		status, out, errs := r.run(rcdLine("shutdown"))

		assert.Equal(t, 1, status, "the process is replaced; if that fails zmrcd stops: %s", tc.original)
		assert.Equal(t, [][]string{tc.argv}, r.execve, tc.original)
		assert.Empty(t, out)
		assert.Contains(t, errs, "ERROR: Can't run "+tc.original+": ")
		assert.Empty(t, r.argvs(), "the commands on stdin are not read")
	}
}

func TestRunRCD_AnRsyncThatCannotBeExecutedIsTheSessionsError(t *testing.T) {
	r := newRCDRig(t)
	r.original = "rsync --server . /d"
	r.execErr = &fs.PathError{Err: syscall.ENOENT}

	status, _, errs := r.run("")

	assert.Equal(t, 1, status)
	assert.Equal(t, "ERROR: Can't run rsync --server . /d: No such file or directory\n", errs)
}

func TestRunRCD_RefusesAnRsyncThatIsNotTheOneOfTheNode(t *testing.T) {
	for _, tc := range []struct{ original, want string }{
		{"/usr/bin/rsync --server x", "command '/usr/bin/rsync' not allowed"},
		{"/tmp/rsync --server x", "command '/tmp/rsync' not allowed"},
		{"./rsync x", "command './rsync' not allowed"},
		{"xrsync --server x", "command 'xrsync' not allowed"},
		{"rsyncx\t--server", ""}, // not rsync at all: goes on to the commands
		{"rsync --server x;id", "invalid arguments in command [rsync --server x;id]"},
		{"rsync --server $(id)", "invalid arguments in command [rsync --server $(id)]"},
		{"rsync --server `id`", "invalid arguments in command [rsync --server `id`]"},
		{"rsync --server a|b", "invalid arguments in command [rsync --server a|b]"},
		{"rsync --server a>b", "invalid arguments in command [rsync --server a>b]"},
		{"rsync --server a*", "invalid arguments in command [rsync --server a*]"},
		{`rsync --server a\ b`, `invalid arguments in command [rsync --server a\ b]`},
		{`rsync --server "unterminated`, `invalid arguments in command [rsync --server "unterminated]`},
		{"rsync --server 'unterminated", "invalid arguments in command [rsync --server 'unterminated]"},
	} {
		r := newRCDRig(t)
		r.original = tc.original

		status, out, errs := r.run(rcdLine("shutdown"))

		assert.Empty(t, r.execve, "nothing was executed for %q", tc.original)

		if tc.want == "" {
			assert.Equal(t, 0, status, tc.original)
			assert.Contains(t, out, "STARTCMD", tc.original)

			continue
		}

		assert.Equal(t, 1, status, tc.original)
		assert.Empty(t, out, tc.original)
		assert.Equal(t, "ERROR: "+tc.want+"\n", errs, tc.original)
		assert.Empty(t, r.argvs(), "the commands on stdin are not read")
	}
}

func TestRunRCD_OtherSessionsRunTheCommandsOnStdin(t *testing.T) {
	for _, original := range []string{"", "/opt/zextras/libexec/zmrcd", " rsync x", "echo rsync"} {
		r := newRCDRig(t)
		r.original = original

		status, _, _ := r.run(rcdLine("status"))

		assert.Equal(t, 0, status, original)
		assert.Equal(t, [][]string{{rcdBinDir + "zmcontrol", "status"}}, r.argvs(), original)
		assert.Empty(t, r.execve, original)
	}
}

func TestRunRCD_ALoggedRsyncIsWrittenToTheLog(t *testing.T) {
	r := newRCDRig(t)
	r.e.lc[rcdLoggingKey] = "TRUE"
	r.fakes.FS.PutDir("/opt/zextras/log", 0o755)
	r.original = "rsync --server . /d"

	_, _, _ = r.run("")

	assert.Equal(t, "exec'ing: rsync --server . /d\nCan't run rsync --server . /d: Permission denied\n", rcdLog(t, r))
}

func TestRCDServiceMap_ListsEnabledServicesOfEveryServer(t *testing.T) {
	m, err := rcdServiceMap(rcdListerFunc(func() (map[string]map[string]string, error) {
		return map[string]map[string]string{
			"a": {"zimbraServiceEnabled": "mta\nproxy", "zimbraId": "1"},
			"b": {"ZIMBRASERVICEENABLED": "mailbox"},
			"c": {"zimbraId": "3"},
		}, nil
	}))

	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"a": {"mta", "proxy"}, "b": {"mailbox"}}, m, "a server with no service is not a host of ALL")

	_, err = rcdServiceMap(rcdListerFunc(func() (map[string]map[string]string, error) { return nil, errors.New("down") }))
	require.EqualError(t, err, "down")
}

type rcdListerFunc func() (map[string]map[string]string, error)

func (f rcdListerFunc) GetAllServersWithAttributes() (map[string]map[string]string, error) {
	return f()
}

func TestRCDWords_SplitLikeAShellWithoutExpansions(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
		ok   bool
	}{
		{"", nil, true},
		{"   ", nil, true},
		{"a b\tc\nd", []string{"a", "b", "c", "d"}, true},
		{"'a b' c", []string{"a b", "c"}, true},
		{`"a b" c`, []string{"a b", "c"}, true},
		{`'a"b' "a'b"`, []string{`a"b`, "a'b"}, true},
		{`"a\"b" "a\\b" "a\nb" "a\$b"`, []string{`a"b`, `a\b`, `a\nb`, `a\$b`}, true},
		{`a\ b a\\b a\'b`, []string{"a b", `a\b`, "a'b"}, true},
		{`'a\b'`, []string{`a\b`}, true},
		{`''`, []string{""}, true},
		{`"" x`, []string{"", "x"}, true},
		{`a''b ""c`, []string{"ab", "c"}, true},
		{"'a'\"b\"c", []string{"abc"}, true},
		{"$(id) `id` $HOME *.pem ~", []string{"$(id)", "`id`", "$HOME", "*.pem", "~"}, true},
		{`a\`, []string{`a\`}, true},
		{`"a`, nil, false},
		{`'a`, nil, false},
		{`a "b 'c'`, nil, false},
	} {
		got, ok := rcdWords(tc.in)

		assert.Equal(t, tc.ok, ok, "%q", tc.in)
		assert.Equal(t, tc.want, got, "%q", tc.in)
	}
}

// The pipeline runs real processes: each stage feeds the next, and the status
// is the one of the last.
func TestRCDPipeline_ConnectsRealProcesses(t *testing.T) {
	e := &Env{Ctx: context.Background(), Sys: sys.Real()}
	s := &rcdSession{e: e, errw: io.Discard}

	var out bytes.Buffer

	status, err := s.pipeline([][]string{{"printf", "b\\na\\nc\\n"}, {"sort"}, {"tr", "a-c", "A-C"}}, &out)

	require.NoError(t, err)
	assert.Equal(t, 0, status)
	assert.Equal(t, "A\nB\nC\n", out.String())

	out.Reset()

	status, err = s.pipeline([][]string{{"false"}, {"sh", "-c", "cat >/dev/null; exit 4"}}, &out)

	require.NoError(t, err)
	assert.Equal(t, 4, status, "the last stage answers, the first one's failure does not")
}

func TestRCDPipeline_AFirstStageThatEndsEarlyDoesNotHangTheLast(t *testing.T) {
	e := &Env{Ctx: context.Background(), Sys: sys.Real()}
	s := &rcdSession{e: e, errw: io.Discard}

	var out bytes.Buffer

	// head closes its input after a line; yes would write for ever.
	status, err := s.pipeline([][]string{{"yes"}, {"head", "-n", "1"}}, &out)

	require.NoError(t, err)
	assert.Equal(t, 0, status)
	assert.Equal(t, "y\n", out.String())
}

func TestRCDPipeline_ACommandKilledBySignalIs128PlusTheSignal(t *testing.T) {
	e := &Env{Ctx: context.Background(), Sys: sys.Real()}
	s := &rcdSession{e: e, errw: io.Discard}

	status, err := s.pipeline([][]string{{"sh", "-c", "kill -KILL $$"}}, io.Discard)

	require.NoError(t, err)
	assert.Equal(t, 128+int(syscall.SIGKILL), status)
}

func TestRCDPipeline_AMissingProgramIsStartError(t *testing.T) {
	e := &Env{Ctx: context.Background(), Sys: sys.Real()}
	s := &rcdSession{e: e, errw: io.Discard}

	_, err := s.pipeline([][]string{{"/nonexistent/zmrcd-test-program"}}, io.Discard)

	require.Error(t, err)
	assert.Equal(t, "No such file or directory", rcdErrText(err))

	_, err = s.pipeline([][]string{{"zmrcd-test-program-not-in-path"}}, io.Discard)

	require.Error(t, err)
	assert.Equal(t, "No such file or directory", rcdErrText(err))
}

func TestRCDExecve_AProgramThatIsNotThereIsAnErrorAndNothingIsReplaced(t *testing.T) {
	err := rcdExecve("zmrcd-test-program-not-in-path", []string{"zmrcd-test-program-not-in-path"})

	require.Error(t, err)
	assert.Equal(t, "No such file or directory", rcdErrText(err))
}

func TestRCDExitCode_AnUnknownDeathIsStatus1(t *testing.T) {
	assert.Equal(t, 7, rcdExitCode(&sys.ExitError{Code: 7}))
	assert.Equal(t, 1, rcdExitCode(&sys.ExitError{Code: -1}))
}

func TestRunRCD_OutputOfConcurrentStagesIsNotInterleavedWithinWrites(t *testing.T) {
	// The relay and the stderr of several stages are written from several
	// goroutines: with -race this fails on an unguarded writer.
	r := newRCDRig(t)

	var wg sync.WaitGroup

	for range 4 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, _, _ = newRCDRig(t).run(rcdLine("zmcollectconfigfiles"))
		}()
	}

	wg.Wait()

	status, _, _ := r.run(rcdLine("zmcollectconfigfiles"))
	assert.Equal(t, 0, status)
}
