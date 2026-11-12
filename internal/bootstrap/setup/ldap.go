// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
)

const (
	ldapStageSetup = "configSetupLdap"
	ldapSchemaAttr = "zimbraLDAPSchemaVersion"

	// ldapSettingPasswordFmt is the detail line logged for each account
	// password set.
	ldapSettingPasswordFmt = "Setting %s password"
)

func ldapEnableReplicaFile(e *Env) string { return e.Paths.Path(".enable_replica") }

// ldapIsDirectory is setup.pl isEnabled("carbonio-directory-server").
func ldapIsDirectory(e *Env) bool { return e.Facts.IsEnabled(ldapPkgDirectory) }

// ldapOnMaster is setup.pl $config{LDAPHOST} eq $config{HOSTNAME}.
func ldapOnMaster(e *Env) bool { return e.Cfg.LDAP.Host == e.Cfg.Host.Hostname }

// ldapTouch creates path when it is not there (open ">>" and close).
func ldapTouch(e *Env, path string) error {
	f, err := e.Sys.FS.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}

	return f.Close()
}

// StepInstallLdapConfig is setup.pl installLdapConfig (L1305-1326), called
// from the main flow (L146-150): it puts the slapd cn=config tree of the
// package into data/ldap/config. Deviation: it is skipped when the tree is
// complete and the database exists, so a repeated run never reverts a
// cn=config that slapd has since changed.
func StepInstallLdapConfig() Step {
	return Step{
		Title: "Install the LDAP configuration database",
		When: func(e *Env) bool {
			return e.Facts.IsInstalled(ldapPkgDirectory) &&
				(e.Facts.NewInstall || ldapMissing(e, filepath.Join(ldapConfigDir(e), "cn=config.ldif"))) &&
				ldapExists(e, ldapConfigDir(e))
		},
		Done: func(e *Env) (bool, error) {
			return ldapInstalledConfig(e) && e.LDAPConfigured(), nil
		},
		Apply: ldapInstallConfig,
	}
}

// StepInstallLdapSchema is setup.pl installLdapSchema (L1328-1330, called at
// L152): zmldapschema as zextras. Optional: setup.pl discarded its result.
func StepInstallLdapSchema() Step {
	return Step{
		Title: "Install the LDAP schema",
		When:  func(e *Env) bool { return e.Facts.IsInstalled(ldapPkgDirectory) },
		// zmldapschema rebuilds the same schema files every time.
		Apply: func(e *Env) error {
			return e.Zextras(e.Paths.Path("libexec", "zmldapschema"))
		},
		Optional: true,
	}
}

// StepZmldapupdateldif is the zmldapupdateldif call of the main flow (setup.pl
// L194-197): on an existing install it brings the directory to the LDIF of the
// installed version. The tool does nothing unless ldap_is_master is true,
// which Done checks as the tool does. Optional: setup.pl ignored the result.
func StepZmldapupdateldif() Step {
	return Step{
		Title: "Update the LDAP directory",
		When:  func(e *Env) bool { return !e.Facts.NewInstall },
		Done: func(e *Env) (bool, error) {
			v, err := e.LCValue(ldapLCIsMaster)

			return !strings.EqualFold(v, "true"), err
		},
		Apply: func(e *Env) error {
			// The tool checks that it runs as root.
			return e.Root(e.Paths.Path("libexec", "zmldapupdateldif"))
		},
		Optional: true,
	}
}

// StepUpdatePasswordsInLocalConfig is setup.pl updatePasswordsInLocalConfig
// (L4542-4658), the first call of applyConfig. zmldappasswd is done in-process
// (the passwords never go on a command line).
//
// Deviations: $ldapConfigured is the real state of the node (setup.pl only set
// it after configuration, so the directory-server branch never ran) and, on a
// new install, the directory-server branch is left to configSetupLdap, which
// sets every account password on the new directory; "changed" is derived from
// the node (localconfig differs, or the directory refuses the password)
// instead of the $ldap*Changed globals; the stop-and-start of a directory
// whose passwords did not change is not done.
func StepUpdatePasswordsInLocalConfig() Step { return ldapStepUpdatePasswords(ldapNativeKit()) }

func ldapStepUpdatePasswords(k *ldapKit) Step {
	return Step{
		Title: "Update the LDAP passwords",
		When: func(e *Env) bool {
			if ldapIsDirectory(e) {
				return !e.Facts.NewInstall && e.LDAPConfigured()
			}

			return e.Facts.IsEnabled(PkgMTA) || e.Facts.IsEnabled(ldapPkgProxy)
		},
		Done: func(e *Env) (bool, error) {
			p, err := ldapPendingPasswords(e, k)

			return len(p) == 0, err
		},
		Apply: func(e *Env) error { return ldapApplyPasswords(e, k) },
	}
}

// ldapPasswordAccounts are the accounts updatePasswordsInLocalConfig looks at.
func ldapPasswordAccounts(e *Env) []ldapAccount {
	if ldapIsDirectory(e) {
		return ldapAllAccounts()
	}

	var accts []ldapAccount

	if e.Facts.IsEnabled(PkgMTA) {
		accts = append(accts, ldapAcctPostfix, ldapAcctAmavis)
	}

	if e.Facts.IsEnabled(ldapPkgProxy) {
		accts = append(accts, ldapAcctNginx)
	}

	return accts
}

// ldapPendingPasswords lists the accounts whose configured password is not
// what the node holds: localconfig differs or, on the directory master that
// is running, the directory does not accept it.
func ldapPendingPasswords(e *Env, k *ldapKit) ([]ldapAccount, error) {
	lc, err := e.LC()
	if err != nil {
		return nil, err
	}

	var pending []ldapAccount

	for _, a := range ldapPasswordAccounts(e) {
		want := a.get(&e.Cfg.LDAP)
		if want == "" {
			continue
		}

		if lc[a.LCKey] != want || ldapDirectoryRefuses(e, k, a, want) {
			pending = append(pending, a)
		}
	}

	return pending, nil
}

// ldapDirectoryRefuses reports whether the local directory, when it is the
// master and runs, does not accept the password of a.
func ldapDirectoryRefuses(e *Env, k *ldapKit, a ldapAccount, password string) bool {
	if !ldapIsDirectory(e) || (!a.root && !ldapOnMaster(e)) {
		return false
	}

	up, err := k.running(e)
	if err != nil || !up {
		return false
	}

	return !ldapPasswordWorks(e, k, a, password)
}

func ldapApplyPasswords(e *Env, k *ldapKit) error {
	pending, err := ldapPendingPasswords(e, k)
	if err != nil {
		return err
	}

	if ldapIsDirectory(e) && len(pending) > 0 {
		if err := k.start(e); err != nil {
			return err
		}
	}

	for _, a := range pending {
		e.Log.Detail(ldapSettingPasswordFmt, a.Name)

		pw := a.get(&e.Cfg.LDAP)

		if a.root || ldapOnMaster(e) {
			err = ldapSetPassword(e, k, a, pw)
		} else {
			err = e.SetLC(map[string]string{a.LCKey: pw})
		}

		if err != nil {
			return fmt.Errorf("setting the %s password: %w", a.Name, err)
		}
	}

	return nil
}

// StepConfigSetupLdap is setup.pl configSetupLdap (L4660-4837), with
// zmldappasswd, preLdapStart, postLdapStart and zmldapapplyldif done in-process
// (ldapinit.pm, bin/ldappasswd.pl, libexec/ldapapplyldif.pl). Its three
// branches, as in setup.pl:
//
//   - directory server, new install, LDAPHOST is this host and no
//     .enable_replica: initialise a new master (stop slapd, write the root
//     and admin passwords, start, load the base entries, set the account
//     passwords, FORCEREPLICATION);
//   - any other directory server: apply the LDIF (zmldapapplyldif on a new
//     install, zmldapupdateldif otherwise) and, when LDAPHOST is another host
//     or .enable_replica exists, enable the replica or the multi-master peer;
//   - not a directory server: only the passwords go to localconfig.
//
// Done is the stage mark: setup.pl returned at the top when configSetupLdap
// was already CONFIGURED.
//
// Deviations: the master initialisation does not depend on $ldapConfigured
// (always 0 in setup.pl at that point) and tolerates entries that exist, so
// it can be repeated after a failure; on a new directory the localconfig
// passwords are written before zmldapapplyldif (setup.pl wrote them after, so
// the root password of a replica was the localconfig default); zmldapupdateldif
// runs as root (setup.pl ran it as zextras, where it refuses to run); empty
// passwords are never written to localconfig. When a replica was enabled, the
// admin and domain of the configuration are no longer created: Cfg.Admin.Create
// is cleared unless carbonio-appserver is enabled, Cfg.Domain.Create always
// (setup.pl DOCREATEADMIN / DOCREATEDOMAIN = "no"), which the steps that
// create them must honour. The failure of the replica enabling disables
// directory-server on this host and goes on, as setup.pl did.
func StepConfigSetupLdap() Step { return ldapStepConfigSetupLdap(ldapNativeKit()) }

func ldapStepConfigSetupLdap(k *ldapKit) Step {
	return Step{
		Name:  ldapStageSetup,
		Title: "Configure LDAP",
		Done: func(e *Env) (bool, error) {
			return e.Facts.Configured[ldapStageSetup], nil
		},
		Apply: func(e *Env) error {
			switch {
			case !ldapIsDirectory(e):
				return ldapConfigRemote(e)
			case ldapIsNewMaster(e):
				return ldapConfigNewMaster(e, k)
			default:
				return ldapConfigDirectory(e, k)
			}
		},
	}
}

// ldapIsNewMaster is the first branch of configSetupLdap.
func ldapIsNewMaster(e *Env) bool {
	return e.Facts.NewInstall && ldapOnMaster(e) && !ldapExists(e, ldapEnableReplicaFile(e))
}

// ldapConfigRemote is the last branch of configSetupLdap: this node only
// holds the passwords of a directory elsewhere.
func ldapConfigRemote(e *Env) error {
	e.Log.Detail("Updating ldap user passwords")

	return ldapSetLC(e, ldapAllAccounts())
}

// ldapSetLC writes the configured password of each account to localconfig,
// skipping the ones the configuration does not give.
func ldapSetLC(e *Env, accts []ldapAccount) error {
	pairs := map[string]string{}

	for _, a := range accts {
		if pw := a.get(&e.Cfg.LDAP); pw != "" {
			pairs[a.LCKey] = pw
		}
	}

	return e.SetLC(pairs)
}

// ldapConfigNewMaster is the first branch of configSetupLdap.
func ldapConfigNewMaster(e *Env, k *ldapKit) error {
	root, admin := e.Cfg.LDAP.RootPassword, e.Cfg.LDAP.AdminPassword
	if root == "" || admin == "" {
		return fmt.Errorf("the LDAP root and admin passwords are required to initialise the directory")
	}

	k.ldapStop(e)

	if err := ldapPreStart(e, root, admin); err != nil {
		return fmt.Errorf("preLdapStart: %w", err)
	}

	if err := k.start(e); err != nil {
		return err
	}

	if rc, err := ldapPostStart(e, k); err != nil {
		return fmt.Errorf("initializing ldap failed (%d): %w", rc, err)
	}

	for _, a := range []ldapAccount{ldapAcctReplica, ldapAcctPostfix, ldapAcctAmavis, ldapAcctNginx} {
		pw := a.get(&e.Cfg.LDAP)
		if pw == "" {
			continue
		}

		e.Log.Detail(ldapSettingPasswordFmt, a.Name)

		if err := ldapSetPassword(e, k, a, pw); err != nil {
			return fmt.Errorf("setting the %s password: %w", a.Name, err)
		}
	}

	if e.Cfg.LDAP.ForceReplication {
		// setup.pl ignored the result and created the marker anyway.
		if err := e.Root(e.Paths.Path("libexec", "zmldapenablereplica")); err != nil {
			e.Log.Warn("zmldapenablereplica: %v", err)
		}

		return ldapTouch(e, ldapEnableReplicaFile(e))
	}

	return nil
}

// ldapConfigDirectory is the second branch of configSetupLdap.
func ldapConfigDirectory(e *Env, k *ldapKit) error {
	remote := !ldapOnMaster(e) || ldapExists(e, ldapEnableReplicaFile(e))

	// stale is the result setup.pl still holds, and tests, when the replica
	// was enabled before this run.
	stale, err := ldapApplyDirectoryLDIF(e, remote)
	if err != nil || !remote {
		return err
	}

	return ldapEnableRemote(e, k, stale)
}

// ldapApplyDirectoryLDIF loads the LDIF of the directory (zmldapapplyldif on
// a new install, zmldapupdateldif otherwise) and, on a node that follows
// another master, writes the passwords to localconfig.
func ldapApplyDirectoryLDIF(e *Env, remote bool) (stale, err error) {
	accts := []ldapAccount{ldapAcctRoot, ldapAcctAdmin, ldapAcctReplica}

	if e.Facts.NewInstall {
		if remote {
			if err := ldapSetLC(e, accts); err != nil {
				return nil, err
			}
		}

		if err := ldapApplyLDIF(e); err != nil {
			return nil, fmt.Errorf("zmldapapplyldif: %w", err)
		}

		return nil, nil
	}

	if stale = e.Root(e.Paths.Path("libexec", "zmldapupdateldif")); stale != nil {
		e.Log.Warn("zmldapupdateldif: %v", stale)
	}

	if remote {
		return stale, ldapSetLC(e, accts)
	}

	return stale, nil
}

// ldapEnableRemote enables replication on a node that follows another master
// and restarts slapd with it.
func ldapEnableRemote(e *Env, k *ldapKit, rc error) error {
	mmr := e.Facts.NewInstall && e.Cfg.LDAP.ReplicationType == bootstrap.ReplicationMMR
	if mmr {
		for _, a := range []ldapAccount{ldapAcctPostfix, ldapAcctAmavis, ldapAcctNginx} {
			if err := ldapSetAppPassword(e, k, a); err != nil {
				return err
			}
		}
	}

	if !ldapExists(e, ldapEnableReplicaFile(e)) {
		rc = ldapEnableReplication(e, k, mmr)

		if err := ldapTouch(e, ldapEnableReplicaFile(e)); err != nil {
			return err
		}
	}

	if rc != nil {
		return ldapReplicationFailed(e, k, rc)
	}

	if !e.Facts.IsEnabled(PkgAppserver) {
		e.Cfg.Admin.Create = false
	}

	e.Cfg.Domain.Create = false

	k.ldapStop(e)

	return k.start(e)
}

// ldapSetAppPassword sets the password of an application account when the
// configuration gives one.
func ldapSetAppPassword(e *Env, k *ldapKit, a ldapAccount) error {
	pw := a.get(&e.Cfg.LDAP)
	if pw == "" {
		return nil
	}

	e.Log.Detail(ldapSettingPasswordFmt, a.Name)

	if err := ldapSetPassword(e, k, a, pw); err != nil {
		return fmt.Errorf("setting the %s password: %w", a.Name, err)
	}

	return nil
}

// ldapEnableReplication enables this directory as a replica of LDAPHOST, or
// as a multi-master peer of it, and returns the result of the tool.
func ldapEnableReplication(e *Env, k *ldapKit, mmr bool) error {
	if !mmr {
		return e.Root(e.Paths.Path("libexec", "zmldapenablereplica"))
	}

	master, err := e.LCValue(ldapLCMasterURL)
	if err != nil {
		return err
	}

	proto := "ldap"
	if e.Cfg.LDAP.Port == 636 {
		proto = "ldaps"
	}

	err = e.SetLC(map[string]string{
		ldapLCIsMaster: ldapTrue,
		ldapLCURL:      fmt.Sprintf("%s://%s:%d %s", proto, e.Cfg.Host.Hostname, e.Cfg.LDAP.Port, master),
	})
	if err != nil {
		return err
	}

	if !strings.HasSuffix(master, "/") {
		master += "/"
	}

	if err := k.start(e); err != nil {
		return err
	}

	return e.Zextras(e.Paths.Path("libexec", "zmldapenable-mmr"),
		"-s", strconv.Itoa(e.Cfg.LDAP.ServerID), "-m", master)
}

// ldapReplicationFailed is the failure branch of configSetupLdap: the
// operator is told to enable replication by hand, directory-server is
// disabled on this host and slapd is stopped. The run goes on, as it did.
func ldapReplicationFailed(e *Env, k *ldapKit, cause error) error {
	e.Log.Warn("enabling ldap replication failed: %v", cause)
	e.Log.Info("You will have to correct the problem and manually enable replication.")

	if err := e.Prov("ms", e.Cfg.Host.Hostname, "-zimbraServiceEnabled", "directory-server"); err != nil {
		e.Log.Warn("disabling ldap on %s failed: %v", e.Cfg.Host.Hostname, err)
	}

	k.ldapStop(e)

	return nil
}

// StepConfigLDAPSchemaVersion is setup.pl configLDAPSchemaVersion
// (L4839-4851): it records the schema version of the package
// (conf/attrs-schema) in the global config. Optional: setup.pl printed
// "failed." and went on. Done reads the global config, which replaces the
// $haveSetLdapSchemaVersion flag.
func StepConfigLDAPSchemaVersion() Step {
	return Step{
		Title: "Update the LDAP schema version",
		When:  ldapIsDirectory,
		Done: func(e *Env) (bool, error) {
			want, err := ldapSchemaVersion(e)
			if err != nil {
				return false, err
			}

			client, err := e.LDAP()
			if err != nil {
				return false, nil //nolint:nilerr // not reachable: Apply reports it
			}
			defer func() { _ = client.Close() }()

			cfg, err := client.GetGlobalConfig()

			return err == nil && cfg[ldapSchemaAttr] == want, nil
		},
		Apply: func(e *Env) error {
			v, err := ldapSchemaVersion(e)
			if err != nil {
				return err
			}

			return e.Prov("mcf", ldapSchemaAttr, v)
		},
		Optional: true,
	}
}

// ldapSchemaVersion is $ldapSchemaVersion of setup.pl (L100-108).
func ldapSchemaVersion(e *Env) (string, error) {
	path := e.Paths.Path("conf", "attrs-schema")

	data, err := e.Sys.FS.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("could not open %s: %w", path, err)
	}

	return strings.TrimSuffix(string(data), "\n"), nil
}

// StepStartLdap is the startLdap of applyConfig (setup.pl L5962):
// `startLdap() if (!$newinstall && $ldapConfigured)`, so that the directory
// of an existing install is up before the servers start. Optional: setup.pl
// ignored the result. $ldapConfigured is the real state of the node (see
// StepUpdatePasswordsInLocalConfig).
func StepStartLdap() Step { return ldapStepStartLdap(ldapNativeKit()) }

func ldapStepStartLdap(k *ldapKit) Step {
	return Step{
		Title: "Start LDAP",
		When:  func(e *Env) bool { return !e.Facts.NewInstall && e.LDAPConfigured() },
		Done: func(e *Env) (bool, error) {
			up, err := k.running(e)

			return err == nil && up, nil
		},
		Apply:    k.start,
		Optional: true,
	}
}
