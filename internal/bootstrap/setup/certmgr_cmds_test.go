// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"crypto/x509"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	cmZextrasUID = 998
	cmZextrasGID = 997
	cmExtraHost  = "store2.example.com"
)

// bootstrap makes what a node has after setup: the CA, deployed, the
// self-signed certificate, deployed.
func (h *cmTest) bootstrap() {
	h.t.Helper()

	h.ok("createca", "-new")
	h.ok("deployca")
	h.ok("createcrt")
	h.ok("deploycrt", "self")
}

// commercial is a certificate request of the node signed by another CA, as a
// commercial CA does: it returns the files of the certificate and the CA
// chain.
func (h *cmTest) commercial() (crtf, caf string) {
	h.t.Helper()

	h.ok("createcsr", "comm")

	keys := cmKeys()
	subject, err := certParseSubject("/O=Commercial/CN=Root CA")
	require.NoError(h.t, err)

	now := h.e.Sys.Clock.Now()
	root, err := certSelfSign(keys[3], subject, nil, 3650, "sha256", now)
	require.NoError(h.t, err)

	csr, err := certParseCSR([]byte(h.read(cmSSL + "/commercial/commercial.csr")))
	require.NoError(h.t, err)

	leaf, err := certSignCSR(&certSignReq{
		CSR: csr, SANs: []string{cmHost}, CA: root.Cert, CAKey: keys[3],
		Serial: big.NewInt(77), Days: 365, Digest: "sha256", Now: now,
	})
	require.NoError(h.t, err)

	crtf, caf = "/tmp/commercial/leaf.crt", "/tmp/commercial/chain.crt"
	h.fakes.FS.Put(crtf, certPEMBlock("CERTIFICATE", leaf.DER), 0o644)
	h.fakes.FS.Put(caf, certPEMBlock("CERTIFICATE", root.DER), 0o644)

	return crtf, caf
}

func TestRunCertMgr_UsageAndExitCodes(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		status     int
		out, errs  string // substrings
		notInOut   string
		notInError string
	}{
		// certmgr.pl 70-120: -help prints the synopsis and the options.
		{"help", []string{"-help"}, 0, "zmcertmgr <command> [options] [-help|-man] [-debug [#]]", "", "", ""},
		{"man", []string{"-man"}, 0, "NAME\n    zmcertmgr", "", "", ""},
		// Getopt::Long refuses an unknown option: the synopsis goes to stdout, status 1.
		{"unknown option", []string{"createca", "-bogus"}, 1, "createca  [-new [-newkey]]", "Unknown option: bogus", "", ""},
		{"no command", nil, 2, "", "zmcertmgr: a command must be specified", "", ""},
		{"unknown command", []string{"nocmd"}, 2, "", "zmcertmgr: unexpected argument(s): nocmd", "", ""},
		{"extra argument", []string{"createcrt", "extra"}, 2, "", "unexpected argument(s): extra", "", ""},
		{"type missing", []string{"deploycrt"}, 2, "", "deploycrt: type 'self' or 'comm' must be specified", "", ""},
		{"viewcrt is viewstagedcrt", []string{"viewcrt", "self", "/none"}, 1, "ERROR: Can't read file '/none'\n", "", "", ""},
		// 2149, 2240, 2263: the settings are validated as the object is made.
		{"bad digest", []string{"createca", "-digest", "md5"}, 1, "ERROR: unknown digest method 'md5'\n", "", "", ""},
		{"bad days", []string{"createcrt", "-days", "0"}, 1, "ERROR: invalid days '0': must be > 0\n", "", "", ""},
		{"bad keysize", []string{"createca", "-keysize", "1024"}, 1, "ERROR: invalid keysize '1024': must be >= 2048\n", "", "", ""},
		{"bad deploy", []string{"deploycrt", "self", "-deploy", "x"}, 1, "ERROR: deploy: unknown service 'x'\n", "", "", ""},
		// 1588, 1682: a die goes to stderr with status 255.
		{"unknown service", []string{"viewdeployedcrt", "foo"}, 255, "",
			"zmcertmgr: ERROR viewdeployedcrt(foo) failed:\n viewdeployedcrt [all|ldap|mailboxd|mta|proxy]: unknown service 'foo'\n", "", ""},
		{"unknown service expiry", []string{"checkcrtexpiration", "foo"}, 255, "",
			"checkcrtexpiration [all|ldap|mailboxd|mta|proxy]: unknown service 'foo'", "", ""},
		// 1030: the command line refuses files for the self-signed certificate.
		{"self with files", []string{"deploycrt", "self", "/x.crt", "/ca.crt"}, 255, "",
			"zmcertmgr: ERROR deploycrt(self /x.crt /ca.crt) failed:\n certfile argument is not allowed for type 'self'\n", "", ""},
		// The command line takes both files or none.
		{"comm with one file", []string{"deploycrt", "comm", "/x.crt"}, 2, "", "unexpected argument(s): /x.crt", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newCMTest(t, nil)

			status, out, errs := h.run(tc.args...)

			assert.Equal(t, tc.status, status, "stdout: %s\nstderr: %s", out, errs)
			assert.Contains(t, out, tc.out)
			assert.Contains(t, errs, tc.errs)
		})
	}
}

func TestRunCertMgr_CAFilesAreOwnedByZextras(t *testing.T) {
	h := newCMTest(t, nil)
	h.ok("createca", "-new")
	h.ok("createcrt")

	// 2149-2160 and the tmpfiles of the package: the process may be root, the
	// files belong to zextras.
	for _, name := range []string{
		cmSSL, cmSSL + "/ca", cmSSL + "/ca/newcerts", cmCACrt, cmCAKey, cmSrl, cmIndex, cmSelfCrt, cmSelfKey,
		cmSelfCSR, "/opt/zextras/conf/ca", cmSSL + "/ca/newcerts/1767225600.pem",
	} {
		uid, gid, ok := h.fakes.FS.Owner(name)
		require.True(t, ok, name)
		assert.Equal(t, cmZextrasUID, uid, name)
		assert.Equal(t, cmZextrasGID, gid, name)
	}

	assert.Equal(t, uint32(0o600), h.mode(cmCAKey))
	assert.Equal(t, uint32(0o640), h.mode(cmSrl))
}

func TestRunCertMgr_ZextrasProcessLeavesOwnersAlone(t *testing.T) {
	h := newCMTest(t, nil)
	h.e.IsZextras = true

	h.ok("createca", "-new")

	uid, _, ok := h.fakes.FS.Owner(cmCAKey)
	require.True(t, ok)
	assert.NotEqual(t, cmZextrasUID, uid, "no chown: the files are the process's own")
}

func TestRunCertMgr_SigningKeepsTheCADatabase(t *testing.T) {
	h := newCMTest(t, nil)
	h.ok("createca", "-new")
	h.ok("createcrt")

	// openssl ca: the serial is read as hex (createCASerial writes the time),
	// the next one is serial+1, newcerts keeps the certificate by serial.
	assert.Equal(t, "1767225601\n", h.read(cmSrl))
	assert.Equal(t, "1767225600\n", h.read(cmSrl+".old"))
	assert.Equal(t, "unique_subject = yes\n", h.read(cmIndex+".attr"))

	crt := h.cert(cmSelfCrt)
	assert.Equal(t, "1767225600", strings.ToUpper(crt.SerialNumber.Text(16)))
	assert.Equal(t, h.read(cmSelfCrt), h.read(cmSSL+"/ca/newcerts/1767225600.pem"))
	assert.Equal(t, "V\t301231000000Z\t\t1767225600\tunknown\t/OU=Zextras Carbonio/CN="+cmHost+"\n", h.read(cmIndex))

	// A second certificate takes the next serial and replaces the database line.
	h.ok("createcrt", "-new")
	assert.Equal(t, "1767225601\n", h.read(cmSrl), "createCASerial starts from the clock again")
}

func TestRunCertMgr_CreateCA(t *testing.T) {
	t.Run("takes the CA of LDAP instead of making one", func(t *testing.T) {
		h := newCMTest(t, nil)
		h.ok("createca", "-new")
		ca, key := h.read(cmCACrt), h.read(cmCAKey)
		h.ok("deployca")

		// A node of the cluster: the directory has the CA, the node has nothing.
		other := newCMTest(t, map[string]string{"ldap_is_master": "false", "ldap_host": "ldap.example.com"})
		other.dir.global = h.dir.global
		other.dir.servers["ldap.example.com"] = map[string]string{}

		out := other.ok("createca")
		assert.Contains(t, out, "** Retrieving CA cert from LDAP... ok\n** Creating '"+cmCACrt+"'\n")
		assert.Contains(t, out, "** Retrieving CA private key from LDAP... ok\n** Creating '"+cmCAKey+"'\n")
		assert.Equal(t, ca, other.read(cmCACrt))
		assert.Equal(t, key, other.read(cmCAKey))
		assert.Equal(t, uint32(0o600), other.mode(cmCAKey))
		assert.Empty(t, other.bits, "no key was made")

		out = other.ok("createca")
		assert.Contains(t, out, "** Using CA cert in '"+cmCACrt+"'\n")
		assert.Contains(t, out, "** Using CA private key in '"+cmCAKey+"'\n")
	})

	t.Run("a new CA is for the master only", func(t *testing.T) {
		h := newCMTest(t, map[string]string{"ldap_is_master": "false", "ldap_host": "ldap.example.com"})

		status, out, _ := h.run("createca", "-new")

		assert.Equal(t, 1, status) // certmgr.pl 615
		assert.Equal(t, "ERROR: '-new' is only allowed on an ldap master\n", out)
		assert.False(t, h.fakes.FS.Exists(cmCACrt))
	})

	t.Run("-new keeps the key that exists", func(t *testing.T) {
		h := newCMTest(t, nil)
		h.ok("createca", "-new")
		key, first := h.read(cmCAKey), h.read(cmCACrt)

		out := h.ok("createca", "-new")
		assert.Contains(t, out, "** Creating CA with existing private key "+cmCAKey+"\n")
		assert.Equal(t, key, h.read(cmCAKey))
		assert.NotEqual(t, first, h.read(cmCACrt), "a new certificate: the clock is the serial and the dates")
		assert.Len(t, h.bits, 1)
	})

	t.Run("-newkey makes a key", func(t *testing.T) {
		h := newCMTest(t, nil)
		h.ok("createca", "-new")

		out := h.ok("createca", "-new", "-newkey", "-keysize", "4096")
		assert.Contains(t, out, "** Creating CA with new private key "+cmCAKey+"\n")
		assert.NotContains(t, out, "Retrieving", "-newkey does not look for the key in LDAP")
		assert.Equal(t, []int{2048, 4096}, h.bits)
	})

	t.Run("without -new the key must be there", func(t *testing.T) {
		h := newCMTest(t, nil)

		status, out, _ := h.run("createca")

		assert.Equal(t, 1, status)
		assert.Contains(t, out, "ERROR: createca: empty key '"+cmCAKey+"'\n") // 734
	})

	t.Run("subject, digest and names", func(t *testing.T) {
		h := newCMTest(t, nil)

		h.ok("createca", "-new", "-subject", "/C=IT/O=Acme/CN=Acme CA", "-digest", "sha512", "-subjectAltNames",
			"alt.example.com,MAIL.example.com")

		ca := h.cert(cmCACrt)
		assert.Equal(t, "/C=IT/O=Acme/CN=Acme CA", mustSlash(t, ca.RawSubject))
		assert.Equal(t, x509.SHA512WithRSA, ca.SignatureAlgorithm)
		assert.Equal(t, []string{cmHost, "alt.example.com"}, ca.DNSNames, "names once each, whatever the case")
	})
}

func TestRunCertMgr_CreateCRT(t *testing.T) {
	t.Run("keeps a valid certificate", func(t *testing.T) {
		h := newCMTest(t, nil)
		h.bootstrap()
		first := h.read(cmSelfCrt)

		out := h.ok("createcrt")

		assert.Contains(t, out, "** "+cmSelfCrt+" already exists.\n") // 782
		assert.Equal(t, first, h.read(cmSelfCrt))
	})

	t.Run("downloads the certificate of LDAP", func(t *testing.T) {
		h := newCMTest(t, nil)
		h.bootstrap()
		crt := h.read(cmSelfCrt)

		h.fakes.FS.Remove(cmSelfCrt)
		h.fakes.FS.Remove(cmSelfKey)

		out := h.ok("createcrt")

		assert.Contains(t, out, "** "+cmSelfCrt+" downloaded from ldap.\n") // 791
		assert.Equal(t, crt, h.read(cmSelfCrt))
	})

	t.Run("-new makes another", func(t *testing.T) {
		h := newCMTest(t, nil)
		h.bootstrap()
		first := h.read(cmSelfCrt)

		out := h.ok("createcrt", "-new", "-subjectAltNames", "a.example.com", "-noDefaultSubjectAltName")

		assert.NotContains(t, out, "already exists")
		assert.NotEqual(t, first, h.read(cmSelfCrt))
		assert.Equal(t, []string{"a.example.com"}, h.cert(cmSelfCrt).DNSNames)
		h.ok("verifycrt", "self")
	})

	t.Run("-allservers names every server", func(t *testing.T) {
		h := newCMTest(t, nil)
		h.dir.all = []string{cmExtraHost, cmHost}
		h.ok("createca", "-new")

		h.ok("createcrt", "-allservers")

		assert.Equal(t, []string{cmHost, cmExtraHost}, h.cert(cmSelfCrt).DNSNames)
		assert.Contains(t, h.dir.mods, "cn=mail.example.com,cn=servers,cn=zimbra zimbraSSLPrivateKey")
	})

	t.Run("the CA that is missing is an error", func(t *testing.T) {
		h := newCMTest(t, nil)

		status, out, _ := h.run("createcrt")

		assert.Equal(t, 1, status)
		assert.Contains(t, out, "ERROR: createca: empty key")
	})

	t.Run("a request without a name", func(t *testing.T) {
		h := newCMTest(t, nil)
		h.ok("createca", "-new")
		h.ok("createcsr", "self", "-subject", "/O=NoName")

		status, out, _ := h.run("createcrt")

		assert.Equal(t, 1, status)
		assert.Contains(t, out, "ERROR: openssl ca failed(1):\nThe commonName field needed to be supplied and was missing\n")
	})
}

func TestRunCertMgr_CreateCSR(t *testing.T) {
	h := newCMTest(t, nil)
	h.ok("createca", "-new")

	out := h.ok("createcsr", "comm", "-subject", "/C=IT/O=Acme/CN=mail.example.com",
		"-subjectAltNames", "b.example.com", "-keysize", "3072", "-digest", "sha384")

	// 858-890
	assert.Contains(t, out, "** Generating a server CSR of type 'comm' for download\n")
	assert.Contains(t, out, "with keysize=3072 digest=sha384\n")
	assert.Contains(t, out, "** Saving config key 'zimbraSSLPrivateKey' via zmprov modifyServer mail.example.com...ok\n")

	csr, err := certParseCSR([]byte(h.read(cmSSL + "/commercial/commercial.csr")))
	require.NoError(t, err)
	assert.Equal(t, "/C=IT/O=Acme/CN=mail.example.com", mustSlash(t, csr.RawSubject))
	assert.Equal(t, []string{cmHost, "b.example.com"}, csr.DNSNames)
	assert.Equal(t, uint32(0o600), h.mode(cmSSL+"/commercial/commercial.key"))
	assert.Equal(t, h.read(cmSSL+"/commercial/commercial.key"), h.dir.servers[cmHost][certAttrSSLKey])

	status, out, _ := h.run("createcsr", "comm")
	assert.Equal(t, 1, status)
	assert.Contains(t, out, "ERROR: Certificate Signing Request already exists: "+cmSSL+"/commercial/commercial.csr\n")

	h.ok("createcsr", "comm", "-new")
}

func TestRunCertMgr_DeployCA(t *testing.T) {
	h := newCMTest(t, nil)
	h.ok("createca", "-new")

	out := h.ok("deployca")

	assert.Contains(t, out, "** Saving config key 'zimbraCertAuthorityCertSelfSigned' via zmprov modifyConfig...ok\n")
	assert.Contains(t, out, "** Importing cert '"+cmCACrt+"' as 'my_ca' into cacerts '"+cmCacerts+"'\n")
	assert.Contains(t, out, "** NOTE: restart mailboxd to use the imported certificate.\n")
	assert.Contains(t, out, "** Creating CA hash symlink '3cafeb07.0' -> 'ca.pem'\n")
	assert.Equal(t, h.read(cmCACrt), h.dir.global[certAttrCACert])
	assert.Equal(t, h.read(cmCAKey), h.dir.global[certAttrCAKey])
	assert.Equal(t, h.read(cmCACrt), h.read(cmConfCA))
	assert.Equal(t, uint32(0o644), h.mode(cmConfCA))

	target, err := h.fakes.FS.Readlink("/opt/zextras/conf/ca/3cafeb07.0")
	require.NoError(t, err)
	assert.Equal(t, "ca.pem", target)

	// -localonly keeps the CA out of LDAP; a replica never saves it.
	h.dir.global = map[string]string{}
	h.ok("deployca", "-localonly")
	assert.Empty(t, h.dir.global)

	replica := newCMTest(t, map[string]string{"ldap_is_master": "false", "ldap_host": "ldap.example.com"})
	replica.fakes.FS.Put(cmCACrt, []byte(h.read(cmCACrt)), 0o640)
	replica.fakes.FS.Put(cmCAKey, []byte(h.read(cmCAKey)), 0o600)
	replica.ok("deployca")
	assert.Empty(t, replica.dir.global)
}

func TestRunCertMgr_DeployCAFailures(t *testing.T) {
	h := newCMTest(t, nil)
	h.ok("createca", "-new")
	h.dir.modErr = assert.AnError

	status, out, _ := h.run("deployca")

	assert.Equal(t, 1, status)
	assert.Contains(t, out, "** Saving config key 'zimbraCertAuthorityCertSelfSigned' via zmprov modifyConfig...failed (rc=1)\n")
	assert.NotContains(t, out, "Importing cert", "the truststore is not touched when LDAP refused the CA")
}

func TestRunCertMgr_DeployCRTSelf(t *testing.T) {
	h := newCMTest(t, nil)
	h.ok("createca", "-new")

	// The certificate that is missing is made first (1030-1045).
	out := h.ok("deploycrt", "self", "-deploy", "ldap", "-deploy", "mta")

	assert.Contains(t, out, "** Signing cert request "+cmSelfCSR+"\n")
	assert.Contains(t, out, "** Installing ldap certificate '/opt/zextras/conf/slapd.crt' and key '/opt/zextras/conf/slapd.key'\n")
	assert.Contains(t, out, "** Installing mta certificate '/opt/zextras/conf/smtpd.crt' and key '/opt/zextras/conf/smtpd.key'\n")
	assert.NotContains(t, out, "nginx")
	assert.NotContains(t, out, "keystore")
	assert.Contains(t, out, "** NOTE: restart services to use the new certificates.\n") // 1121

	for _, svc := range []string{"slapd", "smtpd"} {
		assert.Equal(t, h.read(cmSelfCrt), h.read("/opt/zextras/conf/"+svc+".crt"))
		assert.Equal(t, h.read(cmSelfKey), h.read("/opt/zextras/conf/"+svc+".key"))
		assert.Equal(t, uint32(0o640), h.mode("/opt/zextras/conf/"+svc+".key"))
	}

	assert.Equal(t, h.read(cmSelfCrt), h.dir.servers[cmHost][certAttrSSLCert])
	assert.False(t, h.fakes.FS.Exists("/opt/zextras/conf/nginx.crt"))
	assert.Empty(t, h.remote, "only -allservers runs commands on other servers")
}

func TestRunCertMgr_DeployCRTAllServers(t *testing.T) {
	h := newCMTest(t, nil)
	h.dir.all = []string{cmHost, "Store2.Example.com", "store3.example.com"}
	h.ok("createca", "-new")

	h.ok("deploycrt", "self", "-allservers")

	// The other servers fetch the certificate and install it (1125-1160).
	assert.Equal(t, []string{
		"store2.example.com: HOST:store2.example.com zmcertmgr getcrt self -allservers",
		"store2.example.com: HOST:store2.example.com zmcertmgr deploycrt self",
		"store3.example.com: HOST:store3.example.com zmcertmgr getcrt self -allservers",
		"store3.example.com: HOST:store3.example.com zmcertmgr deploycrt self",
	}, h.remote)
	assert.Contains(t, h.dir.mods, "cn=config,cn=zimbra zimbraSSLCertificate", "-allservers saves in the global config")
}

func TestRunCertMgr_DeployCRTComm(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()
	crtf, caf := h.commercial()

	out := h.ok("deploycrt", "comm", crtf, caf, "-deploy", "proxy")

	assert.Contains(t, out, "** Verifying '"+crtf+"' against '"+cmCommKey+"'\n")
	assert.Contains(t, out, "Valid certificate chain: "+crtf+": OK\n")
	assert.Contains(t, out, "** Copying '"+crtf+"' to '"+cmCommCrt+"'\n")
	assert.Contains(t, out, "** Copying '"+caf+"' to '"+cmCommCA+"'\n")
	assert.Contains(t, out, "** Installing proxy certificate '/opt/zextras/conf/nginx.crt' and key '/opt/zextras/conf/nginx.key'\n")

	// The chain follows the certificate in the files that are deployed.
	deployed := h.read("/opt/zextras/conf/nginx.crt")
	assert.Equal(t, 2, strings.Count(deployed, "BEGIN CERTIFICATE"))
	assert.Equal(t, h.read(cmCommCrt), deployed)
	assert.Equal(t, h.read(cmCommKey), h.read("/opt/zextras/conf/nginx.key"))
	assert.Equal(t, h.read(cmCommCrt), h.dir.servers[cmHost][certAttrSSLCert], "the chain is saved with the certificate")

	// The commercial CA is trusted by the keystore of the JVM now.
	assert.Contains(t, out, "** Importing cert '"+cmCommCA+"' as 'zcs-user-commercial_ca' into cacerts")

	// verifycrt comm sees the deployed certificate and its chain.
	assert.Contains(t, h.ok("verifycrt", "comm"), "Valid certificate chain: "+cmCommCrt+": OK\n")

	// getcrt takes the chain apart again.
	h.fakes.FS.Remove(cmCommCA)
	h.ok("getcrt", "comm")
	assert.Equal(t, 1, strings.Count(h.read(cmCommCA), "BEGIN CERTIFICATE"))
}

func TestRunCertMgr_DeployCRTCommFromLDAP(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()
	crtf, caf := h.commercial()
	h.ok("deploycrt", "comm", crtf, caf, "-deploy", "proxy")

	for _, f := range []string{cmCommCrt, cmCommCA} {
		require.NoError(t, h.fakes.FS.Remove(f))
	}

	out := h.ok("deploycrt", "comm", "-deploy", "mta")

	assert.Contains(t, out, "** Creating "+cmCommCrt+"\n")
	assert.Contains(t, out, "** Creating "+cmCommCA+"\n")
	assert.Equal(t, h.read(cmCommCrt), h.read("/opt/zextras/conf/smtpd.crt"))
}

func TestRunCertMgr_DeployCRTCommRefusesWhatDoesNotVerify(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()
	crtf, caf := h.commercial()

	// The certificate against the CA of the node: not the chain it came with.
	status, out, _ := h.run("deploycrt", "comm", crtf, cmCACrt)
	assert.Equal(t, 1, status)
	assert.Contains(t, out, "ERROR: Unable to validate certificate chain: ")
	assert.Contains(t, out, "error 20 at 0 depth lookup: unable to get local issuer certificate\n")
	assert.False(t, h.fakes.FS.Exists("/opt/zextras/conf/nginx.crt.bak"))

	// The self-signed key does not belong to the commercial certificate.
	h.fakes.FS.Put(cmCommKey, []byte(h.read(cmSelfKey)), 0o600)
	status, out, _ = h.run("deploycrt", "comm", crtf, caf)
	assert.Equal(t, 1, status)
	assert.Contains(t, out, "ERROR: Certificate '"+crtf+"' and private key '"+cmCommKey+"' do not match.\n")
}

func TestRunCertMgr_VerifyCRT(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()

	t.Run("key", func(t *testing.T) {
		status, out, _ := h.run("verifycrtkey", cmCAKey, cmSelfCrt)
		assert.Equal(t, 1, status)
		assert.Equal(t, "** Verifying '"+cmSelfCrt+"' against '"+cmCAKey+"'\n"+
			"ERROR: Certificate '"+cmSelfCrt+"' and private key '"+cmCAKey+"' do not match.\n", out)

		status, out, _ = h.run("verifycrtkey", "/none.key", cmSelfCrt)
		assert.Equal(t, 1, status)
		assert.Contains(t, out, "ERROR: Can't read file '/none.key'")
	})

	t.Run("chain", func(t *testing.T) {
		out := h.ok("verifycrtchain", cmCACrt, cmSelfCrt)
		assert.Equal(t, "** Verifying '"+cmSelfCrt+"' against '"+cmCACrt+"'\nValid certificate chain: "+cmSelfCrt+": OK\n", out)

		// The chain of another CA: openssl verify error 20.
		crtf, caf := h.commercial()
		_ = crtf
		status, out, _ := h.run("verifycrtchain", caf, cmSelfCrt)
		assert.Equal(t, 1, status)
		assert.Contains(t, out, "ERROR: Unable to validate certificate chain: OU = Zextras Carbonio, CN = mail.example.com\n"+
			"error 20 at 0 depth lookup: unable to get local issuer certificate\n"+
			"error "+cmSelfCrt+": verification failed\n")
	})

	t.Run("expired", func(t *testing.T) {
		h.fakes.Clock.Advance(10 * 366 * 24 * time.Hour)

		status, out, _ := h.run("verifycrt", "self")
		assert.Equal(t, 1, status)
		assert.Contains(t, out, "error 10 at 0 depth lookup: certificate has expired\n")
	})
}

func TestRunCertMgr_GetCRTAndSaveCRT(t *testing.T) {
	h := newCMTest(t, nil)
	h.bootstrap()
	crt, key := h.read(cmSelfCrt), h.read(cmSelfKey)

	require.NoError(t, h.fakes.FS.Remove(cmSelfKey))

	out := h.ok("getcrt", "self")

	assert.Equal(t, "** Creating "+cmSelfCrt+"\n** Creating "+cmSelfKey+"\n", out)
	assert.Equal(t, crt, h.read(cmSelfCrt))
	assert.Equal(t, key, h.read(cmSelfKey))
	assert.Equal(t, uint32(0o640), h.mode(cmSelfKey))

	// A value LDAP does not have removes the file (getcrt of 700-760).
	h.dir.servers[cmHost] = map[string]string{}
	h.ok("getcrt", "self")
	assert.False(t, h.fakes.FS.Exists(cmSelfCrt))

	// -allservers reads the global config.
	h.dir.global[certAttrSSLCert], h.dir.global[certAttrSSLKey] = crt, key
	h.ok("getcrt", "self", "-allservers")
	assert.Equal(t, crt, h.read(cmSelfCrt))

	h.dir.mods = nil
	out = h.ok("savecrt", "self", "-allservers")
	assert.Equal(t, "** Saving config key 'zimbraSSLCertificate' via zmprov modifyConfig...ok\n"+
		"** Saving config key 'zimbraSSLPrivateKey' via zmprov modifyConfig...ok\n", out)

	h.dir.modErr = assert.AnError
	status, out, _ := h.run("savecrt", "self")
	assert.Equal(t, 1, status)
	assert.Equal(t, "** Saving config key 'zimbraSSLCertificate' via zmprov modifyServer mail.example.com...failed (rc=1)\n", out)
}

func TestRunCertMgr_LDAPDownIsTolerated(t *testing.T) {
	h := newCMTest(t, nil)
	h.dir.readErr = assert.AnError

	out := h.ok("createca", "-new")
	assert.Contains(t, out, "** Retrieving CA private key from LDAP... failed\n")
	assert.Contains(t, out, "** Creating CA with new private key")

	// The CA is not installed when its settings cannot be saved.
	h.dir.modErr = assert.AnError

	status, out, _ := h.run("deployca")
	assert.Equal(t, 1, status)
	assert.Contains(t, out, "** Saving config key 'zimbraCertAuthorityCertSelfSigned' via zmprov modifyConfig...failed (rc=1)\n")
	assert.NotContains(t, out, "Copying CA")
}
