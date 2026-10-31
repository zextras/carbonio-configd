// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"strings"
)

// milterMainClass is the JVM main class of the milter: the last argument
// milterJavaArgs hands to java, and the identity (ServiceDef.ProcessName)
// under which the running JVM is recognised — legacy zmmilterctl's
// `pgrep -f '/opt/zextras/.*/java.*milter.MilterServer'`, as an exact argv
// token.
const milterMainClass = "com.zimbra.cs.milter.MilterServer"

// milterCustomStart builds and executes the Java command for MilterServer.
// Reuses mailboxJavaBinary for Java path discovery. The milter is a lightweight
// Java process sharing the mailbox classpath but running a different main class.
func (c *Controller) milterCustomStart(ctx context.Context, _ *ServiceDef) error {
	lc, err := c.loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load localconfig: %w", err)
	}

	javaBin, err := c.mailboxJavaBinary(ctx, lc)
	if err != nil {
		return err
	}

	return c.startJavaDetached(ctx, "milter", javaBin, c.p.log+"/milter.out", c.milterJavaArgs(lc))
}

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
