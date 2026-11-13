// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"io/fs"
	"math/big"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The groups -new gives are the RFC 7919 ones: safe primes of the size asked,
// with the 64 high and low bits set, so a typo in a constant is caught.
func TestDHGroups_AreRFC7919SafePrimes(t *testing.T) {
	all := new(big.Int).SetUint64(^uint64(0))

	for _, bits := range dhGroupSizes() {
		data, err := dhGroupPEM(bits)
		require.NoError(t, err)

		block, _ := pem.Decode(data)
		require.NotNil(t, block)

		var params dhParams

		_, err = asn1.Unmarshal(block.Bytes, &params)
		require.NoError(t, err)

		assert.Equal(t, bits, params.P.BitLen())
		assert.Equal(t, int64(2), params.G.Int64())
		assert.True(t, params.P.ProbablyPrime(5), "p is prime")
		assert.True(t, new(big.Int).Rsh(params.P, 1).ProbablyPrime(5), "(p-1)/2 is prime")
		assert.Zero(t, new(big.Int).Rsh(params.P, uint(bits-64)).Cmp(all), "high 64 bits are set")
		assert.Zero(t, new(big.Int).And(params.P, all).Cmp(all), "low 64 bits are set")
	}

	assert.Equal(t, []int{2048, 3072, 4096}, dhGroupSizes())

	_, err := dhGroupPEM(1024)
	require.Error(t, err)
}

func TestDHCheckPEM(t *testing.T) {
	good, err := dhGroupPEM(2048)
	require.NoError(t, err)

	t.Run("skips what surrounds the block and returns it canonical", func(t *testing.T) {
		messy := "junk\r\n" + strings.ReplaceAll(string(good), "\n", "\r\n") + "trailing\n"

		got, err := dhCheckPEM([]byte(messy))
		require.NoError(t, err)
		assert.Equal(t, string(good), string(got))
	})

	for _, tc := range []struct {
		name string
		data func() []byte
		want string
	}{
		{"no PEM", func() []byte { return []byte("hello") }, "no PEM"},
		{"another block", func() []byte { return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1}}) },
			"no PEM"},
		{"not DER", func() []byte { return pem.EncodeToMemory(&pem.Block{Type: dhPEMType, Bytes: []byte("x")}) },
			"not PKCS#3"},
		{"composite p", func() []byte { return dhTestPEM(t, big.NewInt(21*2+1+2), big.NewInt(2)) }, "not prime"},
		{"tiny p", func() []byte { return dhTestPEM(t, big.NewInt(3), big.NewInt(2)) }, "too small"},
		{"g is 1", func() []byte { return dhTestPEM(t, big.NewInt(23), big.NewInt(1)) }, "generator"},
		{"g is p-1", func() []byte { return dhTestPEM(t, big.NewInt(23), big.NewInt(22)) }, "generator"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := dhCheckPEM(tc.data())
			require.ErrorContains(t, err, tc.want)
		})
	}

	t.Run("a small but valid group", func(t *testing.T) {
		_, err := dhCheckPEM(dhTestPEM(t, big.NewInt(23), big.NewInt(5)))
		require.NoError(t, err)
	})
}

func dhTestPEM(t *testing.T, p, g *big.Int) []byte {
	t.Helper()

	der, err := asn1.Marshal(dhParams{P: p, G: g})
	require.NoError(t, err)

	return pem.EncodeToMemory(&pem.Block{Type: dhPEMType, Bytes: der})
}

func TestParseDHParamArgs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		want   dhOptions
		errs   []string
		failed string // what Getopt::Long printed; the options are then not "parsed"
	}{
		{name: "get", args: []string{"get"}, want: dhOptions{Command: "get"}},
		{
			name: "get with files", args: []string{"get", "-in", "a.pem", "--out=b.pem", "-debug"},
			want: dhOptions{Command: "get", In: "a.pem", HasIn: true, Out: "b.pem", HasOut: true, Debug: 1},
		},
		{
			name: "abbreviations and case", args: []string{"set", "-N", "3072", "-O", "-", "-D", "2"},
			want: dhOptions{Command: "set", Bits: 3072, HasNew: true, Out: "-", HasOut: true, Debug: 2},
		},
		{
			name: "new without a number", args: []string{"set", "-new"},
			want: dhOptions{Command: "set", HasNew: true},
		},
		{
			name: "new followed by another option", args: []string{"set", "-new", "-out", "f"},
			want: dhOptions{Command: "set", HasNew: true, Out: "f", HasOut: true},
		},
		{
			name: "debug 0 is off", args: []string{"get", "-debug=0"},
			want: dhOptions{Command: "get"},
		},
		{
			name: "ldap is accepted", args: []string{"get", "-ldap"},
			want: dhOptions{Command: "get"},
		},
		{name: "help", args: []string{"get", "-help"}, want: dhOptions{Command: "get", Help: true}},
		{name: "man", args: []string{"set", "-man"}, want: dhOptions{Command: "set", Man: true},
			errs: []string{"-new or -in must be specified"}},
		{
			name: "help needs no command", args: []string{"-h"},
			want: dhOptions{Help: true}, errs: []string{"a command must be specified"},
		},
		{name: "no command", args: nil, errs: []string{"a command must be specified"}},
		{
			name: "unknown command", args: []string{"put", "-debug"},
			want: dhOptions{Debug: 1}, errs: []string{"unexpected argument(s): put"},
		},
		{
			name: "extra arguments", args: []string{"get", "x", "--", "-y"},
			want: dhOptions{Command: "get"}, errs: []string{"unexpected argument(s): x -y"},
		},
		{
			name: "in and out are one file", args: []string{"get", "-in", "a.pem", "-out", "./a.pem"},
			want: dhOptions{Command: "get", In: "a.pem", HasIn: true, Out: "./a.pem", HasOut: true},
			errs: []string{"-in and -out can not match"},
		},
		{
			name: "stdin and stdout may match", args: []string{"get", "-in", "-", "-out", "-"},
			want: dhOptions{Command: "get", In: "-", HasIn: true, Out: "-", HasOut: true},
		},
		{
			name: "set needs a source", args: []string{"set"},
			want: dhOptions{Command: "set"}, errs: []string{"-new or -in must be specified"},
		},
		{
			name: "new and in are exclusive", args: []string{"set", "-new", "-in", "f"},
			want: dhOptions{Command: "set", HasNew: true, In: "f", HasIn: true},
			errs: []string{"-new and -in are exclusive arguments"},
		},
		{
			name: "unknown option", args: []string{"get", "-bogus"},
			want: dhOptions{Command: "get"}, failed: "Unknown option: bogus\n",
		},
		{
			name: "new is for set only", args: []string{"get", "-new"},
			want: dhOptions{Command: "get"}, failed: "Unknown option: new\n",
		},
		{
			name: "missing value", args: []string{"get", "-out"},
			want: dhOptions{Command: "get"}, failed: "Option out requires an argument\n",
		},
		{
			name: "flag with a value", args: []string{"get", "-help=1"},
			want: dhOptions{Command: "get"}, failed: "Option help does not take an argument\n",
		},
		{
			name: "not a number", args: []string{"set", "-new=big"},
			want: dhOptions{Command: "set"}, errs: []string{"-new or -in must be specified"},
			failed: "Value \"big\" invalid for option new (number expected)\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer

			p := parseDHParamArgs(tc.args, &stderr)

			assert.Equal(t, tc.want, p.opts)
			assert.Equal(t, tc.failed, stderr.String())
			assert.Equal(t, tc.failed == "", p.parsed)

			assert.Equal(t, tc.errs, p.errs)
		})
	}
}

// dhTestRun is one run of zmdhparam against fakes.
type dhTestRun struct {
	code   int
	stdout string
	stderr string
}

func dhExec(t *testing.T, e *Env, stdin string, args ...string) dhTestRun {
	t.Helper()

	var out, errb bytes.Buffer

	code := RunDHParam(e, args, strings.NewReader(stdin), &out, &errb)

	return dhTestRun{code, out.String(), errb.String()}
}

// dhGlobalConfig makes the global config hold value for zimbraSSLDHParam (or
// fail); sequential tests only.
func dhGlobalConfig(t *testing.T, value string, err error) {
	t.Helper()

	orig := certGlobalConfig

	t.Cleanup(func() { certGlobalConfig = orig })

	certGlobalConfig = func(*Env) (map[string]string, error) {
		if err != nil {
			return nil, err
		}

		return map[string]string{certDHParamAttr: value}, nil
	}
}

func TestRunDHParam_Get(t *testing.T) {
	ffdhe2048, err := dhGroupPEM(2048)
	require.NoError(t, err)

	t.Run("prints the parameters of LDAP", func(t *testing.T) {
		e, _, _ := testEnv(t)
		dhGlobalConfig(t, "junk\n"+string(ffdhe2048), nil)

		r := dhExec(t, e, "", "get")

		assert.Equal(t, dhTestRun{0, string(ffdhe2048), ""}, r)
	})

	t.Run("writes them to a file and says so", func(t *testing.T) {
		e, fakes, _ := testEnv(t)
		dhGlobalConfig(t, string(ffdhe2048), nil)

		r := dhExec(t, e, "", "get", "-out", "/tmp/dh.pem")

		assert.Equal(t, dhTestRun{0, "zmdhparam: writing params to '/tmp/dh.pem'\n", ""}, r)

		data, ok := fakes.FS.Data("/tmp/dh.pem")
		require.True(t, ok)
		assert.Equal(t, string(ffdhe2048), string(data))

		r = dhExec(t, e, "", "get", "-out", "/tmp/dh.pem")
		assert.Equal(t, "zmdhparam: (over)writing params to '/tmp/dh.pem'\n", r.stdout)
	})

	t.Run("-out writes in place and keeps the mode and owner of the file", func(t *testing.T) {
		e, fakes, _ := testEnv(t)
		dhGlobalConfig(t, string(ffdhe2048), nil)
		fakes.FS.Put("/tmp/dh.pem", []byte(strings.Repeat("old", 5000)), 0o600)
		require.NoError(t, fakes.FS.Chown("/tmp/dh.pem", 123, 456))

		r := dhExec(t, e, "", "get", "-out", "/tmp/dh.pem")
		assert.Equal(t, 0, r.code)

		data, _ := fakes.FS.Data("/tmp/dh.pem")
		assert.Equal(t, string(ffdhe2048), string(data), "truncated, not appended")

		mode, _ := fakes.FS.Mode("/tmp/dh.pem")
		assert.Equal(t, fs.FileMode(0o600), mode.Perm())

		uid, gid, _ := fakes.FS.Owner("/tmp/dh.pem")
		assert.Equal(t, [2]int{123, 456}, [2]int{uid, gid})
	})

	t.Run("-out - is stdout", func(t *testing.T) {
		e, _, _ := testEnv(t)
		dhGlobalConfig(t, string(ffdhe2048), nil)

		assert.Equal(t, dhTestRun{0, string(ffdhe2048), ""}, dhExec(t, e, "", "get", "-out", "-"))
	})

	t.Run("checks a file and prints it", func(t *testing.T) {
		e, fakes, _ := testEnv(t)
		fakes.FS.Put("/tmp/in.pem", []byte("x\n"+string(ffdhe2048)), 0o644)

		assert.Equal(t, dhTestRun{0, string(ffdhe2048), ""}, dhExec(t, e, "", "get", "-in", "/tmp/in.pem"))
	})

	t.Run("copies a file to another, silently", func(t *testing.T) {
		e, fakes, _ := testEnv(t)
		fakes.FS.Put("/tmp/in.pem", ffdhe2048, 0o644)

		assert.Equal(t, dhTestRun{0, "", ""}, dhExec(t, e, "", "get", "-in", "/tmp/in.pem", "-out", "/tmp/out.pem"))

		data, _ := fakes.FS.Data("/tmp/out.pem")
		assert.Equal(t, string(ffdhe2048), string(data))
	})

	t.Run("reads stdin", func(t *testing.T) {
		e, _, _ := testEnv(t)

		assert.Equal(t, dhTestRun{0, string(ffdhe2048), ""}, dhExec(t, e, string(ffdhe2048), "get", "-in", "-"))
	})

	for _, tc := range []struct {
		name   string
		global string
		gerr   error
		stdin  string
		args   []string
		want   string
	}{
		{"unset in LDAP", "", nil, "", []string{"get"}, "zmdhparam: ERROR: no zimbraSSLDHParam in LDAP\n"},
		{"LDAP down", "", errors.New("down"), "", []string{"get"},
			"zmdhparam: ERROR: reading zimbraSSLDHParam from LDAP: down\n"},
		{"garbage in LDAP", "garbage", nil, "", []string{"get"},
			"zmdhparam: ERROR: check zimbraSSLDHParam in LDAP failed: no PEM \"DH PARAMETERS\" block\n"},
		{"empty stdin", "", nil, "", []string{"get", "-in", "-"},
			"zmdhparam: ERROR: no DH parameters in '-'\n"},
		{"missing file", "", nil, "", []string{"get", "-in", "/nope"},
			"zmdhparam: ERROR: In '/nope': unable to read file\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, _ := testEnv(t)
			dhGlobalConfig(t, tc.global, tc.gerr)

			r := dhExec(t, e, tc.stdin, tc.args...)

			assert.Equal(t, 1, r.code)
			assert.Equal(t, tc.want, r.stderr)
			assert.NotContains(t, r.stdout, "BEGIN")
		})
	}
}

func TestRunDHParam_Set(t *testing.T) {
	const global = "cn=config,cn=zimbra"

	for _, bits := range dhGroupSizes() {
		t.Run("new group in LDAP", func(t *testing.T) {
			e, _, _ := testEnv(t)
			want, err := dhGroupPEM(bits)
			require.NoError(t, err)

			r := dhExec(t, e, "", "set", "-new", strconv.Itoa(bits))

			assert.Equal(t, dhTestRun{0, "zmdhparam: saving 'zimbraSSLDHParam' to LDAP\n", ""}, r)
			assert.Equal(t, []string{string(want)}, provTestLDAP().values(global, certDHParamAttr),
				"the attribute holds the PEM, not the path of the temporary file")
		})
	}

	t.Run("default size", func(t *testing.T) {
		e, _, _ := testEnv(t)
		want, _ := dhGroupPEM(2048)

		r := dhExec(t, e, "", "set", "-new")

		assert.Equal(t, 0, r.code)
		assert.Equal(t, "zmdhparam: defaulting numbits to '2048'\nzmdhparam: saving 'zimbraSSLDHParam' to LDAP\n", r.stdout)
		assert.Equal(t, []string{string(want)}, provTestLDAP().values(global, certDHParamAttr))
	})

	t.Run("-in from stdin or file", func(t *testing.T) {
		e, fakes, _ := testEnv(t)
		want, _ := dhGroupPEM(3072)
		fakes.FS.Put("/tmp/in.pem", want, 0o644)

		for _, args := range [][]string{{"set", "-in", "/tmp/in.pem"}, {"set", "-in", "-"}} {
			provTestLDAP().set(global, certDHParamAttr)

			r := dhExec(t, e, string(want), args...)

			assert.Equal(t, 0, r.code, r.stderr)
			assert.Equal(t, []string{string(want)}, provTestLDAP().values(global, certDHParamAttr))
		}
	})

	t.Run("-out writes a file and leaves LDAP alone", func(t *testing.T) {
		e, fakes, _ := testEnv(t)
		want, _ := dhGroupPEM(2048)

		r := dhExec(t, e, "", "set", "-new", "2048", "-out", "/tmp/new.pem")

		assert.Equal(t, dhTestRun{0, "zmdhparam: writing params to '/tmp/new.pem'\n", ""}, r)

		data, _ := fakes.FS.Data("/tmp/new.pem")
		assert.Equal(t, string(want), string(data))
		assert.Empty(t, provTestLDAP().Calls)

		// zmdhparam did nothing for -in with -out; it now copies.
		fakes.FS.Put("/tmp/in.pem", want, 0o644)

		r = dhExec(t, e, "", "set", "-in", "/tmp/in.pem", "-out", "/tmp/copy.pem")
		assert.Equal(t, 0, r.code)
		assert.True(t, fakes.FS.Exists("/tmp/copy.pem"))
		assert.Empty(t, provTestLDAP().Calls)
	})

	t.Run("an invalid file is not stored", func(t *testing.T) {
		e, _, _ := testEnv(t)

		r := dhExec(t, e, dhTestPEMString(t, big.NewInt(21), big.NewInt(2)), "set", "-in", "-")

		assert.Equal(t, 1, r.code)
		assert.Contains(t, r.stderr, "check DH parameters from '-' failed: p value is not prime")
		assert.Empty(t, provTestLDAP().Calls)

		r = dhExec(t, e, "", "set", "-in", "-")
		assert.Equal(t, "zmdhparam: ERROR: no DH parameters in '-'\n", r.stderr)
	})

	t.Run("a failure of LDAP", func(t *testing.T) {
		e, _, _ := testEnv(t)
		provTestLDAP().FailModify[global] = errors.New("insufficient access")

		r := dhExec(t, e, "", "set", "-new", "2048")

		assert.Equal(t, 1, r.code)
		assert.Contains(t, r.stderr, "zmdhparam: ERROR: save to LDAP failed: provisioning failed")
		assert.Contains(t, r.stderr, "insufficient access")
	})

	t.Run("an unusable temporary directory", func(t *testing.T) {
		e, fakes, _ := testEnv(t)
		fakes.FS.Fail = func(op, _ string) error {
			if op == "CreateTemp" {
				return errors.New("disk full")
			}

			return nil
		}

		r := dhExec(t, e, "", "set", "-new", "2048")

		assert.Equal(t, 1, r.code)
		assert.Contains(t, r.stderr, "save to LDAP failed: disk full")
	})
}

func dhTestPEMString(t *testing.T, p, g *big.Int) string { return string(dhTestPEM(t, p, g)) }

func TestRunDHParam_RefusedCommandLines(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{"no arguments", nil, 1, "zmdhparam: a command must be specified\n" + dhSynopsis, ""},
		{"help", []string{"get", "-help"}, 0, dhSynopsis, ""},
		{"man", []string{"get", "-man"}, 0, dhSynopsis + dhNotes, ""},
		{"unknown option", []string{"get", "-x"}, 1, dhSynopsis, "Unknown option: x\n"},
		{"neither -new nor -in", []string{"set"}, 1, "zmdhparam: -new or -in must be specified\n" + dhSynopsis, ""},
		{"too small", []string{"set", "-new", "1024"}, 1, dhSynopsis, "zmdhparam: ERROR: numbits '1024' must be >= 2048\n"},
		{
			"no group", []string{"set", "-new", "5000"}, 1, dhSynopsis,
			"zmdhparam: ERROR: numbits '5000' is not the size of an RFC 7919 group (2048, 3072, 4096)\n",
		},
		{"unreadable -in", []string{"set", "-in", "/nope"}, 1, dhSynopsis, "zmdhparam: ERROR: In '/nope': unable to read file\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, _ := testEnv(t)

			r := dhExec(t, e, "", tc.args...)

			assert.Equal(t, dhTestRun{tc.code, tc.stdout, tc.stderr}, r)
			assert.Empty(t, provTestLDAP().Calls)
		})
	}
}

func TestRunDHParam_DebugGoesToStderr(t *testing.T) {
	e, _, _ := testEnv(t)

	r := dhExec(t, e, "", "set", "-new", "2048", "-debug")

	assert.Equal(t, 0, r.code)
	assert.Equal(t, "DEBUG: using the RFC 7919 group of 2048 bits\n", r.stderr)
	assert.NotContains(t, r.stdout, "DEBUG")
}

// A -out file that cannot be opened or written is an error naming the file.
func TestRunDHParam_OutThatCannotBeWritten(t *testing.T) {
	for want, fault := range map[string]faultFS{
		"disk full": {write: errors.New("disk full")},
		"read-only": {},
	} {
		t.Run(want, func(t *testing.T) {
			e, fakes, _ := testEnv(t)
			fault.FS = e.Sys.FS
			e.Sys.FS = fault

			if want == "read-only" {
				fakes.FS.Fail = func(op, name string) error {
					if op == "OpenFile" && name == "/tmp/new.pem" {
						return errors.New("read-only")
					}

					return nil
				}
			}

			r := dhExec(t, e, "", "set", "-new", "2048", "-out", "/tmp/new.pem")

			assert.Equal(t, 1, r.code)
			assert.Contains(t, r.stderr, "create '/tmp/new.pem' failed: "+want)
		})
	}
}
