// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/config"
)

func sha256Bytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func writePinnedSkill(t *testing.T, root, name string, skillMD, license []byte) {
	t.Helper()
	dir := filepath.Join(root, ".agents", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), skillMD, 0o644); err != nil {
		t.Fatal(err)
	}
	filesMap := map[string]string{
		"SKILL.md": sha256Bytes(skillMD),
	}
	if len(license) > 0 {
		if err := os.WriteFile(filepath.Join(dir, "LICENSE"), license, 0o644); err != nil {
			t.Fatal(err)
		}
		filesMap["LICENSE"] = sha256Bytes(license)
	}
	record := caveman.SkillPinRecord{
		Repo:    "https://github.com/upstream/skills",
		Tag:     "v1.0.0",
		Commit:  "0123456789abcdef0123456789abcdef01234567",
		License: "MIT",
		Files:   filesMap,
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, caveman.SkillPinRecordName), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeManifestWithUpstream(t *testing.T, root, repo, tag, commit string, skills ...string) {
	t.Helper()
	skillLines := ""
	for _, s := range skills {
		skillLines += "        - " + s + "\n"
	}
	manifest := fmt.Sprintf(`version: 1
agent_clients:
  - claude
skills:
  upstream:
    - repo: %s
      tag: %s
      commit: %s
      skills:
%s`, repo, tag, commit, skillLines)
	if err := os.WriteFile(filepath.Join(root, config.ManifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Positive: pinned upstream skill is projected into .claude/skills and verified by VerifyClientSkills.
func TestPinnedSkills_Positive_ProjectedIntoClientFolders(t *testing.T) {
	root := writeClientSkillFixture(t, "")
	writeManifestWithUpstream(t, root, "https://github.com/upstream/skills", "v1.0.0", "0123456789abcdef0123456789abcdef01234567", "pinned-calc")
	skillContent := []byte("---\nname: pinned-calc\ndescription: A calculation helper skill.\n---\n# Calculator\n\nDetailed guidance.\n")
	licenseContent := []byte("MIT License\n")
	writePinnedSkill(t, root, "pinned-calc", skillContent, licenseContent)

	files, err := CompileAgentSurfaces(t.Context(), io.Discard, root)
	if err != nil {
		t.Fatalf("CompileAgentSurfaces failed: %v", err)
	}

	foundSkill := false
	foundLicense := false
	for _, f := range files {
		if f.RelativePath == ".claude/skills/pinned-calc/SKILL.md" {
			foundSkill = true
			if f.Content != string(skillContent) {
				t.Errorf("projected content mismatch: got %q, want %q", f.Content, string(skillContent))
			}
		}
		if f.RelativePath == ".claude/skills/pinned-calc/LICENSE" {
			foundLicense = true
			if f.Content != string(licenseContent) {
				t.Errorf("projected license mismatch: got %q, want %q", f.Content, string(licenseContent))
			}
		}
	}
	if !foundSkill {
		t.Fatal("CompileAgentSurfaces did not project .claude/skills/pinned-calc/SKILL.md")
	}
	if !foundLicense {
		t.Fatal("CompileAgentSurfaces did not project .claude/skills/pinned-calc/LICENSE")
	}

	verified, err := VerifyClientSkills(t.Context(), root)
	if err != nil {
		t.Fatalf("VerifyClientSkills failed: %v", err)
	}
	if verified < 5 { // 3 bundle skills (3 files) + 1 pinned skill (2 files) = at least 5
		t.Errorf("verified = %d, want at least 5", verified)
	}
}

// Negative: a long unpinned skill in .agents/skills is still refused by the caveman lint (planted).
func TestPinnedSkills_Negative_LongUnpinnedSkillRefusedByCavemanLint(t *testing.T) {
	root := writeClientSkillFixture(t, "")
	// Create an unpinned skill with >600 words of prose
	longProse := "---\nname: unpinned-verbose\n---\n# Verbose Skill\n\n" +
		strings.Repeat("This is a long sentence explaining the complicated concepts in excessive detail without any economy of words whatsoever. ", 60)
	unpinnedDir := filepath.Join(root, ".agents", "skills", "unpinned-verbose")
	if err := os.MkdirAll(unpinnedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unpinnedDir, "SKILL.md"), []byte(longProse), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LintCanonicalSkillFiles(t.Context(), root)
	if err == nil {
		t.Fatal("LintCanonicalSkillFiles succeeded on long unpinned skill, want ErrAgentTextProse")
	}
	if !errors.Is(err, ErrAgentTextProse) {
		t.Errorf("err = %v, want ErrAgentTextProse", err)
	}
}

// Positive: a long pinned skill in .agents/skills skips the caveman lint when pin record matches.
func TestPinnedSkills_Positive_LongPinnedSkillSkipsCavemanLint(t *testing.T) {
	root := writeClientSkillFixture(t, "")
	writeManifestWithUpstream(t, root, "https://github.com/upstream/skills", "v1.0.0", "0123456789abcdef0123456789abcdef01234567", "pinned-verbose")
	longProse := "---\nname: pinned-verbose\n---\n# Verbose Skill\n\n" +
		strings.Repeat("This is a long sentence explaining the complicated concepts in excessive detail without any economy of words whatsoever. ", 60)
	writePinnedSkill(t, root, "pinned-verbose", []byte(longProse), []byte("MIT License\n"))

	count, err := LintCanonicalSkillFiles(t.Context(), root)
	if err != nil {
		t.Fatalf("LintCanonicalSkillFiles failed on valid pinned skill: %v", err)
	}
	if count < 1 {
		t.Errorf("count = %d, want >= 1", count)
	}
}

// Negative: a pinned skill with one changed byte is refused (hash), and its lint scope is lost.
func TestPinnedSkills_Negative_TamperedPinnedSkillRefused(t *testing.T) {
	root := writeClientSkillFixture(t, "")
	writeManifestWithUpstream(t, root, "https://github.com/upstream/skills", "v1.0.0", "0123456789abcdef0123456789abcdef01234567", "pinned-tampered")
	skillContent := []byte("---\nname: pinned-tampered\n---\n# Pinned Tampered\n\nClean text.\n")
	writePinnedSkill(t, root, "pinned-tampered", skillContent, []byte("MIT License\n"))

	// Tamper with SKILL.md by appending one byte
	tamperedPath := filepath.Join(root, ".agents", "skills", "pinned-tampered", "SKILL.md")
	if err := os.WriteFile(tamperedPath, append(skillContent, '!'), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Lint scope is lost: LintCanonicalSkillFiles must refuse the tampered skill
	_, err := LintCanonicalSkillFiles(t.Context(), root)
	if err == nil {
		t.Fatal("LintCanonicalSkillFiles succeeded on tampered pinned skill, want error")
	}
	if !strings.Contains(err.Error(), "hash mismatch") {
		t.Errorf("expected hash mismatch error, got: %v", err)
	}

	// 2. Projection verification must also fail
	_, err = VerifyClientSkills(t.Context(), root)
	if err == nil {
		t.Fatal("VerifyClientSkills succeeded on tampered pinned skill, want error")
	}
}

// Negative: a projected copy with one changed byte differs from the pinned record and fails VerifyClientSkills.
func TestPinnedSkills_Negative_ProjectedCopyDriftFails(t *testing.T) {
	root := writeClientSkillFixture(t, "")
	writeManifestWithUpstream(t, root, "https://github.com/upstream/skills", "v1.0.0", "0123456789abcdef0123456789abcdef01234567", "pinned-drift")
	skillContent := []byte("---\nname: pinned-drift\n---\n# Pinned Drift\n\nText.\n")
	writePinnedSkill(t, root, "pinned-drift", skillContent, []byte("MIT License\n"))

	// Project the surfaces
	if _, err := CompileAgentSurfaces(t.Context(), io.Discard, root); err != nil {
		t.Fatal(err)
	}

	// Modify projected copy in .claude/skills
	projectedFile := filepath.Join(root, ".claude", "skills", "pinned-drift", "SKILL.md")
	if err := os.WriteFile(projectedFile, append(skillContent, '?'), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := VerifyClientSkills(t.Context(), root)
	if err == nil {
		t.Fatal("VerifyClientSkills succeeded on drifted projected copy, want error")
	}
	if !errors.Is(err, ErrAgentProjectionDrift) {
		t.Errorf("err = %v, want ErrAgentProjectionDrift", err)
	}
}

// Negative (planted test 1): an own long skill with a forged .pin.json (matching hashes, no manifest entry)
// is refused by the lint and by --verify (VerifyClientSkills / CompileAgentSurfaces).
func TestPinnedSkills_Negative_ForgedPinWithoutManifestEntryRefused(t *testing.T) {
	root := writeClientSkillFixture(t, "")
	longProse := "---\nname: forged-verbose\n---\n# Forged Verbose Skill\n\n" +
		strings.Repeat("This is a long sentence explaining the complicated concepts in excessive detail without any economy of words whatsoever. ", 60)
	writePinnedSkill(t, root, "forged-verbose", []byte(longProse), []byte("MIT License\n"))

	// 1. Refused by caveman lint because absence of manifest entry lints it as an own skill
	_, err := LintCanonicalSkillFiles(t.Context(), root)
	if err == nil {
		t.Fatal("LintCanonicalSkillFiles succeeded on forged pinned skill without manifest entry, want ErrAgentTextProse")
	}
	if !errors.Is(err, ErrAgentTextProse) {
		t.Errorf("err = %v, want ErrAgentTextProse", err)
	}

	// 2. Refused by compile-context projection naming the skill and the missing manifest entry
	_, err = CompileAgentSurfaces(t.Context(), io.Discard, root)
	if err == nil {
		t.Fatal("CompileAgentSurfaces succeeded on forged pinned skill without manifest entry, want error")
	}
	if !strings.Contains(err.Error(), "forged-verbose") || !strings.Contains(err.Error(), "skills.upstream") {
		t.Errorf("expected error naming skill and skills.upstream, got: %v", err)
	}

	// 3. Refused by VerifyClientSkills
	_, err = VerifyClientSkills(t.Context(), root)
	if err == nil {
		t.Fatal("VerifyClientSkills succeeded on forged pinned skill without manifest entry, want error")
	}
	if !strings.Contains(err.Error(), "forged-verbose") || !strings.Contains(err.Error(), "skills.upstream") {
		t.Errorf("expected error naming skill and skills.upstream, got: %v", err)
	}
}

// Positive (planted test 2): the same skill named in skills.upstream with matching repo, tag and commit is exempt.
func TestPinnedSkills_Positive_DeclaredInManifestExempt(t *testing.T) {
	root := writeClientSkillFixture(t, "")
	writeManifestWithUpstream(t, root, "https://github.com/upstream/skills", "v1.0.0", "0123456789abcdef0123456789abcdef01234567", "forged-verbose")
	longProse := "---\nname: forged-verbose\n---\n# Forged Verbose Skill\n\n" +
		strings.Repeat("This is a long sentence explaining the complicated concepts in excessive detail without any economy of words whatsoever. ", 60)
	writePinnedSkill(t, root, "forged-verbose", []byte(longProse), []byte("MIT License\n"))

	// 1. Exempt from caveman lint
	count, err := LintCanonicalSkillFiles(t.Context(), root)
	if err != nil {
		t.Fatalf("LintCanonicalSkillFiles failed on legitimately declared pinned skill: %v", err)
	}
	if count < 1 {
		t.Errorf("count = %d, want >= 1", count)
	}

	// 2. CompileAgentSurfaces succeeds
	if _, err := CompileAgentSurfaces(t.Context(), io.Discard, root); err != nil {
		t.Fatalf("CompileAgentSurfaces failed on declared pinned skill: %v", err)
	}

	// 3. VerifyClientSkills succeeds
	if _, err := VerifyClientSkills(t.Context(), root); err != nil {
		t.Fatalf("VerifyClientSkills failed on declared pinned skill: %v", err)
	}
}

// Negative (planted test 3): a manifest entry whose commit differs from the record is refused.
func TestPinnedSkills_Negative_ManifestCommitMismatchRefused(t *testing.T) {
	root := writeClientSkillFixture(t, "")
	// Manifest declares a different commit
	writeManifestWithUpstream(t, root, "https://github.com/upstream/skills", "v1.0.0", "fedcba9876543210fedcba9876543210fedcba98", "mismatch-skill")
	skillContent := []byte("---\nname: mismatch-skill\n---\n# Mismatch\n\nText.\n")
	writePinnedSkill(t, root, "mismatch-skill", skillContent, []byte("MIT License\n"))

	// 1. Refused by LintCanonicalSkillFiles
	_, err := LintCanonicalSkillFiles(t.Context(), root)
	if err == nil {
		t.Fatal("LintCanonicalSkillFiles succeeded on commit mismatch, want error")
	}
	if !strings.Contains(err.Error(), "does not match pin record") {
		t.Errorf("expected error mentioning pin mismatch, got: %v", err)
	}

	// 2. Refused by VerifyClientSkills
	_, err = VerifyClientSkills(t.Context(), root)
	if err == nil {
		t.Fatal("VerifyClientSkills succeeded on commit mismatch, want error")
	}
	if !strings.Contains(err.Error(), "pin mismatch") {
		t.Errorf("expected error mentioning pin mismatch, got: %v", err)
	}
}

// Negative: pinned skill with traversal path in .pin.json is refused by projection
func TestPinnedSkills_Negative_PinRecordFileTraversalRefused(t *testing.T) {
	root := writeClientSkillFixture(t, "")
	writeManifestWithUpstream(t, root, "https://github.com/upstream/skills", "v1.0.0", "0123456789abcdef0123456789abcdef01234567", "traversal-skill")
	dir := filepath.Join(root, ".agents", "skills", "traversal-skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillMD := []byte("---\nname: traversal-skill\n---\n# Traversal\n")
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), skillMD, 0o644); err != nil {
		t.Fatal(err)
	}
	record := caveman.SkillPinRecord{
		Repo:    "https://github.com/upstream/skills",
		Tag:     "v1.0.0",
		Commit:  "0123456789abcdef0123456789abcdef01234567",
		License: "MIT",
		Files: map[string]string{
			"../../evil.txt": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		},
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, caveman.SkillPinRecordName), data, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = CompileAgentSurfaces(t.Context(), io.Discard, root)
	if err == nil {
		t.Fatal("CompileAgentSurfaces succeeded on traversal pin record file, want error")
	}
}
