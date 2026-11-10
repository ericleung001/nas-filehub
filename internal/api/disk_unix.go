//go:build unix

package api

import (
	"strings"
	"syscall"
)

func diskUsage(path string) (total, free, used uint64, ok bool) {
	if strings.TrimSpace(path) == "" {
		return 0, 0, 0, false
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, 0, false
	}
	bsize := uint64(st.Bsize)
	if st.Bsize <= 0 || bsize == 0 {
		return 0, 0, 0, false
	}
	total = st.Blocks * bsize
	free = uint64(st.Bavail) * bsize
	var bfree uint64
	bfree = st.Bfree * bsize
	if total >= bfree {
		used = total - bfree
	}
	return total, free, used, total > 0
}
