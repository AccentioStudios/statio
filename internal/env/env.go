// Package env implements the hybrid environment model: a server-managed base set
// (env.base.yaml, with protected/required/secretRef policy) merged with per-deploy
// overrides carried in the deploy event.
//
// The merged result is rendered to app.env, which the service's compose file consumes
// ONLY via `env_file:` (a literal KEY=VALUE reader, no ${...} interpolation). The
// digest interpolation file (interp.env) is a separate, closed file — the two are
// never crossed, which makes "an app secret containing $ or ${...} cannot inject into
// compose config" a structural property, not escaping discipline.
package env

import (
	"fmt"
	"os"
	"strings"

	"github.com/accentiostudios/push/internal/fsutil"
	"gopkg.in/yaml.v3"
)

const (
	apiVersion = "push/v1"
	kind       = "ServiceEnv"
)

// Entry is one base env key with its policy.
type Entry struct {
	Key string `yaml:"key"`
	// Value is an inline literal. Mutually exclusive with SecretRef.
	Value *string `yaml:"value,omitempty"`
	// SecretRef is a file:// URI to a 0600 root file holding the value. Used for
	// secrets that should never appear in the YAML.
	SecretRef string `yaml:"secretRef,omitempty"`
	// Protected forbids the deploy event from overriding this key (fail-closed 422).
	Protected bool `yaml:"protected,omitempty"`
	// Required declares a key the override MUST supply (else the deploy is rejected).
	Required bool `yaml:"required,omitempty"`
}

// BaseEnv is the parsed env.base.yaml.
type BaseEnv struct {
	APIVersion string  `yaml:"apiVersion"`
	Kind       string  `yaml:"kind"`
	Entries    []Entry `yaml:"entries"`
}

// LoadBaseEnv reads and validates env.base.yaml. A missing file yields an empty
// BaseEnv (a service may have no server-side base). When present, the file and any
// referenced secret files must be 0600 root.
func LoadBaseEnv(path string) (*BaseEnv, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &BaseEnv{APIVersion: apiVersion, Kind: kind}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := fsutil.CheckPerm(path); err != nil {
		return nil, err
	}
	var b BaseEnv
	if err := yaml.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if b.APIVersion != apiVersion || b.Kind != kind {
		return nil, fmt.Errorf("%s: expected apiVersion %q kind %q", path, apiVersion, kind)
	}
	seen := map[string]bool{}
	for i, e := range b.Entries {
		if e.Key == "" {
			return nil, fmt.Errorf("%s: entry %d has empty key", path, i)
		}
		if !validKey(e.Key) {
			return nil, fmt.Errorf("%s: invalid env key %q", path, e.Key)
		}
		if seen[e.Key] {
			return nil, fmt.Errorf("%s: duplicate env key %q", path, e.Key)
		}
		seen[e.Key] = true
		if e.Value != nil && e.SecretRef != "" {
			return nil, fmt.Errorf("%s: key %q has both value and secretRef", path, e.Key)
		}
		if !e.Required && e.Value == nil && e.SecretRef == "" {
			return nil, fmt.Errorf("%s: key %q has neither value, secretRef, nor required", path, e.Key)
		}
		// Lint (invariant #7): a secretRef key should also be protected so a forged CI
		// event cannot shadow it. We enforce, not just warn, to fail closed.
		if e.SecretRef != "" && !e.Protected {
			return nil, fmt.Errorf("%s: secretRef key %q must be marked protected", path, e.Key)
		}
	}
	return &b, nil
}

// Save atomically writes the base env file (0600). Used by `push env set/rm`.
func (b *BaseEnv) Save(path string) error {
	b.APIVersion = apiVersion
	b.Kind = kind
	data, err := yaml.Marshal(b)
	if err != nil {
		return err
	}
	return fsutil.SecureWrite(path, data, 0o600)
}

// Set inserts or replaces an entry by key.
func (b *BaseEnv) Set(e Entry) {
	for i := range b.Entries {
		if b.Entries[i].Key == e.Key {
			b.Entries[i] = e
			return
		}
	}
	b.Entries = append(b.Entries, e)
}

// Remove deletes an entry by key; reports whether it existed.
func (b *BaseEnv) Remove(key string) bool {
	for i := range b.Entries {
		if b.Entries[i].Key == key {
			b.Entries = append(b.Entries[:i], b.Entries[i+1:]...)
			return true
		}
	}
	return false
}

// ProtectedKeys returns the set of keys the event may not override.
func (b *BaseEnv) ProtectedKeys() map[string]bool {
	m := make(map[string]bool, len(b.Entries))
	for _, e := range b.Entries {
		if e.Protected {
			m[e.Key] = true
		}
	}
	return m
}

func validKey(k string) bool {
	if k == "" || strings.HasPrefix(k, "PUSH_") {
		return false
	}
	for i, r := range k {
		isAlpha := r == '_' || (r >= 'A' && r <= 'Z')
		isNum := r >= '0' && r <= '9'
		if i == 0 && !isAlpha {
			return false
		}
		if i > 0 && !isAlpha && !isNum {
			return false
		}
	}
	return true
}
