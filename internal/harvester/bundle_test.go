package harvester

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupMockWorkstation(t *testing.T, mockHome, mockVault string) {
	t.Helper()
	mustWriteFile(t, filepath.Join(mockHome, ".gemini", "config", "skills", "test-skill", "SKILL.md"), "# Test Skill")
	mustWriteFile(t, filepath.Join(mockHome, ".claude", "projects", "proj1", "memory", "MEMORY.md"), "# Memory")
	mustWriteFile(t, filepath.Join(mockVault, "dev-patches", "test.patch"), "diff --git a b")
	mustWriteFile(t, filepath.Join(mockHome, ".codex", "config.toml"), "model = 'o3'")
	mustWriteFile(t, filepath.Join(mockHome, ".copilot", "config.json"), "{}")
}

func recordFor(t *testing.T, rep *WorkstationBundleReport, suffix string) BundleFileRecord {
	t.Helper()
	for _, r := range rep.Records {
		if strings.HasSuffix(r.RelativePath, suffix) {
			return r
		}
	}
	t.Fatalf("no bundle record ending in %q; records: %+v", suffix, rep.Records)
	return BundleFileRecord{}
}

func TestBundleWorkstation_Positive(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	mockHome := filepath.Join(tmpDir, "home")
	mockOut := filepath.Join(tmpDir, "out")
	mockVault := filepath.Join(tmpDir, "vault")

	setupMockWorkstation(t, mockHome, mockVault)

	opts := BundleOptions{
		WorkstationName: "test-box",
		OutputDir:       mockOut,
		HomeDir:         mockHome,
		VaultDir:        mockVault,
	}

	rep, err := BundleWorkstation(ctx, opts)
	if err != nil {
		t.Fatalf("BundleWorkstation failed: %v", err)
	}

	if rep.TotalFiles != 5 {
		t.Fatalf("expected 5 bundled files, got: %d (%+v)", rep.TotalFiles, rep.Records)
	}
	if rep.WorkstationName != "test-box" {
		t.Fatalf("expected test-box, got: %s", rep.WorkstationName)
	}
	if _, err := os.Stat(rep.ManifestPath); err != nil {
		t.Fatalf("manifest not created at %s: %v", rep.ManifestPath, err)
	}

	ingest, err := IngestBundle(ctx, mockOut, filepath.Join(mockHome, "existing"), true)
	if err != nil {
		t.Fatalf("IngestBundle failed: %v", err)
	}
	if len(ingest.NovelSkills) != 1 || ingest.NovelSkills[0] != "test-skill" {
		t.Fatalf("expected novel skill 'test-skill', got: %v", ingest.NovelSkills)
	}
	if !ingest.ValidIntegrity {
		t.Fatalf("a freshly written bundle must verify, rejected: %v", ingest.RejectedRecords)
	}
}

// TestBundleWorkstation_OwnerOnlyPermissions pins the credential-exposure fix: nothing the
// bundler writes may be group- or world-readable.
func TestBundleWorkstation_OwnerOnlyPermissions(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	mockHome := filepath.Join(tmpDir, "home")
	mockOut := filepath.Join(tmpDir, "out")

	mustWriteFile(t, filepath.Join(mockHome, ".gemini", "config", "mcp_config.json"), `{"token":"secret"}`)

	rep, err := BundleWorkstation(ctx, BundleOptions{
		WorkstationName: "perm-box",
		OutputDir:       mockOut,
		HomeDir:         mockHome,
	})
	if err != nil {
		t.Fatalf("BundleWorkstation failed: %v", err)
	}

	rootInfo, err := os.Stat(mockOut)
	if err != nil {
		t.Fatal(err)
	}
	if rootInfo.Mode().Perm() != bundleDirPerm {
		t.Fatalf("bundle root mode is %v, want %v", rootInfo.Mode().Perm(), bundleDirPerm)
	}

	rec := recordFor(t, rep, "mcp_config.json")
	copied := filepath.Join(mockOut, filepath.FromSlash(rec.RelativePath))
	info, err := os.Stat(copied)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != bundleFilePerm {
		t.Fatalf("copied credential file mode is %v, want %v", info.Mode().Perm(), bundleFilePerm)
	}

	manifestInfo, err := os.Stat(rep.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifestInfo.Mode().Perm() != bundleFilePerm {
		t.Fatalf("manifest mode is %v, want %v", manifestInfo.Mode().Perm(), bundleFilePerm)
	}
}

// TestBundleWorkstation_SkipsSymlinkedSkillFiles pins the exfiltration fix: a symlink
// planted in a skill directory must not pull an arbitrary file into the bundle.
func TestBundleWorkstation_SkipsSymlinkedSkillFiles(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	mockHome := filepath.Join(tmpDir, "home")
	mockOut := filepath.Join(tmpDir, "out")

	secret := filepath.Join(mockHome, ".ssh", "id_ed25519")
	mustWriteFile(t, secret, "PRIVATE KEY")

	skillDir := filepath.Join(mockHome, ".claude", "skills", "evil")
	mustWriteFile(t, filepath.Join(skillDir, "SKILL.md"), "# Evil")
	if err := os.Symlink(secret, filepath.Join(skillDir, "creds")); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	rep, err := BundleWorkstation(ctx, BundleOptions{
		WorkstationName: "symlink-box",
		OutputDir:       mockOut,
		HomeDir:         mockHome,
	})
	if err != nil {
		t.Fatalf("BundleWorkstation failed: %v", err)
	}

	for _, r := range rep.Records {
		if strings.HasSuffix(r.RelativePath, "/creds") {
			t.Fatalf("symlinked file was bundled: %+v", r)
		}
	}
	if _, err := os.Stat(filepath.Join(mockOut, "agent-skills", "claude", "evil", "creds")); !os.IsNotExist(err) {
		t.Fatal("symlink target content must never land in the bundle")
	}
	if len(rep.Skipped) == 0 {
		t.Fatal("the refused symlink must be recorded in the bundle report")
	}
}

// TestBundleWorkstation_CopiesNestedSkillFiles pins the completeness fix: a skill's
// references/ tree must survive the round trip.
func TestBundleWorkstation_CopiesNestedSkillFiles(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	mockHome := filepath.Join(tmpDir, "home")
	mockOut := filepath.Join(tmpDir, "out")

	skillDir := filepath.Join(mockHome, ".claude", "skills", "dataviz")
	mustWriteFile(t, filepath.Join(skillDir, "SKILL.md"), "# Dataviz")
	mustWriteFile(t, filepath.Join(skillDir, "references", "palette.md"), "# Palette")

	rep, err := BundleWorkstation(ctx, BundleOptions{
		WorkstationName: "nested-box",
		OutputDir:       mockOut,
		HomeDir:         mockHome,
	})
	if err != nil {
		t.Fatalf("BundleWorkstation failed: %v", err)
	}
	if rep.TotalFiles != 2 {
		t.Fatalf("expected SKILL.md and references/palette.md, got %d: %+v", rep.TotalFiles, rep.Records)
	}
	nested := filepath.Join(mockOut, "agent-skills", "claude", "dataviz", "references", "palette.md")
	if _, err := os.Stat(nested); err != nil {
		t.Fatalf("nested skill file missing from bundle: %v", err)
	}
}

// TestBundleWorkstation_ShellHistoryIsOptIn pins the default: history is only bundled when
// the caller explicitly asks for it.
func TestBundleWorkstation_ShellHistoryIsOptIn(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	mockHome := filepath.Join(tmpDir, "home")
	mustWriteFile(t, filepath.Join(mockHome, ".bash_history"), "export GITHUB_TOKEN=ghp_example\n")

	repDefault, err := BundleWorkstation(ctx, BundleOptions{
		WorkstationName: "hist-default",
		OutputDir:       filepath.Join(tmpDir, "out-default"),
		HomeDir:         mockHome,
	})
	if err != nil {
		t.Fatalf("BundleWorkstation failed: %v", err)
	}
	if repDefault.Categories["cli-history"] != 0 {
		t.Fatalf("shell history must not be bundled by default: %+v", repDefault.Records)
	}

	repOptIn, err := BundleWorkstation(ctx, BundleOptions{
		WorkstationName:     "hist-optin",
		OutputDir:           filepath.Join(tmpDir, "out-optin"),
		HomeDir:             mockHome,
		IncludeShellHistory: true,
	})
	if err != nil {
		t.Fatalf("BundleWorkstation failed: %v", err)
	}
	if repOptIn.Categories["cli-history"] != 1 {
		t.Fatalf("opt-in must bundle the shell history, got: %+v", repOptIn.Records)
	}
}

func TestBundleWorkstation_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tmpDir := t.TempDir()
	opts := BundleOptions{
		WorkstationName: "test-box",
		OutputDir:       tmpDir,
		HomeDir:         tmpDir,
	}

	_, err := BundleWorkstation(ctx, opts)
	if err == nil {
		t.Fatal("expected error with cancelled context, got nil")
	}
}

func TestBundleWorkstation_Negative_MissingOutputDir(t *testing.T) {
	_, err := BundleWorkstation(context.Background(), BundleOptions{WorkstationName: "x"})
	if err == nil {
		t.Fatal("expected an error when no output directory is given")
	}
}

func TestBundleWorkstation_Boundary_EmptyInputs(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	opts := BundleOptions{
		WorkstationName: "empty-box",
		OutputDir:       filepath.Join(tmpDir, "out"),
		HomeDir:         filepath.Join(tmpDir, "nonexistent"),
	}

	rep, err := BundleWorkstation(ctx, opts)
	if err != nil {
		t.Fatalf("expected no error with empty inputs, got: %v", err)
	}
	if rep.TotalFiles != 0 {
		t.Fatalf("expected 0 files, got: %d", rep.TotalFiles)
	}
}

// writeBundleFile writes a bundle member and returns its manifest record.
func writeBundleFile(t *testing.T, bundleDir, rel, body, category string) BundleFileRecord {
	t.Helper()
	full := filepath.Join(bundleDir, filepath.FromSlash(rel))
	mustWriteFile(t, full, body)
	sum := sha256.Sum256([]byte(body))
	return BundleFileRecord{
		RelativePath: rel,
		SizeBytes:    int64(len(body)),
		SHA256:       hex.EncodeToString(sum[:]),
		Category:     category,
	}
}

// writeManifest serialises records into <bundleDir>/manifest.json.
func writeManifest(t *testing.T, bundleDir string, records []BundleFileRecord) {
	t.Helper()
	data, err := json.Marshal(WorkstationBundleReport{WorkstationName: "mock-box", Records: records})
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(bundleDir, "manifest.json"), string(data))
}

func TestIngestBundle_DeduplicationAndSystemFilter(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	bundleDir := filepath.Join(tmpDir, "bundle")
	localSkillsDir := filepath.Join(tmpDir, "local-skills")
	mustMkdirAll(t, filepath.Join(localSkillsDir, "existing-skill"))

	records := []BundleFileRecord{
		writeBundleFile(t, bundleDir, "agent-skills/copilot/novel-skill/SKILL.md", "content", "skill"),
		writeBundleFile(t, bundleDir, "agent-skills/copilot/novel-skill/LICENSE.txt", "content", "skill"),
		writeBundleFile(t, bundleDir, "agent-skills/codex/.system/.marker", "content", "skill"),
		writeBundleFile(t, bundleDir, "agent-skills/gemini/existing-skill/SKILL.md", "content", "skill"),
		writeBundleFile(t, bundleDir, "agent-memories/claude/p1/mem.md", "content", "project-memory"),
		writeBundleFile(t, bundleDir, "dev-patches/fix.patch", "content", "patch"),
	}
	records = append(records, records[4], records[5])
	writeManifest(t, bundleDir, records)

	ingest, err := IngestBundle(ctx, bundleDir, localSkillsDir, false)
	if err != nil {
		t.Fatalf("IngestBundle failed: %v", err)
	}

	if len(ingest.NovelSkills) != 1 || ingest.NovelSkills[0] != "novel-skill" {
		t.Fatalf("expected 1 novel skill 'novel-skill', got: %v", ingest.NovelSkills)
	}
	if len(ingest.ExistingSkills) != 1 || ingest.ExistingSkills[0] != "existing-skill" {
		t.Fatalf("expected 1 existing skill 'existing-skill', got: %v", ingest.ExistingSkills)
	}
	if len(ingest.NovelMemories) != 1 || len(ingest.NovelPatches) != 1 {
		t.Fatalf("expected 1 deduped memory and patch, got %d and %d", len(ingest.NovelMemories), len(ingest.NovelPatches))
	}

	copiedSkillMD := filepath.Join(localSkillsDir, "novel-skill", "SKILL.md")
	if _, err := os.Stat(copiedSkillMD); err != nil {
		t.Fatalf("expected copied file at %s: %v", copiedSkillMD, err)
	}
}

// TestIngestBundle_Negative_RejectsPathTraversal pins the containment fix: a manifest path
// that climbs out of the skills directory must be refused, not written.
func TestIngestBundle_Negative_RejectsPathTraversal(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	bundleDir := filepath.Join(tmpDir, "bundle")
	geminiDir := filepath.Join(tmpDir, "home", ".gemini")
	localSkillsDir := filepath.Join(geminiDir, "config", "skills")
	mustMkdirAll(t, localSkillsDir)

	victim := filepath.Join(geminiDir, "config", "hooks.json")
	mustWriteFile(t, victim, `{"hooks":"original"}`)

	rec := writeBundleFile(t, bundleDir, "agent-skills/copilot/evil/hooks.json", "pwned", "skill")
	rec.RelativePath = "agent-skills/copilot/evil/../../hooks.json"
	writeManifest(t, bundleDir, []BundleFileRecord{rec})

	ingest, err := IngestBundle(ctx, bundleDir, localSkillsDir, false)
	if err != nil {
		t.Fatalf("IngestBundle failed: %v", err)
	}
	if ingest.ValidIntegrity {
		t.Fatal("a traversing manifest record must clear ValidIntegrity")
	}
	if len(ingest.RejectedRecords) == 0 {
		t.Fatal("the traversing record must be reported as rejected")
	}
	body, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"hooks":"original"}` {
		t.Fatalf("the traversal overwrote %s: %s", victim, body)
	}
}

// TestIngestBundle_Negative_RejectsTamperedFile pins the integrity fix: content that does
// not match the manifest digest is refused and reported.
func TestIngestBundle_Negative_RejectsTamperedFile(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	bundleDir := filepath.Join(tmpDir, "bundle")
	localSkillsDir := filepath.Join(tmpDir, "local-skills")

	rec := writeBundleFile(t, bundleDir, "agent-skills/copilot/foo/SKILL.md", "original", "skill")
	mustWriteFile(t, filepath.Join(bundleDir, "agent-skills", "copilot", "foo", "SKILL.md"), "tampered")
	writeManifest(t, bundleDir, []BundleFileRecord{rec})

	ingest, err := IngestBundle(ctx, bundleDir, localSkillsDir, false)
	if err != nil {
		t.Fatalf("IngestBundle failed: %v", err)
	}
	if ingest.ValidIntegrity {
		t.Fatal("a tampered bundle file must clear ValidIntegrity")
	}
	if _, err := os.Stat(filepath.Join(localSkillsDir, "foo", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("a tampered file must not be installed")
	}
}

// TestIngestBundle_Negative_RejectsMissingDigest covers a manifest that records no hash.
func TestIngestBundle_Negative_RejectsMissingDigest(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	bundleDir := filepath.Join(tmpDir, "bundle")

	rec := writeBundleFile(t, bundleDir, "agent-skills/copilot/foo/SKILL.md", "original", "skill")
	rec.SHA256 = ""
	writeManifest(t, bundleDir, []BundleFileRecord{rec})

	ingest, err := IngestBundle(ctx, bundleDir, filepath.Join(tmpDir, "local"), false)
	if err != nil {
		t.Fatalf("IngestBundle failed: %v", err)
	}
	if ingest.ValidIntegrity {
		t.Fatal("a record without a digest must clear ValidIntegrity")
	}
}

func TestIngestBundle_Negative_MissingAndMalformedManifest(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	if _, err := IngestBundle(ctx, filepath.Join(tmpDir, "absent"), tmpDir, true); err == nil {
		t.Fatal("expected an error for a missing manifest")
	}

	broken := filepath.Join(tmpDir, "broken")
	mustWriteFile(t, filepath.Join(broken, "manifest.json"), "{not json")
	if _, err := IngestBundle(ctx, broken, tmpDir, true); err == nil {
		t.Fatal("expected an error for a malformed manifest")
	}
}

func TestIngestBundle_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := IngestBundle(ctx, t.TempDir(), t.TempDir(), true); err == nil {
		t.Fatal("expected an error on a cancelled context")
	}
}

func TestIngestBundle_Boundary_EmptyRecordSet(t *testing.T) {
	ctx := context.Background()
	bundleDir := t.TempDir()
	writeManifest(t, bundleDir, nil)

	ingest, err := IngestBundle(ctx, bundleDir, t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !ingest.ValidIntegrity || len(ingest.NovelSkills) != 0 {
		t.Fatalf("an empty bundle must ingest cleanly, got: %+v", ingest)
	}
}

func TestValidateSkillRecordPath_Boundary(t *testing.T) {
	valid := []string{
		"agent-skills/copilot/foo/SKILL.md",
		"agent-skills/claude/foo/references/palette.md",
	}
	for _, rel := range valid {
		if _, err := validateSkillRecordPath(rel); err != nil {
			t.Fatalf("%q should be accepted: %v", rel, err)
		}
	}

	invalid := []string{
		"",
		"/etc/passwd",
		"agent-skills/copilot/foo/../../hooks.json",
		"agent-skills/copilot/foo",
		"agent-plugins/copilot/foo/SKILL.md",
		"agent-skills/copilot/.hidden/SKILL.md",
	}
	for _, rel := range invalid {
		if _, err := validateSkillRecordPath(rel); err == nil {
			t.Fatalf("%q should be rejected", rel)
		} else if !errors.Is(err, ErrBundleRecordPath) {
			t.Fatalf("%q rejected with the wrong error: %v", rel, err)
		}
	}
}

func TestOpenRegularSource_Negative_Irregular(t *testing.T) {
	dir := t.TempDir()
	if _, err := openRegularSource(dir); !errors.Is(err, ErrNotRegularFile) {
		t.Fatalf("a directory must be refused as a bundle source, got: %v", err)
	}
	if _, err := openRegularSource(filepath.Join(dir, "absent")); err == nil {
		t.Fatal("an absent source must be refused")
	}
}
