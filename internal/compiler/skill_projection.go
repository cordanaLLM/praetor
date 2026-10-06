package compiler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
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

// Praetor ships the text register skills (config.RegisterSkillBundle): adoption installs them into
// .agents/skills, and the block compile-context renders names them (LoadRegisterBlock, #235).
// Codex, Gemini CLI, Cursor, Copilot and Windsurf read .agents/skills; an agent client that reads
// skills elsewhere (agentcontext.SkillDirs, .claude/skills for Claude Code) gets a copy of each
// bundle skill the repository carries, projected like a persona copy. A repository's own skills
// are not projected, and a skill directory may hold skills of its own: the projection names no
// orphan there.

// canonicalSkillText reads the SKILL.md of skill name under .agents/skills below root without
// following a symlink (contextopt.ObserveSnapshotIn), and reports whether it exists. A path that
// cannot be read is an error: guessing either way would name, or copy, a skill nobody can open.
func canonicalSkillText(ctx context.Context, root, name string) ([]byte, bool, error) {
	rel := skillEntryRel(CanonicalSkillsRel, name)
	data, exists, err := contextopt.ObserveSnapshotIn(ctx, root, filepath.FromSlash(rel))
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", rel, err)
	}
	return data, exists, nil
}

// clientSkillProjections returns the copy of every bundle skill in each of dirs: the bundle skills
// the repository carries, and those in pending, the bundle skills a caller writes before it
// projects, with their pending bytes (PlanAgentSurfacesOver). A pending name that is no bundle
// skill is refused. It writes nothing.
func clientSkillProjections(ctx context.Context, rootDir string, dirs []string, pending map[string][]byte) ([]projectionFile, error) {
	names := config.RegisterSkillBundle()
	for name := range pending {
		if !slices.Contains(names, name) {
			return nil, fmt.Errorf("pending skill %q is not one Praetor ships (%s)", name, strings.Join(names, ", "))
		}
	}
	if len(dirs) == 0 {
		return nil, nil
	}
	var files []projectionFile
	for i := 0; i < len(names) && i < maxSkillProjections; i++ {
		data, present, err := bundleSkillText(ctx, rootDir, names[i], pending)
		if err != nil {
			return nil, err
		}
		for j := 0; present && j < len(dirs); j++ {
			files = append(files, projectionFile{rel: skillEntryRel(dirs[j], names[i]), data: data})
		}
	}
	return files, nil
}

// bundleSkillText returns the SKILL.md of the bundle skill name as the caller leaves the
// repository: its bytes in pending when it is there, otherwise what .agents/skills holds
// (canonicalSkillText), and whether either has it.
func bundleSkillText(ctx context.Context, rootDir, name string, pending map[string][]byte) ([]byte, bool, error) {
	if data, ok := pending[name]; ok {
		return data, true, nil
	}
	return canonicalSkillText(ctx, rootDir, name)
}

// VerifyClientSkills checks that every bundle skill the repository carries has its copy in the
// skill directory of each agent client agent_clients selects (SelectSkillDirs), matching its
// canonical SKILL.md (verifyProjection). It returns the number of copies verified. A directory
// the selection leaves out is neither required nor read.
func VerifyClientSkills(ctx context.Context, rootDir string) (int, error) {
	dirs, _, err := SelectSkillDirs(ctx, rootDir)
	if err != nil {
		return 0, err
	}
	files, err := clientSkillProjections(ctx, rootDir, dirs, nil)
	if err != nil {
		return 0, err
	}
	for i := range files {
		if err := verifyProjection(ctx, rootDir, files[i].rel, files[i].data); err != nil {
			return i, err
		}
	}
	return len(files), nil
}
