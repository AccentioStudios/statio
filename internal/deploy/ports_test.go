package deploy

import (
	"path/filepath"
	"testing"
)

// allFree is an isFree predicate that treats every port as bindable (deterministic tests).
func allFree(int) bool { return true }

func TestAssignIsStableAndIdempotent(t *testing.T) {
	r := &PortRegistry{Assignments: map[string]int{}}
	p1, err := r.Assign("api", allFree)
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if p1 != PortPoolStart {
		t.Fatalf("first assignment = %d, want %d", p1, PortPoolStart)
	}
	// Re-assigning the same slot returns the same port (idempotent — the proxy target must not move).
	p2, err := r.Assign("api", allFree)
	if err != nil {
		t.Fatalf("re-assign: %v", err)
	}
	if p2 != p1 {
		t.Fatalf("re-assign moved the port: %d -> %d", p1, p2)
	}
}

func TestAssignNeverCollides(t *testing.T) {
	r := &PortRegistry{Assignments: map[string]int{}}
	seen := map[int]string{}
	for _, slot := range []string{"api", "web", "worker", "db-proxy"} {
		p, err := r.Assign(slot, allFree)
		if err != nil {
			t.Fatalf("assign %q: %v", slot, err)
		}
		if other, dup := seen[p]; dup {
			t.Fatalf("port %d assigned to both %q and %q", p, other, slot)
		}
		seen[p] = slot
	}
}

func TestAssignSkipsBoundPorts(t *testing.T) {
	r := &PortRegistry{Assignments: map[string]int{}}
	// Pretend the first two pool ports are held by a non-statio process.
	free := func(p int) bool { return p >= PortPoolStart+2 }
	p, err := r.Assign("api", free)
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if p != PortPoolStart+2 {
		t.Fatalf("assignment = %d, want %d (should skip the two bound ports)", p, PortPoolStart+2)
	}
}

func TestAssignSkipsPortsTakenByOtherSlots(t *testing.T) {
	r := &PortRegistry{Assignments: map[string]int{"existing": PortPoolStart}}
	p, err := r.Assign("new", allFree)
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if p == PortPoolStart {
		t.Fatalf("new slot got the port already held by 'existing'")
	}
	if p != PortPoolStart+1 {
		t.Fatalf("assignment = %d, want %d", p, PortPoolStart+1)
	}
}

func TestReleaseReturnsPortToPool(t *testing.T) {
	r := &PortRegistry{Assignments: map[string]int{}}
	p, _ := r.Assign("api", allFree)
	r.Release("api")
	if _, ok := r.PortFor("api"); ok {
		t.Fatalf("port not released")
	}
	// A fresh slot reclaims the freed port (lowest free).
	p2, _ := r.Assign("web", allFree)
	if p2 != p {
		t.Fatalf("freed port %d not reclaimed (got %d)", p, p2)
	}
}

func TestPoolExhaustion(t *testing.T) {
	r := &PortRegistry{Assignments: map[string]int{}}
	// Nothing is bindable -> no port can be assigned.
	if _, err := r.Assign("api", func(int) bool { return false }); err == nil {
		t.Fatal("expected pool-exhaustion error when no port is free")
	}
}

func TestConflictsDetectsDoubleAssignment(t *testing.T) {
	r := &PortRegistry{Assignments: map[string]int{"a": 41000, "b": 41000, "c": 41001}}
	c := r.Conflicts()
	if len(c) != 1 {
		t.Fatalf("conflicts = %v, want exactly one collided port", c)
	}
	slots := c[41000]
	if len(slots) != 2 || slots[0] != "a" || slots[1] != "b" {
		t.Fatalf("conflict slots = %v, want [a b]", slots)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ports.json")
	r := &PortRegistry{Assignments: map[string]int{}}
	if _, err := r.Assign("api", allFree); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := r.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadPortRegistry(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if p, ok := got.PortFor("api"); !ok || p != PortPoolStart {
		t.Fatalf("round-trip lost the assignment: %d %v", p, ok)
	}
}

func TestLoadMissingFileYieldsEmpty(t *testing.T) {
	got, err := LoadPortRegistry(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if got == nil || got.Assignments == nil {
		t.Fatal("missing file should yield an empty, usable registry")
	}
}

func TestPortHeldOnlyBy(t *testing.T) {
	cases := []struct {
		name    string
		slot    string
		holders []string
		want    bool
	}{
		{"own slot only", "api", []string{"api"}, true},
		{"another slot", "api", []string{"other"}, false},
		{"mixed holders", "api", []string{"api", "other"}, false},
		{"unattributable (bound, unknown holder)", "api", nil, false},
	}
	for _, tc := range cases {
		if got := portHeldOnlyBy(tc.slot, tc.holders); got != tc.want {
			t.Errorf("%s: portHeldOnlyBy(%q, %v) = %v, want %v", tc.name, tc.slot, tc.holders, got, tc.want)
		}
	}
}
