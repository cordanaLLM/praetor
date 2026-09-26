package compiler

import (
	"context"
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
	// SkillEntryName is the declaration file every skill directory carries.
	SkillEntryName = "SKILL.md"
)

// errSkillDirSymlink refuses a skill directory, canonical or projected, that is a symlink.
// IsDir is false for one, so filtering on it skipped the skill without a word.
var errSkillDirSymlink = errors.New("skill directory must be a directory, never a symlink")

// listCanonicalSkills returns the skill directories the repository declares, in name order.
// A symlinked entry is an error, never a skipped skill.
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
	names, err := skillDirNames(CanonicalSkillsRel, entries)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

// skillDirNames returns the directory entries of one skills directory, refusing a symlinked
// one. Plain files beside the skills (a README) are not skills and are skipped. dir is the
// declared slash path the error names.
func skillDirNames(dir string, entries []os.DirEntry) ([]string, error) {
	var names []string
	for i := 0; i < len(entries) && i < maxSkillProjections; i++ {
		if entries[i].Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s/%s: %w", dir, entries[i].Name(), errSkillDirSymlink)
		}
		if entries[i].IsDir() {
			names = append(names, entries[i].Name())
		}
	}
	return names, nil
}

// readCanonicalSkill reads one skill's declaration without following a symlink anywhere below
// rootDir (readConfinedText).
func readCanonicalSkill(ctx context.Context, rootDir, name string) ([]byte, error) {
	data, err := readConfinedText(ctx, rootDir, skillEntryRel(CanonicalSkillsRel, name))
	if err != nil {
		return nil, fmt.Errorf("read canonical skill %s: %w", name, err)
	}
	return data, nil
}

// skillEntryRel is the declared slash path of one skill's SKILL.md below dir.
func skillEntryRel(dir, name string) string {
	return dir + "/" + name + "/" + SkillEntryName
}

// ProjectPluginSkills writes every canonical skill into the plugin, so installing the plugin
// delivers the skills the repository declares rather than the personas alone.
func ProjectPluginSkills(ctx context.Context, rootDir string) (int, error) {
	if !util.FileExists(filepath.Join(rootDir, filepath.FromSlash(PluginManifestRel))) {
		return 0, nil
	}
	names, err := listCanonicalSkills(rootDir)
	if err != nil {
		return 0, err
	}
	written := 0
	for i := 0; i < len(names); i++ {
		data, err := readCanonicalSkill(ctx, rootDir, names[i])
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
		if err := writeVendorAgent(ctx, filepath.Join(dir, SkillEntryName), string(data)); err != nil {
			return written, fmt.Errorf("write plugin skill %s: %w", names[i], err)
		}
		written++
	}
	return written, nil
}

// VerifyPluginSkills checks the projection in both directions: every declared skill is shipped,
// and every shipped skill is one the repository declares. Checking only the first direction
// lets a stale copy survive indefinitely, which is how six orphaned personas came to sit in
// this plugin with one of them holding an absolute developer path.
func VerifyPluginSkills(ctx context.Context, rootDir string) (int, error) {
	if !util.FileExists(filepath.Join(rootDir, filepath.FromSlash(PluginManifestRel))) {
		return 0, nil
	}
	names, err := listCanonicalSkills(rootDir)
	if err != nil {
		return 0, err
	}
	verified := 0
	for i := 0; i < len(names); i++ {
		want, err := readCanonicalSkill(ctx, rootDir, names[i])
		if err != nil {
			return verified, err
		}
		if err := verifyProjection(ctx, rootDir, skillEntryRel(PluginSkillsRel, names[i]), want); err != nil {
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
	shipped, err := skillDirNames(PluginSkillsRel, entries)
	if err != nil {
		return err
	}
	for i := 0; i < len(shipped); i++ {
		if !declared[shipped[i]] {
			return fmt.Errorf("%s/%s declares no canonical skill; every directory in a projection "+
				"is compiled output and must correspond to one in %s",
				PluginSkillsRel, shipped[i], CanonicalSkillsRel)
		}
	}
	return nil
}
