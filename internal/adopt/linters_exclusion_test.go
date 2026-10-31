// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func writeGolangciWorkflow(t *testing.T, root string) {
	t.Helper()
	writeLinterFixtureFile(t, root, "main.go", "package main\nfunc main() {}\n")
	writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", `name: CI
on: push
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - run: golangci-lint run
`)
}

func multipleLintersPolicy(facetID string, linters ...string) *config.EffectivePolicy {
	var linterList []string
	for _, l := range linters {
		linterList = append(linterList, fmt.Sprintf("  - %q", l))
	}
	content := fmt.Sprintf("id: %q\nname: %q\nlinters:\n%s\n", facetID, facetID, strings.Join(linterList, "\n"))
	rel := fmt.Sprintf(".config/archetypes/facets/%s.yaml", strings.ReplaceAll(facetID, ":", "-"))
	return &config.EffectivePolicy{
		CatalogArtifacts: []config.PolicyArtifact{
			{RelativePath: rel, Content: []byte(content)},
		},
	}
}

// Planted negative: exclusion rule without the required comment fails.
func TestAuditDeclaredLinters_Exclusion_Planted_MissingComment(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeGolangciWorkflow(t, root)
	writeLinterFixtureFile(t, root, ".golangci.yml", `version: "2"
linters:
  enable:
    - gocyclo
  exclusions:
    rules:
      - path: internal/util/
        linters:
          - gocyclo
`)

	_, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root,
		Policy:  singleLinterPolicy("framework", "gocyclo"),
		Today:   testToday,
	})
	if err == nil {
		t.Fatal("expected missing comment to fail")
	}
	msg := err.Error()
	for _, want := range []string{".golangci.yml", "gocyclo", "internal/util/", "missing category and expiry comment"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing expected part %q", msg, want)
		}
	}
}

// Planted negative: exclusion rule with an expired date fails.
func TestAuditDeclaredLinters_Exclusion_Planted_ExpiredRule(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeGolangciWorkflow(t, root)
	writeLinterFixtureFile(t, root, ".golangci.yml", `version: "2"
linters:
  enable:
    - gocyclo
  exclusions:
    rules:
      # category B, entered 2026-10-07, expires 2026-10-10, #133
      - path: internal/util/
        linters:
          - gocyclo
`)

	_, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root,
		Policy:  singleLinterPolicy("framework", "gocyclo"),
		Today:   testToday, // 2026-10-12
	})
	if err == nil {
		t.Fatal("expected expired exclusion to fail")
	}
	msg := err.Error()
	for _, want := range []string{".golangci.yml", "gocyclo", "internal/util/", "expired 2026-10-10"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing expected part %q", msg, want)
		}
	}
}

// Order engine-golangci-exceptions-expired-20261029: verify exclusion expiry day boundary.
// Category C entered 2026-10-02: expires 2026-10-11 (yesterday) fails, expires 2026-10-12 (today) passes.
func TestAuditDeclaredLinters_Exclusion_Boundary_ExpiryDay(t *testing.T) {
	cases := []struct {
		name    string
		expires string
		wantErr bool
	}{
		{name: "yesterday_expired", expires: "2026-10-11", wantErr: true},
		{name: "today_holding", expires: "2026-10-12", wantErr: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeGolangciWorkflow(t, root)
			content := fmt.Sprintf("version: \"2\"\nlinters:\n  enable:\n    - gocyclo\n  exclusions:\n    rules:\n      # category C, entered 2026-10-02, expires %s, #1030\n      - path: internal/util/\n        linters:\n          - gocyclo\n", tc.expires)
			writeLinterFixtureFile(t, root, ".golangci.yml", content)

			_, err := AuditDeclaredLinters(t.Context(), DeclaredLintersOptions{
				RootDir: root,
				Policy:  singleLinterPolicy("framework", "gocyclo"),
				Today:   testToday,
			})
			if tc.wantErr {
				assertExpiryError(t, err, tc.expires)
				return
			}
			if err != nil {
				t.Fatalf("expected expires %s to pass, got: %v", tc.expires, err)
			}
		})
	}
}

func assertExpiryError(t *testing.T, err error, expires string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected expires %s to fail", expires)
	}
	msg := err.Error()
	for _, want := range []string{".golangci.yml", "gocyclo", "internal/util/", "expired " + expires} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing expected part %q", msg, want)
		}
	}
}

// Planted negative: category C rule whose expiry is more than 10 days after entered date fails.
func TestAuditDeclaredLinters_Exclusion_Planted_CategoryCExceededInterval(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeGolangciWorkflow(t, root)
	writeLinterFixtureFile(t, root, ".golangci.yml", `version: "2"
linters:
  enable:
    - gocyclo
  exclusions:
    rules:
      # category C, entered 2026-10-01, expires 2026-10-20, #133
      - path: internal/util/
        linters:
          - gocyclo
`)

	_, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root,
		Policy:  singleLinterPolicy("framework", "gocyclo"),
		Today:   testToday, // 2026-10-12
	})
	if err == nil {
		t.Fatal("expected category C interval violation to fail")
	}
	msg := err.Error()
	for _, want := range []string{".golangci.yml", "gocyclo", "internal/util/", "category C requires expires on or before 2026-10-11 (10 days after entered 2026-10-01)"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing expected part %q", msg, want)
		}
	}
}

// Planted negative: a log holding Skipped 0 issues by rules for an exclusion rule fails as stale.
func TestAuditDeclaredLinters_Exclusion_Planted_StaleRule(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeGolangciWorkflow(t, root)
	writeLinterFixtureFile(t, root, ".golangci.yml", `version: "2"
linters:
  enable:
    - gocyclo
  exclusions:
    warn-unused: true
    rules:
      # category C, entered 2026-10-08, expires 2026-10-18, #133
      - path: internal/x/
        linters:
          - gocyclo
`)

	logPath := root + "/lint.log"
	writeLinterFixtureFile(t, root, "lint.log", `WARN [runner] Skipped 0 issues by rules: [Path: "internal/x/", Linters: "gocyclo"]
`)

	_, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir:     root,
		Policy:      singleLinterPolicy("framework", "gocyclo"),
		Today:       testToday,
		LintLogPath: logPath,
	})
	if err == nil {
		t.Fatal("expected stale rule matching no finding to fail")
	}
	msg := err.Error()
	for _, want := range []string{".golangci.yml", "gocyclo", "internal/x/", "matches no finding: remove it"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing expected part %q", msg, want)
		}
	}
}

// Positive: the same rule with Skipped 3 issues passes.
func TestAuditDeclaredLinters_Exclusion_Planted_StaleRule_RuleWithIssuesPasses(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeGolangciWorkflow(t, root)
	writeLinterFixtureFile(t, root, ".golangci.yml", `version: "2"
linters:
  enable:
    - gocyclo
  exclusions:
    warn-unused: true
    rules:
      # category C, entered 2026-10-08, expires 2026-10-18, #133
      - path: internal/x/
        linters:
          - gocyclo
`)

	logPath := root + "/lint.log"
	writeLinterFixtureFile(t, root, "lint.log", `INFO [runner] Skipped 3 issues by rules: [Path: "internal/x/", Linters: "gocyclo"]
`)

	out, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir:     root,
		Policy:      singleLinterPolicy("framework", "gocyclo"),
		Today:       testToday,
		LintLogPath: logPath,
	})
	if err != nil {
		t.Fatalf("expected rule skipping 3 issues to pass, got: %v", err)
	}
	if !strings.Contains(out, "[PASS] Declared linters:") {
		t.Errorf("expected pass message, got: %s", out)
	}
}

// Planted negative: rule with text + path + two linters matches its exact String form.
func TestAuditDeclaredLinters_Exclusion_Planted_StaleRule_ComplexRuleString(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeLinterFixtureFile(t, root, "main.go", "package main\nfunc main() {}\n")
	writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", `name: CI
on: push
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - run: golangci-lint run
`)
	writeLinterFixtureFile(t, root, ".golangci.yml", `version: "2"
linters:
  enable:
    - gocyclo
    - govet
  exclusions:
    warn-unused: true
    rules:
      # category C, entered 2026-10-08, expires 2026-10-18, #133
      - text: "cyclomatic complexity"
        path: internal/x/
        linters:
          - gocyclo
          - govet
`)

	logPath := root + "/lint.log"
	writeLinterFixtureFile(t, root, "lint.log", `level=warning msg="Skipped 0 issues by rules: [Text: \"cyclomatic complexity\", Path: \"internal/x/\", Linters: \"gocyclo, govet\"]"
`)

	_, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir:     root,
		Policy:      multipleLintersPolicy("framework", "gocyclo", "govet"),
		Today:       testToday,
		LintLogPath: logPath,
	})
	if err == nil {
		t.Fatal("expected complex stale rule to fail")
	}
	msg := err.Error()
	for _, want := range []string{".golangci.yml", "gocyclo, govet", "internal/x/", "matches no finding: remove it"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing expected part %q", msg, want)
		}
	}
}

// Gap: no log given results in the named gap line.
func TestAuditDeclaredLinters_Exclusion_Gap_NoLog(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeGolangciWorkflow(t, root)
	writeLinterFixtureFile(t, root, ".golangci.yml", `version: "2"
linters:
  enable:
    - gocyclo
  exclusions:
    warn-unused: true
    rules:
      # category C, entered 2026-10-08, expires 2026-10-18, #133
      - path: internal/util/
        linters:
          - gocyclo
`)

	out, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root,
		Policy:  singleLinterPolicy("framework", "gocyclo"),
		Today:   testToday,
	})
	if err != nil {
		t.Fatalf("expected valid exclusions to pass without log, got: %v", err)
	}
	if !strings.Contains(out, "[PASS] Declared linters:") {
		t.Errorf("expected pass message, got: %s", out)
	}
	if !strings.Contains(out, "[SKIP] Declared linters: stale check not run: no lint log given") {
		t.Errorf("expected 'stale check not run: no lint log given' gap line, got: %s", out)
	}
}

// Gap: linters.exclusions.warn-unused is off results in the named gap line.
func TestAuditDeclaredLinters_Exclusion_Gap_WarnUnusedOff(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeGolangciWorkflow(t, root)
	writeLinterFixtureFile(t, root, ".golangci.yml", `version: "2"
linters:
  enable:
    - gocyclo
  exclusions:
    warn-unused: false
    rules:
      # category C, entered 2026-10-08, expires 2026-10-18, #133
      - path: internal/util/
        linters:
          - gocyclo
`)

	logPath := root + "/lint.log"
	writeLinterFixtureFile(t, root, "lint.log", "some lint output\n")

	out, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir:     root,
		Policy:      singleLinterPolicy("framework", "gocyclo"),
		Today:       testToday,
		LintLogPath: logPath,
	})
	if err != nil {
		t.Fatalf("expected warn-unused off to pass with gap line, got: %v", err)
	}
	if !strings.Contains(out, "[PASS] Declared linters:") {
		t.Errorf("expected pass message, got: %s", out)
	}
	if !strings.Contains(out, "[SKIP] Declared linters: stale check not possible: linters.exclusions.warn-unused is off") {
		t.Errorf("expected 'stale check not possible: linters.exclusions.warn-unused is off' gap line, got: %s", out)
	}
}

// Boundary: a rule for an undeclared linter is ignored.
func TestAuditDeclaredLinters_Exclusion_Boundary_UndeclaredLinterIgnored(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeGolangciWorkflow(t, root)
	// forbidigo is not declared by policy (only gocyclo is). forbidigo rule has no comment.
	writeLinterFixtureFile(t, root, ".golangci.yml", `version: "2"
linters:
  enable:
    - gocyclo
  exclusions:
    rules:
      - path: internal/util/
        linters:
          - forbidigo
`)

	out, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root,
		Policy:  singleLinterPolicy("framework", "gocyclo"),
		Today:   testToday,
	})
	if err != nil {
		t.Fatalf("expected rule for undeclared linter to be ignored, got: %v", err)
	}
	if !strings.Contains(out, "[PASS] Declared linters:") {
		t.Errorf("expected pass message, got: %s", out)
	}
}

// Boundary: category A rule up to MaxExceptionDays (90 days) passes.
func TestAuditDeclaredLinters_Exclusion_Boundary_CategoryAMaxDays(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeGolangciWorkflow(t, root)
	// testToday is 2026-10-12. 2027-01-05 is 85 days ahead (<= 90 days).
	writeLinterFixtureFile(t, root, ".golangci.yml", `version: "2"
linters:
  enable:
    - gocyclo
  exclusions:
    rules:
      # category A, entered 2026-10-12, expires 2027-01-05, #133
      - path: internal/util/
        linters:
          - gocyclo
`)

	out, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root,
		Policy:  singleLinterPolicy("framework", "gocyclo"),
		Today:   testToday,
	})
	if err != nil {
		t.Fatalf("expected category A rule up to 90 days to pass, got: %v", err)
	}
	if !strings.Contains(out, "[PASS] Declared linters:") {
		t.Errorf("expected pass message, got: %s", out)
	}
}

// Boundary: .golangci.toml yields the named gap line.
func TestAuditDeclaredLinters_Exclusion_Boundary_TomlConfigGap(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeLinterFixtureFile(t, root, "main.go", "package main\nfunc main() {}\n")
	writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", `name: CI
on: push
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - run: gocyclo .
`)
	writeLinterFixtureFile(t, root, ".golangci.toml", "[linters]\nenable = [\"gocyclo\"]\n")

	out, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root,
		Policy:  singleLinterPolicy("framework", "gocyclo"),
		Today:   testToday,
	})
	if err != nil {
		t.Fatalf("expected .toml config to pass with named gap, got err: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "exclusions not read (format)") {
		t.Errorf("expected 'exclusions not read (format)' gap line, got: %s", out)
	}
}

// Positive: # baseline #1030 rules have no expiry and are reported as baseline count in [PASS].
func TestAuditDeclaredLinters_Exclusion_BaselineCountedAndNeverExpires(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeGolangciWorkflow(t, root)
	writeLinterFixtureFile(t, root, ".golangci.yml", `version: "2"
linters:
  enable:
    - gocyclo
  exclusions:
    rules:
      `+GeneratedExceptionsBegin+`
      # baseline #1030
      - path: ^internal/a_test\.go$
        linters:
          - gocyclo
        text: func `+"`TestAlpha`"+`
      # baseline #1030
      - path: ^internal/b_test\.go$
        linters:
          - gocyclo
        text: func `+"`TestBeta`"+`
      `+GeneratedExceptionsEnd+`
`)
	futureToday := parseTestDate(t, "2030-05-01")
	out, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root,
		Policy:  singleLinterPolicy("framework", "gocyclo"),
		Today:   futureToday,
	})
	if err != nil {
		t.Fatalf("expected baseline rules to pass without expiry, got: %v", err)
	}
	if !strings.Contains(out, "[PASS] Declared linters: every declared linter is run by a CI workflow (1 verified, 2 baseline).") {
		t.Errorf("expected baseline count in [PASS] summary, got: %s", out)
	}
}

// Negative: dated rule past expiry still fails alongside # baseline #1030 rules.
func TestAuditDeclaredLinters_Exclusion_BaselineWithExpiredDatedRuleFails(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeGolangciWorkflow(t, root)
	writeLinterFixtureFile(t, root, ".golangci.yml", `version: "2"
linters:
  enable:
    - gocyclo
  exclusions:
    rules:
      `+GeneratedExceptionsBegin+`
      # baseline #1030
      - path: ^internal/a_test\.go$
        linters:
          - gocyclo
        text: func `+"`TestAlpha`"+`
      # category C, entered 2026-10-01, expires 2026-10-10, #1030
      - path: ^internal/b_test\.go$
        linters:
          - forbidigo
      `+GeneratedExceptionsEnd+`
`)
	_, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root,
		Policy:  singleLinterPolicy("framework", "gocyclo"),
		Today:   testToday,
	})
	if err == nil {
		t.Fatal("expected expired dated rule in generated block to fail")
	}
	if !strings.Contains(err.Error(), "expired 2026-10-10") {
		t.Errorf("expected expired 2026-10-10 error, got: %v", err)
	}
}

// Negative: unrecognised comment form in generated block fails closed.
func TestAuditDeclaredLinters_Exclusion_UnknownCommentInGeneratedBlockFailsClosed(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeGolangciWorkflow(t, root)
	writeLinterFixtureFile(t, root, ".golangci.yml", `version: "2"
linters:
  enable:
    - gocyclo
  exclusions:
    rules:
      `+GeneratedExceptionsBegin+`
      # permanently exempt test helper
      - path: ^internal/a_test\.go$
        linters:
          - gocyclo
      `+GeneratedExceptionsEnd+`
`)
	_, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root,
		Policy:  singleLinterPolicy("framework", "gocyclo"),
		Today:   testToday,
	})
	if err == nil {
		t.Fatal("expected unknown comment form in generated block to fail closed")
	}
	if !strings.Contains(err.Error(), "unrecognised comment in generated block") {
		t.Errorf("expected unrecognised comment error, got: %v", err)
	}
}
