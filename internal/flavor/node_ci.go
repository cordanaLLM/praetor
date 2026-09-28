package flavor

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cordanaLLM/praetor/internal/nodemanifest"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/templates"
)

// yarnrcFile is the Yarn 2+ configuration file. Yarn 1.17.2 and later read one setting from it,
// yarnPath, and hand the command over to the Yarn release that path names.
const yarnrcFile = ".yarnrc.yml"

// nodeManifest is what the Node CI job reads from package.json.
type nodeManifest struct {
	PackageManager string
	Scripts        map[string]string
}

// nodeCIRequirement resolves the package manager the scaffolded Node CI job
// (templates/node/ci-node.yml.tmpl) installs and runs scripts with, or reports what the
// repository lacks for the job to pass as written.
//
// The job installs from the committed lockfile, fails when package.json disagrees with it, and
// runs the test script; adoption makes it a required status check. typescript-node detects any
// package.json, so it also claims a Go or Rust repository carrying a package.json for its commit
// tooling. Scaffolding a job there, or installing a pnpm, Yarn or Bun project with npm, handed
// the repository a required check no pull request could pass. The manager is the one
// packageManager names (nodemanifest.DeclaredManager), and without a declaration the one whose
// lockfile CI's checkout holds (resolveNodeManager).
func nodeCIRequirement(ctx context.Context, repoPath string) (templates.Context, string) {
	manifest, err := readNodeManifest(repoPath)
	if err != nil {
		return templates.Context{}, err.Error()
	}
	var missing []string
	node, why := resolveNodeManager(ctx, repoPath, manifest.PackageManager)
	if why != "" {
		missing = append(missing, why)
	}
	if !nodemanifest.ScriptRuns(manifest.Scripts["test"]) {
		missing = append(missing, "package.json has no test script the job can pass (it is missing, blank, or the placeholder npm init writes)")
	}
	if len(missing) > 0 {
		return templates.Context{}, strings.Join(missing, "; ")
	}
	node.Lint = nodemanifest.ScriptRuns(manifest.Scripts["lint"])
	node.Build = nodemanifest.ScriptRuns(manifest.Scripts["build"])
	return templates.Context{Node: node}, ""
}

// resolveNodeManager returns the package manager the job installs with, or why none can.
//
// A declared manager needs its own lockfile in CI's checkout; a lockfile of another manager
// beside it is ignored, as that manager ignores it. With no declaration, the committed lockfile
// decides, and lockfiles of two managers leave the choice to the repository.
func resolveNodeManager(ctx context.Context, repoPath, packageManager string) (templates.NodeContext, string) {
	declared, version, ok := nodemanifest.DeclaredManager(packageManager)
	if !ok {
		return templates.NodeContext{}, fmt.Sprintf("package.json names packageManager %q, and the job installs only with npm, pnpm, Yarn or Bun (\"<name>@<version>\")", packageManager)
	}
	candidates := nodemanifest.AllLockfiles()
	if declared != "" {
		candidates = declared.Lockfiles()
	}
	committed, why := committedLockfiles(ctx, repoPath, candidates)
	if why != "" {
		return templates.NodeContext{}, why
	}
	manager := declared
	if manager == "" {
		if manager, why = lockfileOwner(committed); why != "" {
			return templates.NodeContext{}, why
		}
	}
	node := templates.NodeContext{Manager: string(manager)}
	if manager == nodemanifest.ManagerYarn {
		node.YarnBerry, why = yarnBerry(repoPath, declared, version)
	}
	return node, why
}

// committedLockfiles returns the candidate lockfiles CI's checkout will hold, or why it holds
// none of them.
//
// CI checks out what Git commits, not the working tree. A library commonly lists its lockfile
// in .gitignore while a local install still writes one, so the file being present proves
// nothing: the job would install from it here and fail on every pull request there. A lockfile
// counts when Git tracks it or would commit it; one the repository's own ignore rules exclude,
// untracked, does not. The index is consulted (util.GitIgnoredPaths with noIndex false), so a
// lockfile force-added past such a rule still counts, because the checkout carries it. When Git
// cannot answer, outside a work tree or without git, nothing proves the checkout holds the file,
// so the job is withheld.
func committedLockfiles(ctx context.Context, repoPath string, candidates []string) ([]string, string) {
	present := slices.DeleteFunc(slices.Clone(candidates), func(name string) bool {
		return !util.FileExists(filepath.Join(repoPath, name))
	})
	if len(present) == 0 {
		return nil, "no " + orList(candidates) + " for the job's locked install"
	}
	ignored, err := util.GitIgnoredPaths(ctx, repoPath, present, false)
	if err != nil {
		return nil, fmt.Sprintf("cannot tell whether Git commits %s, so CI's checkout may lack it: %v", strings.Join(present, ", "), err)
	}
	committed := slices.DeleteFunc(slices.Clone(present), func(name string) bool { return slices.Contains(ignored, name) })
	if len(committed) == 0 {
		return nil, strings.Join(present, ", ") + " is untracked and git-ignored, so CI's checkout has no lockfile for the job's locked install"
	}
	return committed, ""
}

// lockfileOwner returns the one package manager whose lockfiles these are, or why there is not
// exactly one.
func lockfileOwner(lockfiles []string) (nodemanifest.Manager, string) {
	var owners []nodemanifest.Manager
	for _, name := range lockfiles {
		if owner, ok := nodemanifest.LockfileManager(name); ok && !slices.Contains(owners, owner) {
			owners = append(owners, owner)
		}
	}
	if len(owners) != 1 {
		return "", fmt.Sprintf("package.json names no packageManager and CI's checkout holds lockfiles of %d package managers (%s); name the one that installs in packageManager", len(owners), strings.Join(lockfiles, ", "))
	}
	return owners[0], ""
}

// yarnBerry reports whether the Yarn the job runs is Yarn 2 or later, whose locked install is
// --immutable where Yarn 1's is --frozen-lockfile.
//
// Corepack provisions the Yarn packageManager pins. Without a pin it provisions Yarn 1, which
// hands the command over to the release a .yarnrc.yml yarnPath names, so that setting decides.
func yarnBerry(repoPath string, declared nodemanifest.Manager, version string) (bool, string) {
	if declared == nodemanifest.ManagerYarn {
		major, _, _ := strings.Cut(version, ".")
		n, err := strconv.Atoi(major)
		return err == nil && n >= 2, ""
	}
	if !util.FileExists(filepath.Join(repoPath, yarnrcFile)) {
		return false, ""
	}
	data, err := util.ReadConfinedLimited(repoPath, yarnrcFile, maxSettingBytes)
	if err != nil {
		return false, fmt.Sprintf("%s is unreadable, so which Yarn runs is unknown: %v", yarnrcFile, err)
	}
	var yarnrc struct {
		YarnPath string `yaml:"yarnPath"`
	}
	if err := yaml.Unmarshal(data, &yarnrc); err != nil {
		return false, fmt.Sprintf("%s does not parse, so which Yarn runs is unknown: %v", yarnrcFile, err)
	}
	return strings.TrimSpace(yarnrc.YarnPath) != "", ""
}

// orList joins names as "a", "a or b", or "a, b or c".
func orList(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// readNodeManifest reads the repository's root package.json, bounded like a setting read.
//
// Keys are matched exactly, as npm matches them. Unmarshalling into tagged struct fields would
// match case-insensitively, so a "Scripts" object npm never reads would count as the test script.
func readNodeManifest(repoPath string) (nodeManifest, error) {
	var manifest nodeManifest
	data, err := util.ReadConfinedLimited(repoPath, "package.json", maxSettingBytes)
	if err != nil {
		return manifest, fmt.Errorf("package.json is unreadable: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return manifest, fmt.Errorf("package.json does not parse: %w", err)
	}
	if err := unmarshalPresent(fields["packageManager"], &manifest.PackageManager); err != nil {
		return manifest, fmt.Errorf("package.json packageManager is not a string: %w", err)
	}
	if err := unmarshalPresent(fields["scripts"], &manifest.Scripts); err != nil {
		return manifest, fmt.Errorf("package.json scripts is not an object of strings: %w", err)
	}
	return manifest, nil
}

// unmarshalPresent decodes raw into target when the key was present, and leaves target at its
// zero value when it was not.
func unmarshalPresent(raw json.RawMessage, target any) error {
	if raw == nil {
		return nil
	}
	return json.Unmarshal(raw, target)
}
