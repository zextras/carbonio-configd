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

// startJava launches a JVM service from its JavaSpec: the java binary of
// localconfig, the spec's argument list, a detached session logging to the
// spec's log file, then the service's readiness probe. The mailbox JVM is the
// JVM only: the database is the appserver-db service (the store composite
// starts both, and mailbox Wants appserver-db), and the zmtlsctl
// configuration set runs as the service's ConfigRewrite, skipped by
// `norewrite`.
func (c *Controller) startJava(ctx context.Context, def *ServiceDef) error {
	spec := def.Java

	lc, err := c.loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load localconfig: %w", err)
	}

	javaBin, err := c.javaBinary(ctx, lc)
	if err != nil {
		return err
	}

	args := spec.Args(lc)

	for _, dir := range spec.Mkdirs {
		if err := c.env.FS.MkdirAll(dir, 0o755); err != nil {
			logger.WarnContext(ctx, "Failed to create "+spec.Label+" work directory",
				"path", dir, "error", err)
		}
	}

	// Pre-create the files the JVM only needs write access to, with the
	// right permissions.
	for _, file := range spec.Touch {
		if _, statErr := c.env.FS.Stat(file); errors.Is(statErr, fs.ErrNotExist) {
			if fd, createErr := c.env.FS.OpenFile(file, os.O_CREATE|os.O_WRONLY, 0o640); createErr == nil {
				_ = fd.Close()
			}
		}
	}

	if err := c.startJavaDetached(ctx, def.Name, spec.Label, javaBin, spec.LogFile, args); err != nil {
		return err
	}

	return c.awaitReady(ctx, def, nil)
}

// startJavaDetached builds and starts javaBin with args as a fully detached,
// fire-and-forget child: stdout/stderr go to logFile, it gets its own
// session (Setsid), and the process handle is released right
// after Start. Release is right for every JVM service: nothing ever calls
// cmd.Wait() on the child, so nothing needs the handle once it's launched;
// the process is reaped by init once configd's own process exits.
func (c *Controller) startJavaDetached(
	ctx context.Context, service, label, javaBin, logFile string, args []string,
) error {
	logFd, err := c.openLogFile(logFile)
	if err != nil {
		return err
	}

	defer func() { _ = logFd.Close() }()

	logger.InfoContext(ctx, "Starting "+label+" via Java launcher", "java", javaBin, "log", logFile)

	proc, err := c.startIn(ctx, service, &sys.Cmd{
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

func (c *Controller) javaBinary(ctx context.Context, lc map[string]string) (string, error) {
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

// milterMainClass is the JVM main class of the milter: the last argument
// milterJavaArgs hands to java, and the identity (ServiceDef.ProcessName)
// under which the running JVM is recognised — legacy zmmilterctl's
// `pgrep -f '/opt/zextras/.*/java.*milter.MilterServer'`, as an exact argv
// token.
const milterMainClass = "com.zimbra.cs.milter.MilterServer"

// milterJavaArgs builds JVM arguments for MilterServer.
// Mirrors the legacy milterctl.sh / carbonio-milter.service command.
func (c *Controller) milterJavaArgs(lc map[string]string) []string {
	javaOpts := lc["zimbra_zmjava_options"]
	if javaOpts == "" {
		javaOpts = "-Xmx256m" +
			" -Dhttps.protocols=TLSv1.2,TLSv1.3" +
			" -Djdk.tls.client.protocols=TLSv1.2,TLSv1.3" +
			" -Djava.net.preferIPv4Stack=true"
	}

	javaLibPath := lc["zimbra_zmjava_java_library_path"]
	if javaLibPath == "" {
		javaLibPath = c.p.base + "/lib"
	}

	log4jFile := c.p.conf + "/milter.log4j.properties"

	args := append(javaPreviewArgs(), "-client")
	args = append(args, withoutPreviewArgs(strings.Fields(javaOpts))...)

	return append(args,
		"-Dzimbra.home="+c.p.base,
		"-Djava.library.path="+javaLibPath,
		"-classpath", c.p.mailbox+"/jars/*:"+c.p.conf,
		"-Dlog4j.configurationFile=file:"+log4jFile,
		"-Dzimbra.config="+c.p.conf+"/localconfig.xml",
		milterMainClass,
	)
}
