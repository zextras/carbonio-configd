// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"errors"
	"fmt"
	"os"
	"time"
)

const (
	ldapPkgDirectory = "carbonio-directory-server"
	ldapPkgProxy     = "carbonio-proxy"

	ldapWaitSeconds = 30

	ldapTrue        = "true"
	ldapLCURL       = "ldap_url"
	ldapLCIsMaster  = "ldap_is_master"
	ldapLCMasterURL = "ldap_master_url"
	ldapService     = "ldap"
)

// ldapKit is what the LDAP steps reach outside the process: connections and
// the slapd service. ldapNativeKit is the production one; tests build their
// own.
type ldapKit struct {
	dial    func(e *Env, d ldapDial) (ldapConn, error)
	running func(e *Env) (bool, error)
	// start starts slapd and returns when it answers (setup.pl startLdap
	// plus, with systemd, waitForLdap).
	start func(e *Env) error
	stop  func(e *Env) error
}

// ldapNativeKit is the production kit.
func ldapNativeKit() *ldapKit {
	k := &ldapKit{dial: ldapNativeDial}

	k.running = func(e *Env) (bool, error) {
		return e.Ctl.ServiceStatus(e.Ctx, ldapService)
	}

	k.start = func(e *Env) error {
		if up, err := k.running(e); err == nil && up {
			return nil
		}

		if err := ldapControl(e, "start"); err != nil {
			return fmt.Errorf("starting ldap: %w", err)
		}

		e.Flags.LDAPStarted = true

		if e.Facts.Systemd {
			return k.waitFor(e, ldapWaitSeconds)
		}

		return nil
	}

	k.stop = func(e *Env) error {
		if err := ldapControl(e, "stop"); err != nil {
			return fmt.Errorf("stopping ldap: %w", err)
		}

		return nil
	}

	return k
}

// ldapControl starts or stops slapd like setup.pl startLdap/stopLdap: through
// systemd when the node runs the carbonio units, else "ldap start|stop" as
// zextras. Spawned by this root process, slapd would run as root, and the
// zextras-owned controls (zmcontrol, zmconfigdctl) could not stop it later.
func ldapControl(e *Env, verb string) error {
	if e.Facts.Systemd {
		if verb == "start" {
			return e.Ctl.ServiceStart(e.Ctx, ldapService)
		}

		return e.Ctl.ServiceStop(e.Ctx, ldapService)
	}

	return e.Zextras(e.Paths.Path("bin", ldapService), verb)
}

// waitFor is setup.pl waitForLdap: it polls the ldapi socket, binding as the
// cn=config root, until slapd answers or seconds have passed.
func (k *ldapKit) waitFor(e *Env, seconds int) error {
	var last error

	for i := range seconds {
		if last = k.probe(e); last == nil {
			return nil
		}

		if i == seconds-1 {
			break
		}

		if err := e.Sys.Clock.Sleep(e.Ctx, time.Second); err != nil {
			return err
		}
	}

	return fmt.Errorf("ldap did not answer within %ds: %w", seconds, last)
}

// probe binds once to the ldapi socket as the cn=config root.
func (k *ldapKit) probe(e *Env) error {
	pw, err := e.LCValue("ldap_root_password")
	if err != nil {
		return err
	}

	conn, err := k.dial(e, ldapDial{URLs: []string{ldapSocketURL}})
	if err != nil {
		return err
	}
	defer conn.Close()

	return conn.Bind(ldapConfigDN, pw)
}

// ldapStop stops slapd, ignoring a failure as setup.pl stopLdap did (the
// return code was never looked at); the failure is logged.
func (k *ldapKit) ldapStop(e *Env) {
	if err := k.stop(e); err != nil {
		e.Log.Detail("%v", err)
	}
}

// LDAPConfigured reports whether the local LDAP database exists: setup.pl
// $ldapConfigured (getSystemStatus, data.mdb present), evaluated on the node
// instead of being a variable that was only set after the configuration.
func (e *Env) LDAPConfigured() bool {
	_, err := e.Sys.FS.Stat(e.Paths.Path("data", "ldap", "mdb", "db", "data.mdb"))

	return err == nil
}

// ldapExists reports whether path exists.
func ldapExists(e *Env, path string) bool {
	_, err := e.Sys.FS.Stat(path)

	return err == nil
}

// ldapMissing reports whether path is certainly absent (a stat error other
// than "does not exist" is not).
func ldapMissing(e *Env, path string) bool {
	_, err := e.Sys.FS.Stat(path)

	return errors.Is(err, os.ErrNotExist)
}
