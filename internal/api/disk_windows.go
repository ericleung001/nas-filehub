//go:build windows

package api

import (
	"strings"

	"golang.org/x/sys/windows"
)

func diskUsage(path string) (total, free, used uint64, ok bool) {
	if strings.TrimSpace(path) == "" {
		return 0, 0, 0, false
	}
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, 0, false
	}
	var freeBytes, totalBytes, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(pathPtr, &freeBytes, &totalBytes, &totalFree); err != nil {
		return 0, 0, 0, false
	}
	total = totalBytes
	free = freeBytes
	if total >= totalFree {
		used = total - totalFree
	}
	return total, free, used, total > 0
}
