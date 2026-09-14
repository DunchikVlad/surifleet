//go:build linux

package main

import "golang.org/x/sys/unix"

// diskUsedPercent — заполненность ФС, на которой лежит path, % (statfs).
func diskUsedPercent(path string) float64 {
	if path == "" {
		path = "/"
	}
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0
	}
	total := st.Blocks * uint64(st.Bsize)
	free := st.Bavail * uint64(st.Bsize) // Bavail — доступно непривилегированным
	if total == 0 {
		return 0
	}
	return float64(total-free) / float64(total) * 100
}
