// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/templates"
)

// maxAuditedWorkflows bounds the workflows TestEmittedDraftWorkflows audits (HISS-02).
const maxAuditedWorkflows = 128

// maxHostedGates bounds the hosted gate workflows audited in TestEmittedDraftWorkflows (HISS-02).
const maxHostedGates = 16

// maxWorkflowFixtureBytes bounds one workflow fixture read (HISS-02).
const maxWorkflowFixtureBytes = 1 << 20

// maxWalkEntries bounds the directory traversal for workflow fixtures (HISS-02).
const maxWalkEntries = 512

// TestEmittedDraftWorkflows audits every committed workflow rendering and hosted emitter.
func TestEmittedDraftWorkflows(t *testing.T) {
	files := emittedDraftWorkflowFixtures(t)
	gates := hostedGateRenderings(t, "main")
	if len(gates) > maxHostedGates {
		t.Fatalf("hosted gates unexpectedly above bound: %d", len(gates))
	}
	hostedCount := 0
	for rel, body := range gates {
		hostedCount++
		if hostedCount > maxHostedGates {
			t.Fatalf("hosted gate iterations exceeded bound: %d", maxHostedGates)
		}
		files[rel] = body
	}
	reuse, err := reuseWorkflow("main")
	if err != nil {
		t.Fatal(err)
	}
	files[reuseWorkflowFile] = reuse
	if len(files) < 10 || len(files) > maxAuditedWorkflows {
		t.Fatalf("workflow coverage unexpectedly small or above bound: %d", len(files))
	}
	audited := 0
	for rel, body := range files {
		audited++
		if audited > maxAuditedWorkflows {
			t.Fatalf("workflow audits exceeded bound: %d", maxAuditedWorkflows)
		}
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
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	files := map[string]string{}
	visited := 0
	for _, root := range []string{"../../templates/testdata/rendered", emittedFixtureRoot} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			visited++
			if visited > maxWalkEntries {
				return fmt.Errorf("workflow fixture walk exceeds %d entries", maxWalkEntries)
			}
			return readEmittedWorkflowFixture(ctx, path, entry, files)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// readEmittedWorkflowFixture collects a workflow rendering without reading other configuration.
func readEmittedWorkflowFixture(ctx context.Context, path string, entry fs.DirEntry, files map[string]string) error {
	if entry.IsDir() || filepath.Ext(path) != ".yml" && filepath.Ext(path) != ".yaml" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	body, err := util.ReadFileLimited(path, maxWorkflowFixtureBytes)
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

// TestReadEmittedWorkflowFixtureRefusesCanceledContext proves fixture reads fail closed when context is canceled.
func TestReadEmittedWorkflowFixtureRefusesCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "workflow.yml")
	if err := os.WriteFile(path, []byte("name: test\njobs:\n  test:\n    runs-on: ubuntu-latest\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entry, err := os.ReadDir(tmp)
	if err != nil || len(entry) == 0 {
		t.Fatal(err)
	}
	files := make(map[string]string)
	err = readEmittedWorkflowFixture(ctx, path, entry[0], files)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("files must remain empty on canceled context, got %d", len(files))
	}
}

// TestReadEmittedWorkflowFixtureRefusesOversizedFixture proves fixture reads fail closed on oversized files.
func TestReadEmittedWorkflowFixtureRefusesOversizedFixture(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "oversized.yml")
	large := make([]byte, maxWorkflowFixtureBytes+1)
	if err := os.WriteFile(path, large, 0o600); err != nil {
		t.Fatal(err)
	}
	entry, err := os.ReadDir(tmp)
	if err != nil || len(entry) == 0 {
		t.Fatal(err)
	}
	files := make(map[string]string)
	err = readEmittedWorkflowFixture(t.Context(), path, entry[0], files)
	if !errors.Is(err, util.ErrFileTooLarge) {
		t.Fatalf("expected ErrFileTooLarge, got %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("files must remain empty on oversized fixture, got %d", len(files))
	}
}
