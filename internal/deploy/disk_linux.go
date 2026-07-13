package deploy

import "golang.org/x/sys/unix"

// freeBytes returns the bytes available to non-root on the filesystem holding path. Used by
// preflight's disk_full floor — the agent only runs on Linux servers, so this is the real
// implementation (disk_other.go stubs it for dev/CI builds on other platforms).
func freeBytes(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}
