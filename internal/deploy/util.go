package deploy

import (
	"os"
	"path/filepath"

	"github.com/accentiostudios/push/internal/fsutil"
)

// writeFileSecure creates the parent directory (0700) and atomically writes the file 0600.
func writeFileSecure(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fsutil.SecureWrite(path, data, 0o600)
}
