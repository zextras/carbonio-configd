// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

// defaultRunDir is the runtime directory holding pidfiles, the stats pid
// bundle and the sd_notify sockets.
const defaultRunDir = "/run/carbonio"

// paths is every absolute location the services layer touches, derived once
// from the install base directory (and the runtime directory). A Controller
// owns one; nothing here is mutable package state.
type paths struct {
	base     string
	bin      string
	conf     string
	common   string
	data     string
	lib      string
	log      string
	mailbox  string
	mailboxd string
	store    string
	libexec  string

	// run is the runtime directory (pidfiles, stats bundle, notify sockets).
	run string
	// statsPidDir holds the per-collector pidfiles of the stats service.
	statsPidDir string
	// statsGlob is the identity of every zmstat-* collector: the script path
	// under libexec statsCustomStart launches (perl scripts, so it is an argv
	// token of `perl -w <script>`). A glob over the whole path, so
	// `tail -f .../log/zmstat-proc.out` is not mistaken for a collector.
	statsGlob string

	cacheFile      string
	advancedJARDir string
	configrewrite  string

	milterOptions    string
	cbpolicydDB      string
	cbpolicydInitBin string
	clamdDir         string

	mysqldSafeBin      string
	mysqldSafeLedir    string
	mysqldMallocLib    string
	appserverDBPidFile string
	antispamDBPidFile  string

	postfixBin   string
	postconfBin  string
	postaliasBin string
	mainCf       string
	mtaPidFile   string
	// aliases is the system-wide postfix aliases source, outside the Carbonio
	// tree.
	aliases  string
	mtaSpool string
}

// newPaths derives the production layout under base, with runtime files in
// run.
func newPaths(base, run string) paths {
	p := paths{
		base:     base,
		bin:      base + "/bin",
		conf:     base + "/conf",
		common:   base + "/common",
		data:     base + "/data",
		lib:      base + "/lib",
		log:      base + "/log",
		mailbox:  base + "/mailbox",
		mailboxd: base + "/mailboxd",
		store:    base + "/store",
		libexec:  base + "/libexec",
		run:      run,
	}

	p.statsPidDir = run + "/stats"
	p.statsGlob = p.libexec + "/zmstat-*"

	p.cacheFile = p.log + "/.zmcontrol.cache"
	p.advancedJARDir = p.lib + "/ext/carbonio"
	p.configrewrite = p.libexec + "/configrewrite"

	p.milterOptions = p.conf + "/mta_milter_options"
	p.cbpolicydDB = p.data + "/cbpolicyd/db/cbpolicyd.sqlitedb"
	p.cbpolicydInitBin = p.libexec + "/zmcbpolicydinit"
	p.clamdDir = p.data + "/clamav/db"

	p.mysqldSafeBin = p.common + "/bin/mysqld_safe"
	p.mysqldSafeLedir = p.common + "/sbin"
	p.mysqldMallocLib = p.common + "/lib/libjemalloc.so"
	p.appserverDBPidFile = run + "/mysql.pid"
	p.antispamDBPidFile = run + "/amavisd-mysql.pid"

	// All entries point at upstream postfix — no legacy shell wrappers
	// (/opt/zextras/bin/postfix, libexec/zmmtastatus, libexec/zmmtainit) are
	// invoked from this package. Their logic is reimplemented natively.
	p.postfixBin = p.common + "/sbin/postfix"
	p.postconfBin = p.common + "/sbin/postconf"
	p.postaliasBin = p.common + "/sbin/postalias"
	p.mainCf = p.common + "/conf/main.cf"
	p.aliases = "/etc/aliases"
	p.mtaSpool = p.data + "/postfix/spool"
	p.mtaPidFile = p.mtaSpool + "/pid/master.pid"

	return p
}

// sdNotifySocket returns the NOTIFY_SOCKET path used for a service. The path
// is service-addressable (not per-invocation) so the stop-side listener can
// recreate it and receive STOPPING=1 datagrams from the daemon started by an
// earlier, already-exited configd CLI invocation.
func (p *paths) sdNotifySocket(service string) string {
	return p.run + "/notify-" + service + ".sock"
}
