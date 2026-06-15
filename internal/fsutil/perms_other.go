//go:build !linux

package fsutil

// checkPerm is a no-op on non-Linux platforms (developer hosts). The agent only runs in
// production on Linux, where the real ownership/mode enforcement applies.
func checkPerm(path string) error { return nil }
