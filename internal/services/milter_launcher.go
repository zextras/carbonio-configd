// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// milterCustomStart builds and executes the Java command for MilterServer.
// Reuses mailboxJavaBinary for Java path discovery. The milter is a lightweight
// Java process sharing the mailbox classpath but running a different main class.
func milterCustomStart(ctx context.Context, _ *ServiceDef) error {
	lc, err := localconfig.LoadResolvedConfig()
	if err != nil {
		return fmt.Errorf("failed to load localconfig: %w", err)
	}

	javaBin, err := mailboxJavaBinary(ctx, lc)
	if err != nil {
		return err
	}

	args := milterJavaArgs(lc)
	logFile := logPath + "/milter.out"

	logFd, err := openLogFile(logFile)
	if err != nil {
		return err
	}

	defer func() { _ = logFd.Close() }()

	cmd := exec.CommandContext(ctx, javaBin, args...)
	cmd.Stdout = logFd
	cmd.Stderr = logFd
	cmd.SysProcAttr = detachedSysProcAttr()

	logger.InfoContext(ctx, "Starting milter via Java launcher",
		"java", javaBin, "log", logFile)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start milter: %w", err)
	}

	if err := cmd.Process.Release(); err != nil {
		logger.WarnContext(ctx, "Failed to release milter process handle", "error", err)
	}

	return nil
}

// milterJavaArgs builds JVM arguments for MilterServer.
// Mirrors the legacy milterctl.sh / carbonio-milter.service command.
func milterJavaArgs(lc map[string]string) []string {
	javaOpts := lc["zimbra_zmjava_options"]
	if javaOpts == "" {
		javaOpts = "-Xmx256m" +
			" -Dhttps.protocols=TLSv1.2,TLSv1.3" +
			" -Djdk.tls.client.protocols=TLSv1.2,TLSv1.3" +
			" -Djava.net.preferIPv4Stack=true"
	}

	libPath := lc["zimbra_zmjava_java_library_path"]
	if libPath == "" {
		libPath = basePath + "/lib"
	}

	log4jFile := confPath + "/milter.log4j.properties"

	args := []string{"-client"}
	args = append(args, strings.Fields(javaOpts)...)

	return append(args,
		"-Dzimbra.home="+basePath,
		"-Djava.library.path="+libPath,
		"-classpath", mailboxPath+"/jars/*:"+confPath,
		"-Dlog4j.configurationFile=file:"+log4jFile,
		"-Dzimbra.config="+confPath+"/localconfig.xml",
		"com.zimbra.cs.milter.MilterServer",
	)
}
