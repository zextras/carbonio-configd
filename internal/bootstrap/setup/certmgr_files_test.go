// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/pavlo-v-chernykh/keystore-go/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/pkcs12"
)

const (
	cmViewSelf = "notBefore=Jan  1 00:00:00 2026 GMT\n" +
		"notAfter=Dec 31 00:00:00 2030 GMT\n" +
		"subject=OU=Zextras Carbonio, CN=mail.example.com\n" +
		"issuer=O=CA, OU=Zextras Carbonio, CN=mail.example.com\n" +
		"SubjectAltName=mail.example.com\n"
	cmMtaCrt = "/opt/zextras/conf/smtpd.crt"
)

func TestRunCertMgr_ViewStagedCRTAndCSR(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()

	// 1496-1540: dates, subject and issuer as openssl x509 prints them, then
	// the names without their "DNS:" labels.
	assert.Equal(t, "- "+cmSelfCrt+"\n"+cmViewSelf, h.ok("viewstagedcrt", "self"))
	assert.Equal(t, "- "+cmMtaCrt+"\n"+cmViewSelf, h.ok("viewstagedcrt", "self", cmMtaCrt), "a file of the command line")
	assert.Equal(t, "- "+cmSelfCrt+"\n"+cmViewSelf, h.ok("viewcrt", "self"), "viewcrt is viewstagedcrt")
	assert.Equal(t, "- "+cmSelfCSR+"\nsubject=OU=Zextras Carbonio, CN=mail.example.com\n"+
		"SubjectAltName=mail.example.com\n", h.ok("viewcsr", "self"))

	h.ok("createcrt", "-new", "-subjectAltNames", "b.example.com,10.1.2.3")
	assert.Contains(t, h.ok("viewstagedcrt", "self"), "SubjectAltName=mail.example.com, b.example.com, 10.1.2.3\n")
}

func TestRunCertMgr_ViewRefusesWhatIsNotThere(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()
	h.fakes.FS.Put("/tmp/junk.pem", []byte("junk\n"), 0o644)

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"viewstagedcrt", "comm"}, "ERROR: Can't read file '" + cmCommCrt + "'\n"},
		{[]string{"viewcsr", "comm"}, "ERROR: Can't read file '" + cmSSL + "/commercial/commercial.csr'\n"},
		{[]string{"viewstagedcrt", "self", "/tmp/junk.pem"}, "ERROR: openssl x509 on '/tmp/junk.pem' failed(1)\n- /tmp/junk.pem\n"},
		{[]string{"viewcsr", "self", "/tmp/junk.pem"}, "ERROR: openssl req on '/tmp/junk.pem' failed(1)\n- /tmp/junk.pem\n"},
		{[]string{"viewcsr", "self", cmSelfCrt}, "ERROR: openssl req on '" + cmSelfCrt + "' failed(1)\n- " + cmSelfCrt + "\n"},
	} {
		status, out, _ := h.run(tc.args...)

		assert.Equal(t, 1, status, tc.args)
		assert.Equal(t, tc.want, out, tc.args)
	}
}

func TestRunCertMgr_ViewDeployedCRT(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()

	out := h.ok("viewdeployedcrt")
	for _, svc := range []string{
		"ldap: /opt/zextras/conf/slapd.crt", "mailboxd: /opt/zextras/mailboxd/etc/mailboxd.pem",
		"mta: " + cmMtaCrt, "proxy: /opt/zextras/conf/nginx.crt",
	} {
		assert.Contains(t, out, "- "+svc+"\n"+cmViewSelf)
	}

	assert.Less(t, strings.Index(out, "- ldap:"), strings.Index(out, "- mailboxd:"), "in the order of the names")
	assert.Equal(t, "- mta: "+cmMtaCrt+"\n"+cmViewSelf, h.ok("viewdeployedcrt", "mta"))

	// A certificate that is not there is told, and is not an error.
	require.NoError(t, h.fakes.FS.Remove(cmMtaCrt))

	status, out, _ := h.run("viewdeployedcrt", "mta")
	assert.Equal(t, 0, status)
	assert.Equal(t, "NOTE: mta: file '"+cmMtaCrt+"' does not exist.\n", out)
}

func TestRunCertMgr_ViewDeployedMailboxd(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()

	// The certificate of mailboxd is the one of its keystore (1595-1620).
	assert.Equal(t, "- mailboxd: /opt/zextras/mailboxd/etc/mailboxd.pem\n"+cmViewSelf, h.ok("viewdeployedcrt", "mailboxd"))
	assert.Equal(t, h.read(cmSelfCrt), h.read("/opt/zextras/mailboxd/etc/mailboxd.pem"))

	der, err := h.fakes.FS.ReadFile("/opt/zextras/mailboxd/etc/mailboxd.der")
	require.NoError(t, err)
	assert.Equal(t, h.cert(cmSelfCrt).Raw, der)

	// A server that is not the mailbox one: the keystore is stale, said on stderr.
	require.NoError(t, h.e.SetLC(map[string]string{"mailboxd_server": ""}))
	require.NoError(t, h.fakes.FS.Remove("/opt/zextras/mailboxd/etc/mailboxd.pem"))

	status, out, errs := h.run("viewdeployedcrt", "mailboxd")
	assert.Equal(t, 0, status)
	assert.Empty(t, out)
	assert.Equal(t, "NOTE: possibly stale keystore: /opt/zextras/mailboxd/etc/keystore\n", errs)

	// An alias the keystore does not have.
	require.NoError(t, h.e.SetLC(map[string]string{"mailboxd_server": "other.example.com"}))

	status, out, _ = h.run("viewdeployedcrt", "mailboxd")
	assert.Equal(t, 1, status)
	assert.Contains(t, out, "ERROR: mailboxd: export to '/opt/zextras/mailboxd/etc/mailboxd.der' failed(1):\n"+
		"keytool error: java.lang.Exception: Alias <other.example.com> does not exist\n")

	// A password that is not the keystore's.
	require.NoError(t, h.e.SetLC(map[string]string{"mailboxd_server": cmHost, "mailboxd_keystore_password": "wrong"}))

	status, out, _ = h.run("viewdeployedcrt", "mailboxd")
	assert.Equal(t, 1, status)
	assert.Contains(t, out, "keytool error: java.io.IOException: Keystore was tampered with, or password was incorrect\n")
}

func TestRunCertMgr_CheckCRTExpiration(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()

	// 1722-1750: a certificate that is fine is not listed, unless the input
	// is a terminal.
	status, out, _ := h.run("checkcrtexpiration")
	assert.Equal(t, 0, status)
	assert.Empty(t, out)

	h.e.certTTY = func() bool { return true }
	assert.Equal(t,
		"ldap:     notAfter=Dec 31 00:00:00 2030 GMT '/opt/zextras/conf/slapd.crt' expires outside of 30 days (OK)\n"+
			"mailboxd: notAfter=Dec 31 00:00:00 2030 GMT '/opt/zextras/mailboxd/etc/mailboxd.pem' expires outside of 30 days (OK)\n"+
			"mta:      notAfter=Dec 31 00:00:00 2030 GMT '"+cmMtaCrt+"' expires outside of 30 days (OK)\n"+
			"proxy:    notAfter=Dec 31 00:00:00 2030 GMT '/opt/zextras/conf/nginx.crt' expires outside of 30 days (OK)\n",
		h.ok("checkcrtexpiration"))
	assert.Equal(t, "mta: notAfter=Dec 31 00:00:00 2030 GMT '"+cmMtaCrt+"' expires outside of 30 days (OK)\n",
		h.ok("checkcrtexpiration", "mta"), "the name is as wide as the services looked at")

	h.e.certTTY = func() bool { return false }

	status, out, _ = h.run("checkcrtexpiration", "mta", "-days", "2000")
	assert.Equal(t, 1, status)
	assert.Equal(t, "mta: notAfter=Dec 31 00:00:00 2030 GMT '"+cmMtaCrt+"' EXPIRES within 2000 days!\n", out)

	// Expired: the script printed 0 for the message (a precedence bug, 1731).
	h.fakes.Clock.Advance(6 * 366 * 24 * 60 * 60 * 1e9)

	status, out, _ = h.run("checkcrtexpiration", "proxy")
	assert.Equal(t, 1, status)
	assert.Equal(t, "proxy: notAfter=Dec 31 00:00:00 2030 GMT '/opt/zextras/conf/nginx.crt' EXPIRED!\n", out)

	// Not a certificate: nothing is known about its end, which is not the
	// same as it being over.
	h.fakes.FS.Put("/opt/zextras/conf/nginx.crt", []byte("junk\n"), 0o644)

	status, out, _ = h.run("checkcrtexpiration", "proxy")
	assert.Equal(t, 1, status)
	assert.Equal(t, "proxy: '/opt/zextras/conf/nginx.crt' CANNOT BE CHECKED: no certificate found\n", out)

	// A certificate block that does not parse says why, and is not EXPIRED!.
	h.fakes.FS.Put("/opt/zextras/conf/nginx.crt", certPEMBlock(certPEMCert, []byte("not der")), 0o644)

	status, out, _ = h.run("checkcrtexpiration", "proxy")
	assert.Equal(t, 1, status)
	assert.Contains(t, out, "proxy: '/opt/zextras/conf/nginx.crt' CANNOT BE CHECKED: x509: ")
	assert.NotContains(t, out, "EXPIRED!")
}

func TestRunCertMgr_AddCACert(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()

	// deployca made the store: zextras' (this run is root's), 0640.
	uid, gid, ok := h.fakes.FS.Owner(cmCacerts)
	assert.True(t, ok)
	assert.Equal(t, [2]int{998, 997}, [2]int{uid, gid}, "a new store is zextras'")

	out := h.ok("addcacert", cmCACrt)
	assert.Equal(t, "** Importing cert '"+cmCACrt+"' as 'zcs-user-ca' into cacerts '"+cmCacerts+"'\n"+
		"** NOTE: restart mailboxd to use the imported certificate.\n", out)

	// The CA of deployca and the one of addcacert, under their aliases, in a
	// PKCS12 store that keytool reads: no process runs, and the password is
	// not on any command line.
	trusted := h.trusted(cmPassword)
	ca := h.cert(cmCACrt).Raw

	for _, alias := range []string{"zcs-user-ca", "my_ca"} {
		e, ok := trusted.Get(alias)
		require.True(t, ok, alias)
		assert.Equal(t, ca, e.Cert, alias)
	}

	assert.Len(t, trusted.Entries, 2)
	assert.Equal(t, uint32(0o640), h.mode(cmCacerts))
	assert.Equal(t, []string{cmCacerts}, h.storeFiles(), "no temporary file is left")

	// Again: the alias exists, replaced.
	h.ok("addcacert", cmCACrt)
	assert.Len(t, h.trusted(cmPassword).Entries, 2)

	status, out, _ := h.run("addcacert", "/none.pem")
	assert.Equal(t, 1, status)
	assert.Equal(t, "ERROR: Can't read file '/none.pem'\n", out)
}

// storeFiles are the files in the directory of the cacerts.
func (h *cmTest) storeFiles() []string {
	var files []string

	for _, p := range h.fakes.FS.Paths() {
		if strings.HasPrefix(p, path.Dir(cmCacerts)+"/") {
			files = append(files, p)
		}
	}

	return files
}

func TestRunCertMgr_AddCACertKeepsTheStore(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()

	// A store as the JDK of Carbonio has it: its entries stay, as they are, and
	// so do the encryption and the MAC the file has (RC2 and SHA-1).
	legacy, err := os.ReadFile("../../pkcs12/testdata/legacy.p12")
	require.NoError(t, err)

	h.fakes.FS.Put(cmCacerts, legacy, 0o664)
	require.NoError(t, h.e.SetLC(map[string]string{"mailboxd_truststore_password": "changeit"}))

	before, err := pkcs12.Decode(legacy, "changeit")
	require.NoError(t, err)

	h.ok("addcacert", cmCACrt)

	data := []byte(h.read(cmCacerts))
	assert.NotEqual(t, legacy, data)
	assert.Equal(t, uint32(0o664), h.mode(cmCacerts), "the mode of the file")
	assert.Equal(t, []string{cmCacerts}, h.storeFiles())

	after, err := pkcs12.Decode(data, "changeit")
	require.NoError(t, err)
	require.Len(t, after.Entries, len(before.Entries)+1)
	assert.Equal(t, before.Entries, after.Entries[:len(before.Entries)], "the entries, spelled as they are")

	last := after.Entries[len(after.Entries)-1]
	assert.Equal(t, "zcs-user-ca", last.Alias)
	assert.Equal(t, h.cert(cmCACrt).Raw, last.Cert)

	// An alias is found whatever its case, and takes the spelling of the new one
	// (the legacy store has zcs-user-two, as the file name gives).
	two, err := os.ReadFile("../../pkcs12/testdata/two.pem")
	require.NoError(t, err)
	h.fakes.FS.Put("/tmp/TWO.pem", two, 0o644)
	h.ok("addcacert", "/tmp/TWO.pem")

	replaced := h.trusted("changeit")
	assert.Len(t, replaced.Entries, len(before.Entries)+1, "replaced, not added")

	e, ok := replaced.Get("zcs-user-two")
	require.True(t, ok)
	assert.Equal(t, "zcs-user-TWO", e.Alias)

	// RC2-40 with SHA-1 are still there: the OIDs are in the file.
	assert.Contains(t, string(data), "\x2a\x86\x48\x86\xf7\x0d\x01\x0c\x01\x06", "pbeWithSHA1And40BitRC2-CBC")
	assert.NotContains(t, string(data), "\x2a\x86\x48\x86\xf7\x0d\x01\x05\x0d", "not PBES2")
}

func TestRunCertMgr_AddCACertToAJKSStore(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()

	ks := keystore.New()
	require.NoError(t, ks.SetTrustedCertificateEntry("old", keystore.TrustedCertificateEntry{
		CreationTime: h.e.Sys.Clock.Now(),
		Certificate:  keystore.Certificate{Type: certX509, Content: h.cert(cmCACrt).Raw},
	}))

	var buf bytes.Buffer

	require.NoError(t, ks.Store(&buf, []byte(cmPassword)))
	h.fakes.FS.Put(cmCacerts, buf.Bytes(), 0o640)

	h.fakes.FS.Put("/tmp/New_One.pem", []byte(h.read(cmCACrt)), 0o644)
	h.ok("addcacert", "/tmp/New_One.pem")

	// keytool keeps the type of the file it opens.
	data := []byte(h.read(cmCacerts))
	require.True(t, bytes.HasPrefix(data, jksMagic))

	got := keystore.New()
	require.NoError(t, got.Load(bytes.NewReader(data), []byte(cmPassword)))
	assert.Equal(t, []string{"old", "zcs-user-new_one"}, slices.Sorted(slices.Values(got.Aliases())))
}

func TestRunCertMgr_AddCACertFailures(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()
	h.fakes.FS.Put("/tmp/junk.pem", []byte("junk\n"), 0o644)

	trusted := h.read(cmCacerts)

	// A file that is not a certificate: the store is as it was.
	status, out, _ := h.run("addcacert", "/tmp/junk.pem")
	assert.Equal(t, 1, status)
	assert.Equal(t, "** Importing cert '/tmp/junk.pem' as 'zcs-user-junk' into cacerts '"+cmCacerts+"'\n"+
		"ERROR: importing '/tmp/junk.pem' as 'zcs-user-junk' into cacerts '"+cmCacerts+"' failed: "+
		certKeytoolNoCert+"\n", out)
	assert.Equal(t, trusted, h.read(cmCacerts))

	// A certificate in DER, which keytool reads as well.
	h.fakes.FS.Put("/tmp/ca.der", h.cert(cmCACrt).Raw, 0o644)
	h.ok("addcacert", "/tmp/ca.der")

	e, ok := h.trusted(cmPassword).Get("zcs-user-ca")
	require.True(t, ok)
	assert.Equal(t, h.cert(cmCACrt).Raw, e.Cert)

	// The wrong password: the store is as it was.
	trusted = h.read(cmCacerts)
	require.NoError(t, h.e.SetLC(map[string]string{"mailboxd_truststore_password": "wrong"}))

	status, out, _ = h.run("addcacert", cmCACrt)
	assert.Equal(t, 1, status)
	assert.Contains(t, out, "ERROR: importing '"+cmCACrt+"' as 'zcs-user-ca' into cacerts '"+cmCacerts+"' failed: "+
		certKeytoolBadPass+"\n")
	assert.NotContains(t, out, "restart mailboxd")
	assert.Equal(t, trusted, h.read(cmCacerts))

	// A file that is not a keystore.
	h.fakes.FS.Put(cmCacerts, []byte("garbage\n"), 0o640)

	status, out, _ = h.run("addcacert", cmCACrt)
	assert.Equal(t, 1, status)
	assert.Contains(t, out, certKeytoolFormat+"\n")
	assert.Equal(t, "garbage\n", h.read(cmCacerts))

	// A store that cannot be put in place: the error is shown, and nothing is
	// left beside it.
	h.fakes.FS.Put(cmCacerts, []byte(trusted), 0o640)
	require.NoError(t, h.e.SetLC(map[string]string{"mailboxd_truststore_password": cmPassword}))

	h.fakes.FS.Fail = func(op, name string) error {
		if op == "Rename" && strings.HasPrefix(name, path.Dir(cmCacerts)) {
			return &fs.PathError{Op: "rename", Path: name, Err: syscall.EROFS}
		}

		return nil
	}

	status, out, _ = h.run("addcacert", cmCACrt)
	assert.Equal(t, 1, status)
	assert.Contains(t, out, "failed: Read-only file system\n")
	assert.Equal(t, trusted, h.read(cmCacerts))
	assert.Equal(t, []string{cmCacerts}, h.storeFiles())
}

func TestRunCertMgr_MailboxdKeystore(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()

	data, err := h.fakes.FS.ReadFile(cmKeystore)
	require.NoError(t, err)

	ks := keystore.New()
	require.NoError(t, ks.Load(bytes.NewReader(data), []byte(cmPassword)))
	require.Equal(t, []string{cmHost}, ks.Aliases(), "the alias is the mailbox server, lower case")

	entry, err := ks.GetPrivateKeyEntry(cmHost, []byte(cmPassword))
	require.NoError(t, err)
	require.Len(t, entry.CertificateChain, 1)
	assert.Equal(t, h.cert(cmSelfCrt).Raw, entry.CertificateChain[0].Content)

	key, err := x509.ParsePKCS8PrivateKey(entry.PrivateKey)
	require.NoError(t, err)

	want, err := certParseKey([]byte(h.read(cmSelfKey)))
	require.NoError(t, err)
	assert.True(t, want.(*rsa.PrivateKey).Equal(key), "the key of the keystore is the key file's")

	// A commercial certificate replaces the entry, with its chain.
	crtf, caf := h.commercial()
	h.ok("deploycrt", "comm", crtf, caf, "-deploy", "mailboxd")

	data, err = h.fakes.FS.ReadFile(cmKeystore)
	require.NoError(t, err)

	ks = keystore.New()
	require.NoError(t, ks.Load(bytes.NewReader(data), []byte(cmPassword)))
	require.Equal(t, []string{cmHost}, ks.Aliases())

	chain, err := ks.GetPrivateKeyEntryCertificateChain(cmHost)
	require.NoError(t, err)
	assert.Len(t, chain, 2, "the certificate and its CA")

	// A server without mailboxd keeps its keystore as it is.
	require.NoError(t, h.e.SetLC(map[string]string{"mailboxd_server": ""}))
	before := h.read(cmKeystore)

	h.ok("deploycrt", "self", "-deploy", "mailboxd")
	assert.Equal(t, before, h.read(cmKeystore))
}

func TestRunCertMgr_KeystoreFailures(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()

	// A keystore that is not one.
	h.fakes.FS.Put(cmKeystore, []byte("not a keystore"), 0o640)

	status, out, _ := h.run("deploycrt", "self", "-deploy", "mailboxd")
	assert.Equal(t, 1, status)
	assert.Contains(t, out, "ERROR: com.zimbra.cert.MyPKCS12Import to '"+cmKeystore+"' returned non-zero(1):\n"+
		"keytool error: java.io.IOException: Keystore was tampered with, or password was incorrect\n")

	// A key that is not the certificate's.
	h.fakes.FS.Put(cmSelfKey, []byte(h.read(cmCAKey)), 0o600)

	status, out, _ = h.run("deploycrt", "self", "-deploy", "mailboxd")
	assert.Equal(t, 1, status)
	assert.Contains(t, out, "ERROR: openssl pkcs12 export of '"+cmSelfCrt+"' failed(1):\n")
}

func TestRunCertMgr_CommFilesAreCleaned(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()
	crtf, caf := h.commercial()

	// A certificate with DOS line ends and the chain behind it (1050-1075).
	crt := strings.ReplaceAll(h.read(crtf), "\n", "\r\n")
	h.fakes.FS.Put(crtf, []byte(crt+h.read(caf)), 0o644)

	out := h.ok("deploycrt", "comm", crtf, caf, "-deploy", "ldap")

	assert.Contains(t, out, "** Fixing newlines in '"+crtf+"'\n")
	assert.Contains(t, out, "** Keeping first certificate in '"+crtf+"'\n")
	assert.Equal(t, crt+h.read(caf), h.read(crtf+".bak"), "the original of the newline fix")
	assert.NotEmpty(t, h.read(crtf+".orig"))
	assert.Equal(t, 1, strings.Count(h.read(crtf), "BEGIN CERTIFICATE"))

	// A file with no newline at the end is fixed too.
	h.fakes.FS.Put(caf, bytes.TrimRight([]byte(h.read(caf)), "\n"), 0o644)
	assert.Contains(t, h.ok("deploycrt", "comm", crtf, caf, "-deploy", "ldap"), "** Fixing newlines in '"+caf+"'\n")
}

func TestRunCertMgr_DeployCAInstallsTheCommercialCASplit(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()
	crtf, caf := h.commercial()
	h.ok("deploycrt", "comm", crtf, caf, "-deploy", "ldap")

	// The chain of two CAs: each gets its file and hash link in conf/ca.
	other := certPEMBlock("CERTIFICATE", h.cert(cmCACrt).Raw)
	h.fakes.FS.Put(cmCommCA, append([]byte(h.read(cmCommCA)), other...), 0o644)

	out := h.ok("deployca", "-localonly")

	assert.Contains(t, out, "** Cleaning up ")
	assert.Contains(t, out, "** Creating /opt/zextras/conf/ca/commercial_ca_1.crt\n")
	assert.Contains(t, out, "** Creating /opt/zextras/conf/ca/commercial_ca_2.crt\n")
	assert.Contains(t, out, "** Creating CA hash symlink '")

	assert.Equal(t, certPEMBlock("CERTIFICATE", h.cert(cmCommCA).Raw), []byte(h.read("/opt/zextras/conf/ca/commercial_ca_1.crt")))
}

func TestRunCertMgr_Migrate(t *testing.T) {
	h := newCMTest(t, nil)

	status, out, _ := h.run("migrate")
	assert.Equal(t, 1, status)
	assert.Equal(t, "** No '/opt/zextras/ssl/ssl' directory. Nothing to migrate.\n", out)

	for _, d := range []string{"", "/ca", "/server"} {
		h.fakes.FS.PutDir("/opt/zextras/ssl/ssl"+d, 0o755)
	}

	h.fakes.FS.Put("/opt/zextras/ssl/ssl/ca/ca.pem", []byte("CA\n"), 0o644)
	h.fakes.FS.Put("/opt/zextras/ssl/ssl/ca/ca.key", []byte("KEY\n"), 0o600)
	h.fakes.FS.Put("/opt/zextras/ssl/ssl/server/server.crt", []byte("CRT\n"), 0o644)
	h.fakes.FS.Put("/opt/zextras/ssl/ssl/server/mailboxd.crt", []byte("MBX\n"), 0o644)

	out = h.ok("migrate")

	// 1845-1880: in the order of the script's keys.
	assert.Contains(t, out, "** Migrating ssl certs from '/opt/zextras/ssl/ssl' to '"+cmSSL+"'\n")
	assert.Less(t, strings.Index(out, "'/opt/zextras/ssl/ssl/ca/ca.pem'"), strings.Index(out, "'/opt/zextras/ssl/ssl/ca/ca.key'"))
	assert.Equal(t, "CA\n", h.read(cmCACrt))
	assert.Equal(t, "KEY\n", h.read(cmCAKey))
	assert.Equal(t, "CRT\n", h.read(cmSelfCrt))
	assert.Equal(t, "MBX\n", h.read(cmCommCrt))
	assert.False(t, h.fakes.FS.Exists(cmSelfKey), "what the old directory lacks is not made")
}

func TestRunCertMgr_BackupOncePerRun(t *testing.T) {
	h := newCMTest(t, nil)
	h.ok("createca", "-new")

	out := h.ok("createcrt")

	// The suffix is local time, as the script's localtime: derive it from the
	// fake clock so the test does not depend on the machine's time zone.
	backup := cmSSL + "." + h.e.Sys.Clock.Now().Local().Format("20060102150405")

	assert.Equal(t, 1, strings.Count(out, "** Backup "+cmSSL+" to "+backup+"\n"), out)
	assert.True(t, h.fakes.FS.Exists(backup+"/ca/ca.key"))
	assert.Equal(t, h.read(cmCAKey), h.read(backup+"/ca/ca.key"))
	assert.Equal(t, uint32(0o600), h.mode(backup+"/ca/ca.key"), "the backup keeps the modes")
}

func TestRunCertMgr_ConfigFileFromTheTemplate(t *testing.T) {
	h := newCMTest(t, nil)

	out := h.ok("createca", "-new", "-digest", "sha384", "-subjectAltNames", "a.example.com")

	assert.Contains(t, out, "** Creating "+cmSSL+"/ca/zmssl.cnf\n")
	assert.Equal(t, "CN = mail.example.com\nmd = sha384\nsubjectAltName = DNS:mail.example.com,DNS:a.example.com\n",
		h.read(cmSSL+"/ca/zmssl.cnf"))

	out = h.ok("createca", "-new", "-digest", "sha384", "-subjectAltNames", "a.example.com")
	assert.Contains(t, out, "** Recreating "+cmSSL+"/ca/zmssl.cnf\n")

	// Without the template nothing is written and nothing is said.
	require.NoError(t, h.fakes.FS.Remove("/opt/zextras/conf/zmssl.cnf.in"))
	require.NoError(t, h.fakes.FS.Remove(cmSSL+"/ca/zmssl.cnf"))

	out = h.ok("createca", "-new")
	assert.NotContains(t, out, "zmssl.cnf")
}

func TestRunCertMgr_DebugGoesToStderr(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()

	status, out, errs := h.run("savecrt", "self", "-debug")
	assert.Equal(t, 0, status)
	assert.Contains(t, errs, "DEBUG: savecrt(self)\n")
	assert.NotContains(t, out, "DEBUG")

	_, _, errs = h.run("savecrt", "self")
	assert.Empty(t, errs)
}

func TestRunCertMgr_DaysAreShown(t *testing.T) {
	h := newCMTest(t, nil)
	h.ok("createca", "-new")

	// The days of a certificate are told (validation days: line of 2290).
	out := h.ok("createcrt", "-days", "90")
	assert.True(t, strings.HasPrefix(out, "validation days: 90\n"), out)

	crt := h.cert(cmSelfCrt)
	assert.Equal(t, 90, int(crt.NotAfter.Sub(crt.NotBefore).Hours()/24))
}

func TestRunCertMgr_RemoteServerFailuresDoNotStopTheOthers(t *testing.T) {
	h := newCMTest(t, nil)
	h.dir.all = []string{cmHost, "a.example.com", "b.example.com"}
	h.remoteFail = "getcrt"

	h.ok("createca", "-new")

	status, out, _ := h.run("deploycrt", "self", "-allservers")

	assert.Equal(t, 1, status)
	assert.Contains(t, out, "ERROR: zmrc: zmcertmgr getcrt self -allservers failed(1):\nzmrcd: refused\nconnecting to a.example.com: no route to host\n")
	assert.Len(t, h.remote, 2, "each server was tried once and its deploy skipped")
}

func TestRunCertMgr_LocalFailuresAreReported(t *testing.T) {
	t.Run("the localconfig is unreadable", func(t *testing.T) {
		h := newCMTest(t, nil)
		h.e.Paths.LocalConfig = t.TempDir() // a directory

		status, _, errs := h.run("createca")

		assert.Equal(t, 255, status)
		assert.Contains(t, errs, "zmcertmgr: new:")
	})

	t.Run("a file that is not writable", func(t *testing.T) {
		h := newCMTest(t, nil)
		h.fakes.FS.Fail = func(op, name string) error {
			if op == "OpenFile" && name == cmCAKey {
				return assert.AnError
			}

			return nil
		}

		status, out, _ := h.run("createca", "-new")

		assert.Equal(t, 1, status)
		assert.Contains(t, out, "ERROR: openssl req failed(1):\n")
	})
}

// The script prints no command lines; with -debug they are shown (on stderr).
// No process runs for the keystores now: nothing is shown, and the password
// never is.
func TestRunCertMgr_CommandLinesShowOnlyWithDebug(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()
	h.e.Log = NewLog(nil, nil)

	_, out, errs := h.run("addcacert", cmCACrt)
	assert.NotContains(t, out+errs, "Running as")
	assert.NotContains(t, out+errs, "keytool")

	_, _, errs = h.run("addcacert", cmCACrt, "-debug")
	assert.Contains(t, errs, "DEBUG: addcacert("+cmCACrt+" )")
	assert.NotContains(t, errs, "keytool")
	assert.NotContains(t, errs, cmPassword, "the password is not shown")
}

// The usage is what pod2usage prints: the synopsis indented by 4, the lines
// of the verbatim blocks by 6 (2470-2483).
func TestRunCertMgr_UsageIsIndentedAsPod2usageDoes(t *testing.T) {
	h := newCMTest(t, nil)

	status, out, _ := h.run("-help")
	assert.Equal(t, 0, status)
	assert.True(t, strings.HasPrefix(out, "Usage:\n    zmcertmgr <command> [options] [-help|-man] [-debug [#]]\n\n"+
		"      -help     displays a usage synopsis with information about options\n"+
		"      -man      displays the complete documentation\n\n"+
		"    Where valid command/option combinations are:\n\n"+
		"      createca  [-new [-newkey]] [-keysize keysize]"), out)
	assert.Contains(t, out, "\nOptions:\n  Shared Options:\n    -allservers\n        The effect of this option")

	status, out, _ = h.run("-bogus")
	assert.Equal(t, 1, status, "an option Getopt::Long refuses: the synopsis only")
	assert.True(t, strings.HasPrefix(out, "Usage:\n    zmcertmgr <command>"), out)
	assert.NotContains(t, out, "Options:")
}
