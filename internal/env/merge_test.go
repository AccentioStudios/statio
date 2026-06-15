package env

import (
	"strings"
	"testing"
)

func strp(s string) *string { return &s }

func TestMergeOverrideWins(t *testing.T) {
	base := &BaseEnv{Entries: []Entry{
		{Key: "NODE_ENV", Value: strp("production")},
		{Key: "FLAG", Value: strp("off")},
	}}
	merged, err := MergeEnv(base, map[string]string{"FLAG": "on", "EXTRA": "1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if merged["FLAG"] != "on" || merged["NODE_ENV"] != "production" || merged["EXTRA"] != "1" {
		t.Fatalf("bad merge: %+v", merged)
	}
}

func TestMergeProtectedRejected(t *testing.T) {
	base := &BaseEnv{Entries: []Entry{
		{Key: "DATABASE_URL", SecretRef: "file:///x", Protected: true},
	}}
	resolve := func(string) (string, error) { return "postgres://...", nil }
	_, err := MergeEnv(base, map[string]string{"DATABASE_URL": "postgres://evil"}, resolve)
	if err == nil {
		t.Fatal("expected protected-key rejection")
	}
	if e, ok := err.(*Error); !ok || e.Code != "protected" {
		t.Fatalf("expected protected error, got %v", err)
	}
}

func TestMergeRequiredMissing(t *testing.T) {
	base := &BaseEnv{Entries: []Entry{{Key: "API_KEY", Required: true}}}
	if _, err := MergeEnv(base, nil, nil); err == nil {
		t.Fatal("expected required-missing rejection")
	}
	// satisfied by override
	if _, err := MergeEnv(base, map[string]string{"API_KEY": "k"}, nil); err != nil {
		t.Fatalf("required satisfied should pass: %v", err)
	}
}

func TestMergeSecretRefResolved(t *testing.T) {
	base := &BaseEnv{Entries: []Entry{{Key: "TOKEN", SecretRef: "file:///x", Protected: true}}}
	resolve := func(string) (string, error) { return "s3cr3t", nil }
	merged, err := MergeEnv(base, nil, resolve)
	if err != nil {
		t.Fatal(err)
	}
	if merged["TOKEN"] != "s3cr3t" {
		t.Fatalf("got %q", merged["TOKEN"])
	}
}

func TestMergeRejectsNewlineInSecret(t *testing.T) {
	base := &BaseEnv{Entries: []Entry{{Key: "CERT", SecretRef: "file:///x", Protected: true}}}
	resolve := func(string) (string, error) { return "line1\nline2", nil }
	if _, err := MergeEnv(base, nil, resolve); err == nil {
		t.Fatal("expected control-char rejection for multiline secret in app.env")
	}
}

func TestRenderSortedDeterministic(t *testing.T) {
	out := string(Render(map[string]string{"B": "2", "A": "1", "C": "3"}))
	if out != "A=1\nB=2\nC=3\n" {
		t.Fatalf("not sorted/deterministic: %q", out)
	}
	// A $ in a value is just bytes — never interpolated.
	out2 := string(Render(map[string]string{"X": "${RM} $(rm -rf /)"}))
	if !strings.Contains(out2, "X=${RM} $(rm -rf /)\n") {
		t.Fatalf("value not preserved literally: %q", out2)
	}
}
