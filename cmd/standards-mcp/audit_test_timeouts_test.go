// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import "testing"

// standards_audit runs the CLI audit's test-timeout gate (#1381): a workflow with a 60m test
// timeout and no exceptions entry fails the audit with the CLI's wording.
func TestServerAuditTestTimeoutsLikeTheCLI(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeFixtureFile(t, root, ".github/workflows/portability.yml",
		"on: push\njobs:\n  test:\n    runs-on: windows-latest\n    steps:\n      - run: go test -timeout 60m ./...\n")
	expectError(t, "60m timeout", callTool(t, srv, "standards_audit", nil), ".github/workflows/portability.yml:6 sets 60m")
}
