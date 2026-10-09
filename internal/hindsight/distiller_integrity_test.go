// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package hindsight

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestDistillBugLedgerIntegrity(t *testing.T) {
	const header = "# Bug Ledger\n\n| ID | Title | Severity | Status | Location | Resolution |\n| --- | --- | --- | --- | --- | --- |\n"
	const resolved = "| `BUG-001` | Preserved finding | p1 | resolved | parser.go | Fixed |\n"
	t.Run("resolved evidence remains available", func(t *testing.T) {
		root := writeDistillerLedger(t, header+resolved)
		facts, err := distillStateFacts(context.Background(), root)
		if err != nil || len(facts) != 1 {
			t.Fatalf("expected one resolved fact, got %+v, %v", facts, err)
		}
		if facts[0].Subject != "BUG-001" || facts[0].Category != CategoryBugRuling {
			t.Fatalf("wrong resolved evidence: %+v", facts[0])
		}
	})
	t.Run("missing ledger remains an empty optional source", func(t *testing.T) {
		facts, err := distillStateFacts(context.Background(), t.TempDir())
		if err != nil || len(facts) != 0 {
			t.Fatalf("missing ledger: %+v, %v", facts, err)
		}
	})
	t.Run("malformed source prevents a partial success report", func(t *testing.T) {
		root := writeDistillerLedger(t, header+resolved+"| `BUG-002` | truncated row |\n")
		facts, err := distillStateFacts(context.Background(), root)
		if err == nil || facts != nil {
			t.Fatalf("malformed ledger became facts: %+v, %v", facts, err)
		}
		report, err := DistillWorkspace(context.Background(), root)
		if err == nil || report != nil {
			t.Fatalf("malformed ledger became a successful report: %+v, %v", report, err)
		}
	})
}

func writeDistillerLedger(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	writeDistillerFile(t, root, filepath.Join(".workingdir", "BUGS.md"), content)
	return root
}

// TestDistillFlavorFacts pins BUG-939 in the distiller: a repository no flavor matches used to be
// stored as governed under go-library, because detection substituted that name.
func TestDistillFlavorFacts(t *testing.T) {
	t.Run("matched flavor yields one fact", func(t *testing.T) {
		root := t.TempDir()
		writeDistillerFile(t, root, "go.mod", "module x\n")
		writeDistillerFile(t, root, filepath.Join("cmd", "x", "main.go"), "package main\n")
		facts, err := distillFlavorFacts(context.Background(), root)
		if err != nil || len(facts) != 1 || facts[0].Subject != "go-service" {
			t.Fatalf("expected one go-service fact, got %+v, %v", facts, err)
		}
	})
	t.Run("unmatched repository yields no fact", func(t *testing.T) {
		root := t.TempDir()
		writeDistillerFile(t, root, "Rakefile", "task :default\n")
		facts, err := distillFlavorFacts(context.Background(), root)
		if err != nil || len(facts) != 0 {
			t.Fatalf("an unmatched repository must yield no flavor fact, got %+v, %v", facts, err)
		}
	})
	// BUG-940: the declared profile decides the flavor, and the statement names it a flavor of
	// that profile rather than an archetype.
	t.Run("declared profile decides the fact", func(t *testing.T) {
		root := t.TempDir()
		writeDistillerFile(t, root, ".standards.yaml", "version: 1\nprofiles:\n  - native-gpu-systems\n")
		writeDistillerFile(t, root, "CMakeLists.txt", "project(engine)\n")
		writeDistillerFile(t, root, "pyproject.toml", "[project]\ndependencies = [\"torch\"]\n")
		facts, err := distillFlavorFacts(context.Background(), root)
		if err != nil || len(facts) != 1 || facts[0].Subject != "native-gpu-systems" {
			t.Fatalf("expected one native-gpu-systems fact, got %+v, %v", facts, err)
		}
		if want := "Repository uses flavor native-gpu-systems under profile native-gpu-systems."; !strings.HasPrefix(facts[0].Statement, want) {
			t.Errorf("statement %q does not start with %q", facts[0].Statement, want)
		}
	})
	t.Run("declared profile without flavor yields no fact", func(t *testing.T) {
		root := t.TempDir()
		writeDistillerFile(t, root, ".standards.yaml", "version: 1\nprofiles:\n  - gitops-infra\n")
		writeDistillerFile(t, root, "pyproject.toml", "[project]\ndependencies = [\"torch\"]\n")
		facts, err := distillFlavorFacts(context.Background(), root)
		if err != nil || len(facts) != 0 {
			t.Fatalf("a profile with no flavor must yield no flavor fact, got %+v, %v", facts, err)
		}
	})
	t.Run("empty directory yields no fact", func(t *testing.T) {
		facts, err := distillFlavorFacts(context.Background(), t.TempDir())
		if err != nil || len(facts) != 0 {
			t.Fatalf("an empty directory must yield no flavor fact, got %+v, %v", facts, err)
		}
	})
}

// TestDistillFlavorFacts_Pins: the distiller reads the manifest's flavors pins through the same
// resolver as the audit, one fact per pin, and a pin scoped to a directory says so.
func TestDistillFlavorFacts_Pins(t *testing.T) {
	root := t.TempDir()
	writeDistillerFile(t, root, "go.mod", "module x\n")
	writeDistillerFile(t, root, filepath.Join("cmd", "x", "main.go"), "package main\n")
	writeDistillerFile(t, root, filepath.Join("web", "package.json"), `{"name": "web"}`)
	writeDistillerFile(t, root, ".standards.yaml",
		"version: 1\nflavors:\n  - name: go-library\n  - name: typescript-node\n    path: web\n")
	facts, err := distillFlavorFacts(context.Background(), root)
	if err != nil || len(facts) != 2 || facts[0].Subject != "go-library" || facts[1].Subject != "typescript-node" {
		t.Fatalf("expected the two pinned flavors rather than detected go-service, got %+v, %v", facts, err)
	}
	if !strings.HasPrefix(facts[1].Statement, "Directory web uses flavor typescript-node") {
		t.Errorf("scoped pin statement = %q", facts[1].Statement)
	}
	writeDistillerFile(t, root, ".standards.yaml", "version: 1\nflavors:\n  - name: no-such-flavor\n")
	if _, err := distillFlavorFacts(context.Background(), root); err == nil {
		t.Error("an unknown pinned flavor must be an error, not a silent fall back to detection")
	}
}

// TestDistillFlavorFacts_RecordsTheResolutionProfile: the fact names the profile the resolution
// used (the declared one), not the flavor's own, which differs for a pinned flavor of another
// profile (#1103 review).
func TestDistillFlavorFacts_RecordsTheResolutionProfile(t *testing.T) {
	root := t.TempDir()
	writeDistillerFile(t, root, ".standards.yaml",
		"version: 1\nprofiles:\n  - app-service\nflavors:\n  - name: go-library\n")
	facts, err := distillFlavorFacts(context.Background(), root)
	if err != nil || len(facts) != 1 {
		t.Fatalf("facts = %+v, %v", facts, err)
	}
	if !strings.Contains(facts[0].Statement, "under profile app-service") {
		t.Errorf("statement = %q; want the declared profile app-service", facts[0].Statement)
	}
}
