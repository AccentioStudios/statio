package deploy

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sort"
	"time"

	"github.com/accentiostudios/statio/internal/fsutil"
)

// PortRegistry is the HOST-level map of slot -> assigned loopback host port. statio is the
// single allocator: a deploy never publishes a host port the dev picked, so two slots can
// never request the same one (the silent `docker compose up` -> "port is already allocated"
// crash). Each slot gets ONE stable port, kept across redeploys. Persisted 0600 root at
// <state_dir>/ports.json; freed when the app is removed (`statio app rm`).
//
// An app that publishes a port always gets a host binding on 127.0.0.1 (so the agent's loopback
// health probe and a loopback-mode proxy can reach it); in container mode the app is ALSO reached
// by NPMplus over the shared docker network. An internal-only app (no published port) is never
// allocated a port.
type PortRegistry struct {
	// Assignments maps a slot name to its pinned host port. It is the source of truth: the
	// generated compose binds 127.0.0.1:<assigned>:<container-port>, and the loopback health
	// probe + the NPMplus forward target both read the assigned port from here.
	Assignments map[string]int `json:"assignments"`
}

// The allocation pool. A high, uncommon range so an assigned port is unlikely to collide with
// a hand-run service, and well clear of the 0-1023 privileged range. The dev never sees these:
// they are an internal host<->proxy implementation detail.
const (
	PortPoolStart = 41000
	PortPoolEnd   = 48999
)

// LoadPortRegistry reads the host port registry; a missing file yields an empty (but usable)
// registry so the first-ever deploy can allocate.
func LoadPortRegistry(path string) (*PortRegistry, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &PortRegistry{Assignments: map[string]int{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read port registry: %w", err)
	}
	var r PortRegistry
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse port registry %s: %w", path, err)
	}
	if r.Assignments == nil {
		r.Assignments = map[string]int{}
	}
	return &r, nil
}

// Save atomically writes the registry 0600 root.
func (r *PortRegistry) Save(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.SecureWrite(path, data, 0o600)
}

// PortFor returns the slot's assigned port, if any.
func (r *PortRegistry) PortFor(slot string) (int, bool) {
	p, ok := r.Assignments[slot]
	return p, ok
}

// Assign returns the slot's stable host port, allocating one on first use. It is IDEMPOTENT:
// a slot that already has a port keeps it (so the proxy target never moves under it). A new
// slot gets the lowest pool port not already assigned to another slot AND that `free` reports
// as bindable, so statio never hands out a port some other process on the host is using.
//
// The caller is responsible for persisting (Save) after a successful Assign that allocated a
// new port — Assign mutates the in-memory map but does not write, so a batch can assign then
// save once. free is injected for testability; pass HostPortFree in production.
func (r *PortRegistry) Assign(slot string, free func(int) bool) (int, error) {
	if p, ok := r.Assignments[slot]; ok {
		return p, nil
	}
	taken := make(map[int]bool, len(r.Assignments))
	for _, p := range r.Assignments {
		taken[p] = true
	}
	for p := PortPoolStart; p <= PortPoolEnd; p++ {
		if taken[p] {
			continue
		}
		if free != nil && !free(p) {
			continue
		}
		r.Assignments[slot] = p
		return p, nil
	}
	return 0, fmt.Errorf("port pool exhausted (%d-%d): no free host port to assign to %q", PortPoolStart, PortPoolEnd, slot)
}

// Release drops a slot's assignment (called when an app is removed) so its port returns to the
// pool. A no-op for an unknown slot.
func (r *PortRegistry) Release(slot string) {
	delete(r.Assignments, slot)
}

// Conflicts returns slots that share a host port — an invariant violation that should never
// happen with Assign as the sole allocator, but is cheap to assert (e.g. after a hand-edit of
// ports.json). The map is keyed by port; each value is the slots colliding on it.
func (r *PortRegistry) Conflicts() map[int][]string {
	byPort := map[int][]string{}
	for slot, p := range r.Assignments {
		byPort[p] = append(byPort[p], slot)
	}
	out := map[int][]string{}
	for p, slots := range byPort {
		if len(slots) > 1 {
			sort.Strings(slots)
			out[p] = slots
		}
	}
	return out
}

// HostPortFree reports whether a TCP port on 127.0.0.1 is currently free — i.e. nothing on the
// host is listening on it. It DIALS loopback (never opens a listener: a listener would trip the
// tailnet-only invariant #2 lint, and the agent must not bind public sockets): a refused
// connection means nothing is there (free), a successful connect means something holds it
// (taken). Used both to pick an allocation that won't clash with a non-statio process and, at
// preflight, to detect a slot's assigned port being occupied by something foreign. It does not
// reveal WHO holds it, so callers treat "this slot's own running container" separately.
func HostPortFree(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 250*time.Millisecond)
	if err != nil {
		return true // connection refused / no answer -> nothing is listening
	}
	_ = conn.Close()
	return false // something answered -> the port is in use
}

// portHeldOnlyBy decides whether a bound port belongs solely to slot (its own compose project, a
// redeploy). Empty holders means the port is bound but unattributable -> NOT solely this slot. It
// is pure so the conflict logic is unit-tested without binding a real socket. The caller only
// reaches it when docker DID answer (an empty result then genuinely means "no compose project
// publishes it" -> a foreign, non-docker holder).
func portHeldOnlyBy(slot string, holders []string) bool {
	if len(holders) == 0 {
		return false
	}
	for _, h := range holders {
		if h != slot {
			return false
		}
	}
	return true
}
