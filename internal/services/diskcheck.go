// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"fmt"
	"os"
	"strconv"
	"syscall"


)

const defaultDiskThresholdMB = 100

// ServiceDiskDirs maps service names to directories that should be checked before start.
var ServiceDiskDirs = map[string][]string{
	"mailbox": {storePath, basePath + "/db", basePath + "/index", basePath + "/redolog"},
	"mta":     {dataPath + "/postfix/spool"},
}

// CheckDiskSpace checks available space on a path. Returns available MB and whether it's above threshold.
func CheckDiskSpace(path string, thresholdMB int) (availMB int, ok bool, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, true, fmt.Errorf("statfs %s: %w", path, err) // return ok=true to not block on error
	}

	// #nosec G115 - block size and available blocks are always positive on real filesystems
	availBytes := stat.Bavail * uint64(stat.Bsize) //nolint:gosec
	avail := int(availBytes / (1024 * 1024))       //nolint:gosec

	return avail, avail >= thresholdMB, nil
}

// GetDiskThreshold reads the disk threshold from localconfig, defaulting to 100MB.
func GetDiskThreshold() int {
	lc, err := loadConfig()
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
func CheckServiceDiskSpace(service string, threshold int) {
	dirs, ok := ServiceDiskDirs[service]
	if !ok {
		return
	}

	for _, dir := range dirs {
		if _, err := os.Stat(dir); err != nil {
			continue // directory doesn't exist, skip
		}

		availMB, ok, err := CheckDiskSpace(dir, threshold)
		if err != nil {
			continue
		}

		if !ok {
			fmt.Printf("\tWARNING: Disk space below threshold for %s (%dMB available, %dMB required).\n",
				dir, availMB, threshold)
		}
	}
}
