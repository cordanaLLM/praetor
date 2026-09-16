package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// canonicalSkillsRel holds the skills every agent surface is compiled from.
	canonicalSkillsRel = ".agents/skills"
	// pluginSkillsRel is the plugin copy. The harvester already reads plugin skills from
	// <plugin>/skills (internal/harvester/bundle.go), so a plugin that ships none exposes
	// its personas and nothing else to whoever installs it.
	pluginSkillsRel = ".agents/plugins/praetor/skills"
	// maxSkillProjections bounds the skill loops (HISS-02).
	maxSkillProjections = 128
	skillEntryName      = "SKILL.md"
)

// listCanonicalSkills returns the skill directories the repository declares, in name order.
func listCanonicalSkills(rootDir string) (_ []string, err error) {
	path, err := util.ConfinePath(rootDir, filepath.FromSlash(canonicalSkillsRel))
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
		return nil, fmt.Errorf("%s holds more than %d skills", canonicalSkillsRel, maxSkillProjections)
	}
	var names []string
	for i := 0; i < len(entries) && i < maxSkillProjections; i++ {
		if entries[i].IsDir() {
			names = append(names, entries[i].Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// readCanonicalSkill reads one skill's declaration.
func readCanonicalSkill(rootDir, name string) ([]byte, error) {
	rel := filepath.Join(filepath.FromSlash(canonicalSkillsRel), name, skillEntryName)
	path, err := util.ConfinePath(rootDir, rel)
	if err != nil {
		return nil, err
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
func projectPluginSkills(rootDir string) (int, error) {
	if !util.FileExists(filepath.Join(rootDir, filepath.FromSlash(pluginManifestRel))) {
		return 0, nil
	}
	names, err := listCanonicalSkills(rootDir)
	if err != nil {
		return 0, err
	}
	written := 0
	for i := 0; i < len(names) && i < maxSkillProjections; i++ {
		data, err := readCanonicalSkill(rootDir, names[i])
		if err != nil {
			return written, err
		}
		dir, err := util.ConfinePath(rootDir, filepath.Join(filepath.FromSlash(pluginSkillsRel), names[i]))
		if err != nil {
			return written, err
		}
		if err := util.MkdirSecure(dir, projectedDirPerm); err != nil {
			return written, err
		}
		if err := util.WriteFileSecure(filepath.Join(dir, skillEntryName), data, projectedFilePerm); err != nil {
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
func verifyPluginSkills(rootDir string) (int, error) {
	if !util.FileExists(filepath.Join(rootDir, filepath.FromSlash(pluginManifestRel))) {
		return 0, nil
	}
	names, err := listCanonicalSkills(rootDir)
	if err != nil {
		return 0, err
	}
	verified := 0
	for i := 0; i < len(names) && i < maxSkillProjections; i++ {
		want, err := readCanonicalSkill(rootDir, names[i])
		if err != nil {
			return verified, err
		}
		rel := filepath.Join(filepath.FromSlash(pluginSkillsRel), names[i], skillEntryName)
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
	for i := 0; i < len(names) && i < maxSkillProjections; i++ {
		declared[names[i]] = true
	}
	path, err := util.ConfinePath(rootDir, filepath.FromSlash(pluginSkillsRel))
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
		return fmt.Errorf("%s holds more than %d skills", pluginSkillsRel, maxSkillProjections)
	}
	for i := 0; i < len(entries) && i < maxSkillProjections; i++ {
		if entries[i].IsDir() && !declared[entries[i].Name()] {
			return fmt.Errorf("%s/%s declares no canonical skill; every directory in a projection "+
				"is compiled output and must correspond to one in %s",
				pluginSkillsRel, entries[i].Name(), canonicalSkillsRel)
		}
	}
	return nil
}
