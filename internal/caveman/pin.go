// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package caveman

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// SkillPinRecordName is the declaration file every pinned upstream skill carries.
const SkillPinRecordName = ".pin.json"

// maxPinnedSkillFiles bounds the files one pinned skill may carry (HISS-02).
const maxPinnedSkillFiles = 256

// maxPinRecordBytes bounds the size of .pin.json (HISS-02).
const maxPinRecordBytes = 256 * 1024

// ErrPinHashMismatch indicates a file in a pinned skill does not match its pin record.
var ErrPinHashMismatch = errors.New("pinned skill file hash mismatch")

// SkillPinRecord records the upstream provenance and file hashes of a pinned skill.
type SkillPinRecord struct {
	Repo    string            `json:"repo"`
	Tag     string            `json:"tag"`
	Commit  string            `json:"commit"`
	License string            `json:"license,omitempty"`
	Licence string            `json:"licence,omitempty"`
	Files   map[string]string `json:"files"`
}

// EffectiveLicense returns the license identifier, supporting both license and licence keys.
func (r *SkillPinRecord) EffectiveLicense() string {
	if r == nil {
		return ""
	}
	if r.License != "" {
		return r.License
	}
	return r.Licence
}

// MatchesUpstream reports whether record coordinates match repo, tag, and commit.
func (r *SkillPinRecord) MatchesUpstream(repo, tag, commit string) bool {
	if r == nil {
		return false
	}
	return r.Repo == repo && r.Tag == tag && strings.EqualFold(r.Commit, commit)
}

// ReadSkillPinRecord reads and parses .pin.json from skillDir.
// If .pin.json does not exist, it returns nil, false, nil.
// If .pin.json exists but is malformed, it returns nil, true, error.
func ReadSkillPinRecord(skillDir string) (*SkillPinRecord, bool, error) {
	recordPath := filepath.Join(skillDir, SkillPinRecordName)
	data, err := util.ReadFileLimited(recordPath, maxPinRecordBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", recordPath, err)
	}
	var record SkillPinRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, true, fmt.Errorf("parse %s: %w", recordPath, err)
	}
	if record.Repo == "" || record.Tag == "" || len(record.Commit) != 40 || len(record.Files) == 0 {
		return nil, true, fmt.Errorf("invalid pin record in %s: repo, tag, commit (40 hex) and files are required", recordPath)
	}
	licence := record.EffectiveLicense()
	if licence == "" || strings.EqualFold(licence, "UNKNOWN") {
		return nil, true, fmt.Errorf("invalid pin record in %s: licence is required and must not be unknown", recordPath)
	}
	return &record, true, nil
}

// VerifySkillPin checks whether skillDir carries a valid pin record and whether every file on disk
// matches the record's recorded SHA-256 digest.
// If skillDir has no pin record, it returns (false, nil), meaning the skill is unpinned.
// If skillDir has a pin record, it returns (true, nil) when all files match.
// If any file on disk differs, is missing, or is unrecorded, it returns (true, error).
func VerifySkillPin(skillDir string) (bool, error) {
	record, exists, err := ReadSkillPinRecord(skillDir)
	if err != nil {
		return exists, err
	}
	if !exists {
		return false, nil
	}
	if len(record.Files) > maxPinnedSkillFiles {
		return true, fmt.Errorf("pinned skill in %s declares %d files, exceeds limit %d", skillDir, len(record.Files), maxPinnedSkillFiles)
	}
	diskFiles := make(map[string]bool, len(record.Files))
	walkErr := filepath.WalkDir(skillDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		return verifySkillDiskFile(skillDir, path, record, diskFiles)
	})
	if walkErr != nil {
		return true, walkErr
	}
	return true, checkMissingSkillFiles(skillDir, record, diskFiles)
}

func verifySkillDiskFile(skillDir, path string, record *SkillPinRecord, diskFiles map[string]bool) error {
	rel, err := filepath.Rel(skillDir, path)
	if err != nil {
		return err
	}
	relSlash := filepath.ToSlash(rel)
	if relSlash == SkillPinRecordName {
		return nil
	}
	if len(diskFiles) >= maxPinnedSkillFiles {
		return fmt.Errorf("pinned skill in %s holds more than %d files", skillDir, maxPinnedSkillFiles)
	}
	diskFiles[relSlash] = true

	wantHash, recorded := record.Files[relSlash]
	if !recorded {
		return fmt.Errorf("%w: %s holds unrecorded file %s", ErrPinHashMismatch, skillDir, relSlash)
	}
	gotHash, err := HashFileSHA256(path)
	if err != nil {
		return fmt.Errorf("hash %s: %w", path, err)
	}
	if !strings.EqualFold(gotHash, wantHash) {
		return fmt.Errorf("%w: %s file %s hash mismatch: got %s, want %s", ErrPinHashMismatch, skillDir, relSlash, gotHash, wantHash)
	}
	return nil
}

func checkMissingSkillFiles(skillDir string, record *SkillPinRecord, diskFiles map[string]bool) error {
	for file := range record.Files {
		if !diskFiles[file] {
			return fmt.Errorf("%w: %s missing recorded file %s", ErrPinHashMismatch, skillDir, file)
		}
	}
	return nil
}

// HashFileSHA256 computes the lowercase hex SHA-256 of file at path.
func HashFileSHA256(path string) (digest string, err error) {
	// #nosec G304 -- path was enumerated by WalkDir within the verified skill directory.
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		err = errors.Join(err, f.Close())
	}()
	h := sha256.New()
	if _, cpErr := io.Copy(h, f); cpErr != nil {
		return "", cpErr
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
