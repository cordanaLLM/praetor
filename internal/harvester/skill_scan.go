package harvester

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

var errSkillEntryBound = errors.New("skill directory exceeds entry bound")

// readSkillEntries pins the directory before reading at most one bounded page.
func readSkillEntries(ctx context.Context, path string) (entries []os.DirEntry, err error) {
	root, err := contextopt.OpenDirectory(ctx, path)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	entries, err = dir.ReadDir(MaxSkillsScan + 1)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(entries) > MaxSkillsScan {
		return entries[:MaxSkillsScan], fmt.Errorf("%w (%d)", errSkillEntryBound, MaxSkillsScan)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

func skillRootResult(root skillRoot, err error) SkillRootStatus {
	status := SkillRootStatus{Path: root.dir, Origin: root.origin, Configured: root.configured, Status: "scanned"}
	if err == nil {
		return status
	}
	status.Status, status.Error = "failed", err.Error()
	if errors.Is(err, os.ErrNotExist) && !root.configured {
		status.Status, status.Error = "not_applicable", ""
	}
	if errors.Is(err, errSkillEntryBound) {
		status.Status = "truncated"
	}
	return status
}

func scanSkillDir(ctx context.Context, root skillRoot, registry map[string][]SkillLocation) (SkillRootStatus, error) {
	entries, readErr := readSkillEntries(ctx, root.dir)
	status := skillRootResult(root, readErr)
	if status.Status == "not_applicable" {
		return status, nil
	}
	if readErr != nil {
		return status, fmt.Errorf("read skill root %s: %w", root.dir, readErr)
	}
	var scanErr error
	for i := 0; i < len(entries) && i < MaxSkillsScan; i++ {
		if err := ctx.Err(); err != nil {
			scanErr = errors.Join(scanErr, err)
			break
		}
		status.EntriesExamined++
		entry := entries[i]
		if !entry.IsDir() {
			continue
		}
		found, err := inspectSkillManifest(ctx, root.dir, entry.Name())
		if err != nil {
			scanErr = errors.Join(scanErr, err)
			continue
		}
		if !found {
			continue
		}
		path := filepath.Join(root.dir, entry.Name(), "SKILL.md")
		registry[entry.Name()] = append(registry[entry.Name()], SkillLocation{Path: path, Origin: root.origin})
		status.SkillsFound++
	}
	if scanErr != nil {
		status.Status, status.Error = "failed", scanErr.Error()
	}
	return status, scanErr
}

func inspectSkillManifest(ctx context.Context, path, name string) (found bool, err error) {
	root, err := contextopt.OpenDirectory(ctx, filepath.Join(path, name))
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	info, err := root.Lstat("SKILL.md")
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("skill file is not regular: %s", filepath.Join(path, name, "SKILL.md"))
	}
	return true, nil
}

func scanGeminiBackups(ctx context.Context, path string, report *SkillAuditReport) error {
	entries, err := readSkillEntries(ctx, path)
	status := skillRootResult(skillRoot{dir: path, origin: "gemini-backups"}, err)
	if status.Status == "not_applicable" {
		err = nil
	}
	if err == nil {
		for i := 0; i < len(entries) && i < MaxSkillsScan; i++ {
			if scanErr := ctx.Err(); scanErr != nil {
				err = scanErr
				status.Status, status.Error = "failed", scanErr.Error()
				break
			}
			status.EntriesExamined++
			if strings.HasPrefix(entries[i].Name(), "GEMINI.md.bak-") {
				report.StaleBackups = append(report.StaleBackups, entries[i].Name())
			}
		}
	}
	report.RootStatuses = append(report.RootStatuses, status)
	return err
}
