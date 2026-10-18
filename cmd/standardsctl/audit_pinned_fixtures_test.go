// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// The CLI audit runs the HISS-21 pinned fixture gate: a test that pins a fixture without an eol rule
// in .gitattributes fails the audit, naming the file, the test, and the line to add.
func TestAuditPinnedFixtures_CLI(t *testing.T) {
	f := newAuditFixture(t)
	const srtContent = "1\n00:00:01,000 --> 00:00:04,000\nHello\n"
	writeFixtureFile(t, f.dir, "testdata/sub.srt", srtContent)
	sum := sha256.Sum256([]byte(srtContent))
	hash := hex.EncodeToString(sum[:])
	writeFixtureFile(t, f.dir, "sub_test.go", fmt.Sprintf("package main\n\nconst wantSHA = %q\n", hash))
	testsupport.RunFixtureGit(t, f.dir, []string{"add", "-A"})

	out, err := f.audit(t)
	if err == nil || !strings.Contains(err.Error()+out, "testdata/sub.srt") || !strings.Contains(err.Error()+out, "sub_test.go") {
		t.Fatalf("audit = %v\nOutput: %s\nwant testdata/sub.srt refused", err, out)
	}

	// Adding an eol rule passes the audit.
	writeFixtureFile(t, f.dir, ".gitattributes", "*.srt -text\n")
	testsupport.RunFixtureGit(t, f.dir, []string{"add", "-A"})
	out, err = f.audit(t)
	if err != nil || !strings.Contains(out, "[PASS] Platform neutrality (HISS-21)") {
		t.Fatalf("audit with *.srt -text = %v\nOutput: %s\nwant pass", err, out)
	}
}
