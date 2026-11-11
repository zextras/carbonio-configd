// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrap

import (
	"bufio"
	"context"
	"errors"
	"io/fs"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeHost struct {
	fqdn  string
	roles []string
}

func (h fakeHost) FQDN() string             { return h.fqdn }
func (h fakeHost) InstalledRoles() []string { return h.roles }
func (fakeHost) IPMode() string             { return "ipv4" }
func (fakeHost) Timezone() string           { return "" }
func (fakeHost) Networks() []string         { return []string{"10.1.0.0/16"} }

var singleNode = fakeHost{fqdn: "mail.example.com", roles: []string{RoleDirectoryServer, RoleMTA, RoleAppserver, RoleProxy}}

func readFiles(files map[string]string) ReadFileFunc {
	return func(name string) ([]byte, error) {
		data, ok := files[name]
		if !ok {
			return nil, fs.ErrNotExist
		}

		return []byte(data), nil
	}
}

func TestLayers_Precedence(t *testing.T) {
	r := NewResolved(readFiles(nil))

	_, err := r.ApplyLegacy([]byte("LDAPPORT=1389\nMODE=http\nHOSTNAME=legacy.example.com\n"))
	require.NoError(t, err)
	require.NoError(t, r.ApplyYAML([]byte("ldap:\n  port: 2389\nstore:\n  mode: both\n")))
	require.NoError(t, r.ApplyEnv([]string{"CARBONIO_BOOTSTRAP_LDAP_PORT=3389", "OTHER=1"}))
	require.NoError(t, r.ApplySets([]string{"ldap.port=4389"}))
	require.NoError(t, r.ApplyDefaults(singleNode))

	assert.Equal(t, 4389, r.Config.LDAP.Port)
	assert.Equal(t, SourceFlag, r.Source("ldap.port"))
	assert.Equal(t, "both", r.Config.Store.Mode, "the file overrides the legacy import")
	assert.Equal(t, "legacy.example.com", r.Config.Host.Hostname, "a set key is not replaced by a default")
	assert.Equal(t, "legacy.example.com", r.Config.LDAP.Host, "defaults follow the keys set before them")
	assert.Equal(t, SourceDefault, r.Source("ldap.host"))
}

func TestDefaults_FollowRolesAndHostname(t *testing.T) {
	r := NewResolved(readFiles(nil))
	require.NoError(t, r.ApplyDefaults(singleNode))

	c := r.Config
	assert.Equal(t, "example.com", c.Domain.Name)
	assert.Equal(t, "zextras@example.com", c.Admin.Account)
	assert.True(t, c.Domain.Create)
	assert.True(t, c.Admin.Create)
	assert.Equal(t, []string{"127.0.0.0/8", "[::1]/128", "10.1.0.0/16"}, c.MTA.MyNetworks)
	assert.Equal(t, 7143, c.Store.Ports.IMAP, "the proxy on the node takes the standard ports")
	assert.Equal(t, 143, c.Proxy.Ports.IMAP)
	assert.Equal(t, defaultTimezone, c.Host.Timezone)
	assert.NotEmpty(t, c.LDAP.RootPassword, "the LDAP master generates its passwords")
	assert.NotEqual(t, c.LDAP.RootPassword, c.LDAP.AdminPassword, "each password is drawn separately")

	mta := NewResolved(readFiles(nil))
	require.NoError(t, mta.ApplyDefaults(fakeHost{fqdn: "mta.example.com", roles: []string{RoleMTA}}))
	assert.Empty(t, mta.Config.LDAP.AdminPassword, "only the LDAP master generates passwords")
	assert.Empty(t, mta.Config.LDAP.Host)
	assert.Equal(t, 143, mta.Config.Store.Ports.IMAP)
}

func TestApplyYAML_Errors(t *testing.T) {
	tests := []struct{ name, doc, want string }{
		{"unknown key", "host:\n  hostnme: x\n", `line 2: unknown key "host.hostnme"`},
		{"unknown section", "dns:\n  a: b\n", `unknown key "dns"`},
		{"enum", "store:\n  mode: ftp\n", `"ftp" is not one of`},
		{"type", "ldap:\n  port: high\n", `"high" is not a number`},
		{"list for scalar", "host:\n  hostname: [a, b]\n", "expected a single string"},
		{"file for non-secret", "host:\n  hostname_file: /x\n", `unknown key "host.hostname_file"`},
		{"missing secret file", "ldap:\n  root_password_file: /nope\n", "reading the secret file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewResolved(readFiles(nil)).ApplyYAML([]byte(tt.doc))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestSecretFiles(t *testing.T) {
	files := readFiles(map[string]string{"/s/root": "fromfile\n", "/s/admin": "adminfile\r\n"})
	r := NewResolved(files)

	require.NoError(t, r.ApplyYAML([]byte("ldap:\n  root_password_file: /s/root\n")))
	require.NoError(t, r.ApplyEnv([]string{"CARBONIO_BOOTSTRAP_ADMIN_PASSWORD_FILE=/s/admin"}))

	assert.Equal(t, "fromfile", r.Config.LDAP.RootPassword, "one trailing newline is dropped")
	assert.Equal(t, "adminfile", r.Config.Admin.Password)
	assert.Equal(t, "/s/admin", r.SecretFile("admin.password"))

	require.NoError(t, r.ApplySets([]string{"ldap.root_password=direct"}))
	assert.Empty(t, r.SecretFile("ldap.root_password"), "a later direct value forgets the file")
}

func TestApplyEnv_Errors(t *testing.T) {
	err := NewResolved(readFiles(map[string]string{"/f": "x"})).ApplyEnv([]string{
		"CARBONIO_BOOTSTRAP_LDAP_ROOT_PASSWORD=a",
		"CARBONIO_BOOTSTRAP_LDAP_ROOT_PASSWORD_FILE=/f",
		"CARBONIO_BOOTSTRAP_HOST_HOSTNAME_FILE=/f",
		"CARBONIO_BOOTSTRAP_TYPO=1",
		"CARBONIO_BOOTSTRAP_START_SERVICES=maybe",
	})
	require.Error(t, err)

	msg := err.Error()
	assert.Contains(t, msg, "CARBONIO_BOOTSTRAP_LDAP_ROOT_PASSWORD and CARBONIO_BOOTSTRAP_LDAP_ROOT_PASSWORD_FILE are both set")
	assert.Contains(t, msg, "unknown environment variable CARBONIO_BOOTSTRAP_HOST_HOSTNAME_FILE", "_FILE is for secrets only")
	assert.Contains(t, msg, "unknown environment variable CARBONIO_BOOTSTRAP_TYPO")
	assert.Contains(t, msg, `"maybe" is not a boolean`)
}

func TestApplyEnv_ListsAndBooleans(t *testing.T) {
	r := NewResolved(readFiles(nil))
	require.NoError(t, r.ApplyEnv([]string{
		"CARBONIO_BOOTSTRAP_MTA_MY_NETWORKS=127.0.0.0/8, 10.0.0.0/8",
		"CARBONIO_BOOTSTRAP_START_SERVICES=no",
		"CARBONIO_BOOTSTRAP_LDAP_ROOT_PASSWORD=",
	}))

	assert.Equal(t, []string{"127.0.0.0/8", "10.0.0.0/8"}, r.Config.MTA.MyNetworks)
	assert.False(t, r.Config.StartServices)
	assert.Equal(t, SourceEnv, r.Source("ldap.root_password"), "an empty variable still counts as set")
}

func TestApplyLegacy(t *testing.T) {
	r := NewResolved(readFiles(nil))
	drops, err := r.ApplyLegacy([]byte(strings.Join([]string{
		`HOSTNAME="mail.example.com"`,
		`DOCREATEADMIN="yes"`,
		`MAILPROXY="FALSE"`,
		`zimbraMtaMyNetworks="127.0.0.0/8 [::1]/128"`,
		`PUBLICSERVICEHOSTNAME="UNSET"`,
		`SMTPHOST=""`,
		`LDAPREPLICATIONTYPE="master"`,
		`LDAPROOTPASSSET="set"`,
		`EXPANDMENU="no"`,
		`CREATEADMINPASS="a=b"`,
	}, "\n")))
	require.NoError(t, err)

	assert.Equal(t, "mail.example.com", r.Config.Host.Hostname)
	assert.True(t, r.Config.Admin.Create)
	assert.False(t, r.Config.Proxy.MailEnabled)
	assert.Equal(t, []string{"127.0.0.0/8", "[::1]/128"}, r.Config.MTA.MyNetworks)
	assert.Equal(t, "a=b", r.Config.Admin.Password, "the value runs to the end of the line")
	assert.Equal(t, SourceUnset, r.Source("host.public_hostname"), "UNSET is no value")
	assert.Equal(t, SourceUnset, r.Source("store.smtp_host"), "an empty value leaves the default")
	assert.Equal(t, []LegacyDrop{{"EXPANDMENU", reasonObsolete}, {"LDAPROOTPASSSET", reasonDerived}}, drops)

	_, err = NewResolved(readFiles(nil)).ApplyLegacy([]byte("LDAPREPLICATIONTYPE=mmr\nFOO=1\nbare\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "line 1: LDAPREPLICATIONTYPE=mmr: "+reasonReplication)
	assert.Contains(t, err.Error(), "line 2: unknown key FOO")
	assert.Contains(t, err.Error(), "line 3: expected KEY=VALUE")
}

func TestLegacyKeysAreMappedOrDropped(t *testing.T) {
	for key := range legacyDropped {
		assert.Nil(t, fieldsByLegacy[key], "%s is both mapped and dropped", key)
	}
}

func TestValidate(t *testing.T) {
	valid := func() *Resolved {
		r := NewResolved(readFiles(nil))
		require.NoError(t, r.ApplySets([]string{"admin.password=secret1"}))
		require.NoError(t, r.ApplyDefaults(singleNode))

		return r
	}

	require.NoError(t, Validate(&valid().Config))

	tests := []struct {
		name string
		sets []string
		want string
	}{
		{"short admin password", []string{"admin.password=12345"}, "at least 6 characters"},
		{"no admin password", []string{"admin.password="}, "admin.password is required"},
		{"bad hostname", []string{"host.hostname=mail"}, `host.hostname: "mail" is not a fully qualified name`},
		{"bad address", []string{"admin.account=nobody"}, "is not an email address"},
		{"bad network", []string{"mta.my_networks=10.0.0.0/33"}, "is not an address or a network"},
		{"port range", []string{"ldap.port=70000"}, "70000 is not a port"},
		{"port clash", []string{"proxy.ports.http=80"}, "store.ports.http and proxy.ports.http both use port 80"},
		{"no roles", []string{"roles="}, "roles is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := valid()
			require.NoError(t, r.ApplySets(tt.sets))

			err := Validate(&r.Config)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}

	t.Run("passwords of a non-master node", func(t *testing.T) {
		r := NewResolved(readFiles(nil))
		require.NoError(t, r.ApplyDefaults(fakeHost{fqdn: "proxy.example.com", roles: []string{RoleProxy}}))

		var missing []string
		for _, f := range Missing(&r.Config) {
			missing = append(missing, f.Path)
		}

		assert.Equal(t, []string{"ldap.host", "ldap.admin_password", "ldap.nginx_password"}, missing)
	})
}

func TestRender(t *testing.T) {
	r := NewResolved(readFiles(map[string]string{"/s/root": "rootpw"}))
	require.NoError(t, r.ApplyYAML([]byte("ldap:\n  root_password_file: /s/root\n")))
	require.NoError(t, r.ApplySets([]string{"admin.password=adminpw"}))
	require.NoError(t, r.ApplyDefaults(singleNode))

	printed, err := r.Render(Print)
	require.NoError(t, err)

	saved, err := r.Render(Save)
	require.NoError(t, err)

	for _, out := range []string{string(printed), string(saved)} {
		assert.NotContains(t, out, "rootpw")
		assert.NotContains(t, out, "adminpw")
		assert.NotContains(t, out, r.Config.LDAP.AdminPassword, "generated secrets are not written either")
		assert.Contains(t, out, "root_password_file: /s/root")
	}

	assert.Contains(t, string(printed), "password: '"+Mask+"' # flag")
	assert.Contains(t, string(printed), "hostname: mail.example.com # detected")

	// The saved file is a configuration file again: it reloads to the same
	// values, with the unsaved secrets left to the next run.
	back := NewResolved(readFiles(map[string]string{"/s/root": "rootpw"}))
	require.NoError(t, back.ApplyYAML(saved))
	assert.Equal(t, "rootpw", back.Config.LDAP.RootPassword)
	assert.Empty(t, back.Config.Admin.Password)

	back.Config.Admin.Password = r.Config.Admin.Password
	back.Config.LDAP = r.Config.LDAP
	assert.Equal(t, r.Config, back.Config)
}

type fakeResolver struct{}

func (fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if host == "mail.example.com" {
		return []string{"127.0.1.1"}, nil
	}

	return nil, errors.New("no such host")
}

func (fakeResolver) LookupMX(context.Context, string) ([]*net.MX, error) {
	return nil, errors.New("no MX")
}

func runWizard(t *testing.T, r *Resolved, host Host, mode WizardMode, input string) (string, error) {
	t.Helper()

	var out strings.Builder

	w := &Wizard{In: bufio.NewReader(strings.NewReader(input)), Out: &out, Resolver: fakeResolver{}, Host: host}
	err := w.Run(context.Background(), r, mode)

	return out.String(), err
}

func TestWizard_AsksUnsetKeysAndFollowsAnswers(t *testing.T) {
	r := NewResolved(readFiles(nil))
	require.NoError(t, r.ApplyEnv([]string{"CARBONIO_BOOTSTRAP_HOST_PUBLIC_HOSTNAME=webmail.example.org"}))
	require.NoError(t, r.ApplyDefaults(singleNode))

	input := strings.Join([]string{
		"mx.example.org", // hostname
		"",               // ldap root password: generate
		"",               // ldap admin password: generate
		"",               // create domain: keep
		"",               // domain: keep the default, which follows the hostname
		"",               // create admin: keep
		"",               // admin account: keep
		"",               // admin password: required, asked again
		"abc",            // too short
		"abc",            // repeat
		"secret1",
		"secret2", // differs
		"secret1",
		"secret1",
		"",      // networks
		"",      // smtp host
		"ftp",   // proxy mode: invalid
		"https", // proxy mode
		"",      // start services
		"y",     // accept the summary
	}, "\n") + "\n"

	out, err := runWizard(t, r, singleNode, AskUnset, input)
	require.NoError(t, err, out)

	assert.NotContains(t, out, "Public hostname", "a key set by the environment is not asked")
	assert.Contains(t, out, "Warning: mx.example.org does not resolve in DNS.")
	assert.Contains(t, out, "admin.password is required.")
	assert.Contains(t, out, "at least 6 characters")
	assert.Contains(t, out, "The two entries differ.")
	assert.Contains(t, out, `"ftp" is not one of`)
	assert.NotContains(t, out, "secret1", "secrets are not echoed")

	c := r.Config
	assert.Equal(t, "mx.example.org", c.Host.Hostname)
	assert.Equal(t, "webmail.example.org", c.Host.PublicHostname)
	assert.Equal(t, "example.org", c.Domain.Name, "the domain default follows the new hostname")
	assert.Equal(t, "zextras@example.org", c.Admin.Account)
	assert.Equal(t, "secret1", c.Admin.Password)
	assert.NotEmpty(t, c.LDAP.RootPassword)
	assert.Equal(t, SourceWizard, r.Source("ldap.root_password"), "an accepted generated secret is kept")
	require.NoError(t, Validate(&c))
}

func TestWizard_DeclineAndEndOfInput(t *testing.T) {
	// hostname, public hostname, 2 LDAP passwords, domain x2, admin x2.
	answerAll := strings.Repeat("\n", 8) + "secret1\nsecret1\n" + strings.Repeat("\n", 4)

	r := NewResolved(readFiles(nil))
	require.NoError(t, r.ApplyDefaults(singleNode))

	_, err := runWizard(t, r, singleNode, AskUnset, answerAll+"n\n")
	require.ErrorIs(t, err, ErrAborted)

	r = NewResolved(readFiles(nil))
	require.NoError(t, r.ApplyDefaults(singleNode))

	_, err = runWizard(t, r, singleNode, AskUnset, "mail.example.com\n")
	require.ErrorContains(t, err, "input ended")
}

func TestWizard_AskAllIncludesExplicitKeys(t *testing.T) {
	r := NewResolved(readFiles(nil))
	require.NoError(t, r.ApplySets([]string{"host.hostname=set.example.com", "admin.password=secret1"}))
	require.NoError(t, r.ApplyDefaults(singleNode))

	out, err := runWizard(t, r, singleNode, AskAll, strings.Repeat("\n", 20)+"y\n")
	require.NoError(t, err, out)

	assert.Contains(t, out, "Hostname of this node [set.example.com]: ")
	assert.Contains(t, out, "Administrator password [Enter: keep]: ")
	assert.Equal(t, "set.example.com", r.Config.Host.Hostname)
	assert.Equal(t, SourceFlag, r.Source("host.hostname"), "Enter keeps the value and its source")
}
