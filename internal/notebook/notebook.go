// Package notebook prepares immutable NotebookLM source snapshots for planning.
// Imported sources are data, never executable templates or agent instructions.
package notebook

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const Format = "praetor-notebook-v1"
const MaxSources = 64
const MaxContentBytes = 128 << 10

type Source struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Role    string `json:"role"`
	Locator string `json:"locator"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
}

// Bundle is a Praetor interchange format, not a claimed Google export schema.
// Coverage explicitly records omissions (e.g. studio artifacts not exported).
type Bundle struct {
	Format     string   `json:"format"`
	NotebookID string   `json:"notebook_id"`
	Title      string   `json:"title"`
	CapturedAt string   `json:"captured_at"`
	Connector  string   `json:"connector"`
	Coverage   []string `json:"coverage"`
	Sources    []Source `json:"sources"`
}

type Prepared struct {
	Format         string            `json:"format"`
	BundleSHA256   string            `json:"bundle_sha256"`
	SourceCount    int               `json:"source_count"`
	SourceBytes    int               `json:"source_bytes"`
	ReviewRequired bool              `json:"review_required"`
	Warnings       []string          `json:"warnings"`
	PromptSHA256   string            `json:"prompt_sha256"`
	Files          map[string][]byte `json:"-"`
}

func Digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

// Prepare preserves exact source bytes and creates reusable templates plus a
// cited generation prompt. It does not claim semantic synthesis or live access.
func Prepare(ctx context.Context, raw []byte) (*Prepared, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var bundle Bundle
	if err := Decode(raw, &bundle); err != nil {
		return nil, err
	}
	if err := validateBundle(bundle); err != nil {
		return nil, err
	}
	pack := &Prepared{Format: "praetor-notebook-prepared-v1", BundleSHA256: Digest(raw),
		SourceCount: len(bundle.Sources), ReviewRequired: true,
		Warnings: append([]string{"Source citations and semantic requirements need review; no model generation has run."}, bundle.Coverage...)}
	seen := make(map[string]string)
	for _, source := range bundle.Sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pack.SourceBytes += len(source.Content)
		if previous, ok := seen[source.SHA256]; ok {
			pack.Warnings = append(pack.Warnings, "Duplicate content: "+previous+" and "+source.ID+"; both identities retained.")
		}
		seen[source.SHA256] = source.ID
	}
	pack.Files = templateFiles()
	pack.Files["sources.json"] = append([]byte(nil), raw...)
	pack.Files["prompt.md"] = []byte(generationPrompt(pack.BundleSHA256))
	pack.PromptSHA256 = Digest(pack.Files["prompt.md"])
	manifest, err := json.MarshalIndent(pack, "", "  ")
	if err != nil {
		return nil, err
	}
	pack.Files["manifest.json"] = manifest
	return pack, nil
}

func validateBundle(b Bundle) error {
	if b.Format != Format || !identifier(b.NotebookID) || !label(b.Title) || !label(b.Connector) {
		return fmt.Errorf("valid format, notebook identity, title and connector required")
	}
	if _, err := time.Parse(time.RFC3339, b.CapturedAt); err != nil {
		return fmt.Errorf("captured_at: %w", err)
	}
	if len(b.Sources) == 0 || len(b.Sources) > MaxSources || len(b.Coverage) == 0 || len(b.Coverage) > 16 {
		return fmt.Errorf("1..64 sources and 1..16 explicit coverage statements required")
	}
	for _, statement := range b.Coverage {
		if !label(statement) {
			return fmt.Errorf("invalid coverage statement")
		}
	}
	seen := make(map[string]bool)
	for _, s := range b.Sources {
		if err := validateSource(s); err != nil {
			return err
		}
		if seen[s.ID] {
			return fmt.Errorf("duplicate source id %s", s.ID)
		}
		seen[s.ID] = true
	}
	return nil
}

func validateSource(s Source) error {
	if !identifier(s.ID) || !label(s.Title) || !label(s.Locator) {
		return fmt.Errorf("invalid source identity or provenance")
	}
	if s.Role != "source" && s.Role != "planning_artifact" {
		return fmt.Errorf("unsupported source role")
	}
	if len(strings.TrimSpace(s.Content)) == 0 || len(s.Content) > MaxContentBytes || strings.ContainsRune(s.Content, 0) {
		return fmt.Errorf("source %s must contain 1..%d UTF-8 bytes", s.ID, MaxContentBytes)
	}
	if s.SHA256 != Digest([]byte(s.Content)) {
		return fmt.Errorf("source %s digest mismatch", s.ID)
	}
	return nil
}

func identifier(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func label(s string) bool { return len(s) > 0 && len(s) <= 512 && !strings.ContainsAny(s, "\x00\r\n") }
