// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"fmt"
	"strconv"
)

const defaultDiskThresholdMB = 100

// serviceDiskDirs lists the directories of a service that should be checked before start.
func (c *Controller) serviceDiskDirs(service string) []string {
	switch service {
	case svcMailbox:
		return []string{c.p.store, c.p.base + "/db", c.p.base + "/index", c.p.base + "/redolog"}
	case serviceMTA:
		return []string{c.p.mtaSpool}
	default:
		return nil
	}
}

// CheckDiskSpace checks available space on a path. Returns available MB and whether it's above threshold.
func (c *Controller) CheckDiskSpace(path string, thresholdMB int) (availMB int, ok bool, err error) {
	availBytes, err := c.env.FS.AvailBytes(path)
	if err != nil {
		return 0, true, fmt.Errorf("statfs %s: %w", path, err) // return ok=true to not block on error
	}

	avail := int(availBytes / (1024 * 1024)) //nolint:gosec

	return avail, avail >= thresholdMB, nil
}

// GetDiskThreshold reads the disk threshold from localconfig, defaulting to 100MB.
func (c *Controller) GetDiskThreshold() int {
	lc, err := c.loadConfig()
	if err != nil {
		return defaultDiskThresholdMB
	}

	if val, ok := lc["zimbra_disk_threshold"]; ok {
		if mb, err := strconv.Atoi(val); err == nil && mb > 0 {
			return mb
		}
	}

	return defaultDiskThresholdMB
}

// CheckServiceDiskSpace prints warnings for service-specific directories below threshold.
func (c *Controller) CheckServiceDiskSpace(service string, threshold int) {
	for _, dir := range c.serviceDiskDirs(service) {
		if _, err := c.env.FS.Stat(dir); err != nil {
			continue // directory doesn't exist, skip
		}

		availMB, ok, err := c.CheckDiskSpace(dir, threshold)
		if err != nil {
			continue
		}

		if !ok {
			fmt.Printf("\tWARNING: Disk space below threshold for %s (%dMB available, %dMB required).\n",
				dir, availMB, threshold)
		}
	}
}
