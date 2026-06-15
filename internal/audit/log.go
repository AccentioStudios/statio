// Package audit writes a per-deploy, append-only JSONL record to the server so the CLI
// (`push logs`) and operators can see what deployed, when, by whom, and how each stage
// fared. Records are REDACTED by construction: they are built from the already-sanitized
// deploy stages (no env values, no Bearer tokens, no raw compose output — invariant #24).
package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

// maxSize triggers a single-generation rotation (path -> path.1) so the log stays bounded
// while preserving append-only semantics.
const maxSize = 5 << 20

// Stage mirrors a deploy stage outcome (kept independent so audit does not import deploy).
type Stage struct {
	Stage   string `json:"stage"`
	Status  string `json:"status"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

// Record is one deploy attempt.
type Record struct {
	TS         string  `json:"ts"`
	Service    string  `json:"service"`
	DeploySeq  int64   `json:"deploy_seq"`
	DeployID   string  `json:"deploy_id,omitempty"`
	Identity   string  `json:"identity,omitempty"` // the trusted signer identity (not a secret)
	Src        string  `json:"src,omitempty"`      // tailnet caller (WhoIs login)
	Digest     string  `json:"digest"`
	Outcome    string  `json:"outcome"`
	DurationMS int64   `json:"duration_ms"`
	Stages     []Stage `json:"stages,omitempty"`
}

// Append writes one record as a JSON line (0600), rotating once if the file grew too large.
func Append(path string, rec Record) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxSize {
		_ = os.Rename(path, path+".1")
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}

// Tail returns up to n most-recent records (oldest-first), for `push logs` and the
// read-only endpoint.
func Tail(path string, n int) ([]Record, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var recs []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			recs = append(recs, r)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if n > 0 && len(recs) > n {
		recs = recs[len(recs)-n:]
	}
	return recs, nil
}
