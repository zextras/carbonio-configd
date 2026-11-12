// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const (
	certTestCertmgr = "zmcertmgr"
	certTestHost    = "mail.example.com"
	certTestServer  = "/opt/zextras/ssl/carbonio/server/server.crt"
)

func certTestEnv(t *testing.T, packages ...string) (*Env, *systest.Fakes) {
	t.Helper()

	e, fakes, _ := testEnv(t)
	e.Paths.LocalConfig = filepath.Join(t.TempDir(), "localconfig.xml")
	require.NoError(t, os.WriteFile(e.Paths.LocalConfig,
		[]byte("<?xml version=\"1.0\"?>\n<localconfig>\n</localconfig>\n"), 0o600))

	e.Cfg = &bootstrap.Config{}
	e.Cfg.Host.Hostname = certTestHost
	e.Cfg.LDAP.Host = certTestHost
	e.Facts = &Facts{Installed: map[string]bool{}, Configured: map[string]bool{}}

	for _, p := range packages {
		e.Facts.Installed[p] = true
	}

	// zmcertmgr runs in process; the steps' calls of it are recorded (and
	// answered) as the command "zmcertmgr <args>".
	e.certRun = func(args []string) (int, string) {
		var out bytes.Buffer

		res, err := fakes.Exec.Run(e.Ctx, &sys.Cmd{Path: certTestCertmgr, Args: args, Stdout: &out, Stderr: &out})
		if exitErr, ok := errors.AsType[*sys.ExitError](err); ok {
			return exitErr.Code, out.String()
		}

		return res.ExitCode, out.String()
	}

	return e, fakes
}

// certTestCmds lists the commands run as zextras or root, without the
// runuser wrapper.
func certTestCmds(fakes *systest.Fakes) []string {
	var out []string

	for _, c := range fakes.Exec.Calls() {
		if c.Path == "runuser" {
			out = append(out, strings.Join(c.Args[6:], " "))

			continue
		}

		out = append(out, c.String())
	}

	return out
}

func certTestFail(code int, output string) systest.Handler {
	return func(c *sys.Cmd) (sys.Result, error) {
		_, _ = c.Stderr.Write([]byte(output))

		return sys.Result{ExitCode: code}, nil
	}
}

func certTestVerb(verb string) func(*sys.Cmd) bool {
	return func(c *sys.Cmd) bool {
		return c.Path == certTestCertmgr && len(c.Args) > 0 && c.Args[0] == verb
	}
}

func TestCertAddCACert_OnlyOnExistingInstallWithCA(t *testing.T) {
	e, fakes := certTestEnv(t)
	step := StepAddCACert()

	assert.True(t, step.Optional)

	e.Facts.NewInstall = true
	fakes.FS.Put("/opt/zextras/conf/ca/ca.pem", []byte("x"), 0o644)
	assert.False(t, step.When(e), "new install")

	e.Facts.NewInstall = false
	assert.True(t, step.When(e))

	e2, _ := certTestEnv(t)
	assert.False(t, step.When(e2), "no ca.pem")

	require.NoError(t, step.Apply(e))
	assert.Equal(t, []string{certTestCertmgr + " addcacert /opt/zextras/conf/ca/ca.pem"}, certTestCmds(fakes))
}

func TestCertCASetup_NewInstallMasterCreatesNewCA(t *testing.T) {
	e, fakes := certTestEnv(t)
	e.Facts.NewInstall = true
	e.Cfg.Host.SSLDigest = "sha384"

	require.NoError(t, StepConfigCASetup().Apply(e))

	assert.Equal(t, []string{
		certTestCertmgr + " createca -new",
		certTestCertmgr + " deployca -localonly",
	}, certTestCmds(fakes))
	assert.True(t, e.Flags.NeedNewCert, "a new CA needs a new certificate")

	v, err := e.LCValue("ssl_default_digest")
	require.NoError(t, err)
	assert.Equal(t, "sha384", v)
}

func TestCertCASetup_ExpiredCAOnExistingMaster(t *testing.T) {
	e, fakes := certTestEnv(t)
	fakes.FS.PutDir("/opt/zextras/ssl/carbonio/ca", 0o755)

	key, err := certNewRSAKey(2048)
	require.NoError(t, err)

	subject, err := certParseSubject("/O=CA/CN=" + certTestHost)
	require.NoError(t, err)

	// A CA of one day, made three days ago: setup.pl saw "error 10" of
	// openssl verify and had a new CA made.
	issued, err := certSelfSign(key, subject, nil, 1, "sha256", e.Sys.Clock.Now().AddDate(0, 0, -3))
	require.NoError(t, err)
	fakes.FS.Put("/opt/zextras/conf/ca/ca.pem", certPEMBlock("CERTIFICATE", issued.DER), 0o644)

	require.NoError(t, StepConfigCASetup().Apply(e))

	assert.Equal(t, []string{certTestCertmgr + " createca -new", certTestCertmgr + " deployca -localonly"},
		certTestCmds(fakes))
	assert.True(t, e.Flags.NeedNewCert)
}

func TestCertCASetup_ValidCAOnReplicaKeepsCAAndStoresLDAPCredentials(t *testing.T) {
	e, fakes := certTestEnv(t)
	e.Cfg.LDAP.Host = "ldap.example.com"
	e.Cfg.LDAP.RootPassword = "rootSecret"
	e.Cfg.LDAP.AdminPassword = "adminSecret"
	fakes.FS.Put("/opt/zextras/conf/ca/ca.pem", []byte("x"), 0o644)
	fakes.FS.PutDir("/opt/zextras/ssl/carbonio/ca", 0o755)

	require.NoError(t, StepConfigCASetup().Apply(e))

	cmds := certTestCmds(fakes)
	assert.Equal(t, certTestCertmgr+" createca", cmds[0], "not the master, valid CA: no -new")
	assert.False(t, e.Flags.NeedNewCert)

	for _, c := range cmds {
		assert.NotContains(t, c, "Secret")
	}

	lc, err := e.LC()
	require.NoError(t, err)
	assert.Equal(t, "rootSecret", lc["ldap_root_password"])
	assert.Equal(t, "adminSecret", lc["zimbra_ldap_password"])
}

func TestCertCASetup_FailureEndsTheRun(t *testing.T) {
	e, fakes := certTestEnv(t)
	fakes.Exec.When(certTestVerb("deployca"), certTestFail(1, "boom"))

	err := StepConfigCASetup().Apply(e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deploying the CA")
}

func TestCertCASetup_DoneNeedsStageAndDirectory(t *testing.T) {
	e, fakes := certTestEnv(t)
	step := StepConfigCASetup()

	done, err := step.Done(e)
	require.NoError(t, err)
	assert.False(t, done)

	e.Facts.Configured[certStageCASetup] = true

	done, _ = step.Done(e)
	assert.False(t, done, "stage without the CA directory")

	fakes.FS.PutDir("/opt/zextras/ssl/carbonio/ca", 0o755)

	done, _ = step.Done(e)
	assert.True(t, done)
}

func TestCertSaveCA(t *testing.T) {
	e, fakes := certTestEnv(t)
	step := StepConfigSaveCA()
	require.NoError(t, step.Apply(e))
	assert.Equal(t, []string{certTestCertmgr + " deployca"}, certTestCmds(fakes))

	e.Facts.Configured[certStageSaveCA] = true
	done, _ := step.Done(e)
	assert.True(t, done)
}

func TestCertCreateCert_NewInstallCreatesOnceForAllPackages(t *testing.T) {
	e, fakes := certTestEnv(t, PkgAppserver, "carbonio-directory-server", PkgMTA, "carbonio-proxy")
	e.Facts.NewInstall = true
	e.Flags.NeedNewCert = true
	fakes.Exec.When(certTestVerb("createcrt"), func(*sys.Cmd) (sys.Result, error) {
		fakes.FS.Put(certTestServer, []byte("x"), 0o644)

		return sys.Result{}, nil
	})

	require.NoError(t, StepConfigCreateCert().Apply(e))

	// The first package creates the certificate and creates the mailboxd
	// directory; the others find server.crt and need no second -new.
	assert.Equal(t, []string{
		"mkdir -p /opt/zextras/mailboxd/etc",
		"chown -R zextras:zextras /opt/zextras/mailboxd",
		"chmod 744 /opt/zextras/mailboxd/etc",
		certTestCertmgr + " createcrt -new",
	}, certTestCmds(fakes))
}

func TestCertCreateCert_ExistingInstallWithValidCommercialCert(t *testing.T) {
	e, fakes := certTestEnv(t, PkgMTA)
	e.Flags.NeedNewCert = true // set by the CA step
	fakes.FS.Put("/opt/zextras/conf/smtpd.crt", []byte("x"), 0o644)

	require.NoError(t, StepConfigCreateCert().Apply(e))

	assert.Equal(t, []string{certTestCertmgr + " verifycrt comm"}, certTestCmds(fakes))
	assert.Equal(t, "comm", e.Flags.CertType)
	assert.False(t, e.Flags.NeedNewCert, "a valid commercial certificate cancels the renewal")
}

func TestCertCreateCert_ExistingInstallWithoutValidCertRegeneratesSelfSigned(t *testing.T) {
	e, fakes := certTestEnv(t, PkgMTA, "carbonio-proxy")
	fakes.FS.Put("/opt/zextras/conf/smtpd.crt", []byte("x"), 0o644)
	fakes.FS.Put("/opt/zextras/conf/nginx.crt", []byte("x"), 0o644)
	fakes.Exec.When(certTestVerb("verifycrt"), certTestFail(1, "invalid"))

	require.NoError(t, StepConfigCreateCert().Apply(e))

	assert.Equal(t, []string{
		certTestCertmgr + " verifycrt comm",
		certTestCertmgr + " verifycrt self",
		certTestCertmgr + " createcrt -new",
	}, certTestCmds(fakes))
	assert.Equal(t, "self", e.Flags.CertType)
	assert.True(t, e.Flags.NeedNewCert)
}

func TestCertCreateCert_ValidSelfSignedCertIsKept(t *testing.T) {
	e, fakes := certTestEnv(t, PkgMTA)
	fakes.FS.Put("/opt/zextras/conf/smtpd.crt", []byte("x"), 0o644)
	fakes.Exec.When(func(c *sys.Cmd) bool {
		return certTestVerb("verifycrt")(c) && c.Args[1] == "comm"
	}, certTestFail(1, "no"))

	require.NoError(t, StepConfigCreateCert().Apply(e))

	assert.Equal(t, []string{certTestCertmgr + " verifycrt comm", certTestCertmgr + " verifycrt self"},
		certTestCmds(fakes))
	assert.False(t, e.Flags.NeedNewCert)
}

func TestCertCreateCert_FailureEndsTheRun(t *testing.T) {
	e, fakes := certTestEnv(t, PkgMTA)
	e.Facts.NewInstall = true
	fakes.Exec.When(certTestVerb("createcrt"), certTestFail(1, "boom"))

	err := StepConfigCreateCert().Apply(e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "carbonio-mta")
}

func TestCertCreateCert_DoneNeedsStageAndServerDirectory(t *testing.T) {
	e, fakes := certTestEnv(t)
	e.Facts.Configured[certStageCreateCert] = true

	done, _ := StepConfigCreateCert().Done(e)
	assert.False(t, done)

	fakes.FS.PutDir("/opt/zextras/ssl/carbonio/server", 0o755)

	done, _ = StepConfigCreateCert().Done(e)
	assert.True(t, done)
}

func TestCertSaveCert_UsesCertTypeAndNeedsCertificate(t *testing.T) {
	e, fakes := certTestEnv(t)
	step := StepConfigSaveCert()

	assert.False(t, step.When(e))

	fakes.FS.Put(certTestServer, []byte("x"), 0o644)
	assert.True(t, step.When(e))

	require.NoError(t, step.Apply(e))

	e.Flags.CertType = "comm"
	require.NoError(t, step.Apply(e))

	assert.Equal(t, []string{certTestCertmgr + " savecrt self", certTestCertmgr + " savecrt comm"},
		certTestCmds(fakes))
}

func TestCertInstallCert_DoneAndDeploy(t *testing.T) {
	tests := []struct {
		name string
		step Step
		pkg  string
		file string
	}{
		{"store", StepConfigInstallCertStore(), PkgAppserver, "/opt/zextras/conf/keystore"},
		{"mta", StepConfigInstallCertMTA(), PkgMTA, "/opt/zextras/conf/smtpd.key"},
		{"ldap", StepConfigInstallCertLDAP(), "carbonio-directory-server", "/opt/zextras/conf/slapd.crt"},
		{"proxy", StepConfigInstallCertProxy(), "carbonio-proxy", "/opt/zextras/conf/nginx.key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, fakes := certTestEnv(t)
			require.NoError(t, e.SetLC(map[string]string{"mailboxd_keystore": "/opt/zextras/conf/keystore"}))

			assert.False(t, tt.step.When(e), "package not installed")

			e.Facts.Installed[tt.pkg] = true
			assert.True(t, tt.step.When(e))

			done, _ := tt.step.Done(e)
			assert.False(t, done, "no certificate files")

			fakes.FS.Put(tt.file, []byte("x"), 0o600)

			done, _ = tt.step.Done(e)
			assert.True(t, done)

			e.Flags.NeedNewCert = true
			done, _ = tt.step.Done(e)
			assert.False(t, done, "a new certificate is redeployed")

			e.Flags.CertType = "comm"
			require.NoError(t, tt.step.Apply(e))
			assert.Equal(t, []string{certTestCertmgr + " deploycrt comm"}, certTestCmds(fakes))
		})
	}
}

func TestCertInstallCert_DoneByStageUnlessNewCert(t *testing.T) {
	e, _ := certTestEnv(t, PkgMTA)
	step := StepConfigInstallCertMTA()
	e.Facts.Configured[certStageInstallMTA] = true

	done, _ := step.Done(e)
	assert.True(t, done)

	e.Flags.NeedNewCert = true
	done, _ = step.Done(e)
	assert.False(t, done)
}

func TestCertInstallCertLDAP_RestartsConfiguredLDAPOnly(t *testing.T) {
	restarts := 0
	orig := certRestartLDAP
	certRestartLDAP = func(*Env) error { restarts++; return nil }

	t.Cleanup(func() { certRestartLDAP = orig })

	e, fakes := certTestEnv(t, "carbonio-directory-server")
	step := StepConfigInstallCertLDAP()

	require.NoError(t, step.Apply(e))
	assert.Equal(t, 0, restarts, "no LDAP database yet")

	fakes.FS.Put("/opt/zextras/data/ldap/mdb/db/data.mdb", []byte("x"), 0o600)
	require.NoError(t, step.Apply(e))
	assert.Equal(t, 1, restarts)

	fakes.Exec.When(certTestVerb("deploycrt"), certTestFail(1, "boom"))
	require.Error(t, step.Apply(e))
	assert.Equal(t, 1, restarts, "no restart after a failed deploy")
}

func TestCertDHParam(t *testing.T) {
	const dest = "/opt/zextras/conf/dhparam.pem.crb"

	t.Run("writes the ffdhe2048 group then records", func(t *testing.T) {
		e, fakes := certTestEnv(t)

		require.NoError(t, StepGenerateDHParam().Apply(e))

		assert.Equal(t, "chown zextras:zextras "+dest, certTestCmds(fakes)[0])

		for _, c := range certTestCmds(fakes) {
			assert.NotContains(t, c, "openssl")
			assert.NotContains(t, c, "zmprov", "the attribute is written to LDAP")
		}

		data, err := fakes.FS.ReadFile(dest)
		require.NoError(t, err)
		assertFFDHE2048PEM(t, data)

		// zmprov stores the content of the file named by the binary attribute.
		assert.Equal(t, []string{string(data)}, provTestLDAP().values("cn=config,cn=zimbra", certDHParamAttr))
	})

	t.Run("keeps an existing file", func(t *testing.T) {
		e, fakes := certTestEnv(t)
		fakes.FS.Put(dest, []byte("existing"), 0o640)

		require.NoError(t, StepGenerateDHParam().Apply(e))

		data, err := fakes.FS.ReadFile(dest)
		require.NoError(t, err)
		assert.Equal(t, "existing", string(data))
	})

	t.Run("openssl accepts the file", func(t *testing.T) {
		bin, err := exec.LookPath("openssl")
		if err != nil {
			t.Skip("openssl not available")
		}

		data, err := dhGroupPEM(dhDefaultBits)
		require.NoError(t, err)

		file := filepath.Join(t.TempDir(), "dhparam.pem")
		require.NoError(t, os.WriteFile(file, data, 0o600))

		out, err := exec.Command(bin, "dhparam", "-in", file, "-check", "-noout").CombinedOutput() //nolint:gosec
		require.NoError(t, err, string(out))
	})

	t.Run("applies to new installs only", func(t *testing.T) {
		e, _ := certTestEnv(t)
		assert.False(t, StepGenerateDHParam().When(e))

		e.Facts.NewInstall = true
		assert.True(t, StepGenerateDHParam().When(e))
	})

	t.Run("done needs the file and the attribute", func(t *testing.T) {
		orig := certGlobalConfig

		t.Cleanup(func() { certGlobalConfig = orig })

		attr := ""
		certGlobalConfig = func(*Env) (map[string]string, error) {
			return map[string]string{certDHParamAttr: attr}, nil
		}

		e, fakes := certTestEnv(t)
		step := StepGenerateDHParam()

		done, err := step.Done(e)
		require.NoError(t, err)
		assert.False(t, done, "no file")

		fakes.FS.Put(dest, []byte("dh"), 0o640)

		done, _ = step.Done(e)
		assert.False(t, done, "attribute unset")

		attr = dest
		done, _ = step.Done(e)
		assert.False(t, done, "the attribute is binary: it holds the file, never its path")

		attr = "dh"
		done, _ = step.Done(e)
		assert.True(t, done, "the attribute holds the installed file")

		certGlobalConfig = func(*Env) (map[string]string, error) { return nil, errors.New("ldap down") }
		_, err = step.Done(e)
		require.Error(t, err)
	})
}

// assertFFDHE2048PEM checks that data is a DH PARAMETERS block holding the RFC
// 7919 ffdhe2048 prime (2048 bits) and generator 2.
func assertFFDHE2048PEM(t *testing.T, data []byte) {
	t.Helper()

	block, rest := pem.Decode(data)
	require.NotNil(t, block)
	assert.Empty(t, rest)
	assert.Equal(t, "DH PARAMETERS", block.Type)

	var params struct{ P, G *big.Int }

	tail, err := asn1.Unmarshal(block.Bytes, &params)
	require.NoError(t, err)
	assert.Empty(t, tail)

	want, ok := new(big.Int).SetString(rfc7919FFDHE2048, 16)
	require.True(t, ok)
	assert.Equal(t, 2048, params.P.BitLen())
	assert.Zero(t, want.Cmp(params.P), "p is the RFC 7919 prime")
	assert.Equal(t, int64(2), params.G.Int64())

	q := new(big.Int).Rsh(params.P, 1)
	assert.True(t, params.P.ProbablyPrime(10), "p is prime")
	assert.True(t, q.ProbablyPrime(10), "(p-1)/2 is prime: p is a safe prime")
}

// rfc7919FFDHE2048 is the RFC 7919 appendix A.1 prime as the RFC prints it.
const rfc7919FFDHE2048 = "FFFFFFFFFFFFFFFFADF85458A2BB4A9AAFDC5620273D3CF1D8B9C583CE2D3695" +
	"A9E13641146433FBCC939DCE249B3EF97D2FE363630C75D8F681B202AEC4617A" +
	"D3DF1ED5D5FD65612433F51F5F066ED0856365553DED1AF3B557135E7F57C935" +
	"984F0C70E0E68B77E2A689DAF3EFE8721DF158A136ADE73530ACCA4F483A797A" +
	"BC0AB182B324FB61D108A94BB2C8E3FBB96ADAB760D7F4681D4F42A3DE394DF4" +
	"AE56EDE76372BB190B07A7C8EE0A6D709E02FCE1CDF7E2ECC03404CD28342F61" +
	"9172FE9CE98583FF8E4F1232EEF28183C3FE3B1B4C6FAD733BB5FCBC2EC22005" +
	"C58EF1837D1683B2C6F34A26C1B2EFFA886B423861285C97FFFFFFFFFFFFFFFF"
