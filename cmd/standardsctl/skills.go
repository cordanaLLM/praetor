// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	defaultSkillSyncTimeout = 2 * time.Minute
	maxUpstreamSources      = 64
	maxUpstreamSkills       = 256
	maxSkillFiles           = 256
	maxLicenseBytes         = 1 << 20
	maxSkillEntryBytes      = 1 << 20
	maxSkillFileBytes       = 1 << 20
)

func runSkills(args []string) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		printSkillsUsage()
		return nil
	}
	switch args[0] {
	case "pin-sync":
		return runSkillsPinSync(args[1:])
	case "pin-verify":
		return runSkillsPinVerify(args[1:])
	default:
		return fmt.Errorf("unknown skills subcommand: %s", args[0])
	}
}

func printSkillsUsage() {
	fmt.Println("Usage: standardsctl skills <subcommand> [flags]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  pin-sync    Fetch and record pinned upstream skills from .standards.yaml")
	fmt.Println("  pin-verify  Verify recorded pinned upstream skills and their file digests")
}

func runSkillsPinSync(args []string) error {
	fs := flag.NewFlagSet("skills pin-sync", flag.ContinueOnError)
	root := fs.String("root", ".", "Repository root directory")
	path := fs.String("path", "", "Alias for --root")
	timeout := fs.Duration("timeout", defaultSkillSyncTimeout, "Timeout for git operations")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	targetRoot := *root
	if *path != "" {
		targetRoot = *path
	}
	ctx, cancel := commandContext(*timeout)
	defer cancel()
	return pinSyncUpstream(ctx, targetRoot, *timeout)
}

func pinSyncUpstream(ctx context.Context, rootDir string, timeout time.Duration) error {
	manifestPath := filepath.Join(rootDir, config.ManifestFileName)
	manifest, err := config.LoadManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("load manifest %s: %w", manifestPath, err)
	}
	if manifest.Skills == nil || len(manifest.Skills.Upstream) == 0 {
		fmt.Println("no upstream skills declared in .standards.yaml")
		return nil
	}
	sources := manifest.Skills.Upstream
	for i := 0; i < len(sources) && i < maxUpstreamSources; i++ {
		if err := syncOneUpstreamSource(ctx, rootDir, sources[i], timeout); err != nil {
			return err
		}
	}
	return nil
}

func syncOneUpstreamSource(ctx context.Context, rootDir string, source config.UpstreamSkillSource, timeout time.Duration) (err error) {
	cloneCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tempDir, mkErr := os.MkdirTemp("", "praetor-upstream-clone-*")
	if mkErr != nil {
		return fmt.Errorf("create temporary clone directory: %w", mkErr)
	}
	defer func() {
		err = errors.Join(err, os.RemoveAll(tempDir))
	}()

	if _, err := util.RunGit(cloneCtx, "", "clone", "--branch", source.Tag, source.Repo, tempDir); err != nil {
		return fmt.Errorf("clone %s at tag %s: %w", source.Repo, source.Tag, err)
	}

	head, err := util.RunGit(cloneCtx, tempDir, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("rev-parse clone HEAD for %s: %w", source.Repo, err)
	}
	head = strings.TrimSpace(head)
	if !strings.EqualFold(head, source.Commit) {
		return fmt.Errorf("clone head commit %s does not match pinned commit %s for %s at %s", head, source.Commit, source.Repo, source.Tag)
	}

	repoLicense := findRepoLicense(tempDir)
	for i := 0; i < len(source.Skills) && i < maxUpstreamSkills; i++ {
		if err := copyAndPinOneSkill(rootDir, tempDir, source, source.Skills[i], repoLicense); err != nil {
			return err
		}
	}
	return nil
}

func findRepoLicense(tempDir string) []byte {
	names := []string{"LICENSE", "LICENCE", "LICENSE.md", "LICENCE.md", "LICENSE.txt", "LICENCE.txt"}
	for i := 0; i < len(names) && i < 16; i++ {
		data, err := util.ReadConfinedLimited(tempDir, names[i], maxLicenseBytes)
		if err == nil && len(data) > 0 {
			return data
		}
	}
	return nil
}

func copyAndPinOneSkill(rootDir, tempDir string, source config.UpstreamSkillSource, skillName string, repoLicense []byte) error {
	if !config.ValidSkillName(skillName) {
		return fmt.Errorf("skills: invalid skill name %q", skillName)
	}
	srcDir, err := findSkillSourceDir(tempDir, skillName)
	if err != nil {
		return err
	}
	skillRel := filepath.Join(filepath.FromSlash(compiler.CanonicalSkillsRel), skillName)
	targetDir, err := util.ConfinePath(rootDir, skillRel)
	if err != nil {
		return fmt.Errorf("target skill dir %s: %w", skillName, err)
	}
	if err := os.RemoveAll(targetDir); err != nil {
		return fmt.Errorf("clean target skill dir %s: %w", targetDir, err)
	}
	if err := util.MkdirSecure(targetDir, 0750); err != nil {
		return fmt.Errorf("create target skill dir %s: %w", targetDir, err)
	}
	if err := copyDirectoryContents(rootDir, srcDir, skillRel); err != nil {
		return fmt.Errorf("copy skill %s contents: %w", skillName, err)
	}
	if err := ensureSkillLicense(rootDir, skillRel, repoLicense); err != nil {
		return err
	}
	licence := determineSkillLicense(targetDir)
	if licence == "" || strings.EqualFold(licence, "UNKNOWN") {
		cleanErr := os.RemoveAll(targetDir)
		licErr := fmt.Errorf("skill %s: no licence could be identified from LICENSE text or front matter", skillName)
		return errors.Join(licErr, cleanErr)
	}
	if err := writeSkillPinRecord(rootDir, skillRel, source, licence); err != nil {
		return err
	}
	fmt.Printf("pinned skill %s from %s@%s (%s)\n", skillName, source.Repo, source.Tag, source.Commit[:8])
	return nil
}

func findSkillSourceDir(tempDir, skillName string) (string, error) {
	candidates := []string{
		filepath.Join(tempDir, ".agents", "skills", skillName),
		filepath.Join(tempDir, "skills", skillName),
		filepath.Join(tempDir, ".claude", "skills", skillName),
		filepath.Join(tempDir, skillName),
	}
	for i := 0; i < len(candidates) && i < 16; i++ {
		candidate := candidates[i]
		entryPath := filepath.Join(candidate, compiler.SkillEntryName)
		if info, err := os.Stat(entryPath); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("skill %q not found in upstream repository", skillName)
}

func copyDirectoryContents(rootDir, srcDir, skillRel string) error {
	count := 0
	return filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Name() == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		return copyDirectoryEntry(rootDir, srcDir, skillRel, path, d, &count)
	})
}

func checkCloneEntry(srcDir, path string, d fs.DirEntry) (string, error) {
	if d.Type()&fs.ModeSymlink != 0 {
		return "", fmt.Errorf("refuse symlink in skill source: %s", path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("refuse symlink in skill source: %s", path)
	}
	rel, err := filepath.Rel(srcDir, path)
	if err != nil {
		return "", err
	}
	if rel != "." && (strings.HasPrefix(rel, "..") || filepath.IsAbs(rel)) {
		return "", fmt.Errorf("skill file path %s leaves skill folder", rel)
	}
	if rel != "." {
		if _, err := util.ConfinePath(srcDir, rel); err != nil {
			return "", fmt.Errorf("skill file path %s leaves skill folder: %w", rel, err)
		}
	}
	return rel, nil
}

func copyDirectoryEntry(rootDir, srcDir, skillRel, path string, d fs.DirEntry, count *int) error {
	rel, err := checkCloneEntry(srcDir, path, d)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	targetRel := filepath.Join(skillRel, rel)
	targetPath, err := util.ConfinePath(rootDir, targetRel)
	if err != nil {
		return fmt.Errorf("target path %s escapes repository root: %w", targetRel, err)
	}
	if d.IsDir() {
		return util.MkdirSecure(targetPath, 0750)
	}
	*count++
	if *count > maxSkillFiles {
		return fmt.Errorf("skill directory %s contains more than %d files", srcDir, maxSkillFiles)
	}
	data, err := util.ReadFileLimited(path, maxSkillFileBytes)
	if err != nil {
		return err
	}
	if err := util.MkdirSecure(filepath.Dir(targetPath), 0750); err != nil {
		return err
	}
	return util.WriteFileAtomic(targetPath, data, 0644)
}

func ensureSkillLicense(rootDir, skillRel string, repoLicense []byte) error {
	licenseRel := filepath.Join(skillRel, compiler.SkillLicenseName)
	licensePath, err := util.ConfinePath(rootDir, licenseRel)
	if err != nil {
		return err
	}
	if _, err := os.Stat(licensePath); err == nil {
		return nil
	}
	licenceRel := filepath.Join(skillRel, "LICENCE")
	licencePath, err := util.ConfinePath(rootDir, licenceRel)
	if err != nil {
		return err
	}
	if _, err := os.Stat(licencePath); err == nil {
		return nil
	}
	if len(repoLicense) > 0 {
		if err := util.WriteFileAtomic(licensePath, repoLicense, 0644); err != nil {
			return fmt.Errorf("write repository license to %s: %w", licensePath, err)
		}
	}
	return nil
}

func writeSkillPinRecord(rootDir, skillRel string, source config.UpstreamSkillSource, licence string) error {
	targetDir, err := util.ConfinePath(rootDir, skillRel)
	if err != nil {
		return err
	}
	if licence == "" || strings.EqualFold(licence, "UNKNOWN") {
		return fmt.Errorf("refuse pin: invalid licence %q", licence)
	}
	files, err := hashPinnedSkillFiles(targetDir)
	if err != nil {
		return fmt.Errorf("hash skill files in %s: %w", targetDir, err)
	}
	record := caveman.SkillPinRecord{
		Repo:    source.Repo,
		Tag:     source.Tag,
		Commit:  source.Commit,
		License: licence,
		Licence: licence,
		Files:   files,
	}
	data, err := json.MarshalIndent(&record, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pin record for %s: %w", targetDir, err)
	}
	data = append(data, '\n')
	recordRel := filepath.Join(skillRel, caveman.SkillPinRecordName)
	recordPath, err := util.ConfinePath(rootDir, recordRel)
	if err != nil {
		return err
	}
	if err := util.WriteFileAtomic(recordPath, data, 0644); err != nil {
		return fmt.Errorf("write pin record %s: %w", recordPath, err)
	}
	if _, err := caveman.VerifySkillPin(targetDir); err != nil {
		return fmt.Errorf("verify newly written pin record in %s: %w", targetDir, err)
	}
	return nil
}

func hashPinnedSkillFiles(skillDir string) (map[string]string, error) {
	files := make(map[string]string)
	err := filepath.WalkDir(skillDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("refuse symlink in pinned skill: %s", path)
		}
		rel, err := filepath.Rel(skillDir, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if relSlash == caveman.SkillPinRecordName {
			return nil
		}
		if len(files) >= maxSkillFiles {
			return fmt.Errorf("pinned skill in %s exceeds limit %d files", skillDir, maxSkillFiles)
		}
		digest, err := caveman.HashFileSHA256(path)
		if err != nil {
			return err
		}
		files[relSlash] = digest
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func determineSkillLicense(skillDir string) string {
	licenseData := readSkillLicenseFile(skillDir)
	if id := matchCommonLicenseText(string(licenseData)); id != "" {
		return id
	}
	if spdx := extractSPDXIdentifier(string(licenseData)); spdx != "" {
		return spdx
	}
	if fromFM := extractFrontMatterLicense(skillDir); fromFM != "" {
		return fromFM
	}
	return ""
}

func readSkillLicenseFile(skillDir string) []byte {
	if data, err := util.ReadConfinedLimited(skillDir, compiler.SkillLicenseName, maxLicenseBytes); err == nil {
		return data
	}
	if data, err := util.ReadConfinedLimited(skillDir, "LICENCE", maxLicenseBytes); err == nil {
		return data
	}
	return nil
}

func matchCommonLicenseText(text string) string {
	if text == "" {
		return ""
	}
	patterns := []struct {
		substr string
		id     string
	}{
		{"Apache License", "Apache-2.0"},
		{"MIT License", "MIT"},
		{"The MIT License", "MIT"},
		{"European Union Public Licence", "EUPL-1.2"},
		{"EUPL", "EUPL-1.2"},
		{"BSD 3-Clause", "BSD-3-Clause"},
		{"BSD 2-Clause", "BSD-2-Clause"},
		{"Mozilla Public License", "MPL-2.0"},
		{"ISC License", "ISC"},
	}
	for i := 0; i < len(patterns) && i < 16; i++ {
		if strings.Contains(text, patterns[i].substr) {
			return patterns[i].id
		}
	}
	return ""
}

func extractSPDXIdentifier(text string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines) && i < 32; i++ {
		trimmed := strings.TrimSpace(lines[i])
		const tag = "SPDX-License-Identifier:"
		if idx := strings.Index(trimmed, tag); idx >= 0 {
			id := strings.TrimSpace(trimmed[idx+len(tag):])
			id = strings.Trim(id, " \t\r*/#")
			if id != "" && !strings.EqualFold(id, "UNKNOWN") {
				return id
			}
		}
	}
	return ""
}

type skillFrontMatter struct {
	License  string `yaml:"license"`
	Licence  string `yaml:"licence"`
	Metadata struct {
		License     string `yaml:"license"`
		Licence     string `yaml:"licence"`
		DerivedFrom string `yaml:"derived_from"`
	} `yaml:"metadata"`
}

func extractFrontMatterLicense(skillDir string) string {
	skillData, err := util.ReadConfinedLimited(skillDir, compiler.SkillEntryName, maxSkillEntryBytes)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(string(skillData), "\r\n", "\n"), "\n")
	end, opened := util.FrontMatterEnd(lines, 512)
	if !opened || end <= 1 {
		return ""
	}
	block := []byte(strings.Join(lines[1:end-1], "\n"))
	var front skillFrontMatter
	if err := util.DecodeYAMLDocument(block, &front, util.YAMLDocumentOptions{AllowEmpty: true}); err == nil {
		return selectFrontMatterLicense(&front)
	}
	return ""
}

func selectFrontMatterLicense(front *skillFrontMatter) string {
	if front.License != "" && !strings.EqualFold(front.License, "UNKNOWN") {
		return strings.TrimSpace(front.License)
	}
	if front.Licence != "" && !strings.EqualFold(front.Licence, "UNKNOWN") {
		return strings.TrimSpace(front.Licence)
	}
	if front.Metadata.License != "" && !strings.EqualFold(front.Metadata.License, "UNKNOWN") {
		return strings.TrimSpace(front.Metadata.License)
	}
	if front.Metadata.Licence != "" && !strings.EqualFold(front.Metadata.Licence, "UNKNOWN") {
		return strings.TrimSpace(front.Metadata.Licence)
	}
	return extractDerivedFromLicense(front.Metadata.DerivedFrom)
}

func extractDerivedFromLicense(derived string) string {
	if derived != "" && strings.HasSuffix(derived, ")") {
		if open := strings.LastIndex(derived, "("); open >= 0 {
			id := strings.TrimSpace(derived[open+1 : len(derived)-1])
			if id != "" && !strings.EqualFold(id, "UNKNOWN") {
				return id
			}
		}
	}
	return ""
}

func runSkillsPinVerify(args []string) error {
	fs := flag.NewFlagSet("skills pin-verify", flag.ContinueOnError)
	root := fs.String("root", ".", "Repository root directory")
	path := fs.String("path", "", "Alias for --root")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	targetRoot := *root
	if *path != "" {
		targetRoot = *path
	}
	manifestPath := filepath.Join(targetRoot, config.ManifestFileName)
	manifest, err := config.LoadManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("load manifest %s: %w", manifestPath, err)
	}
	if manifest.Skills == nil || len(manifest.Skills.Upstream) == 0 {
		return nil
	}
	verified := 0
	sources := manifest.Skills.Upstream
	for i := 0; i < len(sources) && i < maxUpstreamSources; i++ {
		v, err := verifyOneUpstreamSource(targetRoot, sources[i])
		if err != nil {
			return err
		}
		verified += v
	}
	fmt.Printf("%d pinned skills verified.\n", verified)
	return nil
}

func verifyOneUpstreamSource(rootDir string, source config.UpstreamSkillSource) (int, error) {
	verified := 0
	for i := 0; i < len(source.Skills) && i < maxUpstreamSkills; i++ {
		skillName := source.Skills[i]
		if !config.ValidSkillName(skillName) {
			return verified, fmt.Errorf("skill %q is not a valid skill name", skillName)
		}
		skillRel := filepath.Join(filepath.FromSlash(compiler.CanonicalSkillsRel), skillName)
		skillDir, err := util.ConfinePath(rootDir, skillRel)
		if err != nil {
			return verified, fmt.Errorf("skill %s: %w", skillName, err)
		}
		record, exists, err := caveman.ReadSkillPinRecord(skillDir)
		if err != nil {
			return verified, fmt.Errorf("read pin record for %s: %w", skillName, err)
		}
		if !exists {
			return verified, fmt.Errorf("skill %s missing %s", skillName, caveman.SkillPinRecordName)
		}
		if record.Repo != source.Repo || record.Tag != source.Tag || !strings.EqualFold(record.Commit, source.Commit) {
			return verified, fmt.Errorf("skill %s pin mismatch: got %s@%s (%s), want %s@%s (%s)",
				skillName, record.Repo, record.Tag, record.Commit, source.Repo, source.Tag, source.Commit)
		}
		if _, err := caveman.VerifySkillPin(skillDir); err != nil {
			return verified, fmt.Errorf("verify pin for %s: %w", skillName, err)
		}
		verified++
	}
	return verified, nil
}
