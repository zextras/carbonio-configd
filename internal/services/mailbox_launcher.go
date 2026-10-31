// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// mailboxMainClass is the JVM main class of mailboxd: the last argument
// mailboxJavaArgs hands to java, and the identity (ServiceDef.ProcessName)
// under which the running JVM is recognised — legacy zmmailboxdctl's
// `pgrep -f com.zextras.mailbox.Mailbox`, as an exact argv token.
const mailboxMainClass = "com.zextras.mailbox.Mailbox"

// mailboxCustomStart starts the appserver mariadb and then the mailboxd JVM,
// in that order. Matches legacy zmstorectl START_ORDER="mysql.server
// zmmailboxdctl". The DB is not a separate registered service — keeping
// `zmcontrol status` aligned with the legacy %allservices list — so its
// lifecycle is stitched in here.
func (c *Controller) mailboxCustomStart(ctx context.Context, def *ServiceDef) error {
	if err := c.startAppserverDB(ctx); err != nil {
		// Match legacy zmstorectl: log the DB failure but proceed with
		// mailboxd start. The daemon reconnects on its own once the DB
		// recovers, and blocking the JVM on an intermittent DB failure
		// would be a bigger outage than degraded operation.
		logger.WarnContext(ctx, "Appserver DB failed to start; continuing with mailbox",
			"error", err)
	}

	lc, err := c.loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load localconfig: %w", err)
	}

	javaBin, err := c.mailboxJavaBinary(ctx, lc)
	if err != nil {
		return err
	}

	args := c.mailboxJavaArgs(lc)

	if err := c.env.FS.MkdirAll(c.p.mailboxd+"/work/service/jsp", 0o755); err != nil {
		logger.WarnContext(ctx, "Failed to create mailbox work directory",
			"path", c.p.mailboxd+"/work/service/jsp", "error", err)
	}

	if err := c.env.FS.MkdirAll(c.p.mailboxd+"/work", 0o755); err != nil {
		logger.WarnContext(ctx, "Failed to create mailbox work directory",
			"path", c.p.mailboxd+"/work", "error", err)
	}

	// Pre-create gc.log with correct permissions (Java needs write access)
	gcLog := c.p.log + "/gc.log"
	if _, statErr := c.env.FS.Stat(gcLog); errors.Is(statErr, fs.ErrNotExist) {
		if gcFd, createErr := c.env.FS.OpenFile(gcLog, os.O_CREATE|os.O_WRONLY, 0o640); createErr == nil {
			_ = gcFd.Close()
		}
	}

	return c.startJavaDetached(ctx, "mailbox", javaBin, c.p.log+"/zmmailboxd.out", args,
		"heap", lc["mailboxd_java_heap_size"]+"m")
}

// mailboxCustomStop terminates mailboxd and then the appserver mariadb in
// that order. Matches legacy zmstorectl STOP_ORDER="zmmailboxdctl
// mysql.server": the JVM exits first so in-flight writes are flushed by the
// application, then stopAppserverDB runs the InnoDB dirty-page flush and
// shuts mariadb down cleanly. DB errors are logged but do not fail the stop
// so `zmcontrol stop` still makes forward progress on the rest of the
// shutdown sequence.
func (c *Controller) mailboxCustomStop(ctx context.Context, def *ServiceDef) error {
	if def.ProcessName != "" {
		if err := c.killProcess(ctx, def.ProcessName); err != nil {
			logger.WarnContext(ctx, "Failed to signal mailbox JVM; proceeding to DB stop",
				"error", err)
		}
	}

	if err := c.stopAppserverDB(ctx); err != nil {
		logger.WarnContext(ctx, "Failed to stop appserver DB",
			"error", err)

		return err
	}

	return nil
}

// startJavaDetached builds and starts javaBin with args as a fully detached,
// fire-and-forget child: stdout/stderr go to logFile, it gets its own
// session (Setsid), and the process handle is released right
// after Start. Shared by mailboxCustomStart and milterCustomStart, whose
// Java-launch skeletons had drifted (milter released the handle, mailbox
// did not) — Release is correct for both: neither caller ever calls
// cmd.Wait() on the child, so nothing needs the handle once it's launched;
// the process is reaped by init once configd's own process exits.
func (c *Controller) startJavaDetached(
	ctx context.Context, label, javaBin, logFile string, args []string, logFields ...any,
) error {
	logFd, err := c.openLogFile(logFile)
	if err != nil {
		return err
	}

	defer func() { _ = logFd.Close() }()

	fields := append([]any{"java", javaBin, "log", logFile}, logFields...)
	logger.InfoContext(ctx, "Starting "+label+" via Java launcher", fields...)

	proc, err := c.env.Exec.Start(ctx, &sys.Cmd{
		Path:   javaBin,
		Args:   args,
		Stdout: logFd,
		Stderr: logFd,
		Setsid: true,
	})
	if err != nil {
		return fmt.Errorf("failed to start %s: %w", label, err)
	}

	if err := proc.Release(); err != nil {
		logger.WarnContext(ctx, "Failed to release "+label+" process handle", "error", err)
	}

	return nil
}

func (c *Controller) mailboxJavaBinary(ctx context.Context, lc map[string]string) (string, error) {
	javaHome := lc["mailboxd_java_home"]
	if javaHome == "" {
		javaHome = lc["zimbra_java_home"]
	}

	if javaHome == "" {
		javaHome = c.p.common + "/lib/jvm/java"
		logger.InfoContext(ctx, "Using fixed Java path (localconfig unresolved)", "path", javaHome)
	}

	javaBin := filepath.Join(javaHome, "bin", "java")
	if _, err := c.env.FS.Stat(javaBin); err != nil {
		return "", fmt.Errorf("java binary not found at %s: %w", javaBin, err)
	}

	return javaBin, nil
}

// javaPreviewArgs returns the JVM flags required by mailbox jars compiled with
// Java preview features (class file version 65.65535). Mirrors the legacy
// zmmailboxdctl / zmmilterctl launchers.
func javaPreviewArgs() []string {
	return []string{"--enable-preview", "--enable-native-access=ALL-UNNAMED"}
}

// withoutPreviewArgs drops the flags emitted by javaPreviewArgs from opts, so
// localconfig options that already carry them do not duplicate them.
func withoutPreviewArgs(opts []string) []string {
	out := opts[:0:0]
	for _, o := range opts {
		if o == "--enable-preview" || strings.HasPrefix(o, "--enable-native-access=") {
			continue
		}

		out = append(out, o)
	}

	return out
}

func (c *Controller) mailboxJavaArgs(lc map[string]string) []string {
	javaXms := lc["mailboxd_java_heap_size"]
	if javaXms == "" {
		javaXms = "512"
	}

	javaXmx := javaXms
	javaOpts := lc["mailboxd_java_options"]
	javaOpts = strings.ReplaceAll(javaOpts,
		"-Xlog:gc*=info,safepoint=info:file="+c.p.log+"/gc.log:time:filecount=20,filesize=10m", "")
	javaOpts = strings.TrimSpace(javaOpts)

	threadStack := lc["mailboxd_thread_stack_size"]
	if threadStack == "" {
		threadStack = "256k"
	}

	if !strings.Contains(javaOpts, "Xss") {
		javaOpts = strings.TrimSpace(javaOpts + " -Xss" + threadStack)
	}

	networkTTL := lc["networkaddress_cache_ttl"]
	if networkTTL == "" {
		networkTTL = "60"
	}

	if !strings.Contains(javaOpts, "sun.net.inetaddr.ttl") {
		javaOpts = strings.TrimSpace(javaOpts + " -Dsun.net.inetaddr.ttl=" + networkTTL)
	}

	log4jProps := lc["zimbra_log4j_properties"]
	if log4jProps != "" && !strings.Contains(javaOpts, "log4j") {
		javaOpts = strings.TrimSpace(javaOpts + " -Dlog4j.configurationFile=" + log4jProps)
	}

	args := append(javaPreviewArgs(), "-Dfile.encoding=UTF-8")
	args = append(args, withoutPreviewArgs(strings.Fields(javaOpts))...)

	return append(args,
		"-Xms"+javaXms+"m",
		"-Xmx"+javaXmx+"m",
		"-Djava.io.tmpdir="+c.p.mailboxd+"/work",
		"-Djava.library.path="+c.p.lib,
		"-Dzimbra.config="+c.p.conf+"/localconfig.xml",
		"-cp", c.p.mailbox+"/jars/mailbox.jar:"+c.p.mailbox+"/jars/*",
		mailboxMainClass,
	)
}
