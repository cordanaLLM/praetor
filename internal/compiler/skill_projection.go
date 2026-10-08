package compiler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// ReadCanonicalSkill reads the SKILL.md of skill name under .agents/skills below root without
// following a symlink at any component (contextopt.ObserveSnapshotIn), and reports whether it
// exists. A path that cannot be read is an error: guessing either way would name, or copy, a
// skill nobody can open. It is the one reader of a canonical skill: the plugin and client
// projections, the register block (AbsentRegisterSkills) and adoption, which reads the skills
// it installs from a Praetor checkout, all read through it.
func ReadCanonicalSkill(ctx context.Context, root, name string) ([]byte, bool, error) {
	rel := CanonicalSkillRel(name)
	data, exists, err := contextopt.ObserveSnapshotIn(ctx, root, filepath.FromSlash(rel))
	if err != nil {
		return nil, false, fmt.Errorf("read canonical skill %s: %w", name, err)
	}
	return data, exists, nil
}

// readCanonicalSkill is ReadCanonicalSkill for a skill directory listCanonicalSkills listed: one
// without its SKILL.md is an error.
func readCanonicalSkill(ctx context.Context, rootDir, name string) ([]byte, error) {
	data, exists, err := ReadCanonicalSkill(ctx, rootDir, name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("read canonical skill %s: %s: %w", name, CanonicalSkillRel(name), os.ErrNotExist)
	}
	return data, nil
}

// SkillEntryRel is the declared slash path of one skill's SKILL.md below dir.
func SkillEntryRel(dir, name string) string {
	return dir + "/" + name + "/" + SkillEntryName
}

// CanonicalSkillRel is the declared slash path of the SKILL.md of skill name under
// CanonicalSkillsRel.
func CanonicalSkillRel(name string) string {
	return SkillEntryRel(CanonicalSkillsRel, name)
}

// SkillNoticeName is the notice file name where an upstream license requires notices to travel
// with copies.
const SkillNoticeName = "NOTICE"

// SkillNoticeRel is the declared slash path of one skill's NOTICE file below dir.
func SkillNoticeRel(dir, name string) string {
	return dir + "/" + name + "/" + SkillNoticeName
}

// CanonicalSkillNoticeRel is the declared slash path of the NOTICE file of skill name under
// CanonicalSkillsRel.
func CanonicalSkillNoticeRel(name string) string {
	return SkillNoticeRel(CanonicalSkillsRel, name)
}

// ReadCanonicalSkillNotice reads the NOTICE file of skill name under .agents/skills below root
// without following a symlink at any component, and reports whether it exists.
func ReadCanonicalSkillNotice(ctx context.Context, root, name string) ([]byte, bool, error) {
	rel := CanonicalSkillNoticeRel(name)
	data, exists, err := contextopt.ObserveSnapshotIn(ctx, root, filepath.FromSlash(rel))
	if err != nil {
		return nil, false, fmt.Errorf("read canonical skill notice %s: %w", name, err)
	}
	return data, exists, nil
}

// SkillRequiresNotice reports whether data indicates an upstream license (such as MIT)
// that requires its copyright and permission notice to travel with copies.
func SkillRequiresNotice(data []byte) bool {
	return bytes.Contains(data, []byte("MIT")) || bytes.Contains(data, []byte("Apache-2.0")) || bytes.Contains(data, []byte("BSD-3-Clause"))
}

var (
	skillMarkdownLinkRe = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)
	skillMarkdownRefRe  = regexp.MustCompile(`(?m)^\[[^\]]+\]:\s*(\S+)`)
)

// CheckShippedSkillReferences refuses a shipped skill text referencing a repository-relative path
// that the adopter does not receive. It protects by default, allowing only absolute URLs and paths
// inside the shipped skill set (the skill's own directory or sibling shipped skills).
func CheckShippedSkillReferences(skillName string, data []byte) error {
	if bytes.Contains(data, []byte("docs/credits.md")) {
		return fmt.Errorf("skill %s references repository path docs/credits.md, which an adopter does not receive", skillName)
	}
	links := skillMarkdownLinkRe.FindAllSubmatch(data, -1)
	for i := 0; i < len(links) && i < maxSkillProjections; i++ {
		target := string(links[i][1])
		if idx := strings.IndexAny(target, " \t"); idx != -1 {
			target = target[:idx]
		}
		if err := validateSkillReferenceTarget(skillName, target); err != nil {
			return err
		}
	}
	refs := skillMarkdownRefRe.FindAllSubmatch(data, -1)
	for i := 0; i < len(refs) && i < maxSkillProjections; i++ {
		if err := validateSkillReferenceTarget(skillName, string(refs[i][1])); err != nil {
			return err
		}
	}
	return nil
}

func isExternalOrAnchorTarget(target string) bool {
	if target == "" || strings.HasPrefix(target, "#") {
		return true
	}
	for _, scheme := range []string{"https://", "http://", "mailto:"} {
		if strings.HasPrefix(target, scheme) {
			return true
		}
	}
	return false
}

func isSiblingSkillTarget(clean string) bool {
	bundle := config.RegisterSkillBundle()
	for i := 0; i < len(bundle) && i < maxSkillProjections; i++ {
		prefix := "../" + bundle[i] + "/"
		if strings.HasPrefix(clean, prefix) || clean == "../"+bundle[i] {
			return true
		}
	}
	return false
}

func isAllowedLocalTarget(clean string) bool {
	if strings.HasPrefix(clean, "../") || clean == ".." || strings.HasPrefix(clean, "/") {
		return false
	}
	base := clean
	if idx := strings.Index(clean, "/"); idx != -1 {
		base = clean[:idx]
	}
	switch base {
	case SkillNoticeName, "LICENSE", SkillEntryName, "references", "scripts":
		return true
	default:
		return false
	}
}

// validateSkillReferenceTarget validates one link target from a shipped skill text.
func validateSkillReferenceTarget(skillName, target string) error {
	target = strings.TrimSpace(target)
	if isExternalOrAnchorTarget(target) {
		return nil
	}
	clean := filepath.ToSlash(filepath.Clean(target))
	if isSiblingSkillTarget(clean) || isAllowedLocalTarget(clean) {
		return nil
	}
	return fmt.Errorf("skill %s references repository path %q, which an adopter does not receive", skillName, target)
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
		files = append(files, projectionFile{rel: SkillEntryRel(PluginSkillsRel, names[i]), data: data})
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
		if err := verifyProjection(ctx, rootDir, SkillEntryRel(PluginSkillsRel, names[i]), want); err != nil {
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
			files = append(files, projectionFile{rel: SkillEntryRel(dirs[j], names[i]), data: data})
		}
	}
	return files, nil
}

// bundleSkillText returns the SKILL.md of the bundle skill name as the caller leaves the
// repository: its bytes in pending when it is there, otherwise what .agents/skills holds
// (ReadCanonicalSkill), and whether either has it.
func bundleSkillText(ctx context.Context, rootDir, name string, pending map[string][]byte) ([]byte, bool, error) {
	if data, ok := pending[name]; ok {
		return data, true, nil
	}
	return ReadCanonicalSkill(ctx, rootDir, name)
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
