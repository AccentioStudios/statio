package env

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/accentiostudios/push/internal/fsutil"
)

// Error is a typed merge failure. Code lets the agent map to an HTTP status:
// "protected" and "required" => 422, others => 400/500. It never carries a secret value.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// SecretResolver turns a secretRef (file:// URI) into its value.
type SecretResolver func(ref string) (string, error)

// ResolveFileSecret reads a file:// secretRef, enforcing 0600 root perms. It is the
// only secretRef scheme supported in v1; the file:// indirection leaves room for
// env://, vault://, etc. later without a schema change. Prefer ConfinedResolver, which
// additionally restricts the path to a single directory.
func ResolveFileSecret(ref string) (string, error) {
	return resolveFileSecret(ref, "")
}

// ConfinedResolver returns a SecretResolver that only reads secret files under baseDir.
// This closes a path-traversal: a crafted secretRef (file:///etc/push/secrets/cloudflare.json)
// must not be able to read an arbitrary 0600-root file outside the service's own secrets dir.
func ConfinedResolver(baseDir string) SecretResolver {
	return func(ref string) (string, error) { return resolveFileSecret(ref, baseDir) }
}

func resolveFileSecret(ref, baseDir string) (string, error) {
	u, err := url.Parse(ref)
	if err != nil || u.Scheme != "file" {
		return "", &Error{Code: "secret", Message: "secretRef must be a file:// URI"}
	}
	path := filepath.Clean(u.Path)
	if baseDir != "" {
		base := filepath.Clean(baseDir)
		if path != base && !strings.HasPrefix(path, base+string(filepath.Separator)) {
			return "", &Error{Code: "secret", Message: "secretRef escapes the service secrets directory"}
		}
	}
	if err := fsutil.CheckPerm(path); err != nil {
		return "", &Error{Code: "secret", Message: err.Error()}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", &Error{Code: "secret", Message: fmt.Sprintf("read secret: %v", err)}
	}
	// Trim a single trailing newline (common in secret files) but reject embedded
	// control chars below.
	return strings.TrimSuffix(string(data), "\n"), nil
}

// MergeEnv computes the final environment from the server base and the per-deploy
// overrides. Order, fail-closed:
//  1. validate override keys/values (defense in depth over spec validation)
//  2. reject (batched) any override that targets a protected key  -> 422
//  3. resolve base entries (inline value or secretRef)
//  4. apply overrides (override wins for non-protected keys)
//  5. reject if any required key is unsatisfied                    -> 422
//  6. validate every final value is control-char free (app.env integrity)
//
// resolve may be nil if no entry uses a secretRef.
func MergeEnv(base *BaseEnv, overrides map[string]string, resolve SecretResolver) (map[string]string, error) {
	protected := base.ProtectedKeys()

	// 1 + 2: validate overrides and collect protected collisions.
	var collisions []string
	for k, v := range overrides {
		if !validKey(k) {
			return nil, &Error{Code: "value", Message: fmt.Sprintf("invalid override key %q", k)}
		}
		if i := strings.IndexFunc(v, isControl); i >= 0 {
			return nil, &Error{Code: "value", Message: fmt.Sprintf("override %q has a control char at byte %d", k, i)}
		}
		if protected[k] {
			collisions = append(collisions, k)
		}
	}
	if len(collisions) > 0 {
		sort.Strings(collisions)
		return nil, &Error{Code: "protected", Message: "event tried to override protected keys: " + strings.Join(collisions, ", ")}
	}

	// 3: resolve base.
	merged := make(map[string]string, len(base.Entries)+len(overrides))
	for _, e := range base.Entries {
		switch {
		case e.Value != nil:
			merged[e.Key] = *e.Value
		case e.SecretRef != "":
			if resolve == nil {
				return nil, &Error{Code: "secret", Message: fmt.Sprintf("no resolver for secretRef key %q", e.Key)}
			}
			val, err := resolve(e.SecretRef)
			if err != nil {
				return nil, err
			}
			merged[e.Key] = val
		}
		// required-only entries with no value contribute nothing here; the override
		// must supply them (checked below).
	}

	// 4: apply overrides.
	for k, v := range overrides {
		merged[k] = v
	}

	// 5: required satisfaction.
	var missing []string
	for _, e := range base.Entries {
		if e.Required {
			if _, ok := merged[e.Key]; !ok {
				missing = append(missing, e.Key)
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, &Error{Code: "required", Message: "missing required env keys: " + strings.Join(missing, ", ")}
	}

	// 6: final integrity check (catches a newline smuggled in via a secretRef file).
	for k, v := range merged {
		if i := strings.IndexFunc(v, isControl); i >= 0 {
			return nil, &Error{Code: "value", Message: fmt.Sprintf("final value for %q has a control char at byte %d (multiline secrets must be mounted as files, not env)", k, i)}
		}
	}
	return merged, nil
}

// Render serializes a merged env to deterministic, sorted KEY=VALUE lines for app.env.
// Determinism gives a stable hash for env-aware idempotency.
func Render(merged map[string]string) []byte {
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(merged[k])
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }
