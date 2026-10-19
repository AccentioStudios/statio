package deploy

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The field failure: the agent keeps each app's state under <state_dir>/services/<app>/, and
// nothing creates that directory before the first deploy reaches persist (the audit log creates
// it, but only after the run). Save must create it, or the first deploy of every new app fails
// at persist with "no such file or directory".
func TestStateSaveCreatesServiceDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "services", "api-staging")
	path := filepath.Join(dir, "state.json")
	st := &State{Service: "api-staging", LastSeq: 7}
	if err := st.Save(path); err != nil {
		t.Fatalf("save into a new service dir: %v", err)
	}
	got, err := LoadState(path, "api-staging")
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSeq != 7 {
		t.Fatalf("want last_seq 7 after reload, got %d", got.LastSeq)
	}
	if runtime.GOOS == "linux" {
		fi, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o700 {
			t.Fatalf("service state dir must be 0700, got %o", perm)
		}
	}
}
