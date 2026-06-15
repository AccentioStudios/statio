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

// TestByteEqualityDiscipline enforces invariant #15 under the per-service-signer flow: the
// handler decodes the payload to PEEK the service name (to select that service's signer),
// verifies the EXACT same bytes, and only then acts. We assert: both VerifyBlob and DecodeBytes
// operate on the same buffer (env.Payload); verification runs before any deploy effect (d.Run);
// and the payload is never re-marshalled in the handler (which would break byte-equality).
func TestByteEqualityDiscipline(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(moduleRoot(t), "internal", "agent", "handler.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	vi := strings.Index(src, "VerifyBlob(")
	di := strings.Index(src, "DecodeBytes(")
	ri := strings.Index(src, "d.Run(")
	if vi < 0 || di < 0 || ri < 0 {
		t.Fatal("handler must call DecodeBytes, VerifyBlob and d.Run")
	}
	// Verify-before-act: the signature gate must run before the deploy executes.
	if vi > ri {
		t.Fatal("VerifyBlob must run BEFORE d.Run (verify-before-act)")
	}
	// Byte-equality: the verified bytes and the decoded bytes are the SAME buffer (env.Payload).
	if !strings.Contains(src, "VerifyBlob(r.Context(), env.Payload,") {
		t.Error("VerifyBlob must verify env.Payload (the exact decoded bytes)")
	}
	if !strings.Contains(src, "DecodeBytes(env.Payload)") {
		t.Error("DecodeBytes must decode env.Payload (the exact verified bytes)")
	}
	// No re-marshal of the payload anywhere in the handler.
	if strings.Contains(src, "Marshal") {
		t.Error("the handler must not (re-)marshal the payload (breaks byte-equality)")
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
