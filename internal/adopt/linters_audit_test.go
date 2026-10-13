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
		writeLinterFixtureFile(t, root, "main.go", "package main\nfunc main() {}\n")
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
		writeLinterFixtureFile(t, root, "main.go", "package main\nfunc main() {}\n")
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

// 14. Script named after tool: positive and negative cases.
func TestAuditDeclaredLinters_ScriptNamedAfterTool_Positive(t *testing.T) {
	ctx := t.Context()
	cases := []struct {
		tool, relScript, runCmd, markerFile string
	}{
		{"markdownlint", "tools/markdownlint/verify.mjs", "node tools/markdownlint/verify.mjs", "README.md"},
		{"semgrep", "scripts/test_hiss_semgrep.py", "python3 -B scripts/test_hiss_semgrep.py", ""},
		{"gitleaks", "scripts/run-gitleaks.sh", "./scripts/run-gitleaks.sh", ""},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			root := t.TempDir()
			if tc.markerFile != "" {
				writeLinterFixtureFile(t, root, tc.markerFile, "# doc\n")
			}
			writeLinterFixtureFile(t, root, tc.relScript, "#!/bin/sh\n")
			writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", "name: CI\non: push\njobs:\n  b:\n    runs-on: u\n    steps:\n      - run: "+tc.runCmd+"\n")
			out, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
				RootDir: root, Policy: singleLinterPolicy("profile", tc.tool), Today: testToday,
			})
			if err != nil || !strings.Contains(out, "[PASS]") {
				t.Fatalf("expected script %s to pass for %s, got: %v, out: %s", tc.runCmd, tc.tool, err, out)
			}
		})
	}
}

func TestAuditDeclaredLinters_ScriptNamedAfterTool_Negative(t *testing.T) {
	ctx := t.Context()
	cases := []struct {
		name, runCmd string
	}{
		{"notsemgrepish is not a word match", "python3 scripts/notsemgrepish.py"},
		{"echo is not interpreter", "echo semgrep"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeLinterFixtureFile(t, root, "scripts/notsemgrepish.py", "print(1)\n")
			writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", "name: CI\non: push\njobs:\n  b:\n    runs-on: u\n    steps:\n      - run: "+tc.runCmd+"\n")
			_, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
				RootDir: root, Policy: singleLinterPolicy("profile", "semgrep"), Today: testToday,
			})
			if err == nil || !strings.Contains(err.Error(), "semgrep") {
				t.Fatalf("expected command %s to fail for semgrep, got: %v", tc.runCmd, err)
			}
		})
	}
}

// 15. Go runners: go tool and go run.
func TestAuditDeclaredLinters_GoRunners(t *testing.T) {
	ctx := t.Context()
	cases := []struct {
		name, tool, runCmd, markerFile string
	}{
		{"go tool with modfile", "gitleaks", "go tool -modfile=tools/go/go.mod gitleaks detect", ""},
		{"go run versioned", "golangci-lint", "go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.60.0 run", "main.go"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.markerFile != "" {
				writeLinterFixtureFile(t, root, tc.markerFile, "package main\n")
			}
			writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", "name: CI\non: push\njobs:\n  b:\n    runs-on: u\n    steps:\n      - run: "+tc.runCmd+"\n")
			out, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
				RootDir: root, Policy: singleLinterPolicy("profile", tc.tool), Today: testToday,
			})
			if err != nil || !strings.Contains(out, "[PASS]") {
				t.Fatalf("expected runner %s to pass for %s, got: %v, out: %s", tc.runCmd, tc.tool, err, out)
			}
		})
	}
}

// 16. Makefile variable expansion.
func TestAuditDeclaredLinters_MakeVariables(t *testing.T) {
	ctx := t.Context()
	t.Run("known variable expands and passes", func(t *testing.T) {
		root := t.TempDir()
		writeLinterFixtureFile(t, root, "Makefile", "GO_SEC := go tool -modfile=tools/go/go.mod\nlint:\n\t$(GO_SEC) gitleaks detect\n")
		writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", "name: CI\non: push\njobs:\n  b:\n    runs-on: u\n    steps:\n      - run: make lint\n")
		out, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
			RootDir: root, Policy: singleLinterPolicy("profile", "gitleaks"), Today: testToday,
		})
		if err != nil || !strings.Contains(out, "[PASS]") {
			t.Fatalf("expected make variable expansion to pass, got: %v, out: %s", err, out)
		}
	})

	t.Run("unknown variable stays as written", func(t *testing.T) {
		root := t.TempDir()
		writeLinterFixtureFile(t, root, "Makefile", "RUN_CMD := gitleaks\nlint:\n\t$(RUN_CMD) $(UNKNOWN_FLAG) detect\n")
		writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", "name: CI\non: push\njobs:\n  b:\n    runs-on: u\n    steps:\n      - run: make lint\n")
		out, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
			RootDir: root, Policy: singleLinterPolicy("profile", "gitleaks"), Today: testToday,
		})
		if err != nil || !strings.Contains(out, "[PASS]") {
			t.Fatalf("expected unknown variable to pass if tool is recognized, got: %v, out: %s", err, out)
		}
	})
}

// 17. Applicability: skipping when inputs are absent.
func TestAuditDeclaredLinters_Applicability_Skip(t *testing.T) {
	ctx := t.Context()
	cases := []struct {
		tool, wantInput string
	}{
		{"golangci-lint", "Go sources"},
		{"clippy", "Cargo.toml"},
		{"cppcheck", "C or C++ sources"},
		{"buf", "*.proto or buf.yaml"},
		{"spectral", "OpenAPI or AsyncAPI document"},
		{"packer", "*.pkr.hcl"},
		{"shellcheck", "shell scripts"},
		{"markdownlint", "*.md"},
		{"kubeconform", "Kubernetes manifests (YAML documents with apiVersion and kind)"},
		{"tflint", "*.tf"},
		{"yamllint", "*.yml or *.yaml"},
		{"checkov", checkovInputDesc},
	}
	for _, tc := range cases {
		t.Run(tc.tool+" absent", func(t *testing.T) {
			root := t.TempDir()
			out, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
				RootDir: root, Policy: singleLinterPolicy("profile", tc.tool), Today: testToday,
			})
			if err != nil {
				t.Fatalf("expected skip to not fail, got err: %v", err)
			}
			wantSkip := fmt.Sprintf("[SKIP] %s: not applicable, no %s", tc.tool, tc.wantInput)
			if !strings.Contains(out, wantSkip) {
				t.Fatalf("expected output to contain %q, got: %s", wantSkip, out)
			}
			if !strings.Contains(out, "[PASS]") {
				t.Fatalf("expected output to contain [PASS], got: %s", out)
			}
		})
	}
}

// 18. Applicability: required when inputs are present.
func TestAuditDeclaredLinters_Applicability_Present(t *testing.T) {
	ctx := t.Context()
	cases := []struct {
		tool, markerPath, markerBody string
	}{
		{"golangci-lint", "pkg/app.go", "package pkg\n"},
		{"clippy", "Cargo.toml", "[package]\nname = \"demo\"\n"},
		{"cppcheck", "src/main.c", "int main() {}\n"},
		{"buf", "buf.yaml", "version: v1\n"},
		{"buf", "proto/service.proto", "syntax = \"proto3\";\n"},
		{"spectral", "openapi.yaml", "openapi: 3.0.0\ninfo:\n  title: API\n"},
		{"spectral", "asyncapi.json", "{\"asyncapi\": \"2.0.0\"}\n"},
		{"spectral", ".spectral.yaml", "extends: spectral:oas\n"},
		{"packer", "build.pkr.hcl", "source \"null\" \"example\" {}\n"},
		{"shellcheck", "scripts/deploy.sh", "#!/bin/sh\n"},
		{"markdownlint", "docs/index.md", "# Guide\n"},
		{"kubeconform", "deploy/app.yaml", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: app\n"},
		{"kubeconform", "deploy/all.yml", "# the first document is no object\nfoo: bar\n---\napiVersion: v1\nkind: Service\n"},
		{"tflint", "infra/main.tf", "terraform {}\n"},
		{"tflint", "infra/main.tf.json", "{\"terraform\": {}}\n"},
		{"yamllint", "config/app.yml", "key: value\n"},
		{"checkov", "infra/main.tf", "terraform {}\n"},
		{"checkov", "infra/main.tf.json", "{\"terraform\": {}}\n"},
		{"checkov", "deploy/app.yaml", "apiVersion: apps/v1\nkind: Deployment\n"},
		{"checkov", "charts/app/Chart.yaml", "apiVersion: v2\nname: app\n"},
		{"checkov", "overlays/prod/kustomization.yaml", "resources: []\n"},
		{"checkov", "Dockerfile", "FROM scratch\n"},
		{"checkov", "images/Containerfile", "FROM scratch\n"},
		{"checkov", "infra/main.bicep", "param name string\n"},
		{"checkov", "stack/template.yaml", "AWSTemplateFormatVersion: '2010-09-09'\nResources: {}\n"},
		{"checkov", "stack/template.json", "{\"AWSTemplateFormatVersion\": \"2010-09-09\"}\n"},
		{"checkov", "serverless.yml", "service: app\n"},
	}
	for _, tc := range cases {
		t.Run(tc.tool+" present", func(t *testing.T) {
			root := t.TempDir()
			writeLinterFixtureFile(t, root, tc.markerPath, tc.markerBody)
			_, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
				RootDir: root, Policy: singleLinterPolicy("profile", tc.tool), Today: testToday,
			})
			if err == nil || !strings.Contains(err.Error(), tc.tool) {
				t.Fatalf("expected missing %s to fail when inputs present, got err: %v", tc.tool, err)
			}
		})
	}
}

// 19. Unknown linter: always required (fail closed).
func TestAuditDeclaredLinters_UnknownLinterRequired(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	_, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir: root, Policy: singleLinterPolicy("profile", "trivy"), Today: testToday,
	})
	if err == nil || !strings.Contains(err.Error(), "trivy") {
		t.Fatalf("expected unknown linter trivy to fail in empty directory, got: %v", err)
	}
}
