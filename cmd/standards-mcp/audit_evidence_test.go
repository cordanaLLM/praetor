// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"

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
