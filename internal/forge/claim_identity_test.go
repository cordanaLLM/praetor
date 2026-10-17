// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/forge/forgetest"
)

func TestPlantedNegative_PlainSessionNeverRendered(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	c := forge.Claim{
		Session: "confidential-session-99",
		Lane:    "private-lane-alpha",
		Branch:  "feat/safe",
		Started: claimEpoch,
		Updated: claimEpoch,
		Stage:   "implementing",
	}

	marker, err := forge.RenderClaimMarkerWithKey(c, key)
	if err != nil {
		t.Fatalf("render marker: %v", err)
	}
	if strings.Contains(marker, "confidential-session-99") {
		t.Fatalf("marker leaked plain session: %s", marker)
	}
	if strings.Contains(marker, "private-lane-alpha") {
		t.Fatalf("marker leaked plain lane: %s", marker)
	}

	wantSessionTag := forge.ClaimTag(key, "session", "confidential-session-99")
	wantLaneTag := forge.ClaimTag(key, "lane", "private-lane-alpha")
	if !strings.Contains(marker, "session="+wantSessionTag) {
		t.Fatalf("marker missing session tag %s: %s", wantSessionTag, marker)
	}
	if !strings.Contains(marker, "lane="+wantLaneTag) {
		t.Fatalf("marker missing lane tag %s: %s", wantLaneTag, marker)
	}

	// Now verify desk writes comment without plain session or lane
	f, clock := forgetest.NewClaimFake(), &deskClock{now: claimEpoch}
	d := &forge.ClaimDesk{
		Open:  f.Opener(),
		Stale: 6 * time.Hour,
		Now:   clock.Now,
		Key:   key,
	}
	res, err := d.Claim(context.Background(), testRef(), forge.ClaimRequest{
		Session: "confidential-session-99",
		Lane:    "private-lane-alpha",
		Branch:  "feat/safe",
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if res.Action != "created" {
		t.Fatalf("unexpected action: %s", res.Action)
	}
	if len(f.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(f.Comments))
	}
	body := f.Comments[0].Body
	if strings.Contains(body, "confidential-session-99") {
		t.Fatalf("desk comment leaked plain session: %s", body)
	}
	if strings.Contains(body, "private-lane-alpha") {
		t.Fatalf("desk comment leaked plain lane: %s", body)
	}
	if !strings.Contains(body, wantSessionTag) || !strings.Contains(body, wantLaneTag) {
		t.Fatalf("desk comment missing tags: %s", body)
	}
}

func TestPlantedNegative_PrivateRepoRefusedByGuard(t *testing.T) {
	tempDir := t.TempDir()
	privateFile := filepath.Join(tempDir, "private-names.txt")
	names := "secret-repo\noffice-zurich\nprivate-lane\n"
	if err := os.WriteFile(privateFile, []byte(names), 0o600); err != nil {
		t.Fatalf("write private names: %v", err)
	}

	guard := forge.NewPublicTextGuard(privateFile)

	// Refusal for private name in PR body
	err := guard.Check(context.Background(), "Update readme", "This fixes issues in secret-repo for the client")
	if err == nil {
		t.Fatal("expected guard to refuse private repo name")
	}
	var protErr *forge.ProtectedTextError
	if !errors.As(err, &protErr) {
		t.Fatalf("expected ProtectedTextError, got: %v", err)
	}
	if protErr.Kind != "private name" {
		t.Fatalf("expected kind 'private name', got %q", protErr.Kind)
	}
	if strings.Contains(err.Error(), "secret-repo") {
		t.Fatalf("error message must not leak the private value: %s", err.Error())
	}

	// Word boundary test: "secret-repo-public" should NOT match "secret-repo"
	if err := guard.Check(context.Background(), "Title", "References secret-repo-public only"); err != nil {
		t.Fatalf("word boundary check failed, should not match prefix: %v", err)
	}

	// Refusal for hostname
	hostname, errHost := os.Hostname()
	if errHost == nil && hostname != "" {
		hostErr := guard.Check(context.Background(), "Title", "Built on host "+hostname)
		if hostErr == nil {
			t.Fatal("expected guard to refuse hostname")
		}
		if !errors.As(hostErr, &protErr) || protErr.Kind != "hostname" {
			t.Fatalf("expected kind 'hostname', got %v", hostErr)
		}
		if strings.Contains(hostErr.Error(), hostname) {
			t.Fatalf("error must not leak hostname: %v", hostErr)
		}
	}

	// Refusal for session/lane in context
	ctx := forge.WithProtectedIdentity(context.Background(), "my-sess-99", "secret-lane")
	sessErr := guard.Check(ctx, "Title", "Fixes done by my-sess-99 here")
	if sessErr == nil {
		t.Fatal("expected guard to refuse session from context")
	}
	if !errors.As(sessErr, &protErr) || protErr.Kind != "session" {
		t.Fatalf("expected kind 'session', got %v", sessErr)
	}
	if strings.Contains(sessErr.Error(), "my-sess-99") {
		t.Fatalf("error must not leak session: %v", sessErr)
	}
}

func TestPlantedNegative_TagGuessingAndPrefixResistance(t *testing.T) {
	key1 := []byte("11111111111111111111111111111111")
	key2 := []byte("22222222222222222222222222222222")

	dict := []string{
		"session-1", "s1", "worker", "agent-alpha", "dev-session", "test",
		"session-007", "implementer", "reviewer",
	}

	for _, name := range dict {
		tag1 := forge.ClaimTag(key1, "session", name)
		tag2 := forge.ClaimTag(key2, "session", name)

		if tag1 == tag2 {
			t.Fatalf("tags under different keys must not collide for %s: %s vs %s", name, tag1, tag2)
		}
		if !strings.HasPrefix(tag1, "h:") || len(tag1) != 14 {
			t.Fatalf("tag format invalid: %s", tag1)
		}
		if !strings.HasPrefix(tag2, "h:") || len(tag2) != 14 {
			t.Fatalf("tag format invalid: %s", tag2)
		}

		// Verify tag reveals no prefix of the plain name
		rawHex := tag1[2:]
		cleanName := strings.ToLower(name)
		if strings.HasPrefix(rawHex, cleanName[:1]) && len(cleanName) > 2 && strings.HasPrefix(rawHex, cleanName[:2]) {
			t.Fatalf("tag hex %s reveals prefix of name %s", rawHex, name)
		}
	}
}

func TestPlantedNegative_LegacyPlainMarkerCompatibility(t *testing.T) {
	key := []byte("key-for-legacy-compatibility-123")
	f, clock := forgetest.NewClaimFake(), &deskClock{now: claimEpoch}
	d := &forge.ClaimDesk{
		Open:  f.Opener(),
		Stale: 6 * time.Hour,
		Now:   clock.Now,
		Key:   key,
	}

	// Seed with legacy plain marker
	legacyComment := "<!-- praetor-claim v=1 session=legacy-sess lane=legacy-lane branch=feat/legacy started=2026-10-08T12:00:00Z updated=2026-10-08T12:00:00Z stage=implementing assignee=claim -->\n" +
		"**Praetor claim** - session `legacy-sess`, lane `legacy-lane`, branch `feat/legacy`\n\n- Stage: implementing\n"
	f.Comments = []forge.IssueComment{
		{ID: 10, Author: "operator", Association: "OWNER", Body: legacyComment},
	}
	f.NextID = 20

	// 1. Legacy marker parses properly
	claim, ok := forge.ParseClaimMarker(legacyComment)
	if !ok || claim.Session != "legacy-sess" || claim.Lane != "legacy-lane" {
		t.Fatalf("legacy marker must parse: %+v, ok=%v", claim, ok)
	}

	// 2. Different session is refused naming the legacy holder
	_, err := d.Claim(context.Background(), testRef(), forge.ClaimRequest{
		Session: "other-session",
		Lane:    "other-lane",
		Branch:  "feat/other",
	})
	var heldErr *forge.ClaimHeldError
	if !errors.Is(err, forge.ErrClaimHeld) || !errors.As(err, &heldErr) {
		t.Fatalf("second claimer must be refused: %v", err)
	}
	if heldErr.Holder.Session != "legacy-sess" {
		t.Fatalf("refusal must name legacy session: %s", heldErr.Holder.Session)
	}

	// 3. Same session resumes the legacy claim
	resumed, err := d.Claim(context.Background(), testRef(), forge.ClaimRequest{
		Session: "legacy-sess",
		Lane:    "legacy-lane",
		Branch:  "feat/legacy",
	})
	if err != nil {
		t.Fatalf("resume legacy claim: %v", err)
	}
	if resumed.Action != "resumed" {
		t.Fatalf("expected resumed action, got %s", resumed.Action)
	}

	// 4. Status update tags the comment
	statusRes, err := d.Status(context.Background(), testRef(), forge.StatusRequest{
		Session: "legacy-sess",
		Stage:   "review",
		Note:    "ready",
	})
	if err != nil {
		t.Fatalf("status update on legacy claim: %v", err)
	}
	if statusRes.Action != "updated" {
		t.Fatalf("expected updated action: %s", statusRes.Action)
	}
	// The edited comment must now have tagged session, not plain
	updatedMarker, ok := forge.ParseClaimMarker(f.Comments[0].Body)
	if !ok {
		t.Fatalf("updated marker not parseable: %s", f.Comments[0].Body)
	}
	wantLegacyTag := forge.ClaimTag(key, "session", "legacy-sess")
	if updatedMarker.Session != wantLegacyTag {
		t.Fatalf("updated marker should have tagged session %s, got %s", wantLegacyTag, updatedMarker.Session)
	}

	// 5. Release works
	relRes, err := d.Release(context.Background(), testRef(), forge.ReleaseRequest{
		Session: "legacy-sess",
		Outcome: "landed",
	})
	if err != nil {
		t.Fatalf("release legacy claim: %v", err)
	}
	if relRes.Action != "released" {
		t.Fatalf("expected released action: %s", relRes.Action)
	}
}

func TestPublicTextGuard_Boundary_GuardedTextsLimit(t *testing.T) {
	guard := forge.NewPublicTextGuard("")
	texts32 := make([]string, 32)
	for i := 0; i < 32; i++ {
		texts32[i] = "safe text"
	}
	if err := guard.Check(context.Background(), texts32...); err != nil {
		t.Fatalf("32 texts must pass, got: %v", err)
	}

	texts33 := make([]string, 33)
	for i := 0; i < 33; i++ {
		texts33[i] = "safe text"
	}
	err := guard.Check(context.Background(), texts33...)
	if err == nil {
		t.Fatal("33 texts must fail, got nil")
	}
	if !errors.Is(err, forge.ErrTooManyGuardedTexts) {
		t.Fatalf("expected ErrTooManyGuardedTexts, got: %v", err)
	}
}

func TestPublicTextGuard_Boundary_PrivateNamesCount(t *testing.T) {
	tempDir := t.TempDir()

	// Exactly 1000 names must pass
	file1000 := filepath.Join(tempDir, "names-1000.txt")
	var buf1000 strings.Builder
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&buf1000, "project-name-%d\n", i)
	}
	if err := os.WriteFile(file1000, []byte(buf1000.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	guard1000 := forge.NewPublicTextGuard(file1000)
	if err := guard1000.Check(context.Background(), "safe text"); err != nil {
		t.Fatalf("1000 private names must pass, got: %v", err)
	}

	// 1001 names must fail
	file1001 := filepath.Join(tempDir, "names-1001.txt")
	var buf1001 strings.Builder
	for i := 0; i < 1001; i++ {
		fmt.Fprintf(&buf1001, "project-name-%d\n", i)
	}
	if err := os.WriteFile(file1001, []byte(buf1001.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	guard1001 := forge.NewPublicTextGuard(file1001)
	err := guard1001.Check(context.Background(), "safe text")
	if err == nil {
		t.Fatal("1001 private names must fail, got nil")
	}
	if !errors.Is(err, forge.ErrTooManyPrivateNames) {
		t.Fatalf("expected ErrTooManyPrivateNames, got: %v", err)
	}
}

func TestPublicTextGuard_Boundary_PrivateNamesByteLimit(t *testing.T) {
	tempDir := t.TempDir()

	// Exactly 65536 bytes must pass
	fileExact := filepath.Join(tempDir, "exact.txt")
	contentExact := make([]byte, 65536)
	for i := range contentExact {
		contentExact[i] = '#'
	}
	contentExact[65535] = '\n'
	if err := os.WriteFile(fileExact, contentExact, 0o600); err != nil {
		t.Fatal(err)
	}
	guardExact := forge.NewPublicTextGuard(fileExact)
	if err := guardExact.Check(context.Background(), "safe text"); err != nil {
		t.Fatalf("file of exactly 65536 bytes must pass, got: %v", err)
	}

	// 65537 bytes must fail
	fileOver := filepath.Join(tempDir, "over.txt")
	contentOver := make([]byte, 65537)
	for i := range contentOver {
		contentOver[i] = '#'
	}
	contentOver[65536] = '\n'
	if err := os.WriteFile(fileOver, contentOver, 0o600); err != nil {
		t.Fatal(err)
	}
	guardOver := forge.NewPublicTextGuard(fileOver)
	err := guardOver.Check(context.Background(), "safe text")
	if err == nil {
		t.Fatal("file of 65537 bytes must fail, got nil")
	}
	if !errors.Is(err, forge.ErrPrivateNamesFile) {
		t.Fatalf("expected ErrPrivateNamesFile, got: %v", err)
	}
}

func TestPublicTextGuard_Boundary_UnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping unreadable file test: Windows file permissions do not support POSIX 0000 mode")
	}
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "unreadable.txt")
	if err := os.WriteFile(filePath, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filePath, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if chErr := os.Chmod(filePath, 0o600); chErr != nil {
			t.Logf("cleanup chmod %s: %v", filePath, chErr)
		}
	}()

	guard := forge.NewPublicTextGuard(filePath)
	err := guard.Check(context.Background(), "safe text")
	if err == nil {
		t.Fatal("unreadable private names file must fail Check, got nil")
	}
	if !errors.Is(err, forge.ErrPrivateNamesFile) {
		t.Fatalf("expected ErrPrivateNamesFile, got: %v", err)
	}
}

func TestPublicTextGuard_Boundary_MissingFileContinues(t *testing.T) {
	tempDir := t.TempDir()
	missingPath := filepath.Join(tempDir, "does-not-exist.txt")
	guard := forge.NewPublicTextGuard(missingPath)
	if err := guard.Check(context.Background(), "safe text"); err != nil {
		t.Fatalf("missing private names file must continue, got: %v", err)
	}
}
