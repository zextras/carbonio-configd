// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"

	goldap "github.com/go-ldap/ldap/v3"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// certDirectory is the LDAP access of zmcertmgr: the client of the node.
type certDirectory interface {
	GetGlobalConfig() (map[string]string, error)
	GetServerConfig(hostname string) (map[string]string, error)
	GetAllServers() ([]string, error)
	BaseDN() string
	ModifyValues(dn string, add, del, replace map[string][]string) error
	Close() error
}

// The attributes zmcertmgr keeps in LDAP.
const (
	certAttrCAKey   = "zimbraCertAuthorityKeySelfSigned"
	certAttrCACert  = "zimbraCertAuthorityCertSelfSigned"
	certAttrSSLCert = "zimbraSSLCertificate"
	certAttrSSLKey  = "zimbraSSLPrivateKey"
)

// certAttrMax are the attributes with a length limit, which zmprov enforces
// (max="2048" in zimbra-attrs.xml).
var certAttrMax = map[string]int{certAttrCAKey: 2048, certAttrCACert: 2048}

// certScopeGlobal and certScopeServer are the places getConfKey and
// saveConfKey take: the global config, or the entry of the server ("server":
// this one).
const (
	certScopeGlobal = "global"
	certScopeServer = "server"
)

// directory opens the directory once per run.
func (m *certMgr) directory() (certDirectory, error) {
	if m.dirOpened {
		return m.dir, m.dirErr
	}

	m.dirOpened = true

	if m.e.certDir != nil {
		m.dir, m.dirErr = m.e.certDir(m.e)

		return m.dir, m.dirErr
	}

	c, err := m.e.ldapClient(m.ldapContext())
	if err != nil {
		m.dirErr = err

		return nil, err
	}

	m.dir = c

	return m.dir, nil
}

// ldapContext is the lifecycle context of the LDAP client. The script only
// prints "failed" when LDAP is down (as it is early in setup.pl, before slapd
// starts): the warnings of the client's failed attempts are shown with -debug
// only.
func (m *certMgr) ldapContext() context.Context {
	ctx := m.e.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	if m.o.Debug > 0 {
		return ctx
	}

	return logger.ContextWithLogger(ctx, slog.New(slog.DiscardHandler))
}

// serverHost is the server a scope names.
func (m *certMgr) serverHost(loc string) string {
	if loc == certScopeServer {
		return m.lc["zimbra_server_hostname"]
	}

	return loc
}

// getConfKey is getConfKey: the value of a global config or server
// attribute. ok is false when the directory cannot be read (the script's
// undef); an attribute that is not set is "" with ok.
func (m *certMgr) getConfKey(key, loc string) (val string, ok bool) {
	m.debugf("getting '%s' via zmprov %s\n", key, loc)

	d, err := m.directory()
	if err != nil {
		return "", false
	}

	var attrs map[string]string

	if loc == certScopeGlobal {
		attrs, err = d.GetGlobalConfig()
	} else {
		attrs, err = d.GetServerConfig(m.serverHost(loc))
	}

	if err != nil {
		return "", false
	}

	for k, v := range attrs {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}

	return "", true
}

// saveConfKey is saveConfKey: the content of file becomes the value of the
// attribute of the global config or of the server.
func (m *certMgr) saveConfKey(key, loc, file string) bool {
	if !m.checkFiles(file) {
		return false
	}

	verb := "modifyConfig"
	if loc != certScopeGlobal {
		verb = "modifyServer " + m.serverHost(loc)
	}

	m.say("** Saving config key '%s' via zmprov %s...", key, verb)

	data, err := m.slurp(file)
	if err == nil {
		err = m.modifyAttr(key, loc, string(data))
	}

	if err != nil {
		m.say("failed (rc=1)\n")

		return false
	}

	m.say("ok\n")

	return true
}

var errCertAttrTooLong = errors.New("value too long")

// modifyAttr sets (or, for an empty value, unsets) an attribute.
func (m *certMgr) modifyAttr(key, loc, value string) error {
	if limit, ok := certAttrMax[key]; ok && len(value) > limit {
		return errCertAttrTooLong
	}

	d, err := m.directory()
	if err != nil {
		return err
	}

	dn := "cn=config," + d.BaseDN()
	if loc != certScopeGlobal {
		dn = "cn=" + goldap.EscapeDN(m.serverHost(loc)) + ",cn=servers," + d.BaseDN()
	}

	if value == "" {
		return d.ModifyValues(dn, nil, map[string][]string{key: nil}, nil)
	}

	return d.ModifyValues(dn, nil, nil, map[string][]string{key: {value}})
}

// allServers is `zmprov -m -l gas`: the servers of the directory, sorted; an
// unreadable directory gives none.
func (m *certMgr) allServers() []string {
	d, err := m.directory()
	if err != nil {
		return nil
	}

	servers, err := d.GetAllServers()
	if err != nil {
		return nil
	}

	servers = slices.Clone(servers)
	slices.Sort(servers)

	return servers
}

// remote runs a zmcertmgr command on another server through zmrc (the script
// piped "HOST:<server> <command>" into libexec/zmrc <server>): a failure is
// reported and false.
func (m *certMgr) remote(server, command string) bool {
	out, err := m.e.Output(&Cmd{
		AsZextras: true,
		Path:      m.e.Paths.Path("libexec", "zmrc"),
		Args:      []string{server},
		Stdin:     strings.NewReader("HOST:" + server + " " + command + "\n"),
	})
	if err == nil {
		return true
	}

	status := 1

	if ce, ok := errors.AsType[*CmdError](err); ok {
		status = ce.Code
	}

	m.say("ERROR: zmrc: %s failed(%d):\n%s", command, status, out)

	return false
}
