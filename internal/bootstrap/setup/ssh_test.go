// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const (
	sshTestHost     = "store.example.com"
	sshTestKeyPath  = "/opt/zextras/.ssh/zimbra_identity"
	sshTestAuthPath = "/opt/zextras/.ssh/authorized_keys"
	sshTestCmd      = `command="/opt/zextras/libexec/zmrcd" `
)

// sshEnv is a node whose localconfig knows its host name and the zextras ids;
// logs gets the log file.
func sshEnv(t *testing.T) (e *Env, fakes *systest.Fakes, logs *bytes.Buffer) {
	t.Helper()

	e, fakes = svcEnv(t)
	e.Paths.LocalConfig = t.TempDir() + "/localconfig.xml"
	logs = &bytes.Buffer{}
	e.Log = NewLog(&bytes.Buffer{}, logs)

	require.NoError(t, e.SetLC(map[string]string{
		"zimbra_server_hostname": sshTestHost, "zimbra_uid": "999", "zimbra_gid": "998",
	}))

	return e, fakes, logs
}

// sshFail makes the filesystem operation op fail with msg.
func sshFail(fakes *systest.Fakes, op, msg string) {
	fakes.FS.Fail = func(o, _ string) error {
		if o == op {
			return errors.New(msg)
		}

		return nil
	}
}

func assertSSHFile(t *testing.T, fakes *systest.Fakes, name string, perm uint32) []byte {
	t.Helper()

	data, ok := fakes.FS.Data(name)
	require.True(t, ok, "%s must exist", name)

	mode, _ := fakes.FS.Mode(name)
	assert.EqualValues(t, perm, mode.Perm(), "mode of %s", name)

	uid, gid, _ := fakes.FS.Owner(name)
	assert.Equal(t, [2]int{999, 998}, [2]int{uid, gid}, "owner of %s: zextras", name)

	return data
}

func TestSSHKeygen_WritesTheKeyPairAndPublishesTheKey(t *testing.T) {
	e, fakes, _ := sshEnv(t)
	dir := &svcFakeDir{}
	svcWithDir(t, dir)

	e.Facts.NewInstall = true
	require.NoError(t, StepZmsshkeygen().Apply(e))

	assert.Empty(t, fakes.Exec.Lines(), "no zmsshkeygen, no ssh-keygen")

	mode, _ := fakes.FS.Mode("/opt/zextras/.ssh")
	assert.EqualValues(t, 0o700, mode.Perm())

	uid, gid, _ := fakes.FS.Owner("/opt/zextras/.ssh")
	assert.Equal(t, [2]int{999, 998}, [2]int{uid, gid})

	// The private key is what ssh-keygen -m PEM writes: unencrypted PKCS#1.
	privPEM := assertSSHFile(t, fakes, sshTestKeyPath, 0o600)
	assert.True(t, bytes.HasPrefix(privPEM, []byte("-----BEGIN RSA PRIVATE KEY-----\n")))
	assert.True(t, bytes.HasSuffix(privPEM, []byte("-----END RSA PRIVATE KEY-----\n")))

	block, _ := pem.Decode(privPEM)
	require.NotNil(t, block)
	assert.Empty(t, block.Headers, "no passphrase")

	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	require.NoError(t, err)
	assert.Equal(t, 2048, priv.N.BitLen())

	// The public key: "ssh-rsa <base64> <host>", of that private key.
	pubFile := assertSSHFile(t, fakes, sshTestKeyPath+".pub", 0o644)
	require.True(t, strings.HasSuffix(string(pubFile), "\n"))
	assert.Equal(t, 1, strings.Count(string(pubFile), "\n"))

	pub, comment, _, _, err := ssh.ParseAuthorizedKey(pubFile)
	require.NoError(t, err)
	assert.Equal(t, "ssh-rsa", pub.Type())
	assert.Equal(t, sshTestHost, comment)

	want, err := ssh.NewPublicKey(&priv.PublicKey)
	require.NoError(t, err)
	assert.Equal(t, want.Marshal(), pub.Marshal())

	// The same line goes to the server entry.
	assert.Equal(t, map[string]map[string]string{
		sshTestHost: {"zimbraSshPublicKey": strings.TrimSpace(string(pubFile))},
	}, dir.set)
}

func TestSSHKeygen_ReplacesTheOldKeyEveryRun(t *testing.T) {
	e, fakes, _ := sshEnv(t)
	dir := &svcFakeDir{}
	svcWithDir(t, dir)

	fakes.FS.Put(sshTestKeyPath, []byte("old private key"), 0o644)
	fakes.FS.Put(sshTestKeyPath+".pub", []byte("ssh-rsa AAAAold "+sshTestHost+"\n"), 0o600)

	require.NoError(t, GenerateSSHKey(e))

	first := assertSSHFile(t, fakes, sshTestKeyPath, 0o600)
	assert.NotContains(t, string(first), "old private key")

	pub1 := assertSSHFile(t, fakes, sshTestKeyPath+".pub", 0o644)
	assert.NotContains(t, string(pub1), "AAAAold")

	require.NoError(t, GenerateSSHKey(e))

	second := assertSSHFile(t, fakes, sshTestKeyPath, 0o600)
	assert.NotEqual(t, first, second, "a re-run generates a new key")
	assert.Equal(t, strings.TrimSpace(string(assertSSHFile(t, fakes, sshTestKeyPath+".pub", 0o644))),
		dir.set[sshTestHost]["zimbraSshPublicKey"], "and publishes that one")
}

func TestSSHKeygen_Failures(t *testing.T) {
	t.Run("no uid in localconfig", func(t *testing.T) {
		e, fakes, _ := sshEnv(t)
		svcWithDir(t, &svcFakeDir{})
		require.NoError(t, e.SetLC(map[string]string{"zimbra_uid": "-1"}))

		err := GenerateSSHKey(e)
		require.ErrorContains(t, err, "zimbra_uid")
		assert.False(t, fakes.FS.Exists(sshTestKeyPath), "no key is written with unknown ownership")
	})

	t.Run("LDAP refuses the key", func(t *testing.T) {
		e, fakes, _ := sshEnv(t)
		svcWithDir(t, &svcFakeDir{setErr: errors.New("noSuchObject")})

		err := GenerateSSHKey(e)
		require.ErrorContains(t, err, "zimbraSshPublicKey")
		assert.ErrorContains(t, err, sshTestHost)
		assert.ErrorContains(t, err, "noSuchObject")
		assert.True(t, fakes.FS.Exists(sshTestKeyPath), "as the script, the files stay")
	})

	t.Run("no host name", func(t *testing.T) {
		e, _, _ := sshEnv(t)
		svcWithDir(t, &svcFakeDir{})
		e.Paths.LocalConfig = t.TempDir() + "/none.xml"
		e.lc = map[string]string{"zimbra_uid": "999", "zimbra_gid": "998"}

		require.ErrorContains(t, GenerateSSHKey(e), "zimbra_server_hostname")
	})

	t.Run("filesystem refuses", func(t *testing.T) {
		e, fakes, _ := sshEnv(t)
		svcWithDir(t, &svcFakeDir{})
		sshFail(fakes, "WriteFileAtomic", "disk full")

		require.ErrorContains(t, GenerateSSHKey(e), "disk full")
	})
}

func TestSSHKeygen_OnlyOnNewInstalls(t *testing.T) {
	e, _, _ := sshEnv(t)
	s := StepZmsshkeygen()

	assert.False(t, s.When(e))

	e.Facts.NewInstall = true
	assert.True(t, s.When(e))
	assert.True(t, s.Optional)
}

const (
	sshTestKeyA = "ssh-rsa AAAAa host-a.example.com"
	sshTestKeyB = "ssh-rsa AAAAb host-b.example.com"
	sshTestKeyC = "ssh-rsa AAAAc host-c.example.com"
)

func TestSSHAuthKeys_ReplacesTheHostLineKeepsTheOthersAddsTheNew(t *testing.T) {
	e, fakes, logs := sshEnv(t)
	svcWithDir(t, &svcFakeDir{keys: map[string]string{
		"host-a.example.com": "ssh-rsa AAAAa2 host-a.example.com", // changed
		"host-b.example.com": sshTestKeyB,                         // unchanged
		"host-c.example.com": sshTestKeyC,                         // new
		"host-d.example.com": "",                                  // no key
	}})
	fakes.FS.Put(sshTestAuthPath, []byte(
		"ssh-rsa AAAAother manual-entry\n"+
			sshTestCmd+sshTestKeyA+"\n"+
			sshTestCmd+"ssh-rsa AAAAgone host-gone.example.com\n"+
			sshTestCmd+sshTestKeyB+"\n\n\n"), 0o600)

	require.NoError(t, StepZmupdateauthkeys().Apply(e))

	got := assertSSHFile(t, fakes, sshTestAuthPath, 0o644)
	assert.Equal(t,
		"ssh-rsa AAAAother manual-entry\n"+ // a line with a 3rd, no 4th field is never ours
			sshTestCmd+"ssh-rsa AAAAgone host-gone.example.com\n"+ // other hosts' lines stay in order
			sshTestCmd+"ssh-rsa AAAAa2 host-a.example.com\n"+ // servers are taken in name order, each
			sshTestCmd+sshTestKeyB+"\n"+ // line dropped and appended again
			sshTestCmd+sshTestKeyC+"\n",
		string(got))

	assert.Contains(t, logs.String(), "Key for host-d.example.com NOT FOUND")
	assert.NotContains(t, logs.String(), "Key for host-a.example.com NOT FOUND")
	assert.Empty(t, fakes.Exec.Lines(), "no zmupdateauthkeys, no zmprov")
}

func TestSSHAuthKeys_SecondRunChangesNothing(t *testing.T) {
	e, fakes, _ := sshEnv(t)
	svcWithDir(t, &svcFakeDir{keys: map[string]string{
		"host-a.example.com": sshTestKeyA, "host-b.example.com": sshTestKeyB, "host-c.example.com": sshTestKeyC,
	}})

	require.NoError(t, UpdateAuthKeys(e))

	first := string(assertSSHFile(t, fakes, sshTestAuthPath, 0o644))
	assert.Equal(t, sshTestCmd+sshTestKeyA+"\n"+sshTestCmd+sshTestKeyB+"\n"+sshTestCmd+sshTestKeyC+"\n", first,
		"no file yet: no leading blank line, one trailing newline")

	require.NoError(t, UpdateAuthKeys(e))
	assert.Equal(t, first, string(assertSSHFile(t, fakes, sshTestAuthPath, 0o644)))
}

func TestSSHAuthKeys_NoKeysAtAllKeepsTheFile(t *testing.T) {
	e, fakes, logs := sshEnv(t)
	svcWithDir(t, &svcFakeDir{keys: map[string]string{"host-a.example.com": ""}})
	fakes.FS.Put(sshTestAuthPath, []byte("ssh-rsa AAAAother manual-entry\n\n"), 0o600)

	require.NoError(t, UpdateAuthKeys(e))

	assert.Equal(t, "ssh-rsa AAAAother manual-entry\n", string(assertSSHFile(t, fakes, sshTestAuthPath, 0o644)))
	assert.Contains(t, logs.String(), "Key for host-a.example.com NOT FOUND")
}

func TestSSHAuthKeys_KeyWithoutHostCommentDropsLinesWithoutFourthField(t *testing.T) {
	// awk '{print $3}' printed nothing, which matched the lines awk '{print $4}'
	// printed nothing for.
	got := sshDropHost([]string{"", "ssh-rsa AAAAx", sshTestCmd + sshTestKeyA}, "ssh-rsa AAAAnew")
	assert.Equal(t, []string{sshTestCmd + sshTestKeyA}, got)
}

func TestSSHAuthKeys_Failures(t *testing.T) {
	t.Run("no uid", func(t *testing.T) {
		e, _, _ := sshEnv(t)
		svcWithDir(t, &svcFakeDir{})
		require.NoError(t, e.SetLC(map[string]string{"zimbra_gid": "-1"}))

		require.ErrorContains(t, UpdateAuthKeys(e), "zimbra_gid")
	})

	t.Run("LDAP unreadable leaves the file alone", func(t *testing.T) {
		e, fakes, _ := sshEnv(t)
		svcWithDir(t, &svcFakeDir{})

		old := svcOpenDir
		svcOpenDir = func(*Env) (svcDir, error) { return nil, errors.New("ldap down") }

		t.Cleanup(func() { svcOpenDir = old })

		fakes.FS.Put(sshTestAuthPath, []byte("keep\n"), 0o644)

		require.ErrorContains(t, UpdateAuthKeys(e), "ldap down")

		data, _ := fakes.FS.Data(sshTestAuthPath)
		assert.Equal(t, "keep\n", string(data))
	})

	t.Run("unreadable file", func(t *testing.T) {
		e, fakes, _ := sshEnv(t)
		svcWithDir(t, &svcFakeDir{})
		sshFail(fakes, "ReadFile", "EIO")

		require.ErrorContains(t, UpdateAuthKeys(e), "EIO")
	})

	t.Run("write fails", func(t *testing.T) {
		e, fakes, _ := sshEnv(t)
		svcWithDir(t, &svcFakeDir{})
		sshFail(fakes, "WriteFileAtomic", "read-only filesystem")

		require.ErrorContains(t, UpdateAuthKeys(e), "read-only filesystem")
	})
}

func TestSSHAuthKeys_RunsOnEveryNode(t *testing.T) {
	s := StepZmupdateauthkeys()

	assert.Nil(t, s.When)
	assert.Nil(t, s.Done)
	assert.True(t, s.Optional)
}

// sshFakeLDAP is the LDAP client under svcLDAP.
type sshFakeLDAP struct {
	svcLDAPClient

	servers map[string]map[string]string
	err     error
	dn      string
	attr    string
	value   string
}

func (c *sshFakeLDAP) GetAllServersWithAttributes() (map[string]map[string]string, error) {
	return c.servers, c.err
}

func (c *sshFakeLDAP) ModifyAttribute(dn, attr, value string) error {
	c.dn, c.attr, c.value = dn, attr, value

	return c.err
}

func (c *sshFakeLDAP) BaseDN() string { return "cn=zimbra" }

func TestSvcLDAP_ServerAttribute(t *testing.T) {
	c := &sshFakeLDAP{servers: map[string]map[string]string{
		"a.example.com": {"zimbraSshPublicKey": sshTestKeyA, "zimbraServiceEnabled": "mta"},
		"b.example.com": {"zimbrasshpublickey": sshTestKeyB},
		"c.example.com": {"zimbraServiceEnabled": "mta"},
	}}

	got, err := svcLDAP{c: c}.ServerAttribute("zimbraSshPublicKey")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"a.example.com": sshTestKeyA, "b.example.com": sshTestKeyB, "c.example.com": "",
	}, got, "every server is listed, the attribute name matched in any case")

	c.err = errors.New("ldap down")
	_, err = svcLDAP{c: c}.ServerAttribute("zimbraSshPublicKey")
	require.ErrorContains(t, err, "ldap down")
}

func TestSvcLDAP_SetServerAttributeModifiesTheServerEntry(t *testing.T) {
	c := &sshFakeLDAP{}

	require.NoError(t, svcLDAP{c: c}.SetServerAttribute("store.example.com", "zimbraSshPublicKey", sshTestKeyA))
	assert.Equal(t, "cn=store.example.com,cn=servers,cn=zimbra", c.dn)
	assert.Equal(t, "zimbraSshPublicKey", c.attr)
	assert.Equal(t, sshTestKeyA, c.value)
}

func TestSSHKeygen_DirectoryAndOwnershipFailuresStopBeforeTheKeyIsWritten(t *testing.T) {
	for _, op := range []string{"MkdirAll", "Chmod", "Chown"} {
		t.Run(op, func(t *testing.T) {
			e, fakes, _ := sshEnv(t)
			dir := &svcFakeDir{}
			svcWithDir(t, dir)
			sshFail(fakes, op, "operation not permitted")

			require.ErrorContains(t, GenerateSSHKey(e), "operation not permitted")
			assert.False(t, fakes.FS.Exists(sshTestKeyPath))
			assert.Empty(t, dir.set, "the key is not published")
		})
	}
}

func TestSSHKeygen_LDAPUnavailable(t *testing.T) {
	e, _, _ := sshEnv(t)

	old := svcOpenDir
	svcOpenDir = func(*Env) (svcDir, error) { return nil, errors.New("ldap down") }

	t.Cleanup(func() { svcOpenDir = old })

	require.ErrorContains(t, GenerateSSHKey(e), "ldap down")
}
