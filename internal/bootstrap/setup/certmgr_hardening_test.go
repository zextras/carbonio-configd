// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	cmRootSecret = "/etc/shadow"
	cmRootOnly   = "root-only\n"
	cmSlapdKey   = "/opt/zextras/conf/slapd.key"
	cmSymlinkErr = "symbolic link"
)

// Root works in trees zextras controls: a link planted there must not make it
// write to, or read from, a file only root can reach.
func TestRunCertMgr_RootDoesNotFollowLinks(t *testing.T) {
	t.Run("a link is not written through", func(t *testing.T) {
		h := newCMTest(t, nil)
		h.ok("createca", "-new")
		h.fakes.FS.Put(cmRootSecret, []byte(cmRootOnly), 0o600)
		require.NoError(t, h.fakes.FS.Remove(cmSrl))
		h.fakes.FS.PutLink(cmSrl, cmRootSecret)

		status, out, errs := h.run("createcrt")

		assert.NotEqual(t, 0, status, "stdout: %s", out)
		assert.Contains(t, out+errs, cmSymlinkErr)
		assert.Equal(t, cmRootOnly, h.read(cmRootSecret), "the target of the link is untouched")
	})

	t.Run("a dangling link does not make root create its target", func(t *testing.T) {
		h := newCMTest(t, nil)
		h.fakes.FS.PutLink(cmCAKey, "/etc/planted")

		status, out, errs := h.run("createca", "-new")

		assert.NotEqual(t, 0, status, "stdout: %s", out)
		assert.Contains(t, out+errs, cmSymlinkErr)
		assert.False(t, h.fakes.FS.Exists("/etc/planted"))
	})

	t.Run("a key that is a link is not copied", func(t *testing.T) {
		h := newCMTest(t, nil)
		h.bootstrap()
		h.fakes.FS.Put(cmRootSecret, []byte(cmRootOnly), 0o600)
		require.NoError(t, h.fakes.FS.Remove(cmSelfKey))
		require.NoError(t, h.fakes.FS.Remove(cmSlapdKey))
		h.fakes.FS.PutLink(cmSelfKey, cmRootSecret)

		status, out, _ := h.run("deploycrt", "self", "-deploy", "ldap")

		assert.Equal(t, 1, status, out)
		assert.Contains(t, out, cmSymlinkErr)

		data, ok := h.fakes.FS.Data(cmSlapdKey)
		assert.True(t, !ok || len(data) == 0, "the content of the target must not reach a file zextras can read")
	})

	t.Run("a link given on the command line is what was asked for", func(t *testing.T) {
		h := newCMTest(t, nil)
		h.bootstrap()
		crtf, caf := h.commercial()
		h.fakes.FS.PutLink("/tmp/commercial/live.crt", crtf)

		h.ok("deploycrt", "comm", "/tmp/commercial/live.crt", caf, "-deploy", "ldap")

		assert.Equal(t, h.read(crtf), h.read(cmCommCrt)[:len(h.read(crtf))])
	})
}

// zextras follows its own links: one to a key that lives elsewhere gives it
// nothing it did not have.
func TestRunCertMgr_ZextrasFollowsItsOwnLinks(t *testing.T) {
	h := newCMTest(t, nil)
	h.e.IsZextras = true
	h.ok("createca", "-new")
	require.NoError(t, h.fakes.FS.Remove(cmSrl))
	h.fakes.FS.Put("/var/lib/serial", []byte("0A\n"), 0o640)
	h.fakes.FS.PutLink(cmSrl, "/var/lib/serial")

	h.ok("createcrt")

	assert.NotEqual(t, "0A\n", h.read("/var/lib/serial"), "the serial went to where the link points")
}

// A key never exists with the process's owner or umask: its mode and owner
// are set through the open file before the first byte is written.
func TestRunCertMgr_NewFilesAreOwnedBeforeTheyAreWritten(t *testing.T) {
	h := newCMTest(t, nil)

	h.ok("createca", "-new")

	chmods, chowns := h.fakes.Rec.Of("fs.fchmod"), h.fakes.Rec.Of("fs.fchown")
	assert.Contains(t, chmods, cmCAKey+" mode=0600")
	assert.Contains(t, chowns, cmCAKey+" 998:997")
	assert.Empty(t, h.fakes.Rec.Of("fs.chown"), "no file is given away by its name: a path can be swapped for a link")
}

// An unreadable directory is not an empty value: getcrt must not remove the
// key and the certificate that are there.
func TestRunCertMgr_GetCRTKeepsFilesWhenLDAPCannotBeRead(t *testing.T) {
	for typ, files := range map[string][]string{
		"self": {cmSelfCrt, cmSelfKey},
		"comm": {cmCommCrt, cmCommKey, cmCommCA},
	} {
		t.Run(typ, func(t *testing.T) {
			h := newCMTest(t, nil)
			h.bootstrap()

			for _, f := range files {
				h.fakes.FS.Put(f, []byte("local "+f+"\n"), 0o640)
			}

			h.dir.readErr = assert.AnError

			status, out, _ := h.run("getcrt", typ)

			assert.Equal(t, 1, status)
			assert.Contains(t, out, "ERROR: getcrt: cannot read 'zimbraSSLCertificate' from LDAP")

			for _, f := range files {
				assert.Equal(t, "local "+f+"\n", h.read(f), f)
			}
		})
	}
}

// A certificate that Go parses more strictly than OpenSSL does not make the
// whole bundle unreadable: the chain is verified with the certificates that
// do parse, as openssl verify -CAfile does.
func TestRunCertMgr_CABundleWithAnUnparsableCertificate(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()

	ca := h.read(cmCACrt)
	bad := string(certPEMBlock(certPEMCert, []byte("not der")))

	h.fakes.FS.Put(cmCACrt, []byte(bad+ca), 0o640)
	status, out, _ := h.run("verifycrt", "self")
	assert.Equal(t, 1, status, "the first certificate is the leaf: it must parse")
	assert.Contains(t, out, "Error loading file "+cmCACrt)

	h.fakes.FS.Put(cmCACrt, []byte(ca+bad), 0o640)
	assert.Contains(t, h.ok("verifycrt", "self"), "Valid certificate chain: "+cmSelfCrt+": OK\n")
}

func TestCertParseCerts_SkipsUnparsableCertificatesAfterTheLeaf(t *testing.T) {
	h := newCMTest(t, nil)
	h.ok("createca", "-new")
	h.ok("createcrt")

	leaf, ca := h.read(cmSelfCrt), h.read(cmCACrt)
	bad := string(certPEMBlock(certPEMCert, []byte("not der")))

	certs, err := certParseCerts([]byte(leaf + bad + ca))
	require.NoError(t, err)
	assert.Len(t, certs, 2, "the one that does not parse is left out")

	_, err = certParseCerts([]byte(bad + leaf))
	require.Error(t, err, "a leaf that does not parse is an error")
}
