// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"errors"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const (
	iniTestFile   = "/opt/zextras/conf/my.cnf"
	iniTestSample = "# my.cnf\n[mysqld]\nport = 3306\nbind-address = 127.0.0.1  # local only\n" +
		"max_connections=100\nname = foo\n\n[client]\nport=3306\n"
)

func iniRun(t *testing.T, content string, args ...string) (out, errw string, code int, fakes *systest.Fakes) {
	t.Helper()

	_, fakes, _ = ldapTestEnv(t)
	fakes.FS.Put(iniTestFile, []byte(content), 0o640)

	var o, e bytes.Buffer

	code = RunIniUtil(fakes.FS, append(args, iniTestFile), &o, &e)

	return o.String(), e.String(), code, fakes
}

// TestRunIniUtil_Modifications follows libexec/iniutil.pl 210-277: set
// (rewrites the line without its comment), set without value, unset, a key
// that is new (inserted after the header, with the blank line the script
// prints, 235-243), setmin and setmax (numeric compare, a non numeric value
// counts as 0, 258-271), only inside the section asked.
func TestRunIniUtil_Modifications(t *testing.T) {
	m := "--section=mysqld"

	cases := map[string]struct {
		args []string
		want string
	}{
		"set replaces the line": {
			[]string{"--set", m, "--key=bind-address", "--value=::1", "--backup=.b"},
			"# my.cnf\n[mysqld]\nport = 3306\nbind-address = ::1\nmax_connections=100\nname = foo\n\n[client]\nport=3306\n",
		},
		"set without value": {
			[]string{"--set", m, "--key=bind-address", "--backup=.b"},
			"# my.cnf\n[mysqld]\nport = 3306\nbind-address\nmax_connections=100\nname = foo\n\n[client]\nport=3306\n",
		},
		"unset": {
			[]string{"--unset", m, "--key=bind-address", "--backup=.b"},
			"# my.cnf\n[mysqld]\nport = 3306\nmax_connections=100\nname = foo\n\n[client]\nport=3306\n",
		},
		"new key after the header": {
			[]string{"--set", m, "--key=skip", "--value=1", "--backup=.b"},
			"# my.cnf\n[mysqld]\n\nskip = 1\nport = 3306\nbind-address = 127.0.0.1  # local only\n" +
				"max_connections=100\nname = foo\n\n[client]\nport=3306\n",
		},
		"new key without value": {
			[]string{"--set", "--section=client", "--key=skip", "--backup=.b"},
			"# my.cnf\n[mysqld]\nport = 3306\nbind-address = 127.0.0.1  # local only\nmax_connections=100\n" +
				"name = foo\n\n[client]\n\nskip\nport=3306\n",
		},
		"unknown section": {
			[]string{"--set", "--section=none", "--key=skip", "--value=1", "--backup=.b"}, iniTestSample,
		},
		"setmin raises": {
			[]string{"--setmin", m, "--key=max_connections", "--value=200", "--backup=.b"},
			"# my.cnf\n[mysqld]\nport = 3306\nbind-address = 127.0.0.1  # local only\nmax_connections = 200\n" +
				"name = foo\n\n[client]\nport=3306\n",
		},
		"setmin keeps a bigger value": {
			[]string{"--setmin", m, "--key=max_connections", "--value=50", "--backup=.b"}, iniTestSample,
		},
		"setmax lowers": {
			[]string{"--setmax", m, "--key=max_connections", "--value=50", "--backup=.b"},
			"# my.cnf\n[mysqld]\nport = 3306\nbind-address = 127.0.0.1  # local only\nmax_connections = 50\n" +
				"name = foo\n\n[client]\nport=3306\n",
		},
		"setmax keeps a smaller value": {
			[]string{"--setmax", m, "--key=max_connections", "--value=500", "--backup=.b"}, iniTestSample,
		},
		"a text counts as 0 for setmin": {
			[]string{"--setmin", m, "--key=name", "--value=5", "--backup=.b"},
			"# my.cnf\n[mysqld]\nport = 3306\nbind-address = 127.0.0.1  # local only\nmax_connections=100\n" +
				"name = 5\n\n[client]\nport=3306\n",
		},
		"a text counts as 0 for setmax": {
			[]string{"--setmax", m, "--key=name", "--value=5", "--backup=.b"}, iniTestSample,
		},
		"setmin does not create the key before the header": {
			[]string{"--setmin", m, "--key=new", "--value=5", "--backup=.b"},
			"# my.cnf\n[mysqld]\n\nnew = 5\nport = 3306\nbind-address = 127.0.0.1  # local only\n" +
				"max_connections=100\nname = foo\n\n[client]\nport=3306\n",
		},
		"the other section is left alone": {
			[]string{"--unset", "--section=client", "--key=port", "--backup=.b"},
			"# my.cnf\n[mysqld]\nport = 3306\nbind-address = 127.0.0.1  # local only\nmax_connections=100\n" +
				"name = foo\n\n[client]\n",
		},
	}

	for name, c := range cases {
		out, errw, code, fakes := iniRun(t, iniTestSample, c.args...)

		assert.Equal(t, 0, code, name)
		assert.Empty(t, out+errw, name)

		got, _ := fakes.FS.Data(iniTestFile)
		assert.Equal(t, c.want, string(got), name)

		backup, ok := fakes.FS.Data(iniTestFile + ".b")
		assert.True(t, ok, name)
		assert.Equal(t, iniTestSample, string(backup), "%s: the backup is the original", name)

		mode, _ := fakes.FS.Mode(iniTestFile)
		assert.Equal(t, os.FileMode(0o640), mode.Perm(), "%s: the mode stays", name)
	}
}

func TestRunIniUtil_UnsetWithoutBackupWritesDotBak(t *testing.T) { // 223-227
	_, _, code, fakes := iniRun(t, iniTestSample, "--unset", "--section=client", "--key=port")

	assert.Equal(t, 0, code)

	backup, ok := fakes.FS.Data(iniTestFile + ".bak")
	assert.True(t, ok)
	assert.Equal(t, iniTestSample, string(backup))
}

func TestRunIniUtil_EveryLineOfTheKeyIsChanged(t *testing.T) {
	_, _, code, fakes := iniRun(t, "[a]\nk=1\nk=2\n[b]\nk=3\n", "--unset", "--section=a", "--key=k")

	assert.Equal(t, 0, code)

	got, _ := fakes.FS.Data(iniTestFile)
	assert.Equal(t, "[a]\n[b]\nk=3\n", string(got))
}

// iniutil.pl 175-208: list, get and isset do not touch the file.
func TestRunIniUtil_Queries(t *testing.T) {
	cases := map[string]struct {
		args []string
		out  string
		code int
	}{
		"list is sorted":     {[]string{"--list"}, "client->port=3306\nmysqld->bind-address=127.0.0.1\nmysqld->max_connections=100\nmysqld->name=foo\nmysqld->port=3306\n", 0},
		"get":                {[]string{"--get", "--section=mysqld", "--key=bind-address"}, "127.0.0.1\n", 0},
		"get is per section": {[]string{"--get", "--section=client", "--key=port"}, "3306\n", 0},
		"get missing":        {[]string{"--get", "--section=mysqld", "--key=nope"}, "", 1},
		"isset":              {[]string{"--isset", "--section=mysqld", "--key=port"}, "", 0},
		"isset missing":      {[]string{"--isset", "--section=client", "--key=bind-address"}, "", 1},
	}

	for name, c := range cases {
		out, errw, code, fakes := iniRun(t, iniTestSample, c.args...)

		assert.Equal(t, c.out, out, name)
		assert.Equal(t, c.code, code, name)
		assert.Empty(t, errw, name)
		assert.False(t, fakes.FS.Exists(iniTestFile+".bak"), name)
	}
}

func TestRunIniUtil_KeyWithoutValueIsSetButEmpty(t *testing.T) {
	out, _, code, _ := iniRun(t, "[a]\nskip-networking\n", "--get", "--section=a", "--key=skip-networking")

	assert.Equal(t, "\n", out)
	assert.Equal(t, 0, code)
}

func TestRunIniUtil_Debug(t *testing.T) { // dp, 101-109, and 124-141
	out, _, code, _ := iniRun(t, "[s]\nk = v\nj # c\n", "--debug", "--list")

	assert.Equal(t, 0, code)
	assert.Equal(t, "parse: new section=s\n\n"+
		"parser: ==== content='k = v\n'\nparser: section='s'\nparser: keyPre=''\nparser: key='k'\n"+
		"parser: keyPost=' '\nparser: equals='='\nparser: valuePre=' '\nparser: value='v'\nparser: valuePost='\n'\n"+
		"parser: ==== content='j # c\n'\nparser: section='s'\nparser: keyPre=''\nparser: key='j'\n"+
		"parser: keyPost=' '\nparser: equals=''\nparser: valuePre=''\nparser: value=''\nparser: valuePost='# c'\n"+
		"s->j=\ns->k=v\n", out)
}

// iniutil.pl 21-98: every usage error is printed on stdout with exit 1.
func TestRunIniUtil_UsageErrors(t *testing.T) {
	sk := []string{"--section=a", "--key=b"}
	cases := map[string]struct {
		args []string
		want string
	}{
		"no action": {nil, "Error: one of [list|isset|get|set|setmin|setmax|unset] must be specified\n"},
		"two actions": {
			[]string{"--get", "--set"}, "Error: only one of [list|isset|get|set|setmin|setmax|unset] must be specified\n",
		},
		"no key":     {[]string{"--get", "--section=a"}, "Error: --key not specified\n"},
		"no section": {[]string{"--get", "--key=a"}, "Error: --section not specified\n"},
		"value on get": {
			append([]string{"--get", "--value=1"}, sk...), "Error: --value can only be specified with [set|setmin|setmax]\n",
		},
		"setmin without value": {
			append([]string{"--setmin", "--backup=.b"}, sk...), "Error: --value must be specified with [setmin|setmax]\n",
		},
		"no backup": {
			append([]string{"--set"}, sk...), "Error: --backup must be specified with [set|setmin|setmax|unset]\n",
		},
		"value not a number": {
			append([]string{"--setmax", "--value=x", "--backup=.b"}, sk...), "Error: --value must be a number\n",
		},
	}

	for name, c := range cases {
		out, _, code, _ := iniRun(t, iniTestSample, c.args...)

		assert.Equal(t, 1, code, name)
		assert.Equal(t, c.want, out, name)
	}
}

func TestRunIniUtil_FileArgument(t *testing.T) {
	_, fakes, _ := ldapTestEnv(t)

	var out, errb bytes.Buffer

	assert.Equal(t, 1, RunIniUtil(fakes.FS, []string{"--list"}, &out, &errb))
	assert.Equal(t, "Error: one inifile must be specified\n", out.String())

	out.Reset()
	assert.Equal(t, 1, RunIniUtil(fakes.FS, []string{"--list", "a", "b"}, &out, &errb))
	assert.Equal(t, "Error: one inifile must be specified\n", out.String())
}

func TestRunIniUtil_HelpAndUnknownOption(t *testing.T) {
	_, fakes, _ := ldapTestEnv(t)

	var out, errb bytes.Buffer

	assert.Equal(t, 0, RunIniUtil(fakes.FS, []string{"--help"}, &out, &errb))
	assert.Contains(t, out.String(), "Usage: ")
	assert.Contains(t, out.String(), "--setmin")

	out.Reset()
	assert.Equal(t, 1, RunIniUtil(fakes.FS, []string{"--bogus", "f"}, &out, &errb))
	assert.Equal(t, "Error: Unknown option!\n", out.String())
	assert.Equal(t, "Unknown option: bogus\n", errb.String())
}

func TestRunIniUtil_MissingFileExitsWithErrno(t *testing.T) { // die: the exit status is $!
	_, fakes, _ := ldapTestEnv(t)

	var out, errb bytes.Buffer

	code := RunIniUtil(fakes.FS, []string{"--list", "/opt/zextras/conf/none.cnf"}, &out, &errb)

	assert.NotEqual(t, 0, code)
	assert.Contains(t, errb.String(), "can't open file /opt/zextras/conf/none.cnf")

	assert.Equal(t, 2, (&iniDie{err: &os.PathError{Err: syscall.ENOENT}}).status())
	assert.Equal(t, 255, (&iniDie{err: errors.New("x")}).status())
}

func TestIniApply_ConsistentWithRender(t *testing.T) {
	_, fakes, _ := ldapTestEnv(t)
	fakes.FS.Put(iniTestFile, []byte("[a]\nk=1"), 0o600)

	status, err := iniApply(fakes.FS, iniTestFile, &iniEdit{
		Op: iniSet, Section: "a", Key: "k", Value: "2", HasValue: true, Backup: ".x", HasBackup: true, HasSection: true,
	}, &bytes.Buffer{}, &bytes.Buffer{})
	require.NoError(t, err)
	assert.Equal(t, 0, status)

	got, _ := fakes.FS.Data(iniTestFile)
	assert.Equal(t, "[a]\nk = 2\n", string(got), "a last line without line end gets one")
}
