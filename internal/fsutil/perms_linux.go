//go:build linux

package fsutil

import (
	"fmt"
	"os"
	"syscall"
)

// checkPerm enforces 0600-ish (group/other unreadable) and root ownership on Linux.
func checkPerm(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Errorf("%s has insecure mode %#o (must be group/other unreadable)", path, mode)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 {
		return fmt.Errorf("%s is not owned by root", path)
	}
	return nil
}
