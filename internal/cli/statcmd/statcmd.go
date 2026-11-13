// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package statcmd is the command line of the statistics collectors,
// "configd stat <name> [options]" and "configd stat run <collector>...": it
// builds the Env of internal/stat from the host (localconfig, LDAP, statfs, the
// users) and runs the collectors. It is apart from the bootstrap packages so
// the configd binary does not link them.
package statcmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	goldap "github.com/go-ldap/ldap/v3"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/stat"
	"github.com/zextras/carbonio-configd/internal/sys"
)

const (
	// statsPidDir is where the collectors keep their pid files.
	statsPidDir = "/run/carbonio/stats"
	// slapdSocketURL is the ldapi:// URL of the local slapd.
	slapdSocketURL = "ldapi://%2Frun%2Fcarbonio%2Frun%2Fldapi/"

	slapdTimeout = 30 * time.Second
)

// runStat is the collector runner, replaced by tests.
var runStat = stat.Run

// Run executes "stat <name> [options]": the zmstat-<name>
// collector of carbonio-core, with the options of the script, or "stat
// run <collector>...": several of them in this process. It runs until
// SIGTERM or SIGINT; the exit status is the script's. Unlike the other tools
// it must run as the zimbra user (as Zextras::Mon::Stat userCheck), root only
// for "fd".
func Run(ctx context.Context, env sys.System, stdout, stderr io.Writer, args []string) int {
	if len(args) > 0 && args[0] == "run" {
		stat.TuneMemory()
	}

	return runStat(ctx, statEnv(env, stdout, stderr), args)
}

// statEnv is the process the collectors run in.
func statEnv(env sys.System, stdout, stderr io.Writer) *stat.Env {
	lc, err := localconfig.LoadEffective()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Warning: cannot read localconfig: %v\n", err)

		lc = map[string]string{}
	}

	uid := os.Geteuid()
	base := config.BaseDir()
	e := &stat.Env{
		Sys: env, Root: os.DirFS("/"), Statfs: statfs, LC: lc,
		Pid: os.Getpid(), Base: base, Dir: filepath.Join(base, "zmstat"), PidDir: statsPidDir,
		Stdout: stdout, Stderr: stderr, Syslog: stat.NewSystemSyslog(os.Getpid()),
		UserName: userName,
		Enabled:  func(ctx context.Context, host string) ([]string, error) { return enabledServices(ctx, lc, host) },
		Monitor:  func(ctx context.Context, q []stat.MonitorQuery) ([]string, error) { return slapdMonitor(ctx, lc, q) },
	}

	e.User = userName(uid)

	if uid == 0 {
		e.Chown = chownToZextras
	}

	return e
}

// userName is the login name of a uid, "" when it has none.
func userName(uid int) string {
	u, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		return ""
	}

	return u.Username
}

// chownToZextras gives a file or directory to zextras (the collectors that run
// as root create files the zextras collectors and tools must read).
func chownToZextras(path string) error {
	u, err := user.Lookup(config.ZextrasUser)
	if err != nil {
		return fmt.Errorf("look up %s: %w", config.ZextrasUser, err)
	}

	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return fmt.Errorf("uid of %s: %w", config.ZextrasUser, err)
	}

	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return fmt.Errorf("gid of %s: %w", config.ZextrasUser, err)
	}

	return os.Chown(path, uid, gid) //nolint:wrapcheck // plain syscall error
}

// statfs is statfs(2).
func statfs(path string) (stat.Statfs, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return stat.Statfs{}, fmt.Errorf("statfs %s: %w", path, err)
	}

	size := st.Frsize
	if size == 0 {
		size = st.Bsize
	}

	//nolint:gosec // a block size is positive
	return stat.Statfs{Blocks: st.Blocks, Bfree: st.Bfree, Bavail: st.Bavail, Bsize: uint64(size)}, nil
}

// enabledServices is zimbraServiceEnabled of a server, from LDAP.
func enabledServices(ctx context.Context, lc map[string]string, host string) ([]string, error) {
	cfg, err := ldap.ParamsFrom(ctx, lc, ldap.PurposeRead, nil)
	if err != nil {
		return nil, fmt.Errorf("ldap parameters: %w", err)
	}

	client, err := ldap.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("ldap client: %w", err)
	}

	defer func() { _ = client.Close() }()

	return client.GetEnabledServices(host)
}

// slapdMonitor reads cn=Monitor over the ldapi socket, bound as the zimbra
// user with the password of the localconfig (which stays off every command
// line): one attribute per query.
func slapdMonitor(ctx context.Context, lc map[string]string, queries []stat.MonitorQuery) ([]string, error) {
	conn, err := goldap.DialURL(slapdSocketURL, goldap.DialWithDialer(&net.Dialer{Timeout: slapdTimeout}))
	if err != nil {
		return nil, fmt.Errorf("connect to slapd: %w", err)
	}

	defer func() { _ = conn.Close() }()

	conn.SetTimeout(slapdTimeout)

	if lc["zimbra_ldap_password"] == "" {
		return nil, fmt.Errorf("%w: no zimbra_ldap_password", stat.ErrBind)
	}

	if err := conn.Bind(lc["zimbra_ldap_userdn"], lc["zimbra_ldap_password"]); err != nil {
		return nil, fmt.Errorf("%w: %w", stat.ErrBind, err)
	}

	out := make([]string, len(queries))

	for i, q := range queries {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("monitor: %w", err)
		}

		res, err := conn.Search(goldap.NewSearchRequest(q.DN, goldap.ScopeBaseObject, goldap.NeverDerefAliases,
			0, 0, false, "(objectClass=*)", []string{q.Attr}, nil))
		if err != nil || len(res.Entries) == 0 {
			continue // an entry slapd does not have reads empty
		}

		out[i] = res.Entries[0].GetAttributeValue(q.Attr)
	}

	return out, nil
}
