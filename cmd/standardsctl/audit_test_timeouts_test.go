// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"
)

// The CLI audit runs the test-timeout gate (#1381): a workflow that sets a 60m test timeout with no
// exceptions entry fails the audit and names the line.
func TestAuditTestTimeouts_CLI(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, ".github/workflows/portability.yml",
		"on: push\njobs:\n  test:\n    runs-on: windows-latest\n    steps:\n      - run: go test -timeout 60m ./...\n")
	out, err := f.audit(t)
	if err == nil || !strings.Contains(err.Error()+out, ".github/workflows/portability.yml:6 sets 60m") {
		t.Fatalf("audit = %v\nOutput: %s\nwant the 60m timeout refused", err, out)
	}
}
