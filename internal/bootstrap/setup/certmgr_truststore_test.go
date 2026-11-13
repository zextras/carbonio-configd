// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/pkcs12"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// realMgr is a run on the file system of the machine.
func realMgr(zextras bool) *certMgr {
	return &certMgr{e: &Env{Sys: sys.Real(), IsZextras: zextras}}
}

func TestReplaceFile_KeepsTheModeAndTheOwner(t *testing.T) {
	name := filepath.Join(t.TempDir(), "store")
	require.NoError(t, os.WriteFile(name, []byte("old"), 0o600))
	require.NoError(t, os.Chmod(name, 0o640))

	before, err := os.Stat(name)
	require.NoError(t, err)

	// root's run: the owner is kept, whatever the file's was.
	require.NoError(t, realMgr(false).replaceFile(name, []byte("new"), 0o600))

	after, err := os.Stat(name)
	require.NoError(t, err)

	data, err := os.ReadFile(name)
	require.NoError(t, err)
	assert.Equal(t, "new", string(data))
	assert.Equal(t, fs.FileMode(0o640), after.Mode().Perm(), "the mode of the file, not the one asked")
	assert.False(t, os.SameFile(before, after), "a new file was put in its place")

	uid, gid, ok := fileOwner(after)
	require.True(t, ok)

	wantUID, wantGID, _ := fileOwner(before)
	assert.Equal(t, [2]int{wantUID, wantGID}, [2]int{uid, gid})

	entries, err := os.ReadDir(filepath.Dir(name))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no temporary file is left")
}

func TestReplaceFile_GivesAnotherOwnerBackToIt(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("changing the owner takes root")
	}

	const other = 12345

	name := filepath.Join(t.TempDir(), "store")
	require.NoError(t, os.WriteFile(name, []byte("old"), 0o640))
	require.NoError(t, os.Chown(name, other, other+1))

	require.NoError(t, realMgr(false).replaceFile(name, []byte("new"), 0o600))

	fi, err := os.Stat(name)
	require.NoError(t, err)

	uid, gid, ok := fileOwner(fi)
	require.True(t, ok)
	assert.Equal(t, [2]int{other, other + 1}, [2]int{uid, gid})
}

func TestReplaceFile_NewFile(t *testing.T) {
	name := filepath.Join(t.TempDir(), "store")

	require.NoError(t, realMgr(true).replaceFile(name, []byte("new"), 0o640))

	fi, err := os.Stat(name)
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o640), fi.Mode().Perm(), "whatever the umask is")

	// A directory that is not there.
	assert.Error(t, realMgr(true).replaceFile(filepath.Join(t.TempDir(), "none", "store"), nil, 0o640))
}

func TestReplaceFile_Links(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")

	require.NoError(t, os.WriteFile(target, []byte("old"), 0o640))
	require.NoError(t, os.Symlink(target, link))

	// root does not follow a link a user could have planted: the directory
	// can be written by others.
	require.NoError(t, os.Chmod(dir, 0o777)) //nolint:gosec // that is the case
	err := realMgr(false).replaceFile(link, []byte("new"), 0o640)
	assert.ErrorIs(t, err, errCertSymlink)

	data, _ := os.ReadFile(target)
	assert.Equal(t, "old", string(data))

	// zextras writes through it: the link stays, the target is replaced.
	require.NoError(t, realMgr(true).replaceFile(link, []byte("new"), 0o640))

	data, _ = os.ReadFile(target)
	assert.Equal(t, "new", string(data))

	fi, err := os.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, fi.Mode()&fs.ModeSymlink, "still a link")
}

// ownerFS tells the owner of the paths it is given (the fake file system does
// not know owners).
type ownerFS struct {
	sys.FS
	owners map[string]*syscall.Stat_t
}

type ownedInfo struct {
	fs.FileInfo
	st *syscall.Stat_t
}

func (o ownedInfo) Sys() any { return o.st }

func (o ownerFS) owned(fi fs.FileInfo, name string, err error) (fs.FileInfo, error) {
	if st, ok := o.owners[name]; ok && err == nil {
		return ownedInfo{fi, st}, nil
	}

	return fi, err
}

func (o ownerFS) Stat(name string) (fs.FileInfo, error) {
	fi, err := o.FS.Stat(name)
	return o.owned(fi, name, err)
}
func (o ownerFS) Lstat(name string) (fs.FileInfo, error) {
	fi, err := o.FS.Lstat(name)
	return o.owned(fi, name, err)
}

const (
	cmRealCacerts = "/opt/zextras/common/etc/java/cacerts"
	cmSecurityDir = "/opt/zextras/common/lib/jvm/java/lib/security"
)

// The cacerts of a node is a link, in a directory of root, to a file of
// zextras in another one of root's: root follows it, replaces the file it
// leads to and leaves the link alone.
func TestRunCertMgr_CacertsIsALinkInADirOfRoot(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()

	// The store the image has: zextras' file, behind the link.
	data := h.read(cmCacerts)
	require.NoError(t, h.fakes.FS.Remove(cmCacerts))
	h.fakes.FS.PutDir(path.Dir(cmRealCacerts), 0o755)
	h.fakes.FS.Put(cmRealCacerts, []byte(data), 0o644)
	require.NoError(t, h.fakes.FS.Chown(cmRealCacerts, 998, 997))
	h.fakes.FS.PutLink(cmCacerts, cmRealCacerts)

	root := map[string]*syscall.Stat_t{
		cmSecurityDir: {Uid: 0},
		cmRealCacerts: {Uid: 998, Gid: 997},
	}
	h.e.Sys.FS = ownerFS{FS: h.fakes.FS, owners: root}

	out := h.ok("addcacert", cmCACrt)
	assert.Contains(t, out, "restart mailboxd")

	target, err := h.fakes.FS.Readlink(cmCacerts)
	require.NoError(t, err)
	assert.Equal(t, cmRealCacerts, target, "still a link")

	ca := h.cert(cmCACrt).Raw
	e, ok := pkcs12Entry(t, h.read(cmRealCacerts), "zcs-user-ca")
	require.True(t, ok)
	assert.Equal(t, ca, e)
	assert.Equal(t, uint32(0o644), h.mode(cmRealCacerts), "the mode of the target")

	uid, gid, _ := h.fakes.FS.Owner(cmRealCacerts)
	assert.Equal(t, [2]int{998, 997}, [2]int{uid, gid}, "the owner of the target")
	assert.Equal(t, []string{cmRealCacerts}, h.dirFiles(path.Dir(cmRealCacerts)), "no temporary file is left")

	// The same link in a directory a user can write is not followed.
	root[cmSecurityDir] = &syscall.Stat_t{Uid: 998}
	h.fakes.FS.Fail = nil

	status, out, _ := h.run("addcacert", cmCACrt)
	assert.Equal(t, 1, status)
	assert.Contains(t, out, "failed: Is a symbolic link: refusing to follow it as root\n")

	root[cmSecurityDir] = &syscall.Stat_t{}
	require.NoError(t, h.fakes.FS.Chmod(cmSecurityDir, 0o775))

	status, out, _ = h.run("addcacert", cmCACrt)
	assert.Equal(t, 1, status, "group can write it")
	assert.Contains(t, out, "Is a symbolic link")
}

// pkcs12Entry is the certificate of an alias of a PKCS12 file.
func pkcs12Entry(t *testing.T, data, alias string) ([]byte, bool) {
	t.Helper()

	s, err := pkcs12.Decode([]byte(data), cmPassword)
	require.NoError(t, err)

	e, ok := s.Get(alias)

	return e.Cert, ok
}

// dirFiles are the files of a directory.
func (h *cmTest) dirFiles(dir string) []string {
	var files []string

	for _, p := range h.fakes.FS.Paths() {
		if path.Dir(p) == dir {
			files = append(files, p)
		}
	}

	return files
}

// Links are followed in a chain, and a loop is an error.
func TestResolve(t *testing.T) {
	h := newCMTest(t, nil)
	m := &certMgr{e: h.e, o: &certOpts{}}
	h.e.Sys.FS = ownerFS{FS: h.fakes.FS, owners: map[string]*syscall.Stat_t{"/etc/links": {}}}
	h.fakes.FS.PutDir("/etc/links", 0o755)
	h.fakes.FS.Put("/etc/real", nil, 0o644)
	h.fakes.FS.PutLink("/etc/links/a", "b")
	h.fakes.FS.PutLink("/etc/links/b", "/etc/real")
	h.fakes.FS.PutLink("/etc/links/loop", "loop")

	got, err := m.resolve("/etc/links/a")
	require.NoError(t, err)
	assert.Equal(t, "/etc/real", got, "a relative link is relative to its directory")

	got, err = m.resolve("/etc/real")
	require.NoError(t, err)
	assert.Equal(t, "/etc/real", got)

	got, err = m.resolve("/etc/none")
	require.NoError(t, err)
	assert.Equal(t, "/etc/none", got)

	_, err = m.resolve("/etc/links/loop")
	assert.ErrorIs(t, err, syscall.ELOOP)

	// What root reads and writes through such a link is the file it leads to.
	h.fakes.FS.Put("/etc/real", []byte("data\n"), 0o644)

	data, err := m.slurpOwn("/etc/links/a")
	require.NoError(t, err)
	assert.Equal(t, "data\n", string(data))

	require.NoError(t, m.writeFile("/etc/links/a", []byte("more\n"), 0o640))
	assert.Equal(t, "more\n", h.read("/etc/real"))

	h.fakes.FS.PutLink("/etc/links/dangling", "/etc/new")
	require.NoError(t, m.writeFile("/etc/links/dangling", []byte("new\n"), 0o640))
	assert.Equal(t, "new\n", h.read("/etc/new"))
	uid, gid, _ := h.fakes.FS.Owner("/etc/new")
	assert.Equal(t, [2]int{998, 997}, [2]int{uid, gid}, "a file root creates is zextras'")
}

func TestFileOwner(t *testing.T) {
	fi, err := os.Stat(t.TempDir())
	require.NoError(t, err)

	uid, gid, ok := fileOwner(fi)
	require.True(t, ok)
	assert.Equal(t, os.Getuid(), uid)

	st := fi.Sys().(*syscall.Stat_t)
	assert.Equal(t, int(st.Gid), gid)
}

func TestOpenTrustStore_Errors(t *testing.T) {
	h := newCMTest(t, nil)
	m := &certMgr{e: h.e}

	_, err := m.openTrustStore("/none/store", cmPassword, false)
	assert.EqualError(t, err, "keytool error: java.io.FileNotFoundException: /none/store (No such file or directory)")

	// A store that is not there is a new PKCS12 one, for what writes.
	ts, err := m.openTrustStore("/none/store", cmPassword, true)
	require.NoError(t, err)
	assert.IsType(t, p12Store{}, ts)

	h.fakes.FS.Put("/tmp/store", []byte("junk"), 0o640)
	h.fakes.FS.Fail = func(op, name string) error {
		if op == "ReadFile" && name == "/tmp/store" {
			return &fs.PathError{Op: "open", Path: name, Err: syscall.EACCES}
		}

		return nil
	}

	_, err = m.openTrustStore("/tmp/store", cmPassword, false)
	assert.EqualError(t, err, certKeytoolIO+"Permission denied")

	h.fakes.FS.Fail = nil

	_, err = m.openTrustStore("/tmp/store", cmPassword, false)
	assert.EqualError(t, err, certKeytoolFormat)

	// What starts like a PKCS12 file and is not one.
	h.fakes.FS.Put("/tmp/store", []byte{0x30, 0x03, 0x02, 0x01, 0x03}, 0o640)

	_, err = m.openTrustStore("/tmp/store", cmPassword, false)
	assert.EqualError(t, err, certKeytoolFormat)

	// What starts like a JKS file and is not one.
	h.fakes.FS.Put("/tmp/store", append(append([]byte{}, jksMagic...), 0, 0), 0o640)

	_, err = m.openTrustStore("/tmp/store", cmPassword, false)
	assert.EqualError(t, err, certKeytoolTampered)

	// A password that is not the store's.
	legacy, err := os.ReadFile("../../pkcs12/testdata/legacy.p12")
	require.NoError(t, err)
	h.fakes.FS.Put("/tmp/store", legacy, 0o640)

	_, err = m.openTrustStore("/tmp/store", cmPassword, false)
	assert.EqualError(t, err, certKeytoolBadPass)

	// A store with entries that are not certificates.
	keys, err := os.ReadFile("../../pkcs12/testdata/keyentry.p12")
	require.NoError(t, err)
	h.fakes.FS.Put("/tmp/store", keys, 0o640)

	ts, err = m.openTrustStore("/tmp/store", "changeit", false)
	require.NoError(t, err)

	der, ok := ts.get("mailboxd")
	assert.True(t, ok, "the certificate of a key entry can be exported")
	assert.NotEmpty(t, der)

	assert.Error(t, m.saveTrustStore(ts, "/tmp/store", "changeit"), "and the store cannot be written: the key would be lost")
	assert.Equal(t, string(keys), h.read("/tmp/store"))
}

func TestCertFromFile(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()

	ca := h.cert(cmCACrt)
	two := h.cert(cmSelfCrt)

	// The first certificate of a PEM file.
	got, err := certFromFile([]byte(h.read(cmCACrt) + h.read(cmSelfCrt)))
	require.NoError(t, err)
	assert.Equal(t, ca.Raw, got.Raw)

	// DER.
	got, err = certFromFile(two.Raw)
	require.NoError(t, err)
	assert.Equal(t, two.Raw, got.Raw)

	_, err = certFromFile([]byte("junk\n"))
	assert.EqualError(t, err, certKeytoolNoCert)
}

func TestExportFromAPKCS12Keystore(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()

	// viewdeployedcrt mailboxd with a keystore of the type keytool makes today.
	s := pkcs12.New()
	s.Set(cmHost, h.cert(cmSelfCrt).Raw)

	data, err := s.Encode(cmPassword)
	require.NoError(t, err)

	h.fakes.FS.Put(cmKeystore, data, 0o640)

	h.ok("viewdeployedcrt", "mailboxd")
	assert.Equal(t, h.read(cmSelfCrt), h.read("/opt/zextras/mailboxd/etc/mailboxd.pem"))

	// An alias it does not have, and a password that is not its.
	require.NoError(t, h.e.SetLC(map[string]string{"mailboxd_server": "other.example.com"}))

	status, out, _ := h.run("viewdeployedcrt", "mailboxd")
	assert.Equal(t, 1, status)
	assert.Contains(t, out, "failed(1):\n"+certKeytoolNoAlias("other.example.com")+"\n")

	require.NoError(t, h.e.SetLC(map[string]string{"mailboxd_server": cmHost, "mailboxd_keystore_password": "wrong"}))

	status, out, _ = h.run("viewdeployedcrt", "mailboxd")
	assert.Equal(t, 1, status)
	assert.Contains(t, out, "failed(1):\n"+certKeytoolBadPass+"\n")
}

func TestJKSStoreEntries(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()

	// The mailboxd keystore is JKS: the entry is a key, and its certificate is
	// the one to export.
	ts, err := (&certMgr{e: h.e}).openTrustStore(cmKeystore, cmPassword, false)
	require.NoError(t, err)
	require.IsType(t, jksStore{}, ts)

	der, ok := ts.get(cmHost)
	require.True(t, ok)
	assert.Equal(t, h.cert(cmSelfCrt).Raw, der)

	// A trusted one is set, found whatever its case, and deleted.
	ts.set("Anchor", h.cert(cmCACrt))

	der, ok = ts.get("ANCHOR")
	require.True(t, ok)
	assert.Equal(t, h.cert(cmCACrt).Raw, der)

	_, ok = ts.get("none")
	assert.False(t, ok)

	ts.remove("anchor")
	_, ok = ts.get("anchor")
	assert.False(t, ok)
}
