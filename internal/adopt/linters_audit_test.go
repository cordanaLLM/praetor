// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

var testToday = time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)

func writeLinterFixtureFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func singleLinterPolicy(facetID, linter string) *config.EffectivePolicy {
	content := fmt.Sprintf("id: %q\nname: %q\nlinters:\n  - %q\n", facetID, facetID, linter)
	rel := fmt.Sprintf(".config/archetypes/facets/%s.yaml", strings.ReplaceAll(facetID, ":", "-"))
	return &config.EffectivePolicy{
		CatalogArtifacts: []config.PolicyArtifact{
			{RelativePath: rel, Content: []byte(content)},
		},
	}
}

// 1. Direct binary execution: passes.
func TestAuditDeclaredLinters_PositiveDirectBinary(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeLinterFixtureFile(t, root, ".github/workflows/security.yml", `name: Security
on: push
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - run: gitleaks detect --verbose
`)
	out, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root, Policy: singleLinterPolicy("security:high", "gitleaks"), Today: testToday,
	})
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("expected direct binary to pass, got err: %v, out: %s", err, out)
	}
}

// 2. Action uses: step with dash-separated word: passes.
func TestAuditDeclaredLinters_PositiveActionUses(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeLinterFixtureFile(t, root, ".github/workflows/security.yml", `name: Security
on: push
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: gitleaks/gitleaks-action@v2
`)
	out, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root, Policy: singleLinterPolicy("security:high", "gitleaks"), Today: testToday,
	})
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("expected action uses to pass, got err: %v, out: %s", err, out)
	}
}

// 3. Container image: docker run image repository base name: passes.
func TestAuditDeclaredLinters_PositiveContainerImage(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeLinterFixtureFile(t, root, ".github/workflows/security.yml", `name: Security
on: push
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - run: docker run --rm -v $(pwd):/path ghcr.io/gitleaks/gitleaks:latest detect
`)
	out, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root, Policy: singleLinterPolicy("security:high", "gitleaks"), Today: testToday,
	})
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("expected container image to pass, got err: %v, out: %s", err, out)
	}
}

// 4. Make target depth 2: verify-all -> lint -> gitleaks: passes.
func TestAuditDeclaredLinters_PositiveMakeDepth2(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeLinterFixtureFile(t, root, "Makefile", "verify-all: lint\nlint:\n\t@gitleaks detect\n")
	writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", `name: CI
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: make verify-all
`)
	out, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root, Policy: singleLinterPolicy("security:high", "gitleaks"), Today: testToday,
	})
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("expected make target depth 2 to pass, got err: %v, out: %s", err, out)
	}
}

// 5. golangci sub-linter enabled vs not enabled.
func TestAuditDeclaredLinters_GolangciSubLinter(t *testing.T) {
	ctx := t.Context()
	policy := singleLinterPolicy("framework", "gocyclo")

	t.Run("enabled in .golangci.yml passes", func(t *testing.T) {
		root := t.TempDir()
		writeLinterFixtureFile(t, root, ".golangci.yml", "version: \"2\"\nlinters:\n  enable:\n    - gocyclo\n")
		writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", `name: CI
on: push
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - run: golangci-lint run
`)
		out, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
			RootDir: root, Policy: policy, Today: testToday,
		})
		if err != nil || !strings.Contains(out, "[PASS]") {
			t.Fatalf("expected gocyclo to pass when enabled, got err: %v, out: %s", err, out)
		}
	})

	t.Run("not enabled in .golangci.yml fails", func(t *testing.T) {
		root := t.TempDir()
		writeLinterFixtureFile(t, root, ".golangci.yml", "version: \"2\"\nlinters:\n  default: none\n")
		writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", `name: CI
on: push
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - run: golangci-lint run
`)
		_, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
			RootDir: root, Policy: policy, Today: testToday,
		})
		if err == nil || !strings.Contains(err.Error(), "gocyclo") {
			t.Fatalf("expected missing gocyclo failure when not enabled, got: %v", err)
		}
	})
}

// 6. Echo-only mention FAILS.
// Planted regression note: on round-1 code (bbe99ccd0), stepRunsTool used naive substring matching
// (strings.Contains(runLower, toolLower)), so echo "gitleaks" falsely passed (green on old).
// With the shell command parser, the command's program is "echo", so gitleaks is not run (red on old, fixed here).
func TestAuditDeclaredLinters_NegativeEchoOnlyPlanted(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", `name: CI
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo "gitleaks detect"
`)
	_, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root, Policy: singleLinterPolicy("security:high", "gitleaks"), Today: testToday,
	})
	if err == nil || !strings.Contains(err.Error(), "gitleaks") {
		t.Fatalf("expected echo-only mention to fail, got: %v", err)
	}
}

// 7. Comment-only FAILS.
func TestAuditDeclaredLinters_NegativeCommentOnly(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", "name: CI\non: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: |\n          # gitleaks detect\n          echo \"ready\"\n")
	_, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root, Policy: singleLinterPolicy("security:high", "gitleaks"), Today: testToday,
	})
	if err == nil || !strings.Contains(err.Error(), "gitleaks") {
		t.Fatalf("expected comment-only mention to fail, got: %v", err)
	}
}

// 8. Continue-on-error step FAILS.
func TestAuditDeclaredLinters_NegativeContinueOnError(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", `name: CI
on: push
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - continue-on-error: true
        run: gitleaks detect
`)
	_, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root, Policy: singleLinterPolicy("security:high", "gitleaks"), Today: testToday,
	})
	if err == nil || !strings.Contains(err.Error(), "gitleaks") {
		t.Fatalf("expected continue-on-error step to fail, got: %v", err)
	}
}

// 9. if: false job FAILS.
func TestAuditDeclaredLinters_NegativeIfFalseJob(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", `name: CI
on: push
jobs:
  lint:
    if: false
    runs-on: ubuntu-latest
    steps:
      - run: gitleaks detect
`)
	_, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root, Policy: singleLinterPolicy("security:high", "gitleaks"), Today: testToday,
	})
	if err == nil || !strings.Contains(err.Error(), "gitleaks") {
		t.Fatalf("expected if: false job to fail, got: %v", err)
	}
}

// 10. Live, expired, and stale exceptions.
func TestAuditDeclaredLinters_ExceptionsLifecycle(t *testing.T) {
	ctx := t.Context()
	policy := singleLinterPolicy("security:high", "gitleaks")

	t.Run("live exception passes as excused", func(t *testing.T) {
		root := t.TempDir()
		writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", "name: CI\non: push\njobs:\n  b:\n    runs-on: u\n    steps:\n      - run: echo no\n")
		exceptions := []config.Exception{
			{Rule: config.ExceptionRuleDeclaredLinters, Tool: "gitleaks", Reason: "waived temporarily", Expires: "2026-11-01"},
		}
		out, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
			RootDir: root, Policy: policy, Exceptions: exceptions, Today: testToday,
		})
		if err != nil || !strings.Contains(out, "[PASS]") || !strings.Contains(out, "excepted") {
			t.Fatalf("expected live exception to pass as excused, got err: %v, out: %s", err, out)
		}
	})

	t.Run("expired exception fails like missing", func(t *testing.T) {
		root := t.TempDir()
		writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", "name: CI\non: push\njobs:\n  b:\n    runs-on: u\n    steps:\n      - run: echo no\n")
		exceptions := []config.Exception{
			{Rule: config.ExceptionRuleDeclaredLinters, Tool: "gitleaks", Reason: "expired waiver", Expires: "2026-10-01"},
		}
		_, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
			RootDir: root, Policy: policy, Exceptions: exceptions, Today: testToday,
		})
		if err == nil || !strings.Contains(err.Error(), "expired") || !strings.Contains(err.Error(), "gitleaks") {
			t.Fatalf("expected expired exception to fail, got: %v", err)
		}
	})

	t.Run("stale exception fails until removed", func(t *testing.T) {
		root := t.TempDir()
		writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", "name: CI\non: push\njobs:\n  b:\n    runs-on: u\n    steps:\n      - run: gitleaks detect\n")
		exceptions := []config.Exception{
			{Rule: config.ExceptionRuleDeclaredLinters, Tool: "gitleaks", Reason: "already run", Expires: "2026-11-01"},
		}
		_, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
			RootDir: root, Policy: policy, Exceptions: exceptions, Today: testToday,
		})
		if err == nil || !strings.Contains(err.Error(), "excuse no linter") {
			t.Fatalf("expected stale exception to fail, got: %v", err)
		}
	})
}

// 11. Make depth limit: depth > 4 FAILS.
func TestAuditDeclaredLinters_BoundaryMakeDepthLimit(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	// Chain of depth 5: t1 -> t2 -> t3 -> t4 -> t5. t5 runs gitleaks. Depth limit is 4.
	writeLinterFixtureFile(t, root, "Makefile", "t1: t2\nt2: t3\nt3: t4\nt4: t5\nt5:\n\t@gitleaks detect\n")
	writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", "name: CI\non: push\njobs:\n  b:\n    runs-on: u\n    steps:\n      - run: make t1\n")
	_, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root, Policy: singleLinterPolicy("security:high", "gitleaks"), Today: testToday,
	})
	if err == nil || !strings.Contains(err.Error(), "gitleaks") {
		t.Fatalf("expected make target beyond depth limit to fail, got: %v", err)
	}
}

// 12. Candidate limit: > 64 candidates FAILS.
func TestAuditDeclaredLinters_BoundaryCandidateLimit(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", "name: CI\non: push\njobs:\n  b:\n    runs-on: u\n    steps:\n      - run: echo ok\n")

	var linters []string
	for i := 0; i < 65; i++ {
		linters = append(linters, fmt.Sprintf("tool-%d", i))
	}
	policy := &config.EffectivePolicy{
		Policy: config.ResolvedPolicy{Linters: linters},
	}
	_, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root, Policy: policy, Today: testToday,
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds 64") {
		t.Fatalf("expected candidate limit error, got: %v", err)
	}
}

// 13. No declared linters = pass.
func TestAuditDeclaredLinters_BoundaryNoDeclaredLinters(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	out, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root, Policy: &config.EffectivePolicy{}, Today: testToday,
	})
	if err != nil || !strings.Contains(out, "policy declares no linters") {
		t.Fatalf("expected empty linters policy to pass, got err: %v, out: %s", err, out)
	}
}
