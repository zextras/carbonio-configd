// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"archive/zip"
	"bufio"
	"io"
	"path/filepath"
	"strings"
)

const (
	// buildInfoJarRel is the mailbox jar that carries com/zimbra/cs/util/buildInfo.properties,
	// the source of the legacy BuildInfo.VERSION / BuildInfo.RELEASE constants.
	buildInfoJarRel = "mailbox/jars/mailbox.jar"
	// buildInfoPropsEntry is the properties resource read by com.zimbra.cs.util.BuildInfo.
	buildInfoPropsEntry = "com/zimbra/cs/util/buildInfo.properties"
	// buildInfoUnknown is the value BuildInfo keeps when the properties cannot be loaded.
	buildInfoUnknown = "unknown"
)

// buildInfo returns BuildInfo.VERSION and BuildInfo.RELEASE exactly as the legacy
// ProxyConfGen (which runs with mailbox.jar on its classpath) sees them: both are
// read from buildInfo.properties inside mailbox.jar and stay "unknown" when the
// resource cannot be loaded.
func (g *Generator) buildInfo() (version, release string) {
	version, release = buildInfoUnknown, buildInfoUnknown

	zr, err := zip.OpenReader(filepath.Join(g.WorkingDir, buildInfoJarRel))
	if err != nil {
		return version, release
	}

	defer func() { _ = zr.Close() }()

	rc, err := zr.Open(buildInfoPropsEntry)
	if err != nil {
		return version, release
	}

	defer func() { _ = rc.Close() }()

	return parseBuildInfo(rc)
}

// parseBuildInfo extracts VERSION and RELEASE from a java.util.Properties stream.
// A missing key yields "unknown", matching BuildInfo's field defaults.
func parseBuildInfo(r io.Reader) (version, release string) {
	version, release = buildInfoUnknown, buildInfoUnknown

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}

		switch strings.TrimSpace(key) {
		case "VERSION":
			version = strings.TrimSpace(val)
		case "RELEASE":
			release = strings.TrimSpace(val)
		}
	}

	return version, release
}
