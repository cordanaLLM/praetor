// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/templates"
)

// TestEmittedDraftWorkflows audits every committed workflow rendering and hosted emitter.
func TestEmittedDraftWorkflows(t *testing.T) {
	files := emittedDraftWorkflowFixtures(t)
	for rel, body := range hostedGateRenderings(t, "main") {
		files[rel] = body
	}
	reuse, err := reuseWorkflow("main")
	if err != nil {
		t.Fatal(err)
	}
	files[reuseWorkflowFile] = reuse
	if len(files) < 10 || len(files) > 128 {
		t.Fatalf("workflow coverage unexpectedly small or above bound: %d", len(files))
	}
	for rel, body := range files {
		t.Run(rel, func(t *testing.T) {
			out := auditEmittedDraftWorkflow(t, body)
			if !strings.HasPrefix(out, "[PASS]") {
				t.Fatalf("emitted workflow runs on drafts or misses ready_for_review:\n%s", out)
			}
		})
	}
}

// emittedDraftWorkflowFixtures reads workflow YAML only, with a scalar cap on visited entries.
func emittedDraftWorkflowFixtures(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	visited := 0
	for _, root := range []string{"../../templates/testdata/rendered", emittedFixtureRoot} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			visited++
			if visited > 512 {
				return fmt.Errorf("workflow fixture walk exceeds 512 entries")
			}
			return readEmittedWorkflowFixture(path, entry, files)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// readEmittedWorkflowFixture collects a workflow rendering without reading other configuration.
func readEmittedWorkflowFixture(path string, entry fs.DirEntry, files map[string]string) error {
	if entry.IsDir() || filepath.Ext(path) != ".yml" && filepath.Ext(path) != ".yaml" {
		return nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read workflow fixture %s: %w", path, err)
	}
	spec, err := ghworkflow.Parse(body)
	if err != nil {
		return fmt.Errorf("parse workflow fixture %s: %w", path, err)
	}
	if len(spec.Jobs) > 0 {
		files[path] = string(body)
	}
	return nil
}

// auditEmittedDraftWorkflow runs the existing workflow audit against a temporary root.
func auditEmittedDraftWorkflow(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".github/workflows/emitted.yml"), body)
	out, err := forge.AuditWorkflowTriggers(t.Context(), forge.WorkflowTriggerOptions{Root: root})
	if err != nil {
		t.Fatalf("audit emitted workflow: %v", err)
	}
	return out
}

// TestEmittedDraftWorkflowPlantedCases proves missing draft and ready guards get findings.
func TestEmittedDraftWorkflowPlantedCases(t *testing.T) {
	body, err := templates.RenderFile("go/ci-go.yml.tmpl", templates.Context{})
	if err != nil {
		t.Fatal(err)
	}
	for name, mutated := range map[string]string{
		"no draft gate":    strings.Replace(body, ghworkflow.HostedGateDraftStep, "", 1),
		"no ready trigger": strings.Replace(body, ", ready_for_review", "", 1),
	} {
		if mutated == body {
			t.Fatalf("%s: planted case changed nothing", name)
		}
		if out := auditEmittedDraftWorkflow(t, mutated); !strings.Contains(out, "[WARN]") {
			t.Fatalf("%s: planted case escaped workflow audit:\n%s", name, out)
		}
	}
}
