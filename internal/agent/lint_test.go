package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoPublicListeners enforces security invariant #2 as a build-time check: the agent
// must listen ONLY on the tailnet via tsnet. A call to tsnet ListenFunnel (public edge)
// or stdlib net.Listen (public OS socket) anywhere in the module is forbidden.
func TestNoPublicListeners(t *testing.T) {
	root := moduleRoot(t)
	// Match call sites, not prose: ".ListenFunnel(" is a method call; "net.Listen("
	// is a stdlib public listener. Comments referencing the names by word are fine.
	forbidden := []string{".ListenFunnel(", "net.Listen("}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "lint_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, bad := range forbidden {
			if strings.Contains(string(data), bad) {
				t.Errorf("%s contains forbidden %q (breaks tailnet-only invariant #2)", path, bad)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestByteEqualityDiscipline enforces invariant #15: the handler must verify the EXACT
// bytes it decodes. It asserts VerifyBlob runs before DecodeBytes and that no marshal
// happens between them (which would make the verified document differ from the applied one).
func TestByteEqualityDiscipline(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(moduleRoot(t), "internal", "agent", "handler.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	vi := strings.Index(src, "VerifyBlob(")
	di := strings.Index(src, "DecodeBytes(")
	if vi < 0 || di < 0 {
		t.Fatal("handler must call VerifyBlob then DecodeBytes")
	}
	if vi > di {
		t.Fatal("VerifyBlob must run BEFORE DecodeBytes (verify-before-decode)")
	}
	if strings.Contains(src[vi:di], "Marshal") {
		t.Error("no (re-)marshal may occur between VerifyBlob and DecodeBytes (breaks byte-equality)")
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
