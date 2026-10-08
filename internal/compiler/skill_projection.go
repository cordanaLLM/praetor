package compiler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

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
	return readSkillFile(ctx, root, CanonicalSkillRel(name), "skill "+name)
}

// readSkillFile is the one reader of a skill file, SKILL.md and LICENSE alike: it reads rel below
// root without following a symlink at any component and reports whether the file exists. what
// names the file in the error.
func readSkillFile(ctx context.Context, root, rel, what string) ([]byte, bool, error) {
	data, exists, err := contextopt.ObserveSnapshotIn(ctx, root, filepath.FromSlash(rel))
	if err != nil {
		return nil, false, fmt.Errorf("read canonical %s: %w", what, err)
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

// SkillLicenseName is the file name of the upstream licence text that travels beside a derived
// skill. The name is the one REUSE tooling and licence scanners read as a licence file, so the
// MIT text needs no header of its own in an adopter repository.
const SkillLicenseName = "LICENSE"

// SkillLicenseRel is the declared slash path of one skill's LICENSE file below dir.
func SkillLicenseRel(dir, name string) string {
	return dir + "/" + name + "/" + SkillLicenseName
}

// CanonicalSkillLicenseRel is the declared slash path of the LICENSE file of skill name under
// CanonicalSkillsRel.
func CanonicalSkillLicenseRel(name string) string {
	return SkillLicenseRel(CanonicalSkillsRel, name)
}

// ReadCanonicalSkillLicense reads the LICENSE file of skill name under .agents/skills below root
// through readSkillFile, and reports whether it exists.
func ReadCanonicalSkillLicense(ctx context.Context, root, name string) ([]byte, bool, error) {
	return readSkillFile(ctx, root, CanonicalSkillLicenseRel(name), "skill licence "+name)
}

// noNoticeLicenses are the SPDX identifiers whose terms require no licence text or notice
// to travel with copies of the work.
var noNoticeLicenses = []string{"0BSD", "CC0-1.0", "MIT-0", "Unlicense"}

// errSkillLicenseUndeclared refuses a skill that names an upstream without the licence it took.
var errSkillLicenseUndeclared = errors.New("metadata.derived_from names no licence in trailing parentheses")

// errSkillLicenseUnparseable indicates an invalid licence expression in metadata.derived_from.
var errSkillLicenseUnparseable = errors.New("unparseable licence expression in metadata.derived_from")

// SkillRequiresLicense reports whether the skill text in data declares an upstream
// (metadata.derived_from, AssetDerivedFrom) under a licence that requires its text to travel
// with copies. It protects by default: every licence requires notice except the explicit
// no-notice set (noNoticeLicenses). A skill that declares no upstream needs none; one that
// declares an upstream without a licence or with an unparseable expression is an error.
func SkillRequiresLicense(data []byte) (bool, error) {
	derived, err := AssetDerivedFrom(data)
	if err != nil || derived == "" {
		return false, err
	}
	expr, err := extractLicenseExpression(derived)
	if err != nil {
		return false, err
	}
	words := strings.Fields(expr)
	if len(words) == 0 {
		return false, fmt.Errorf("%w: %q", errSkillLicenseUndeclared, derived)
	}
	return parseLicenseWords(words, expr)
}

func extractLicenseExpression(derived string) (string, error) {
	trimmed := strings.TrimSpace(derived)
	if !strings.HasSuffix(trimmed, ")") {
		return "", fmt.Errorf("%w: %q", errSkillLicenseUndeclared, derived)
	}
	open := strings.LastIndex(trimmed, "(")
	if open < 0 {
		return "", fmt.Errorf("%w: %q", errSkillLicenseUndeclared, derived)
	}
	expr := strings.TrimSpace(trimmed[open+1 : len(trimmed)-1])
	if expr == "" {
		return "", fmt.Errorf("%w: %q", errSkillLicenseUndeclared, derived)
	}
	if strings.ContainsAny(expr, "()") {
		return "", fmt.Errorf("%w: nested parentheses in %q", errSkillLicenseUnparseable, derived)
	}
	if strings.ContainsAny(expr, "/,;\\|") {
		return "", fmt.Errorf("%w: invalid delimiter in %q", errSkillLicenseUnparseable, expr)
	}
	return expr, nil
}

func parseLicenseWords(words []string, expr string) (bool, error) {
	if isSPDXOperator(words[0]) || isSPDXOperator(words[len(words)-1]) {
		return false, fmt.Errorf("%w: misplaced operator in %q", errSkillLicenseUnparseable, expr)
	}
	needsLicense := false
	for i := 0; i < len(words); i++ {
		word := words[i]
		if isSPDXOperator(word) {
			continue
		}
		if !isSPDXIdentifier(word) {
			return false, fmt.Errorf("%w: invalid identifier %q", errSkillLicenseUnparseable, word)
		}
		if !slices.Contains(noNoticeLicenses, word) {
			needsLicense = true
		}
	}
	return needsLicense, nil
}

func isSPDXOperator(w string) bool {
	return w == "AND" || w == "OR" || w == "WITH"
}

func isSPDXIdentifier(w string) bool {
	if w == "" {
		return false
	}
	for _, r := range w {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '-' && r != '+' {
			return false
		}
	}
	return true
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
	files := make([]projectionFile, 0, len(names)*2)
	for i := 0; i < len(names); i++ {
		data, err := readCanonicalSkill(ctx, rootDir, names[i])
		if err != nil {
			return nil, err
		}
		files = append(files, projectionFile{rel: SkillEntryRel(PluginSkillsRel, names[i]), data: data})
		license, exists, err := ReadCanonicalSkillLicense(ctx, rootDir, names[i])
		if err != nil {
			return nil, err
		}
		if exists {
			files = append(files, projectionFile{rel: SkillLicenseRel(PluginSkillsRel, names[i]), data: license})
		}
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
		if err := verifyPluginSkill(ctx, rootDir, names[i]); err != nil {
			return verified, err
		}
		verified++
	}
	return verified, rejectOrphanSkills(ctx, rootDir, names)
}

// verifyPluginSkill checks the plugin copy of one canonical skill: its SKILL.md and, when the
// canonical skill carries one, its LICENSE. When canonical carries no LICENSE, it verifies that
// no stale LICENSE copy remains in the plugin.
func verifyPluginSkill(ctx context.Context, rootDir, name string) error {
	want, err := readCanonicalSkill(ctx, rootDir, name)
	if err != nil {
		return err
	}
	if err := verifyProjection(ctx, rootDir, SkillEntryRel(PluginSkillsRel, name), want); err != nil {
		return err
	}
	license, exists, err := ReadCanonicalSkillLicense(ctx, rootDir, name)
	if err != nil {
		return err
	}
	if exists {
		return verifyProjection(ctx, rootDir, SkillLicenseRel(PluginSkillsRel, name), license)
	}
	return verifyStaleLicense(ctx, rootDir, SkillLicenseRel(PluginSkillsRel, name))
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

// clientSkillProjections returns the copy of every bundle skill in each of dirs, SKILL.md and
// LICENSE: the bundle skills the repository carries, and those in pending, the bundle skills a
// caller writes before it projects, with their pending bytes (PlanAgentSurfacesOver). A pending
// name that is no bundle skill is refused. A skill without a LICENSE projects none. It writes
// nothing.
func clientSkillProjections(ctx context.Context, rootDir string, dirs []string, pending PendingSources) ([]projectionFile, error) {
	names := config.RegisterSkillBundle()
	for _, set := range []map[string][]byte{pending.Skills, pending.Licenses} {
		for name := range set {
			if !slices.Contains(names, name) {
				return nil, fmt.Errorf("pending skill %q is not one Praetor ships (%s)", name, strings.Join(names, ", "))
			}
		}
	}
	if len(dirs) == 0 {
		return nil, nil
	}
	var files []projectionFile
	for i := 0; i < len(names) && i < maxSkillProjections; i++ {
		copies, err := bundleSkillCopies(ctx, rootDir, names[i], dirs, pending)
		if err != nil {
			return nil, err
		}
		files = append(files, copies...)
	}
	return files, nil
}

// bundleSkillCopies returns the copies of the bundle skill name in dirs, as the caller leaves the
// repository: the SKILL.md and LICENSE bytes in pending when they are there, otherwise what
// .agents/skills holds (ReadCanonicalSkill, ReadCanonicalSkillLicense). A file neither has is
// not copied.
func bundleSkillCopies(ctx context.Context, rootDir, name string, dirs []string, pending PendingSources) ([]projectionFile, error) {
	text, present, err := pendingOr(pending.Skills, name, func() ([]byte, bool, error) { return ReadCanonicalSkill(ctx, rootDir, name) })
	if err != nil || !present {
		return nil, err
	}
	license, hasLicense, err := pendingOr(pending.Licenses, name, func() ([]byte, bool, error) { return ReadCanonicalSkillLicense(ctx, rootDir, name) })
	if err != nil {
		return nil, err
	}
	files := make([]projectionFile, 0, 2*len(dirs))
	for j := 0; j < len(dirs) && j < maxSkillProjections; j++ {
		files = append(files, projectionFile{rel: SkillEntryRel(dirs[j], name), data: text})
		if hasLicense {
			files = append(files, projectionFile{rel: SkillLicenseRel(dirs[j], name), data: license})
		}
	}
	return files, nil
}

// pendingOr returns the bytes pending holds for name, or what read returns when it holds none.
func pendingOr(pending map[string][]byte, name string, read func() ([]byte, bool, error)) ([]byte, bool, error) {
	if data, ok := pending[name]; ok {
		return data, true, nil
	}
	return read()
}

// VerifyClientSkills checks that every bundle skill the repository carries has its copy in the
// skill directory of each agent client agent_clients selects (SelectSkillDirs), matching its
// canonical SKILL.md and LICENSE (verifyProjection), and that no stale LICENSE copy remains.
// It returns the number of copies verified. A directory the selection leaves out is neither
// required nor read.
func VerifyClientSkills(ctx context.Context, rootDir string) (int, error) {
	dirs, _, err := SelectSkillDirs(ctx, rootDir)
	if err != nil {
		return 0, err
	}
	files, err := clientSkillProjections(ctx, rootDir, dirs, PendingSources{})
	if err != nil {
		return 0, err
	}
	for i := range files {
		if err := verifyProjection(ctx, rootDir, files[i].rel, files[i].data); err != nil {
			return i, err
		}
	}
	if err := verifyStaleClientLicenses(ctx, rootDir, dirs); err != nil {
		return len(files), err
	}
	return len(files), nil
}

// verifyStaleLicense checks that rel below rootDir does not exist. An existing copy is a drift:
// the canonical skill carries no LICENSE, so any projected copy is stale.
func verifyStaleLicense(ctx context.Context, rootDir, rel string) error {
	_, exists, err := contextopt.ObserveSnapshotIn(ctx, rootDir, filepath.FromSlash(rel))
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("%w: %s exists but canonical skill carries no LICENSE (run 'praetorctl compile-context' to remove it)", ErrAgentProjectionDrift, rel)
	}
	return nil
}

// verifyStaleClientLicenses checks that no selected client directory retains a LICENSE for a
// skill Praetor ships whose canonical skill carries none (staleClientLicenses). A client copy of
// a skill the repository does not carry belongs to the adopter and is never read.
func verifyStaleClientLicenses(ctx context.Context, rootDir string, dirs []string) error {
	stale, err := staleClientLicenses(ctx, rootDir, dirs, PendingSources{})
	if err != nil {
		return err
	}
	if len(stale) > 0 {
		return fmt.Errorf("%w: %s exists but canonical skill carries no LICENSE (run 'praetorctl compile-context' to remove it)", ErrAgentProjectionDrift, stale[0])
	}
	return nil
}

// staleClientLicenses is the one selector of the client LICENSE copies to remove: those that
// exist in dirs for a skill Praetor ships (config.RegisterSkillBundle) that the repository
// carries (its canonical SKILL.md, or the pending one) and whose canonical LICENSE is absent.
// verify, plan and removal all read it, so they name the same files. A client copy of a skill
// without a canonical SKILL.md belongs to the adopter and is never selected, observed or
// refused; the copy of a skill Praetor ships is observed without following a symlink, so a
// symlinked skill directory is an error naming it.
func staleClientLicenses(ctx context.Context, rootDir string, dirs []string, pending PendingSources) ([]string, error) {
	names := config.RegisterSkillBundle()
	var stale []string
	for i := 0; i < len(names) && i < maxSkillProjections; i++ {
		_, shipped, err := pendingOr(pending.Skills, names[i], func() ([]byte, bool, error) { return ReadCanonicalSkill(ctx, rootDir, names[i]) })
		if err != nil {
			return nil, err
		}
		if !shipped {
			continue
		}
		_, hasLicense, err := pendingOr(pending.Licenses, names[i], func() ([]byte, bool, error) { return ReadCanonicalSkillLicense(ctx, rootDir, names[i]) })
		if err != nil {
			return nil, err
		}
		if hasLicense {
			continue
		}
		found, err := existingLicenses(ctx, rootDir, dirs, names[i])
		if err != nil {
			return nil, err
		}
		stale = append(stale, found...)
	}
	return stale, nil
}

// existingLicenses returns the LICENSE copies of skill name that exist in dirs, observed without
// following a symlink at any component.
func existingLicenses(ctx context.Context, rootDir string, dirs []string, name string) ([]string, error) {
	var found []string
	for j := 0; j < len(dirs) && j < maxSkillProjections; j++ {
		rel := SkillLicenseRel(dirs[j], name)
		_, exists, err := contextopt.ObserveSnapshotIn(ctx, rootDir, filepath.FromSlash(rel))
		if err != nil {
			return nil, fmt.Errorf("skill %s is one Praetor ships, so %s is checked: %w", name, rel, err)
		}
		if exists {
			found = append(found, rel)
		}
	}
	return found, nil
}

// stalePluginLicenses returns the plugin LICENSE copies that exist for a canonical skill that
// carries no LICENSE. A repository that does not ship the plugin has none.
func stalePluginLicenses(ctx context.Context, rootDir string) ([]string, error) {
	if !shipsPlugin(rootDir) {
		return nil, nil
	}
	names, err := listCanonicalSkills(ctx, rootDir)
	if err != nil {
		return nil, err
	}
	var stale []string
	for i := 0; i < len(names) && i < maxSkillProjections; i++ {
		_, hasLicense, err := ReadCanonicalSkillLicense(ctx, rootDir, names[i])
		if err != nil {
			return nil, err
		}
		if hasLicense {
			continue
		}
		found, err := existingLicenses(ctx, rootDir, []string{PluginSkillsRel}, names[i])
		if err != nil {
			return nil, err
		}
		stale = append(stale, found...)
	}
	return stale, nil
}

// removeStaleSkillLicenses removes the stale LICENSE copies a checked plan selected
// (planAgentSurfaces), the plugin and the selected client directories alike.
func removeStaleSkillLicenses(ctx context.Context, rootDir string, stale []string) error {
	for i := 0; i < len(stale) && i < maxSkillProjections*maxSkillProjections; i++ {
		if err := removeConfinedFile(ctx, rootDir, stale[i]); err != nil {
			return err
		}
	}
	return nil
}
