package changelog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

const renderJournalName = ".praetor-changelog-render.json"

type renderJournal struct {
	Protocol     int                `json:"protocol"`
	Version      string             `json:"version"`
	Date         string             `json:"date"`
	BeforeExists bool               `json:"before_exists"`
	BeforeSHA256 string             `json:"before_sha256"`
	AfterSHA256  string             `json:"after_sha256"`
	Section      string             `json:"section"`
	Fragments    []fragmentSnapshot `json:"fragments"`
}

func contentHash(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func loadRenderJournal(ctx context.Context, root *os.Root) (*renderJournal, []byte, error) {
	raw, exists, err := contextopt.ObserveRootSnapshot(ctx, root, renderJournalName)
	if err != nil || !exists {
		return nil, nil, err
	}
	var journal renderJournal
	if err := json.Unmarshal(raw, &journal, json.RejectUnknownMembers(true)); err != nil {
		return nil, nil, fmt.Errorf("invalid retained changelog render journal: %w", err)
	}
	if err := validateRenderJournal(journal); err != nil {
		return nil, nil, err
	}
	return &journal, raw, nil
}

func validateRenderJournal(journal renderJournal) error {
	if journal.Protocol != 1 || journal.Version == "" || journal.Date == "" || journal.Section == "" {
		return errors.New("retained render journal lacks required identity")
	}
	if !validContentHash(journal.BeforeSHA256) || !validContentHash(journal.AfterSHA256) {
		return errors.New("retained render journal has invalid changelog hashes")
	}
	return validateJournalFragments(journal.Fragments)
}

func validateJournalFragments(fragments []fragmentSnapshot) error {
	if len(fragments) == 0 || len(fragments) > maxFragmentEntries {
		return errors.New("retained render journal has invalid fragment count")
	}
	seen := make(map[string]bool)
	for _, fragment := range fragments {
		if !fragmentFilename(fragment.Name) || seen[fragment.Name] || !validContentHash(fragment.SHA256) {
			return errors.New("retained render journal has invalid or duplicate fragment identity")
		}
		seen[fragment.Name] = true
	}
	return nil
}

func validContentHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func prepareRenderJournal(ctx context.Context, repoPath string, repo, fragmentsRoot *os.Root, version, date string) (*renderJournal, []byte, error) {
	fragments, snapshots, err := loadFragmentSnapshots(ctx, fragmentsRoot)
	if err != nil || len(fragments) == 0 {
		return nil, nil, err
	}
	before, exists, err := contextopt.ObserveRootSnapshot(ctx, repo, "CHANGELOG.md")
	if err != nil {
		return nil, nil, err
	}
	if version == "" || strings.Contains(string(before), "## ["+version+"] - ") {
		return nil, nil, errors.New("release version is empty or already published without a recovery journal")
	}
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02")
	}
	section := buildReleaseSection(fragments, version, date)
	after := spliceChangelog(before, exists, section)
	if len(after) > contextopt.MaxSourceBytes {
		return nil, nil, errors.New("rendered changelog exceeds snapshot byte limit")
	}
	journal := &renderJournal{Protocol: 1, Version: version, Date: date, BeforeExists: exists,
		BeforeSHA256: contentHash(before), AfterSHA256: contentHash(after), Section: section, Fragments: snapshots}
	raw, err := json.Marshal(journal)
	if err != nil {
		return nil, nil, err
	}
	path := filepath.Join(repoPath, renderJournalName)
	if err := contextopt.ReplaceSnapshot(ctx, path, raw, contextopt.ReplaceOptions{Mode: 0o600}); err != nil {
		return nil, nil, fmt.Errorf("retain changelog render journal: %w", err)
	}
	return journal, raw, nil
}
