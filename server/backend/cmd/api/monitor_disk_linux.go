//go:build linux

package main

import "syscall"

// diskUsagePercent matches df: used / (used + available to non-root).
func diskUsagePercent(path string) (float64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	used := float64(st.Blocks - st.Bfree)
	avail := float64(st.Bavail)
	if used+avail == 0 {
		return 0, nil
	}
	return used / (used + avail) * 100, nil
}
