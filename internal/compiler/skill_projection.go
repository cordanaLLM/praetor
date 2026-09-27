package compiler

import (
	"context"
	"errors"
	"fmt"
	"os"
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
func listCanonicalSkills(ctx context.Context, rootDir string) ([]string, error) {
	return listSkillDir(ctx, rootDir, CanonicalSkillsRel)
}

// listSkillDir returns the skill directories in dir below rootDir, by name, through
// readConfinedDir, so a symlinked component on the way to dir is refused rather than listed
// through. An absent directory holds none.
func listSkillDir(ctx context.Context, rootDir, dir string) ([]string, error) {
	entries, err := readConfinedDir(ctx, rootDir, dir, maxSkillProjections)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	if len(entries) > maxSkillProjections {
		return nil, fmt.Errorf("%s holds more than %d skills", dir, maxSkillProjections)
	}
	return skillDirNames(dir, entries)
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

// pluginSkillProjections reads every canonical skill once (readCanonicalSkill) and returns its
// plugin copy, or nothing when the repository does not ship the plugin. It writes nothing.
func pluginSkillProjections(ctx context.Context, rootDir string) ([]projectionFile, error) {
	if !shipsPlugin(rootDir) {
		return nil, nil
	}
	names, err := listCanonicalSkills(ctx, rootDir)
	if err != nil {
		return nil, err
	}
	files := make([]projectionFile, 0, len(names))
	for i := 0; i < len(names); i++ {
		data, err := readCanonicalSkill(ctx, rootDir, names[i])
		if err != nil {
			return nil, err
		}
		files = append(files, projectionFile{rel: skillEntryRel(PluginSkillsRel, names[i]), data: data})
	}
	return files, nil
}

// VerifyPluginSkills checks the projection in both directions: every declared skill is shipped,
// and every shipped skill is one the repository declares. Checking only the first direction
// lets a stale copy survive indefinitely, which is how six orphaned personas came to sit in
// this plugin with one of them holding an absolute developer path.
func VerifyPluginSkills(ctx context.Context, rootDir string) (int, error) {
	if !shipsPlugin(rootDir) {
		return 0, nil
	}
	names, err := listCanonicalSkills(ctx, rootDir)
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
	return verified, rejectOrphanSkills(ctx, rootDir, names)
}

// rejectOrphanSkills fails when the plugin ships a skill the repository does not declare.
func rejectOrphanSkills(ctx context.Context, rootDir string, names []string) error {
	declared := make(map[string]bool, len(names))
	for i := 0; i < len(names); i++ {
		declared[names[i]] = true
	}
	shipped, err := listSkillDir(ctx, rootDir, PluginSkillsRel)
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
