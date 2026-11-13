// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/pkcs12"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const (
	cmHost     = "mail.example.com"
	cmSSL      = "/opt/zextras/ssl/carbonio"
	cmCACrt    = cmSSL + "/ca/ca.pem"
	cmCAKey    = cmSSL + "/ca/ca.key"
	cmSrl      = cmSSL + "/ca/ca.srl"
	cmIndex    = cmSSL + "/ca/index.txt"
	cmSelfCrt  = cmSSL + "/server/server.crt"
	cmSelfKey  = cmSSL + "/server/server.key"
	cmSelfCSR  = cmSSL + "/server/server.csr"
	cmCommCrt  = cmSSL + "/commercial/commercial.crt"
	cmCommKey  = cmSSL + "/commercial/commercial.key"
	cmCommCA   = cmSSL + "/commercial/commercial_ca.crt"
	cmKeystore = "/opt/zextras/mailboxd/etc/keystore"
	cmCacerts  = "/opt/zextras/common/lib/jvm/java/lib/security/cacerts"
	cmConfCA   = "/opt/zextras/conf/ca/ca.pem"
	cmPassword = "secretpw"
)

// cmKeys are RSA keys made once: generating one takes longer than the tests.
var cmKeys = sync.OnceValue(func() []crypto.Signer {
	keys := make([]crypto.Signer, 4)

	for i := range keys {
		k, err := certNewRSAKey(2048)
		if err != nil {
			panic(err)
		}

		keys[i] = k
	}

	return keys
})

// cmDir is the directory of a test: global and server attributes, the
// servers, and what the command modified.
type cmDir struct {
	global  map[string]string
	servers map[string]map[string]string
	all     []string
	mods    []string
	readErr error
	modErr  error
}

func (d *cmDir) GetGlobalConfig() (map[string]string, error) { return d.global, d.readErr }

func (d *cmDir) GetServerConfig(host string) (map[string]string, error) {
	return d.servers[host], d.readErr
}

func (d *cmDir) GetAllServers() ([]string, error) { return d.all, d.readErr }
func (d *cmDir) BaseDN() string                   { return "cn=zimbra" }
func (d *cmDir) Close() error                     { return nil }

func (d *cmDir) ModifyValues(dn string, _, del, replace map[string][]string) error {
	if d.modErr != nil {
		return d.modErr
	}

	attrs := map[string]string{}
	if dn == "cn=config,cn=zimbra" {
		attrs = d.global
	} else {
		host := strings.TrimSuffix(strings.TrimPrefix(dn, "cn="), ",cn=servers,cn=zimbra")
		if d.servers[host] == nil {
			d.servers[host] = attrs
		}

		attrs = d.servers[host]
	}

	for k, v := range replace {
		attrs[k] = v[0]
		d.mods = append(d.mods, dn+" "+k)
	}

	for k := range del {
		delete(attrs, k)
		d.mods = append(d.mods, dn+" -"+k)
	}

	return nil
}

// cmTest is a zmcertmgr run environment: the files of the node are the
// in-memory fake, the directory a fake, the keys come from cmKeys.
type cmTest struct {
	t      *testing.T
	e      *Env
	fakes  *systest.Fakes
	dir    *cmDir
	keyN   int
	bits   []int
	remote []string
	// remoteFail makes the commands sent through zmrc that contain it fail.
	remoteFail string
	// remoteStartErr makes every zmrc call fail with it.
	remoteStartErr error
}

func newCMTest(t *testing.T, lc map[string]string) *cmTest {
	t.Helper()

	e, fakes := certTestEnv(t)
	h := &cmTest{t: t, e: e, fakes: fakes, dir: &cmDir{
		global:  map[string]string{},
		servers: map[string]map[string]string{cmHost: {}},
		all:     []string{cmHost},
	}}

	base := map[string]string{
		"zimbra_server_hostname":       cmHost,
		"ldap_host":                    cmHost,
		"ldap_is_master":               "true",
		"mailboxd_server":              cmHost,
		"mailboxd_keystore":            cmKeystore,
		"mailboxd_keystore_password":   cmPassword,
		"mailboxd_truststore":          cmCacerts,
		"mailboxd_truststore_password": cmPassword,
	}
	for k, v := range lc {
		base[k] = v
	}

	require.NoError(t, e.SetLC(base))

	e.certDir = func(*Env) (certDirectory, error) { return h.dir, nil }
	e.certKeyGen = func(bits int) (crypto.Signer, error) {
		h.bits = append(h.bits, bits)
		h.keyN++

		return cmKeys()[(h.keyN-1)%len(cmKeys())], nil
	}
	e.certTTY = func() bool { return false }

	// The zmrc client: what each server was sent, or why it cannot be reached.
	e.rc = &rcSeam{run: func(host string, in io.Reader, _, errw io.Writer) error {
		line, _ := io.ReadAll(in)
		h.remote = append(h.remote, host+": "+strings.TrimSpace(string(line)))

		if h.remoteStartErr != nil {
			return h.remoteStartErr
		}

		if h.remoteFail != "" && strings.Contains(string(line), h.remoteFail) {
			_, _ = io.WriteString(errw, "zmrcd: refused\n")

			return errors.New("connecting to " + host + ": no route to host")
		}

		return nil
	}}
	fakes.Exec.OnCmdline("id -u zextras", systest.Reply("998\n"))
	fakes.Exec.OnCmdline("id -g zextras", systest.Reply("997\n"))
	fakes.FS.PutDir("/opt/zextras/conf", 0o755)
	fakes.FS.Put("/opt/zextras/conf/zmssl.cnf.in", []byte("CN = @@HOSTNAME@@\nmd = @@ssl_default_digest@@\n@@SUBJECT_ALT_NAMES@@\n"), 0o644)
	fakes.FS.PutDir("/opt/zextras/mailboxd/etc", 0o755)
	fakes.FS.PutDir("/opt/zextras/common/lib/jvm/java/lib/security", 0o755)

	return h
}

// trusted is the truststore (cacerts) the commands wrote.
func (h *cmTest) trusted(pass string) *pkcs12.Store {
	h.t.Helper()

	s, err := pkcs12.Decode([]byte(h.read(cmCacerts)), pass)
	require.NoError(h.t, err)

	return s
}

// run is zmcertmgr <args>.
func (h *cmTest) run(args ...string) (status int, stdout, stderr string) {
	h.t.Helper()

	var out, errb bytes.Buffer

	status = RunCertMgr(h.e, args, &out, &errb)

	return status, out.String(), errb.String()
}

// ok runs a command that must succeed and returns its output.
func (h *cmTest) ok(args ...string) string {
	h.t.Helper()

	status, out, errs := h.run(args...)
	require.Equal(h.t, 0, status, "zmcertmgr %v\nstdout: %s\nstderr: %s", args, out, errs)

	return out
}

func (h *cmTest) read(name string) string {
	h.t.Helper()

	data, err := h.fakes.FS.ReadFile(name)
	require.NoError(h.t, err, name)

	return string(data)
}

func (h *cmTest) cert(name string) *x509.Certificate {
	h.t.Helper()

	certs, err := certParseCerts([]byte(h.read(name)))
	require.NoError(h.t, err, name)
	require.NotEmpty(h.t, certs, name)

	return certs[0]
}

func (h *cmTest) mode(name string) uint32 {
	h.t.Helper()

	fi, err := h.fakes.FS.Stat(name)
	require.NoError(h.t, err, name)

	return uint32(fi.Mode().Perm())
}

func TestRunCertMgr_CreatesTheCAAndTheCertificate(t *testing.T) {
	h := newCMTest(t, nil)

	out := h.ok("createca", "-new")
	assert.Contains(t, out, "** Creating CA with new private key "+cmCAKey+"\n")
	assert.Contains(t, out, "** Creating directory '"+cmSSL+"/ca'\n")

	ca := h.cert(cmCACrt)
	assert.True(t, ca.IsCA)
	assert.Equal(t, "/O=CA/OU=Zextras Carbonio/CN="+cmHost, mustSlash(t, ca.RawSubject))
	assert.Equal(t, []string{cmHost}, ca.DNSNames)
	assert.Equal(t, uint32(0o600), h.mode(cmCAKey))
	assert.Equal(t, uint32(0o640), h.mode(cmCACrt))
	assert.Equal(t, []int{2048}, h.bits)

	out = h.ok("createcrt")
	assert.Contains(t, out, "** Signing cert request "+cmSelfCSR+"\n")
	assert.Contains(t, out, "** Creating server cert request "+cmSelfCSR+" with keysize=2048 digest=sha256\n")

	crt := h.cert(cmSelfCrt)
	assert.False(t, crt.IsCA)
	assert.Equal(t, []string{cmHost}, crt.DNSNames)
	assert.Equal(t, ca.Subject.String(), crt.Issuer.String())
	assert.NoError(t, crt.CheckSignatureFrom(ca))
	assert.Equal(t, 1825*24*time.Hour, crt.NotAfter.Sub(crt.NotBefore).Round(24*time.Hour))
	assert.Equal(t, uint32(0o640), h.mode(cmSelfCrt))
	assert.Equal(t, uint32(0o600), h.mode(cmSelfKey))

	out = h.ok("verifycrt", "self")
	assert.Equal(t, "** Verifying '"+cmSelfCrt+"' against '"+cmSelfKey+"'\n"+
		"Certificate '"+cmSelfCrt+"' and private key '"+cmSelfKey+"' match.\n"+
		"** Verifying '"+cmSelfCrt+"' against '"+cmCACrt+"'\n"+
		"Valid certificate chain: "+cmSelfCrt+": OK\n", out)
}

func mustSlash(t *testing.T, raw []byte) string {
	t.Helper()

	dn, err := certParseDN(raw)
	require.NoError(t, err)

	return dn.slash()
}
