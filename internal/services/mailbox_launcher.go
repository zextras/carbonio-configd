// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// mailboxCustomStart builds and executes the Java command for zmmailboxd.
// Mirrors the logic in carbonio-appserver.service + legacy zmmailboxdctl.
//
//nolint:gocyclo // legacy-compatible launcher assembly is inherently branchy
func mailboxCustomStart(ctx context.Context, def *ServiceDef) error {
	lc, err := localconfig.LoadResolvedConfig()
	if err != nil {
		return fmt.Errorf("failed to load localconfig: %w", err)
	}

	javaBin, err := mailboxJavaBinary(ctx, lc)
	if err != nil {
		return err
	}

	args := mailboxJavaArgs(lc)

	_ = os.MkdirAll(mailboxdPath+"/work/service/jsp", 0o755)
	_ = os.MkdirAll(mailboxdPath+"/work", 0o755)

	logFile := logPath + "/zmmailboxd.out"

	logFd, err := openLogFile(logFile)
	if err != nil {
		return err
	}

	defer func() { _ = logFd.Close() }()

	// Pre-create gc.log with correct permissions (Java needs write access)
	gcLog := logPath + "/gc.log"
	if _, statErr := os.Stat(gcLog); os.IsNotExist(statErr) {
		if gcFd, createErr := os.OpenFile(gcLog, os.O_CREATE|os.O_WRONLY, 0o644); createErr == nil {
			_ = gcFd.Close()
		}
	}

	cmd := exec.CommandContext(ctx, javaBin, args...)
	cmd.Stdout = logFd
	cmd.Stderr = logFd
	cmd.SysProcAttr = detachedSysProcAttr()

	logger.InfoContext(ctx, "Starting mailbox via Java launcher",
		"java", javaBin, "heap", lc["mailboxd_java_heap_size"]+"m", "log", logFile)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start mailbox: %w", err)
	}

	return nil
}

func mailboxJavaBinary(ctx context.Context, lc map[string]string) (string, error) {
	javaHome := lc["mailboxd_java_home"]
	if javaHome == "" {
		javaHome = lc["zimbra_java_home"]
	}

	if javaHome == "" {
		javaHome = commonPath + "/lib/jvm/java"
		logger.InfoContext(ctx, "Using fixed Java path (localconfig unresolved)", "path", javaHome)
	}

	javaBin := filepath.Join(javaHome, "bin", "java")
	if _, err := os.Stat(javaBin); err != nil {
		return "", fmt.Errorf("java binary not found at %s: %w", javaBin, err)
	}

	return javaBin, nil
}

func mailboxJavaArgs(lc map[string]string) []string {
	javaXms := lc["mailboxd_java_heap_size"]
	if javaXms == "" {
		javaXms = "512"
	}

	javaXmx := javaXms
	javaOpts := lc["mailboxd_java_options"]
	javaOpts = strings.ReplaceAll(javaOpts,
		"-Xlog:gc*=info,safepoint=info:file="+logPath+"/gc.log:time:filecount=20,filesize=10m", "")
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

	args := []string{"-Dfile.encoding=UTF-8"}
	if javaOpts != "" {
		args = append(args, strings.Fields(javaOpts)...)
	}

	return append(args,
		"-Xms"+javaXms+"m",
		"-Xmx"+javaXmx+"m",
		"-Djava.io.tmpdir="+mailboxdPath+"/work",
		"-Djava.library.path="+libPath,
		"-Dzimbra.config="+confPath+"/localconfig.xml",
		"-cp", mailboxPath+"/jars/mailbox.jar:"+mailboxPath+"/jars/*",
		"com.zextras.mailbox.Mailbox",
	)
}
