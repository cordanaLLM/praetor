// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// standards_audit holds the evidence directory to the check the CLI audit runs
// (compiler.CheckEvidenceIgnored): negative, a Git work tree whose .gitignore re-includes
// .workingdir/evidence/ fails the audit with the CLI's wording; positive, the same tree ignoring
// .workingdir/ passes that check; boundary, a fixture in no work tree has nothing to publish.
func TestServerAuditChecksEvidenceIgnoreLikeTheCLI(t *testing.T) {
	const failure = "Agent context evidence directory: git does not ignore .workingdir/evidence/"

	srv, root := newFixtureServer(t)
	testsupport.InitGitRepoWithOrigin(t, root, "")
	writeFixtureFile(t, root, ".gitignore", ".workingdir/*\n!.workingdir/evidence/\n")
	expectError(t, "evidence re-included", callTool(t, srv, "standards_audit", nil), failure)

	writeFixtureFile(t, root, ".gitignore", ".workingdir/\n")
	if text := callTool(t, srv, "standards_audit", nil).Content[0].Text; strings.Contains(text, "evidence directory") {
		t.Fatalf("an ignored evidence directory still fails the audit:\n%s", text)
	}

	plain, _ := newFixtureServer(t)
	if text := callTool(t, plain, "standards_audit", nil).Content[0].Text; strings.Contains(text, "evidence directory") {
		t.Fatalf("a fixture in no Git work tree failed the evidence check:\n%s", text)
	}
}

// standards_audit probes the directory register.evidence.dir names, as the CLI audit does:
// negative, a tree ignoring only .workingdir/ fails naming scratch/evidence/; positive, the same
// tree ignoring /scratch/ passes that check although .workingdir/ is no longer ignored.
func TestServerAuditProbesConfiguredEvidenceDir(t *testing.T) {
	srv, root := newFixtureServer(t)
	testsupport.InitGitRepoWithOrigin(t, root, "")
	writeFixtureFile(t, root, ".standards.yaml", "version: 1\nrepository:\n  owner: \"fixture\"\n  name: \"repo\"\n"+
		"profiles:\n  - \"framework\"\nfacets: []\nregister:\n  evidence:\n    dir: scratch/evidence/\n")
	agents := filepath.Join(root, "AGENTS.md")
	if _, err := compiler.SyncRegisterBlock(context.Background(), root, agents, true); err != nil {
		t.Fatalf("splice the configured register block: %v", err)
	}
	tr := compiler.NewTranspiler()
	res, err := tr.Compile(agents)
	if err != nil {
		t.Fatalf("compile fixture AGENTS.md: %v", err)
	}
	if err := tr.WriteOutputs(res, root); err != nil {
		t.Fatalf("write fixture vendor targets: %v", err)
	}
	writeFixtureFile(t, root, ".gitignore", "/.workingdir/\n")
	expectError(t, "configured directory unignored", callTool(t, srv, "standards_audit", nil),
		"Agent context evidence directory: git does not ignore scratch/evidence/")

	writeFixtureFile(t, root, ".gitignore", "/scratch/\n")
	if text := callTool(t, srv, "standards_audit", nil).Content[0].Text; strings.Contains(text, "evidence directory") {
		t.Fatalf("an ignored configured directory still fails the audit:\n%s", text)
	}
}
