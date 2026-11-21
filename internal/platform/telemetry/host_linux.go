//go:build linux

package telemetry

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// hostMemoryAvailable is MemAvailable from /proc/meminfo: what the machine
// can still hand out without swapping (0 if unknown).
func hostMemoryAvailable() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(sc.Text(), "MemAvailable:"); ok {
			kb, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64)
			if err != nil {
				return 0
			}
			return kb * 1024
		}
	}
	return 0
}

// hostDiskFree is the free space on the filesystem holding path (0 if
// unknown). In a container, "/" is the host's disk unless mounted elsewhere.
func hostDiskFree(path string) int64 {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
