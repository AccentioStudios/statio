package deploy

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/accentiostudios/statio/internal/fsutil"
)

// State is the per-service deploy state, persisted atomically to state.json (0600 root).
type State struct {
	Service string `json:"service"`
	// LastSeq is the highest accepted deploy_seq; the agent rejects any payload with a
	// lower seq (anti-replay/downgrade, invariant #17). Persisted across restarts.
	LastSeq  int64      `json:"last_seq,omitempty"`
	LastGood *Snapshot  `json:"last_good,omitempty"`
	History  []Snapshot `json:"history,omitempty"`
}

// Snapshot records a known-good deploy. RunRef points at the tmpfs dir holding the exact
// generated compose + env files that shipped, so rollback restores them as a unit (these
// live in /run and survive until reboot; offline rollback after a reboot is not supported
// by design — redeploy from CI).
type Snapshot struct {
	Digest   string      `json:"digest"`
	EnvHash  string      `json:"env_hash"`
	RunRef   string      `json:"run_ref,omitempty"`
	Proxy    *ProxyState `json:"proxy,omitempty"`
	DNS      *DNSState   `json:"dns,omitempty"`
	DeployID string      `json:"deploy_id,omitempty"`
	At       string      `json:"at,omitempty"`
}

// ProxyState caches the NPMplus proxy-host identity for later in-place updates.
type ProxyState struct {
	HostID int    `json:"host_id"`
	Domain string `json:"domain"`
}

// DNSState caches the Cloudflare record identity.
type DNSState struct {
	RecordID string `json:"record_id"`
	Name     string `json:"name"`
	Content  string `json:"content"`
}

const maxHistory = 5

// LoadState reads state.json; a missing file yields a fresh state.
func LoadState(path, service string) (*State, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &State{Service: service}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse state %s: %w", path, err)
	}
	if s.Service == "" {
		s.Service = service
	}
	return &s, nil
}

// Save atomically writes the state.
func (s *State) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.SecureWrite(path, data, 0o600)
}

// Advance sets snap as the new last_good and appends the previous good to history
// (capped, oldest-first prune). Call only after a healthy probe.
func (s *State) Advance(snap Snapshot) {
	if s.LastGood != nil {
		s.History = append(s.History, *s.LastGood)
		if len(s.History) > maxHistory {
			s.History = s.History[len(s.History)-maxHistory:]
		}
	}
	s.LastGood = &snap
}

// FindDigest returns the snapshot for a digest from last_good or history, used by
// with-digest rollback to restore the env that shipped with that digest.
func (s *State) FindDigest(digest string) *Snapshot {
	if s.LastGood != nil && s.LastGood.Digest == digest {
		return s.LastGood
	}
	for i := len(s.History) - 1; i >= 0; i-- {
		if s.History[i].Digest == digest {
			return &s.History[i]
		}
	}
	return nil
}
