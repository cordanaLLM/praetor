package compiler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// CanonicalSkillsRel holds the skills every agent surface is compiled from.
	CanonicalSkillsRel = ".agents/skills"
	// PluginSkillsRel is the plugin copy. The harvester already reads plugin skills from
	// <plugin>/skills (internal/harvester/bundle.go), so a plugin that ships none exposes
	// its personas and nothing else to whoever installs it.
	PluginSkillsRel = ".agents/plugins/praetor/skills"
	// maxSkillProjections bounds the skill loops (HISS-02).
	maxSkillProjections = 128
	SkillEntryName      = "SKILL.md"
)

// listCanonicalSkills returns the skill directories the repository declares, in name order.
func listCanonicalSkills(rootDir string) (_ []string, err error) {
	path, err := util.ConfinePath(rootDir, filepath.FromSlash(CanonicalSkillsRel))
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(entries) > maxSkillProjections {
		return nil, fmt.Errorf("%s holds more than %d skills", CanonicalSkillsRel, maxSkillProjections)
	}
	var names []string
	if len(entries) > maxSkillProjections {
		return nil, fmt.Errorf("%s holds more than %d files", path, maxSkillProjections)
	}
	for i := 0; i < len(entries); i++ {
		if entries[i].IsDir() {
			names = append(names, entries[i].Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// readCanonicalSkill reads one skill's declaration.
func readCanonicalSkill(rootDir, name string) ([]byte, error) {
	rel := filepath.Join(filepath.FromSlash(CanonicalSkillsRel), name, SkillEntryName)
	path, err := util.ConfinePath(rootDir, rel)
	if err != nil {
		return nil, err
	}

	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file, not a symlink or directory", path)
	}
	// #nosec G304 -- path was confined to the repository root by ConfinePath.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read canonical skill %s: %w", name, err)
	}
	return data, nil
}

// projectPluginSkills writes every canonical skill into the plugin, so installing the plugin
// delivers the skills the repository declares rather than the personas alone.
func ProjectPluginSkills(rootDir string) (int, error) {
	if !util.FileExists(filepath.Join(rootDir, filepath.FromSlash(PluginManifestRel))) {
		return 0, nil
	}
	names, err := listCanonicalSkills(rootDir)
	if err != nil {
		return 0, err
	}
	written := 0
	for i := 0; i < len(names); i++ {
		data, err := readCanonicalSkill(rootDir, names[i])
		if err != nil {
			return written, err
		}
		dir, err := util.ConfinePath(rootDir, filepath.Join(filepath.FromSlash(PluginSkillsRel), names[i]))
		if err != nil {
			return written, err
		}
		if err := util.MkdirSecure(dir, projectedDirPerm); err != nil {
			return written, err
		}
		if err := util.WriteFileSecure(filepath.Join(dir, SkillEntryName), data, projectedFilePerm); err != nil {
			return written, fmt.Errorf("write plugin skill %s: %w", names[i], err)
		}
		written++
	}
	return written, nil
}

// verifyPluginSkills checks the projection in both directions: every declared skill is shipped,
// and every shipped skill is one the repository declares. Checking only the first direction
// lets a stale copy survive indefinitely, which is how six orphaned personas came to sit in
// this plugin with one of them holding an absolute developer path.
func VerifyPluginSkills(rootDir string) (int, error) {
	if !util.FileExists(filepath.Join(rootDir, filepath.FromSlash(PluginManifestRel))) {
		return 0, nil
	}
	names, err := listCanonicalSkills(rootDir)
	if err != nil {
		return 0, err
	}
	verified := 0
	for i := 0; i < len(names); i++ {
		want, err := readCanonicalSkill(rootDir, names[i])
		if err != nil {
			return verified, err
		}
		rel := filepath.Join(filepath.FromSlash(PluginSkillsRel), names[i], SkillEntryName)
		if err := verifyProjection(rootDir, rel, want); err != nil {
			return verified, err
		}
		verified++
	}
	return verified, rejectOrphanSkills(rootDir, names)
}

// rejectOrphanSkills fails when the plugin ships a skill the repository does not declare.
func rejectOrphanSkills(rootDir string, names []string) error {
	declared := make(map[string]bool, len(names))
	for i := 0; i < len(names); i++ {
		declared[names[i]] = true
	}
	path, err := util.ConfinePath(rootDir, filepath.FromSlash(PluginSkillsRel))
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) > maxSkillProjections {
		return fmt.Errorf("%s holds more than %d skills", PluginSkillsRel, maxSkillProjections)
	}
	for i := 0; i < len(entries); i++ {
		if entries[i].IsDir() && !declared[entries[i].Name()] {
			return fmt.Errorf("%s/%s declares no canonical skill; every directory in a projection "+
				"is compiled output and must correspond to one in %s",
				PluginSkillsRel, entries[i].Name(), CanonicalSkillsRel)
		}
	}
	return nil
}
