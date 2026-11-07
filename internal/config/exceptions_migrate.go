// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/tools/secrets/rules"
	"go.yaml.in/yaml/v3"
)

// ExceptionMigration describes one legacy entry, including unresolved entries left untouched.
type ExceptionMigration struct {
	Entry      Exception
	Source     string
	Unresolved string
}

// ExceptionMigrationPlan retains the exact source bytes alongside the proposed manifest and entry migrations.
type ExceptionMigrationPlan struct {
	Source    []byte
	Data      []byte
	Proposals []ExceptionMigration
}

// PlanExceptionMigration reads a manifest without requiring its legacy exceptions to validate.
// It returns a comment-preserving YAML edit and one proposal per entry missing both fields.
func PlanExceptionMigration(ctx context.Context, path string, today time.Time) ([]byte, []ExceptionMigration, error) {
	plan, err := PlanExceptionMigrationSnapshot(ctx, path, today)
	return plan.Data, plan.Proposals, err
}

// PlanExceptionMigrationSnapshot plans migrations and retains the parsed source for a pre-write comparison.
func PlanExceptionMigrationSnapshot(ctx context.Context, path string, today time.Time) (ExceptionMigrationPlan, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	data, err := readConfigFile(path)
	if err != nil {
		return ExceptionMigrationPlan{}, fmt.Errorf("read migration manifest: %w", err)
	}
	encoded, proposals, err := planExceptionMigration(ctx, path, data, today)
	return ExceptionMigrationPlan{Source: data, Data: encoded, Proposals: proposals}, err
}

// planExceptionMigration derives a comment-preserving migration from the supplied manifest snapshot.
func planExceptionMigration(ctx context.Context, path string, data []byte, today time.Time) ([]byte, []ExceptionMigration, error) {
	if _, err := DecodeManifest(data); err != nil {
		return nil, nil, fmt.Errorf("decode migration manifest: %w", err)
	}
	root, err := decodePolicyDocument(ctx, data)
	if err != nil {
		return nil, nil, fmt.Errorf("decode migration YAML: %w", err)
	}
	entries := policyMember(root, "exceptions")
	if entries == nil {
		return data, nil, nil
	}
	if entries.Kind != yaml.SequenceNode || len(entries.Content) > MaxExceptions {
		return nil, nil, fmt.Errorf("exceptions must be a sequence of at most %d entries", MaxExceptions)
	}
	proposals, err := migrateExceptionNodes(ctx, path, entries, today)
	if err != nil || len(proposals) == 0 {
		return data, proposals, err
	}
	encoded, err := util.EncodeYAMLDocument(root)
	if err != nil {
		return nil, proposals, fmt.Errorf("encode migrated exceptions: %w", err)
	}
	return encoded, proposals, nil
}

// migrateExceptionNodes walks exception YAML nodes and applies proposed migrations to entries lacking category or entered fields.
func migrateExceptionNodes(ctx context.Context, path string, entries *yaml.Node, today time.Time) ([]ExceptionMigration, error) {
	var proposals []ExceptionMigration
	for i := 0; i < len(entries.Content) && i < MaxExceptions; i++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("migrate exceptions: %w", err)
		}
		node := entries.Content[i]
		var entry Exception
		if err := node.Decode(&entry); err != nil {
			return nil, fmt.Errorf("decode exception %d: %w", i, err)
		}
		if entry.Category != "" || entry.Entered != "" {
			continue
		}
		proposal := proposeExceptionMigration(ctx, path, node.Line, entry, today)
		proposals = append(proposals, proposal)
		if proposal.Unresolved != "" {
			continue
		}
		// Encode through the shared YAML encoder; node edits retain comments and mapping order.
		for _, field := range []struct{ key, value string }{{"category", proposal.Entry.Category}, {"entered", proposal.Entry.Entered}} {
			setMigrationScalar(node, field.key, field.value)
		}
	}
	return proposals, nil
}

// setMigrationScalar updates or appends a key-value scalar pair in a YAML mapping node.
func setMigrationScalar(node *yaml.Node, key, value string) {
	if existing := policyMember(node, key); existing != nil {
		existing.Kind, existing.Tag, existing.Value = yaml.ScalarNode, "!!str", value
		return
	}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

// proposeExceptionMigration determines the entered date and expiration category for an unmigrated exception.
func proposeExceptionMigration(ctx context.Context, path string, line int, entry Exception, today time.Time) ExceptionMigration {
	// Keep the source alongside the date; a fallback is always visible in the command output.
	entered, source := exceptionEntered(ctx, path, line, today)
	entry.Entered = entered
	category := entry.ExpiryCategory()
	if category != "" {
		entry.Category = category
		return ExceptionMigration{Entry: entry, Source: source, Unresolved: rules.CategoryExpiryProblem(category, entered, entry.Expires, today)}
	}
	for _, candidate := range migrationCategories() {
		if rules.CategoryExpiryProblem(candidate, entered, entry.Expires, today) == "" {
			entry.Category = candidate
			return ExceptionMigration{Entry: entry, Source: source}
		}
	}
	return ExceptionMigration{Entry: entry, Source: source, Unresolved: "no category admits existing expiry"}
}

// migrationCategories returns known exception categories sorted in ascending order of duration.
func migrationCategories() []string {
	categories := []string{"A", "B", "C", "D"}
	slices.SortFunc(categories, func(a, b string) int {
		aDays, _ := rules.CategoryDays(a)
		bDays, _ := rules.CategoryDays(b)
		return cmp.Compare(aDays, bDays)
	})
	return categories
}

// exceptionEntered resolves an exception line's creation date and provenance via git blame or falls back to today.
func exceptionEntered(ctx context.Context, path string, line int, today time.Time) (string, string) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result, err := util.RunGitBytes(ctx, filepath.Dir(path), 8192,
		"blame", "--line-porcelain", "-L", fmt.Sprintf("%d,+1", line), "--", filepath.Base(path))
	if err != nil {
		return ExceptionDay(today).Format(ExceptionDateLayout), "today (git history unavailable)"
	}
	return enteredFromBlame(string(result.Stdout), today)
}

// enteredFromBlame extracts the commit date and commit hash from git porcelain blame output.
func enteredFromBlame(output string, today time.Time) (string, string) {
	lines := strings.SplitN(output, "\n", 128)
	header := strings.Fields(lines[0])
	if len(header) == 0 || strings.Trim(header[0], "0") == "" {
		return ExceptionDay(today).Format(ExceptionDateLayout), "today (entry uncommitted)"
	}
	for i := 1; i < len(lines) && i < 128; i++ {
		if stamp, ok := strings.CutPrefix(lines[i], "committer-time "); ok {
			seconds, err := strconv.ParseInt(stamp, 10, 64)
			if err == nil {
				return ExceptionDay(time.Unix(seconds, 0)).Format(ExceptionDateLayout), "git-blame:" + header[0]
			}
		}
	}
	return ExceptionDay(today).Format(ExceptionDateLayout), "today (git commit date unavailable)"
}
