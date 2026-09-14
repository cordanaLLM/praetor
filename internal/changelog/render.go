package changelog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// ErrRenderTextUnrepresentable reports a version or date that cannot be recorded in the
// recovery journal or written to the changelog.
var ErrRenderTextUnrepresentable = errors.New("changelog render arguments must be UTF-8 text without NUL bytes")

// RenderTextRepresentable reports whether a release argument can be journaled and written.
// It mirrors the constraint the snapshot writer enforces (internal/contextopt validateText),
// so the boundary refuses exactly what the storage layer would refuse, rather than a
// narrower subset that lets the difference surface later as an opaque write failure.
func RenderTextRepresentable(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

// validateRenderText refuses release arguments the recovery journal cannot represent.
func validateRenderText(version, date string) error {
	if !RenderTextRepresentable(version) {
		return fmt.Errorf("%w: version", ErrRenderTextUnrepresentable)
	}
	if !RenderTextRepresentable(date) {
		return fmt.Errorf("%w: date", ErrRenderTextUnrepresentable)
	}
	return nil
}

// RenderReleaseContext publishes once and safely resumes interrupted fragment
// cleanup. The retained journal binds recovery to exact input and output hashes.
// Changed fragments or changelogs are preserved and require reconciliation.
func RenderReleaseContext(ctx context.Context, repoPath, version, date string) (err error) {
	if ctx == nil {
		return errors.New("changelog rendering requires a context")
	}
	// The version and date are written verbatim into the recovery journal, which is JSON.
	// Rejecting them here names the offending argument; without this the render fails deep
	// inside journal serialization with a jsontext offset that identifies nothing a caller
	// can act on, after the fragment lock has already been taken.
	if err := validateRenderText(version, date); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	fragmentsRoot, absent, err := openFragments(ctx, repoPath)
	if err != nil {
		return err
	}
	if absent {
		return checkAbsentFragments(ctx, repoPath)
	}
	defer func() { err = errors.Join(err, fragmentsRoot.Close()) }()
	unlock, err := contextopt.LockDirectory(ctx, fragmentsRoot)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	repo, err := contextopt.OpenDirectory(ctx, repoPath)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, repo.Close()) }()
	return renderWithRoots(ctx, repoPath, repo, fragmentsRoot, version, date)
}

func checkAbsentFragments(ctx context.Context, repoPath string) error {
	_, exists, err := contextopt.ObserveSnapshot(ctx, filepath.Join(repoPath, renderJournalName))
	if err != nil {
		return err
	}
	if exists {
		return errors.New("pending render journal exists but fragment directory is missing")
	}
	return nil
}

func renderWithRoots(ctx context.Context, repoPath string, repo, fragmentsRoot *os.Root, version, date string) error {
	journal, raw, err := loadRenderJournal(ctx, repo)
	if err != nil {
		return err
	}
	if journal == nil {
		journal, raw, err = prepareRenderJournal(ctx, repoPath, repo, fragmentsRoot, version, date)
	}
	if err != nil || journal == nil {
		return err
	}
	if journal.Version != version || (date != "" && journal.Date != date) {
		return errors.New("pending changelog render belongs to another version or date; resume that render first")
	}
	if err := publishJournalChangelog(ctx, repoPath, repo, fragmentsRoot, journal); err != nil {
		return err
	}
	if err := verifyPublishedChangelog(ctx, repo, journal); err != nil {
		return err
	}
	if err := removeRenderedFragments(ctx, fragmentsRoot, journal.Fragments); err != nil {
		return err
	}
	return removeRenderJournal(ctx, repo, raw)
}

func verifyPublishedChangelog(ctx context.Context, root *os.Root, journal *renderJournal) error {
	content, err := contextopt.ReadRootSnapshot(ctx, root, "CHANGELOG.md")
	if err != nil {
		return err
	}
	if contentHash(content) != journal.AfterSHA256 {
		return errors.New("published changelog changed before fragment cleanup; journal and fragments retained")
	}
	return nil
}

func publishJournalChangelog(ctx context.Context, repoPath string, repo, fragmentsRoot *os.Root, journal *renderJournal) error {
	current, exists, err := observeRenderFile(ctx, repo, "CHANGELOG.md")
	if err != nil {
		return err
	}
	if exists && contentHash(current) == journal.AfterSHA256 {
		return nil // Publication already succeeded; resume only verified cleanup.
	}
	if exists != journal.BeforeExists || contentHash(current) != journal.BeforeSHA256 {
		return errors.New("changelog changed since render preparation; journal and fragments retained")
	}
	if err := verifyPendingFragments(ctx, fragmentsRoot, journal.Fragments); err != nil {
		return err
	}
	updated := spliceChangelog(current, exists, journal.Section)
	if contentHash(updated) != journal.AfterSHA256 {
		return errors.New("retained render section does not match expected output hash")
	}
	return contextopt.ReplaceSnapshot(ctx, filepath.Join(repoPath, "CHANGELOG.md"), updated,
		contextopt.ReplaceOptions{Expected: current, Exists: exists, Mode: 0o644})
}

func verifyPendingFragments(ctx context.Context, root *os.Root, fragments []fragmentSnapshot) error {
	for _, fragment := range fragments {
		data, err := contextopt.ReadRootSnapshot(ctx, root, fragment.Name)
		if err != nil {
			return fmt.Errorf("verify pending fragment %s: %w", fragment.Name, err)
		}
		if contentHash(data) != fragment.SHA256 {
			return fmt.Errorf("pending fragment changed: %s; render journal retained", fragment.Name)
		}
	}
	return nil
}

func removeRenderedFragments(ctx context.Context, root *os.Root, fragments []fragmentSnapshot) error {
	for _, fragment := range fragments {
		data, exists, err := observeRenderFile(ctx, root, fragment.Name)
		if err != nil {
			return err
		}
		if !exists {
			continue // A previous recovery attempt already removed this rendered input.
		}
		if contentHash(data) != fragment.SHA256 {
			return fmt.Errorf("rendered fragment changed: %s; refusing deletion and retaining journal", fragment.Name)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := root.Remove(fragment.Name); err != nil {
			return fmt.Errorf("remove rendered fragment %s (resume matching render): %w", fragment.Name, err)
		}
	}
	return contextopt.SyncDirectory(ctx, root)
}

func removeRenderJournal(ctx context.Context, root *os.Root, expected []byte) error {
	current, err := contextopt.ReadRootSnapshot(ctx, root, renderJournalName)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, expected) {
		return errors.New("render journal changed before cleanup; retained for inspection")
	}
	if err := root.Remove(renderJournalName); err != nil {
		return err
	}
	return contextopt.SyncDirectory(ctx, root)
}
