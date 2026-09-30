package main

import "syscall"

// diskUsage reports the filesystem that holds path (the host's / mounted
// read-only into the container).
func diskUsage(path string) (total, used uint64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	bs := uint64(st.Bsize)
	total = st.Blocks * bs
	used = total - st.Bfree*bs
	return total, used
}
