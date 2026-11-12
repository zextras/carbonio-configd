// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"errors"
	"io/fs"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// faultFS opens files that fail on demand, as a full disk or a filesystem
// without ownership would.
type faultFS struct {
	sys.FS
	write, chmod, chown error
}

func (f faultFS) OpenFile(name string, flag int, perm fs.FileMode) (sys.File, error) {
	file, err := f.FS.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}

	return faultFile{File: file, write: f.write, chmod: f.chmod, chown: f.chown}, nil
}

type faultFile struct {
	sys.File
	write, chmod, chown error
}

func (f faultFile) Write(p []byte) (int, error) {
	if f.write != nil {
		return 0, f.write
	}

	return f.File.Write(p)
}

func (f faultFile) Chmod(mode fs.FileMode) error {
	if f.chmod != nil {
		return f.chmod
	}

	return f.File.Chmod(mode)
}

func (f faultFile) Chown(uid, gid int) error {
	if f.chown != nil {
		return f.chown
	}

	return f.File.Chown(uid, gid)
}

// A link that appears between the check and the open is refused too: the open
// itself does not follow it.
func TestRunCertMgr_RootRefusesALinkSwappedInAfterTheCheck(t *testing.T) {
	h := newCMTest(t, nil)
	h.fakes.FS.PutLink(cmCAKey, "/etc/planted")
	h.fakes.FS.Fail = func(op, name string) error {
		if op == "Lstat" && name == cmCAKey {
			return errors.New("not there yet")
		}

		return nil
	}

	status, out, errs := h.run("createca", "-new")

	assert.NotEqual(t, 0, status, "stdout: %s", out)
	assert.Contains(t, out+errs, cmSymlinkErr)
	assert.False(t, h.fakes.FS.Exists("/etc/planted"))
}

// What the operator names is read through a link as zextras too: only the
// script's own trees are the object of the link check, and zextras may follow
// its own links there.
func TestRunCertMgr_ZextrasCopiesAKeyThatIsALink(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()
	h.e.IsZextras = true
	h.fakes.FS.Put("/var/lib/real.key", []byte("real key\n"), 0o600)
	require.NoError(t, h.fakes.FS.Remove(cmSelfKey))
	require.NoError(t, h.fakes.FS.Remove(cmSlapdKey))
	h.fakes.FS.PutLink(cmSelfKey, "/var/lib/real.key")

	h.ok("deploycrt", "self", "-deploy", "ldap")

	assert.Equal(t, "real key\n", h.read(cmSlapdKey))
}

// A new file whose mode or owner cannot be set, or whose data cannot be
// written, is an error of the command, not a key left with the wrong owner.
func TestRunCertMgr_NewFileThatCannotBeInitialised(t *testing.T) {
	for want, fault := range map[string]faultFS{
		"chmod refused": {chmod: errors.New("chmod refused")},
		"chown refused": {chown: errors.New("chown refused")},
		"disk full":     {write: errors.New("disk full")},
	} {
		t.Run(want, func(t *testing.T) {
			h := newCMTest(t, nil)
			fault.FS = h.e.Sys.FS
			h.e.Sys.FS = fault

			status, out, errs := h.run("createca", "-new")

			assert.Equal(t, 1, status, "stdout: %s", out)
			assert.Contains(t, strings.ToLower(out+errs), want)
		})
	}
}

// getcrt that cannot read LDAP keeps the local files; createcrt without -new
// then makes a new certificate rather than giving up.
func TestRunCertMgr_CreateCRTCarriesOnWhenLDAPCannotBeRead(t *testing.T) {
	h := newCMTest(t, nil)
	h.ok("createca", "-new")
	h.dir.readErr = assert.AnError

	status, out, errs := h.run("createcrt")

	assert.Equal(t, 0, status, "stdout: %s\nstderr: %s", out, errs)
	assert.Contains(t, out, "ERROR: getcrt: cannot read 'zimbraSSLCertificate' from LDAP")
	assert.NotNil(t, h.cert(cmSelfCrt), "a new certificate was made")
}

// A zmrc that cannot even be started is reported with the reason.
func TestRunCertMgr_RemoteThatCannotBeStarted(t *testing.T) {
	h := newCMTest(t, nil)
	h.dir.all = []string{cmHost, "a.example.com"}
	h.remoteStartErr = errors.New("fork/exec zmrc: no such file")

	h.ok("createca", "-new")

	status, out, _ := h.run("deploycrt", "self", "-allservers")

	assert.Equal(t, 1, status)
	assert.Contains(t, out, "ERROR: zmrc: zmcertmgr getcrt self -allservers failed(1):\n/opt/zextras/libexec/zmrc: fork/exec zmrc: no such file\n")
}

// A certificate file that cannot be read cannot be checked either.
func TestRunCertMgr_CheckCRTExpirationOfAnUnreadableFile(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()
	h.fakes.FS.Fail = func(op, name string) error {
		if op == "ReadFile" && name == "/opt/zextras/conf/nginx.crt" {
			return errors.New("input/output error")
		}

		return nil
	}

	status, out, _ := h.run("checkcrtexpiration", "proxy")

	assert.Equal(t, 1, status)
	assert.Equal(t, "proxy: '/opt/zextras/conf/nginx.crt' CANNOT BE CHECKED: input/output error\n", out)
}

// closedLDAPURL is the URL of a port nothing listens on.
func closedLDAPURL(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())

	return "ldap://127.0.0.1:" + strconv.Itoa(port)
}

// Without an injected directory the script reaches LDAP itself; when it is
// down (early in setup, before slapd starts) getcrt says it could not read it
// and keeps the files it has, with or without -debug.
func TestRunCertMgr_RealLDAPThatIsDown(t *testing.T) {
	for _, debug := range []bool{false, true} {
		h := newCMTest(t, map[string]string{
			"ldap_url":                closedLDAPURL(t),
			"ldap_master_url":         closedLDAPURL(t),
			"ldap_starttls_supported": "0",
			"zimbra_ldap_userdn":      "uid=zimbra,cn=admins,cn=zimbra",
			"zimbra_ldap_password":    "secret",
		})
		h.bootstrap()
		h.e.certDir = nil
		h.e.Ctx = nil
		h.fakes.FS.Put(cmSelfCrt, []byte("local crt\n"), 0o640)

		args := []string{"getcrt", "self"}
		if debug {
			args = append(args, "-debug")
		}

		status, out, _ := h.run(args...)

		assert.Equal(t, 1, status, "debug=%v\nstdout: %s", debug, out)
		assert.Contains(t, out, "ERROR: getcrt: cannot read 'zimbraSSLCertificate' from LDAP")
		assert.Equal(t, "local crt\n", h.read(cmSelfCrt), "the local certificate is kept")
	}
}
