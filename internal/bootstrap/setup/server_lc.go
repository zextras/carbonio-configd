// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"cmp"
	"crypto/rand"
	"fmt"
	"maps"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/localconfig"
)

// The localconfig keys the steps of this file write more than once.
const (
	srvLCKeystore         = "mailboxd_keystore"
	srvLCKeystorePassword = "mailboxd_keystore_password"
	srvLCTrustPassword    = "mailboxd_truststore_password"
	srvLCLDAPStartTLS     = "ldap_starttls_supported"
	srvDefaultBaseDN      = "cn=zimbra"
	srvTrueLower          = "true"
	srvTrueUpper          = "TRUE"
	srvFalseUpper         = "FALSE"
)

// srvLCFile is the localconfig.xml as stored (no defaults): what zmlocalconfig
// -e compares with.
func srvLCFile(e *Env) map[string]string {
	lc, err := localconfig.LoadLocalConfigFromFile(e.Paths.LocalConfig)
	if err != nil {
		return map[string]string{}
	}

	return lc
}

// srvLCMatches reports whether every pair is stored as given.
func srvLCMatches(e *Env, want map[string]string) bool {
	have := srvLCFile(e)

	for k, v := range want {
		if got, ok := have[k]; !ok || got != v {
			return false
		}
	}

	return true
}

func srvBool01(b bool) string {
	if b {
		return "1"
	}

	return "0"
}

// srvBoolUpper is the zmprov spelling of a boolean attribute.
func srvBoolUpper(b bool) string {
	if b {
		return srvTrueUpper
	}

	return srvFalseUpper
}

// srvRandomPassword is a random alphanumeric password (setup.pl genRandomPass
// ran com.zimbra.common.util.RandomPassword).
func srvRandomPassword() (string, error) {
	const (
		alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
		length   = 10
	)

	out := make([]byte, length)

	for i := range out {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", fmt.Errorf("random password: %w", err)
		}

		out[i] = alphabet[n.Int64()]
	}

	return string(out), nil
}

func srvDirExists(e *Env, path string) bool {
	fi, err := e.Sys.FS.Stat(path)

	return err == nil && fi.IsDir()
}

// srvMailboxdLC are the two keys setup.pl rewrites at every run, ahead of the
// configLCValues stage ("default values rather than loaded from previous
// installs"): the keystore, and the server when the jetty directory exists.
func srvMailboxdLC(e *Env) map[string]string {
	dir := e.Paths.Path("mailboxd")
	if srvDirExists(e, dir) {
		return map[string]string{srvLCKeystore: dir + "/etc/keystore", "mailboxd_server": "jetty"}
	}

	return map[string]string{srvLCKeystore: e.Paths.Path("conf", "keystore")}
}

// StepConfigLCMailboxd ports the head of setup.pl configLCValues (4392-4399),
// which sets mailboxd_keystore and mailboxd_server on every run of an
// appserver node, even when the configLCValues stage is already CONFIGURED.
//
// When: isEnabled(carbonio-appserver). Deviation: mailboxd_server is only
// written when the jetty directory exists (setup.pl wrote an empty value).
func StepConfigLCMailboxd() Step {
	return Step{
		Title: "Setting mailboxd local config values",
		When:  func(e *Env) bool { return e.Facts.IsEnabled(PkgAppserver) },
		Done: func(e *Env) (bool, error) {
			return srvLCMatches(e, srvMailboxdLC(e)), nil
		},
		Apply: func(e *Env) error { return e.SetLC(srvMailboxdLC(e)) },
	}
}

// srvMailboxdMemoryMB is the mailboxd heap: Config.Store.MailboxdMemoryMB, or
// the share of the system memory setup.pl mailboxdMemoryMB computed.
func srvMailboxdMemoryMB(e *Env) (int, bool) {
	if mb := e.Cfg.Store.MailboxdMemoryMB; mb > 0 {
		return mb, true
	}

	data, err := e.Sys.FS.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, false
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		rest, ok := strings.CutPrefix(line, "MemTotal:")
		if !ok {
			continue
		}

		f := strings.Fields(rest)
		if len(f) == 0 {
			return 0, false
		}

		kb, err := strconv.ParseFloat(f[0], 64)
		if err != nil {
			return 0, false
		}

		// setup.pl rounds the memory to 0.1 GB (sprintf "%0.1f") first.
		gb := math.Round(kb/(1024*1024)*10) / 10
		share := 0.25

		if gb > 16 {
			share = 0.2
		}

		return int(share * gb * 1024), true
	}

	return 0, false
}

// srvZextrasID is `id <flag> zextras`.
func srvZextrasID(e *Env, flag string) (string, error) {
	out, err := e.Output(&Cmd{Path: "id", Args: []string{flag, config.ZextrasUser}})
	if err != nil {
		return "", fmt.Errorf("id %s %s: %w", flag, config.ZextrasUser, err)
	}

	return strings.TrimSpace(string(out)), nil
}

// srvLDAPURLs are the ldap_master_url, ldap_url and ldap_starttls_supported
// values of a new install.
func srvLDAPURLs(c *bootstrap.Config) map[string]string {
	if c.LDAP.Port == 636 {
		url := fmt.Sprintf("ldaps://%s:%d", c.LDAP.Host, c.LDAP.Port)

		return map[string]string{"ldap_master_url": url, "ldap_url": url, srvLCLDAPStartTLS: "0"}
	}

	url := fmt.Sprintf("ldap://%s:%d", c.LDAP.Host, c.LDAP.Port)

	return map[string]string{
		"ldap_master_url": url, "ldap_url": url,
		srvLCLDAPStartTLS: srvBool01(c.Host.RequireInterprocessSecurity),
	}
}

// srvAVDomain is setup.pl AVDOMAIN: the domain of the antivirus notification
// address, else the default domain; an existing install keeps its value.
func srvAVDomain(e *Env, file map[string]string) string {
	if !e.Facts.NewInstall && file["av_notify_domain"] != "" {
		return file["av_notify_domain"]
	}

	if _, d, ok := strings.Cut(e.Cfg.Notifications.AVNotifyUser, "@"); ok && d != "" {
		return d
	}

	return e.Cfg.Domain.Name
}

// srvLCValues are the localconfig keys of configLCValues. A value of "" is a
// key whose value must be generated (the keystore password of a node that has
// none yet).
func srvLCValues(e *Env) map[string]string {
	c := e.Cfg
	file := srvLCFile(e)

	want := map[string]string{
		"zimbra_server_hostname":               strings.ToLower(c.Host.Hostname),
		"zimbra_require_interprocess_security": srvBool01(c.Host.RequireInterprocessSecurity),
		"zimbra_zmprov_default_to_ldap":        srvTrueLower,
		"ldap_port":                            strconv.Itoa(c.LDAP.Port),
		"ldap_host":                            c.LDAP.Host,
		"zimbra_user":                          config.ZextrasUser,
		"zimbra_java_home":                     e.Paths.Path("common", "lib", "jvm", "java"),
		"ssl_default_digest":                   cmp.Or(c.Host.SSLDigest, "sha256"),
		"mailboxd_directory":                   e.Paths.Path("mailboxd"),
		"mailboxd_truststore":                  e.Paths.Path("common", "lib", "jvm", "java", "lib", "security", "cacerts"),
		srvLCKeystorePassword:                  file[srvLCKeystorePassword],
		srvLCTrustPassword:                     cmp.Or(file[srvLCTrustPassword], "changeit"),
		"zimbra_ldap_userdn":                   cmp.Or(file["zimbra_ldap_userdn"], "uid=zimbra,cn=admins,"+srvBaseDN(c)),
	}

	maps.Copy(want, srvMailboxdLC(e))

	if e.Facts.IsEnabled(PkgAppserver) && e.Facts.ServiceWebapp {
		want["zimbra_zmprov_default_to_ldap"] = "false"
	}

	if e.Facts.NewInstall {
		maps.Copy(want, srvLDAPURLs(c))

		want["ssl_allow_untrusted_certs"] = srvTrueLower
		want["ssl_allow_mismatched_certs"] = srvTrueLower
	}

	if mb, ok := srvMailboxdMemoryMB(e); ok {
		want["mailboxd_java_heap_size"] = strconv.Itoa(mb)
	}

	if c.Notifications.AVNotifyUser != "" {
		want["av_notify_user"] = c.Notifications.AVNotifyUser
	}

	if d := srvAVDomain(e, file); d != "" {
		want["av_notify_domain"] = d
	}

	if base := srvBaseDN(c); base != srvDefaultBaseDN {
		want["ldap_dit_base_dn_config"] = base
	}

	return want
}

func srvBaseDN(c *bootstrap.Config) string { return cmp.Or(c.LDAP.BaseDN, srvDefaultBaseDN) }

// StepConfigLCValues ports setup.pl configLCValues (4392-4483): the
// localconfig keys of the node: host name, interprocess security, LDAP
// location (new install), zmprov default, zextras uid/gid, notification
// address, SSL digest, mailboxd directory/keystore/heap, and the LDAP bind
// DN and base DN. The mailboxd heap comes from Config.Store.MailboxdMemoryMB.
//
// Name: configLCValues. When: always. The keystore and truststore passwords
// are kept when localconfig already has them, else generated (setup.pl
// regenerated the keystore one at every run and kept the stored one only on
// an existing install). The MySQL memory percentage is not a localconfig key
// of this stage: it is passed to zmmtainit by configInitSql.
func StepConfigLCValues() Step {
	return Step{
		Name:  "configLCValues",
		Title: "Setting local config values",
		Done: func(e *Env) (bool, error) {
			want := srvLCValues(e)

			if want[srvLCKeystorePassword] == "" {
				return false, nil
			}

			uid, err := srvZextrasID(e, "-u")
			if err != nil {
				return false, err
			}

			gid, err := srvZextrasID(e, "-g")
			if err != nil {
				return false, err
			}

			want["zimbra_uid"], want["zimbra_gid"] = uid, gid

			return srvLCMatches(e, want), nil
		},
		Apply: func(e *Env) error {
			want := srvLCValues(e)

			var err error

			if want[srvLCKeystorePassword] == "" {
				if want[srvLCKeystorePassword], err = srvRandomPassword(); err != nil {
					return err
				}
			}

			if want["zimbra_uid"], err = srvZextrasID(e, "-u"); err != nil {
				return err
			}

			if want["zimbra_gid"], err = srvZextrasID(e, "-g"); err != nil {
				return err
			}

			return e.SetLC(want)
		},
	}
}
