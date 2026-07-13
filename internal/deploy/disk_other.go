//go:build !linux

package deploy

import "math"

// freeBytes on non-Linux (dev/CI builds; the agent only ships on Linux) reports "plenty", so the
// preflight disk_full floor never false-fails a development run.
func freeBytes(string) (uint64, error) {
	return math.MaxUint64, nil
}
