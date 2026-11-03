// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/tools/secrets/rules"
)

// migrationManifest writes a temporary test manifest containing the specified exception entries.
func migrationManifest(t *testing.T, entries string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ManifestFileName)
	if err := os.WriteFile(path, []byte("# manifest comment\nversion: 1\nexceptions:\n"+entries), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestExceptionMigrationDecodesLegacyValuesWithoutPolicyValidation pins decoding of legacy exceptions without failing policy validation.
func TestExceptionMigrationDecodesLegacyValuesWithoutPolicyValidation(t *testing.T) {
	today := ExceptionDay(time.Now())
	for _, days := range []int{0, 91, 121} {
		path := migrationManifest(t, legacyMigrationEntry(days, "legacy migration", today))
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeManifest(before); err != nil {
			t.Fatalf("structural decoder refused legacy values: %v", err)
		}
		if _, err := ParseManifest(path, before); (err != nil) != (days > 90) {
			t.Fatalf("unexpected legacy policy validation for %d days: %v", days, err)
		}
		_, proposals, err := PlanExceptionMigration(t.Context(), path, today)
		if err != nil || len(proposals) != 1 {
			t.Fatalf("legacy values never reached planning: proposals=%+v err=%v", proposals, err)
		}
		if (proposals[0].Unresolved != "") != (days > 120) {
			t.Fatalf("wrong legacy outcome: %+v", proposals[0])
		}
	}
}

// legacyMigrationEntry formats a legacy exception entry with the given duration, reason, and base date.
func legacyMigrationEntry(days int, reason string, today time.Time) string {
	return fmt.Sprintf("  - rule: declared-linters # entry comment\n    tool: semgrep\n    reason: %s\n    expires: %s # expiry comment\n",
		util.YAMLScalar(reason), today.AddDate(0, 0, days).Format(ExceptionDateLayout))
}

// TestExceptionMigrationCategoryBoundaries pins category assignment boundaries across varied exception durations.
func TestExceptionMigrationCategoryBoundaries(t *testing.T) {
	today := ExceptionDay(time.Now())
	for _, tc := range []struct {
		days int
		want string
	}{{0, "B"}, {3, "B"}, {4, "C"}, {10, "C"}, {11, "A"}, {90, "A"}, {91, "D"}, {120, "D"}, {121, ""}} {
		t.Run(fmt.Sprint(tc.days), func(t *testing.T) {
			path := migrationManifest(t, legacyMigrationEntry(tc.days, "migration", today))
			data, proposals, err := PlanExceptionMigration(t.Context(), path, today)
			if err != nil || len(proposals) != 1 {
				t.Fatalf("proposals=%v err=%v", proposals, err)
			}
			p := proposals[0]
			if p.Entry.Category != tc.want || (p.Unresolved != "") != (tc.want == "") {
				t.Fatalf("proposal=%+v want category=%s", p, tc.want)
			}
			assertMigrationValid(t, path, data, p, today)
		})
	}
}

// assertMigrationValid verifies that a proposed migration satisfies expiry rules and parses successfully.
func assertMigrationValid(t *testing.T, path string, data []byte, proposal ExceptionMigration, today time.Time) {
	t.Helper()
	if proposal.Unresolved != "" {
		return
	}
	entry := proposal.Entry
	if problem := rules.CategoryExpiryProblem(entry.Category, entry.Entered, entry.Expires, today); problem != "" {
		t.Fatal(problem)
	}
	if _, err := ParseManifest(path, data); err != nil {
		t.Fatal(err)
	}
}

// TestExceptionMigrationPreservesPolicyAndComments pins comment preservation and mapping order during exception migration.
func TestExceptionMigrationPreservesPolicyAndComments(t *testing.T) {
	today := ExceptionDay(time.Now())
	entries := legacyMigrationEntry(91, "category D, rollout", today) + legacyMigrationEntry(121, "too long", today)
	path := migrationManifest(t, entries)
	data, proposals, err := PlanExceptionMigration(t.Context(), path, today)
	if err != nil || len(proposals) != 2 || proposals[0].Entry.Category != "D" || proposals[1].Unresolved == "" {
		t.Fatalf("proposals=%+v err=%v", proposals, err)
	}
	for _, comment := range []string{"# manifest comment", "# entry comment", "# expiry comment"} {
		if !strings.Contains(string(data), comment) {
			t.Errorf("lost comment %s: %s", comment, data)
		}
	}
	manifest, err := DecodeManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Exceptions[1].Category != "" || manifest.Exceptions[1].Entered != "" {
		t.Fatal("unresolved entry changed")
	}
	if strings.Index(string(data), "reason:") > strings.Index(string(data), "expires:") {
		t.Fatal("mapping order changed")
	}
}

// TestExceptionMigrationRejectsAmbiguousInput pins error reporting for duplicate keys, multi-document streams, and invalid YAML.
func TestExceptionMigrationRejectsAmbiguousInput(t *testing.T) {
	for _, content := range []string{
		"version: 1\nversion: 1\n", "version: 1\n---\nversion: 1\n",
		"version: 1\nunknown: true\n", "version: 1\nexceptions: &entries []\n",
	} {
		path := migrationManifest(t, "")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := PlanExceptionMigration(t.Context(), path, time.Now())
		if err == nil {
			t.Fatalf("accepted ambiguous or unknown YAML: %s", content)
		}
	}
}

// TestExceptionMigrationReasonAndExistingFields pins handling of category-prefixed reasons and pre-existing lifecycle fields.
func TestExceptionMigrationReasonAndExistingFields(t *testing.T) {
	today := ExceptionDay(time.Now())
	for _, tc := range []struct{ reason, extra, want string }{
		{"category C, migration", "", ""},
		{"CATEGORY D: migration", "", "D"},
		{"migration", "    category: D\n", "skip"},
		{"migration", "    entered: 2026-10-28\n", "skip"},
	} {
		path := migrationManifest(t, legacyMigrationEntry(91, tc.reason, today)+tc.extra)
		_, proposals, err := PlanExceptionMigration(t.Context(), path, today)
		if err != nil {
			t.Fatal(err)
		}
		if tc.want == "skip" {
			if len(proposals) != 0 {
				t.Fatalf("changed existing lifecycle fields: %+v", proposals)
			}
			continue
		}
		if len(proposals) != 1 || (proposals[0].Unresolved == "") != (tc.want != "") {
			t.Fatalf("reason prefix ignored: %+v", proposals)
		}
	}
}

// TestExceptionEnteredBlame pins resolving the entered date and commit source from git blame history.
func TestExceptionEnteredBlame(t *testing.T) {
	// Serial: time.Local is process-wide; restore it before other tests run.
	local := time.Local
	t.Cleanup(func() { time.Local = local })
	time.Local = time.FixedZone("plus-one", 3600)
	// testdate: fact — 2026-11-02 23:30 UTC is November 3 in plus-one.
	const instant int64 = 1793662200
	today := ExceptionDay(time.Unix(instant, 0))
	path := migrationManifest(t, legacyMigrationEntry(5, "migration", today))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	dir := filepath.Dir(path)
	for _, args := range [][]string{{"init", "-q"}, {"add", ManifestFileName}} {
		if _, err := util.RunGit(ctx, dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := util.RunGit(ctx, dir, "write-tree")
	if err != nil {
		t.Fatal(err)
	}
	date := fmt.Sprintf("%d +0000", instant)
	commitCtx, err := util.WithCommandEnvironment(ctx, util.InheritedEnvironment([]string{
		"GIT_COMMITTER_DATE=" + date, "GIT_AUTHOR_DATE=" + date,
	}))
	if err != nil {
		t.Fatal(err)
	}
	commit, err := util.RunGit(commitCtx, dir, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit-tree", tree, "-m", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGit(ctx, dir, "update-ref", "HEAD", commit); err != nil {
		t.Fatal(err)
	}
	_, proposals, err := PlanExceptionMigration(ctx, path, today.AddDate(0, 0, 1))
	if err != nil || len(proposals) != 1 {
		t.Fatalf("proposals=%+v err=%v", proposals, err)
	}
	if proposals[0].Source != "git-blame:"+commit || proposals[0].Entry.Entered != ExceptionDay(time.Unix(instant, 0)).Format(ExceptionDateLayout) {
		t.Fatalf("history ignored: %+v", proposals[0])
	}
}

// TestExceptionEnteredBlameFallback pins fallback behavior when blame porcelain output is empty, uncommitted, or malformed.
func TestExceptionEnteredBlameFallback(t *testing.T) {
	today := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	for _, text := range []string{"", "000000000 1 1 1\ncommitter-time 1", "abc 1 1 1\ncommitter-time bad"} {
		entered, source := enteredFromBlame(text, today)
		if entered != "2026-11-02" || !strings.HasPrefix(source, "today") {
			t.Fatalf("unexpected fallback %s %s", entered, source)
		}
	}
}
