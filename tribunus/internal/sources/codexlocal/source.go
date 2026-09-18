// Package codexlocal reads the Codex CLI's own subscription usage window
// from the newest local session log. It is read-only: it never writes to
// the sessions directory.
//
// Session logs are JSON Lines under ~/.codex/sessions/**/*.jsonl. Each line
// that reports usage has the shape (verified live against a real session
// log on this workstation, 2026-09-18):
//
//	{"timestamp":"...","ordinal":N,"type":"event_msg",
//	 "payload":{"type":"...","info":{...},
//	            "rate_limits":{"limit_id":"codex","limit_name":null,
//	              "primary":{"used_percent":92.0,"window_minutes":10080,"resets_at":1789858494},
//	              "secondary":null,
//	              "credits":{"has_credits":false,"unlimited":false,"balance":"0"},
//	              "plan_type":"pro"}}}
//
// resets_at is Unix seconds. Fetch reports the *primary* window only in
// slice 1 (design scope); the secondary window and credits balance are
// parsed but not yet surfaced as catalog fields.
package codexlocal

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/tribunus/catalog"
)

// SourceName identifies this source in Snapshot.SourceRuns and CLI flags.
const SourceName = "codex-local"

const (
	// maxSessionFilesScanned bounds the directory walk (HISS-02).
	maxSessionFilesScanned = 20000
	// maxSessionFileBytes bounds how much of the newest session file Fetch
	// will read. Session logs run a few MB in practice; this is generous
	// but finite (HISS-02).
	maxSessionFileBytes = 128 << 20
	// maxScanLines bounds the line-scan loop over the newest session file
	// (HISS-02).
	maxScanLines = 500000
	// maxLineBytes bounds a single JSONL line the scanner will accept.
	maxLineBytes = 4 << 20
)

// Result is one Fetch attempt's outcome.
type Result struct {
	Records []catalog.Record
	Status  catalog.Status
	Count   int
	Detail  string
}

// DefaultSessionsDir returns ~/.codex/sessions. Callers pass its own
// sessionsDir explicitly to Fetch so tests never depend on $HOME.
func DefaultSessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("codex-local: resolve home directory: %w", err)
	}
	return filepath.Join(home, ".codex", "sessions"), nil
}

// Fetch finds the newest *.jsonl session file under sessionsDir and turns
// its last rate_limits.primary window into one catalog.Record. A missing
// directory, a directory with no session files, or a newest session that
// never logged rate_limits are all reported as StatusSkip with a reason,
// never as a guessed record.
func Fetch(ctx context.Context, sessionsDir string) Result {
	newest, found, err := newestSessionFile(ctx, sessionsDir)
	if err != nil {
		return Result{Status: catalog.StatusFail, Detail: err.Error()}
	}
	if !found {
		return Result{Status: catalog.StatusSkip, Detail: fmt.Sprintf("no *.jsonl session files under %s", sessionsDir)}
	}

	rl, err := latestRateLimits(ctx, newest)
	if err != nil {
		return Result{Status: catalog.StatusFail, Detail: err.Error()}
	}
	if rl == nil {
		return Result{Status: catalog.StatusSkip, Detail: fmt.Sprintf("newest session %s has no rate_limits entries", newest)}
	}
	if rl.Primary == nil {
		return Result{Status: catalog.StatusSkip, Detail: fmt.Sprintf("newest session %s rate_limits carries no primary window", newest)}
	}

	rec := toRecord(*rl, time.Now().UTC())
	return Result{Records: []catalog.Record{rec}, Status: catalog.StatusOK, Count: 1}
}

// newestSessionFile walks sessionsDir and returns the *.jsonl path with the
// latest modification time. A missing directory is reported as found=false,
// err=nil: that is a skip, not a failure.
func newestSessionFile(ctx context.Context, root string) (path string, found bool, err error) {
	var (
		best     string
		bestTime time.Time
		scanned  int
	)
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if d.IsDir() {
			return nil
		}
		scanned++
		if scanned > maxSessionFilesScanned {
			return fmt.Errorf("codex-local: %s exceeds %d files scanned", root, maxSessionFilesScanned)
		}
		if filepath.Ext(p) != ".jsonl" {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		if info.ModTime().After(bestTime) {
			bestTime = info.ModTime()
			best = p
		}
		return nil
	})
	if walkErr != nil {
		if errors.Is(walkErr, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("codex-local: walk %s: %w", root, walkErr)
	}
	return best, best != "", nil
}

// sessionLine is the JSONL envelope; only the fields Fetch needs are typed.
type sessionLine struct {
	Payload struct {
		RateLimits *rateLimits `json:"rate_limits"`
	} `json:"payload"`
}

type rateLimits struct {
	LimitID  string  `json:"limit_id"`
	PlanType string  `json:"plan_type"`
	Primary  *window `json:"primary"`
}

type window struct {
	UsedPercent   *float64 `json:"used_percent"`
	WindowMinutes *int     `json:"window_minutes"`
	ResetsAt      *int64   `json:"resets_at"`
}

// latestRateLimits scans path and returns the last non-null rate_limits
// object logged, or nil if the file has none. Malformed lines are skipped:
// a session log can carry lines this parser does not model, and skipping
// them is correct, not a data loss, because only rate_limits lines matter.
func latestRateLimits(ctx context.Context, path string) (*rateLimits, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("codex-local: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("codex-local: stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("codex-local: %s is not a regular file", path)
	}
	if info.Size() > maxSessionFileBytes {
		return nil, fmt.Errorf("codex-local: %s exceeds %d bytes", path, maxSessionFileBytes)
	}

	scanner := bufio.NewScanner(io.LimitReader(f, maxSessionFileBytes+1))
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	var latest *rateLimits
	lines := 0
	for scanner.Scan() {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		lines++
		if lines > maxScanLines {
			return nil, fmt.Errorf("codex-local: %s exceeds %d lines scanned", path, maxScanLines)
		}
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		var line sessionLine
		if jsonErr := json.Unmarshal(raw, &line); jsonErr != nil {
			continue // not every line is a rate_limits-bearing event_msg
		}
		if line.Payload.RateLimits != nil {
			latest = line.Payload.RateLimits
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return nil, fmt.Errorf("codex-local: scan %s: %w", path, scanErr)
	}
	return latest, nil
}

// toRecord turns a parsed rate_limits payload into one catalog.Record
// describing the Codex CLI subscription's usage window. There is no single
// "model" behind a CLI-wide rate limit, so the record id names the CLI and
// the limit bucket it measured.
func toRecord(rl rateLimits, fetchedAt time.Time) catalog.Record {
	limitID := rl.LimitID
	if limitID == "" {
		limitID = "unknown"
	}
	rec := catalog.Record{
		ModelID:    "codex-cli/" + limitID,
		Provider:   "openai",
		AccessPath: catalog.AccessSubscriptionCLI,
		Provenance: catalog.Provenance{
			Source:    SourceName,
			FetchedAt: fetchedAt,
			Kind:      catalog.KindMeasured,
		},
		Absent: map[string]string{
			"context_window":  "not reported by codex session rate_limits",
			"price_in_per_m":  "subscription plan, not metered pricing",
			"price_out_per_m": "subscription plan, not metered pricing",
			"limits":          "rate_limits reports a usage window, not rpm/tpm caps",
		},
	}
	uw := &catalog.UsageWindow{}
	if rl.Primary.UsedPercent != nil {
		uw.UsedPercent = rl.Primary.UsedPercent
	}
	if rl.Primary.WindowMinutes != nil {
		uw.WindowMinutes = rl.Primary.WindowMinutes
	}
	if rl.Primary.ResetsAt != nil {
		t := time.Unix(*rl.Primary.ResetsAt, 0).UTC()
		uw.ResetsAt = &t
	}
	rec.UsageWindow = uw
	if rl.PlanType != "" {
		rec.Capabilities = []string{"plan:" + rl.PlanType}
	}
	return rec
}
