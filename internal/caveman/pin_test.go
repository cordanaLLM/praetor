// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package caveman

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func createPinnedSkillFixture(t *testing.T, skillName string, skillContent, licenseContent []byte) (string, SkillPinRecord) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), skillName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(skillPath, skillContent, 0o644); err != nil {
		t.Fatal(err)
	}
	licensePath := filepath.Join(dir, "LICENSE")
	if err := os.WriteFile(licensePath, licenseContent, 0o644); err != nil {
		t.Fatal(err)
	}
	record := SkillPinRecord{
		Repo:    "https://github.com/example/skills",
		Tag:     "v0.1.0",
		Commit:  "0123456789abcdef0123456789abcdef01234567",
		License: "MIT",
		Files: map[string]string{
			"SKILL.md": sha256Hex(skillContent),
			"LICENSE":  sha256Hex(licenseContent),
		},
	}
	recordBytes, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SkillPinRecordName), recordBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, record
}

func TestVerifySkillPin_Positive_ValidPinRecord(t *testing.T) {
	skillData := []byte("# Fixture Skill\n\nLong descriptive text that is not caveman.\n")
	licenseData := []byte("MIT License\n")
	dir, _ := createPinnedSkillFixture(t, "valid-skill", skillData, licenseData)

	pinned, err := VerifySkillPin(dir)
	if err != nil {
		t.Fatalf("VerifySkillPin failed: %v", err)
	}
	if !pinned {
		t.Fatal("VerifySkillPin returned pinned=false, want true")
	}
}

func TestVerifySkillPin_Negative_OneByteChanged(t *testing.T) {
	skillData := []byte("# Fixture Skill\n\nLong descriptive text that is not caveman.\n")
	licenseData := []byte("MIT License\n")
	dir, _ := createPinnedSkillFixture(t, "tampered-skill", skillData, licenseData)

	// Tamper with SKILL.md by appending one byte
	tampered := append(skillData, '!')
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), tampered, 0o644); err != nil {
		t.Fatal(err)
	}

	pinned, err := VerifySkillPin(dir)
	if err == nil {
		t.Fatal("VerifySkillPin succeeded on tampered skill, want error")
	}
	if !pinned {
		t.Fatal("VerifySkillPin returned pinned=false on tampered pinned skill, want pinned=true")
	}
	if !errors.Is(err, ErrPinHashMismatch) {
		t.Errorf("error %v is not ErrPinHashMismatch", err)
	}
}

func TestVerifySkillPin_Negative_UnrecordedFile(t *testing.T) {
	skillData := []byte("# Fixture Skill\n")
	licenseData := []byte("MIT License\n")
	dir, _ := createPinnedSkillFixture(t, "extra-file-skill", skillData, licenseData)

	// Add an unrecorded file
	if err := os.WriteFile(filepath.Join(dir, "extra.txt"), []byte("extra"), 0o644); err != nil {
		t.Fatal(err)
	}

	pinned, err := VerifySkillPin(dir)
	if err == nil {
		t.Fatal("VerifySkillPin succeeded with unrecorded file, want error")
	}
	if !pinned {
		t.Fatal("VerifySkillPin returned pinned=false, want pinned=true")
	}
	if !errors.Is(err, ErrPinHashMismatch) {
		t.Errorf("error %v is not ErrPinHashMismatch", err)
	}
}

func TestVerifySkillPin_Negative_MissingRecordedFile(t *testing.T) {
	skillData := []byte("# Fixture Skill\n")
	licenseData := []byte("MIT License\n")
	dir, _ := createPinnedSkillFixture(t, "missing-file-skill", skillData, licenseData)

	// Remove LICENSE
	if err := os.Remove(filepath.Join(dir, "LICENSE")); err != nil {
		t.Fatal(err)
	}

	pinned, err := VerifySkillPin(dir)
	if err == nil {
		t.Fatal("VerifySkillPin succeeded with missing file, want error")
	}
	if !pinned {
		t.Fatal("VerifySkillPin returned pinned=false, want pinned=true")
	}
	if !errors.Is(err, ErrPinHashMismatch) {
		t.Errorf("error %v is not ErrPinHashMismatch", err)
	}
}

func TestVerifySkillPin_Boundary_UnpinnedDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "unpinned-skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Unpinned\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pinned, err := VerifySkillPin(dir)
	if err != nil {
		t.Fatalf("VerifySkillPin failed on unpinned directory: %v", err)
	}
	if pinned {
		t.Fatal("VerifySkillPin returned pinned=true for unpinned directory, want false")
	}
}

func TestSkillPinRecord_MatchesUpstream(t *testing.T) {
	record := &SkillPinRecord{
		Repo:   "https://github.com/example/skills",
		Tag:    "v1.0.0",
		Commit: "0123456789abcdef0123456789abcdef01234567",
	}
	if !record.MatchesUpstream("https://github.com/example/skills", "v1.0.0", "0123456789abcdef0123456789abcdef01234567") {
		t.Fatal("MatchesUpstream returned false for identical coordinates")
	}
	if !record.MatchesUpstream("https://github.com/example/skills", "v1.0.0", "0123456789ABCDEF0123456789ABCDEF01234567") {
		t.Fatal("MatchesUpstream returned false for uppercase commit")
	}
	if record.MatchesUpstream("https://github.com/other/skills", "v1.0.0", "0123456789abcdef0123456789abcdef01234567") {
		t.Fatal("MatchesUpstream returned true for mismatched repo")
	}
	if record.MatchesUpstream("https://github.com/example/skills", "v2.0.0", "0123456789abcdef0123456789abcdef01234567") {
		t.Fatal("MatchesUpstream returned true for mismatched tag")
	}
	if record.MatchesUpstream("https://github.com/example/skills", "v1.0.0", "fedcba9876543210fedcba9876543210fedcba98") {
		t.Fatal("MatchesUpstream returned true for mismatched commit")
	}
	var nilRecord *SkillPinRecord
	if nilRecord.MatchesUpstream("repo", "tag", "commit") {
		t.Fatal("MatchesUpstream returned true on nil record")
	}
}

func TestReadSkillPinRecord_Negative_EmptyOrUnknownLicenseRefused(t *testing.T) {
	for _, tc := range []struct {
		name    string
		license string
	}{
		{"empty license", ""},
		{"unknown license", "UNKNOWN"},
		{"lowercase unknown", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "test-skill")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			record := SkillPinRecord{
				Repo:    "https://github.com/example/skills",
				Tag:     "v1.0.0",
				Commit:  "0123456789abcdef0123456789abcdef01234567",
				License: tc.license,
				Files: map[string]string{
					"SKILL.md": "abc",
				},
			}
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, SkillPinRecordName), data, 0o644); err != nil {
				t.Fatal(err)
			}
			_, exists, err := ReadSkillPinRecord(dir)
			if !exists {
				t.Fatal("expected exists=true")
			}
			if err == nil {
				t.Fatal("expected error for empty/unknown license, got nil")
			}
		})
	}
}
