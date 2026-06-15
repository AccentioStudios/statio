// Package fsutil provides atomic, permission-safe file writes and startup permission
// checks used across the agent. All secret/state files are written 0600 and validated
// to be root-owned and not group/other-readable (enforced on Linux; see perms_*.go).
package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// SecureWrite atomically writes data to path with the given perm: it writes a temp
// file in the same directory, fsyncs it, then renames over the target and fsyncs the
// directory. A reader therefore never observes a half-written file (e.g. app.env mid
// deploy). The temp file is removed on any error.
func SecureWrite(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".push-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err = tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("fsync temp: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmpName, path, err)
	}
	syncDir(dir)
	return nil
}

// syncDir best-effort fsyncs a directory so the rename is durable. No-op where the OS
// does not support directory fsync.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// CheckPerm verifies path is not readable by group/other (mode & 0077 == 0) and is
// owned by root. It fails closed: a botched chmod or wrong owner aborts startup. The
// real enforcement is Linux-only (production); on other platforms it is a no-op so the
// agent and tests build/run on developer machines. See perms_linux.go / perms_other.go.
func CheckPerm(path string) error { return checkPerm(path) }
