// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package workstation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"time"

	"github.com/cordanaLLM/praetor/internal/buildid"
	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Build identifies a running engine binary: the main module Go recorded, the VCS stamp it
// embedded at build time, and the executable file itself.
type Build struct {
	// Module is the main module path from the build information.
	Module string
	// Revision is the full vcs.revision; empty for an unstamped build (go run, go test).
	Revision string
	// Modified is vcs.modified: the tree had uncommitted changes when it was built.
	Modified bool
	// Executable is the absolute, symlink-resolved path of the binary; empty when unknown.
	Executable string
	// ModTime is the executable's modification time; zero when unknown.
	ModTime time.Time
}

// ErrStaleEngine reports that the running engine binary does not match the engine checkout
// it would write agent context into (BUG-1004). An older install rewrote the AGENTS.md
// register block and every vendor file with the text its own, older compiler renders.
var ErrStaleEngine = errors.New("engine build does not match this checkout")

// Bounds on the build comparison (HISS-02).
const (
	maxGoModBytes          = 1 << 20
	maxBuildInputs         = 1 << 16
	buildInputProbeBytes   = 8 << 20
	buildInputProbeTimeout = 20 * time.Second
	// maxCheckoutDepth bounds the directories buildCheckout climbs from an executable.
	maxCheckoutDepth = 256
)

// objectNamePattern is a full SHA-1 or SHA-256 object name, the only revision form Go stamps.
var objectNamePattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// RunningBuild describes the current process. A field the runtime cannot answer stays zero.
func RunningBuild() Build {
	info := buildid.RunningInfo()
	executable, err := os.Executable()
	if err != nil {
		executable = ""
	}
	return describeBuild(info, executable)
}

// describeBuild combines the build information of a binary with the executable file it was
// read from; a nil info or an empty executable leaves those fields zero.
func describeBuild(info *debug.BuildInfo, executable string) Build {
	var build Build
	if info != nil {
		build.Module = info.Main.Path
		build.Revision, build.Modified = buildid.Stamp(info)
	}
	if executable == "" {
		return build
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	build.Executable = executable
	if stat, err := os.Stat(executable); err == nil {
		build.ModTime = stat.ModTime()
	}
	return build
}

// CheckBuildCurrent refuses a build that cannot be shown to match the engine checkout at
// root, so a caller writes agent context only with a compiler whose rendering is the
// checkout's own. It returns nil when root is not a checkout of build.Module (any other
// repository: an adopter's AGENTS.md is compiled by whichever engine it installed), and for
// an unstamped build, which `go run` produces by compiling the checkout on the spot.
//
// A stamped build matches when no Go build input -- a non-test .go file, go.mod or go.sum --
// differs between its revision and the working tree. A build of a modified tree may carry edits
// its stamp does not name (carriesEdits): built inside root, it matches when every changed input
// is older than it, which is the binary the checkout's own build or hook just produced; built
// elsewhere, it matches only when the checkout holding its executable changes no Go build input
// either, so it compiles exactly its revision's inputs and is judged as a clean build. Every
// other build fails with ErrStaleEngine and a one-line reason.
//
// Go marks a build modified for an untracked file alone, such as the gate receipt
// .standards-receipt.json. The second rule lets the checkout's own bin/praetorctl write into a
// worktree of its revision: ci generated render runs compile-context in a temporary worktree of
// HEAD below the checkout (#760). An installed copy lies in no checkout, so Install builds a
// checkout whose tracked files match HEAD from a clean clone of it (prepareBuildSource), and
// scripts/dev_mcp.py builds under the checkout's bin/.
func CheckBuildCurrent(ctx context.Context, root string, build Build) error {
	if ctx == nil {
		return errors.New("workstation: build check requires a context")
	}
	if !judgesCheckout(root, build) {
		return nil
	}
	if !objectNamePattern.MatchString(build.Revision) {
		return staleBuild(build, "stamps no comparable revision")
	}
	absRoot, err := resolvedRoot(root)
	if err != nil {
		return fmt.Errorf("workstation: resolve checkout %s: %w", root, err)
	}
	edited, err := carriesEdits(ctx, absRoot, build)
	if err != nil {
		return err
	}
	changed, err := changedBuildInputs(ctx, absRoot, build.Revision)
	if err != nil {
		return staleBuild(build, fmt.Sprintf("cannot be compared with this checkout (%v)", err))
	}
	return judgeChanges(absRoot, build, edited, changed)
}

// carriesEdits reports whether build may hold Go edits its revision does not name, so
// judgeChanges accepts a changed input of root only when the executable is newer. A clean build
// holds none, and a build of a modified tree inside root holds root's own edits. A build of a
// modified tree elsewhere is judged against the checkout holding its executable
// (buildCheckout): when no Go build input there differs from the revision, only other files
// marked it modified and it holds no edit. Any other build of a modified tree outside root is
// refused: its edits are unknown, so no comparison with root can show it current.
func carriesEdits(ctx context.Context, root string, build Build) (bool, error) {
	if !build.Modified {
		return false, nil
	}
	if builtInside(root, build) {
		return true, nil
	}
	const outside = "was built from a modified tree outside this checkout"
	home := buildCheckout(build)
	if home == "" {
		return false, staleBuild(build, outside)
	}
	edits, err := changedBuildInputs(ctx, home, build.Revision)
	if err != nil {
		return false, staleBuild(build, fmt.Sprintf("%s (%s cannot be compared: %v)", outside, home, err))
	}
	if len(edits) > 0 {
		return false, staleBuild(build, fmt.Sprintf("%s (%s changes %d Go build inputs, first %s)", outside, home, len(edits), edits[0]))
	}
	return false, nil
}

// buildCheckout returns the nearest directory holding build's executable whose go.mod declares
// build.Module: the checkout a binary under its bin/ was built from. It returns "" for an
// unknown executable and for one in no checkout of the module, such as an installed copy.
func buildCheckout(build Build) string {
	if build.Executable == "" {
		return ""
	}
	dir := filepath.Dir(build.Executable)
	for depth := 0; depth < maxCheckoutDepth; depth++ {
		if checkoutModule(dir) == build.Module {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

// judgesCheckout reports whether CheckBuildCurrent judges build against root at all: a
// stamped build of the module root's go.mod declares.
func judgesCheckout(root string, build Build) bool {
	return build.Revision != "" && build.Module != "" && checkoutModule(root) == build.Module
}

// builtInside reports whether build's executable lies inside the resolved checkout root.
func builtInside(root string, build Build) bool {
	return build.Executable != "" && util.WithinRoot(root, build.Executable)
}

// judgeChanges decides a stamped build against the Go build inputs that differ from its
// revision; edited is the answer of carriesEdits. See CheckBuildCurrent.
func judgeChanges(root string, build Build, edited bool, changed []string) error {
	if len(changed) == 0 {
		return nil
	}
	if !edited {
		return staleBuild(build, fmt.Sprintf("lacks %d changed Go build inputs (first %s)", len(changed), changed[0]))
	}
	if newer, found := firstNewerInput(root, changed, build.ModTime); found {
		return staleBuild(build, fmt.Sprintf("cannot be shown to contain changed input %s", newer))
	}
	return nil
}

// staleBuild formats the one-line refusal: which build, why, and the command that writes
// with the checkout's own compiler instead.
func staleBuild(build Build, reason string) error {
	return fmt.Errorf("%w: build %s %s; rebuild bin/praetorctl from the checkout or run go run %s compile-context",
		ErrStaleEngine, buildLabel(build), reason, buildPackages["praetorctl"])
}

// buildLabel names a build the way every praetor binary reports its own stamped identity:
// the short revision, suffixed -dirty for a modified tree (buildid.FromStamp).
func buildLabel(build Build) string {
	return buildid.FromStamp(build.Revision, build.Modified).String()
}

// checkoutModule reads the module path root's go.mod declares, or "" when root holds no
// readable go.mod or it declares no module.
func checkoutModule(root string) string {
	data, err := util.ReadConfinedLimited(root, "go.mod", maxGoModBytes)
	if err != nil {
		return ""
	}
	module, _ := gomanifest.ModuleDirective(data)
	return module
}

// resolvedRoot returns root as a clean, absolute, symlink-resolved path, the form
// util.WithinRoot compares and os.Executable resolves to.
func resolvedRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// changedBuildInputs lists the Go build inputs that differ between revision and root's
// working tree: tracked files git diff reports against revision, then untracked files git
// would add, each kept only when it is a non-test Go source, go.mod or go.sum.
func changedBuildInputs(ctx context.Context, root, revision string) ([]string, error) {
	diff, err := util.RunGitProbeWithin(ctx, root, buildInputProbeBytes, buildInputProbeTimeout,
		"diff", "--name-only", "--no-renames", "-z", revision, "--", "*.go", "go.mod", "go.sum")
	if err != nil {
		return nil, fmt.Errorf("git diff against %s: %w", buildid.Short(revision), err)
	}
	untracked, err := util.RunGitProbeWithin(ctx, root, buildInputProbeBytes, buildInputProbeTimeout,
		"ls-files", "-z", "--others", "--exclude-standard", "--", "*.go")
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	listed := append(bytes.Split(diff.Stdout, []byte{0}), bytes.Split(untracked.Stdout, []byte{0})...)
	inputs := make([]string, 0, len(listed))
	for i := 0; i < len(listed) && i < maxBuildInputs; i++ {
		if name := string(listed[i]); isBuildInput(name) {
			inputs = append(inputs, name)
		}
	}
	return inputs, nil
}

// isBuildInput reports whether a slash-separated repository path is compiled into, or pins
// the dependencies of, the engine binary.
func isBuildInput(name string) bool {
	return name == "go.mod" || name == "go.sum" || util.IsGoNonTestSource(name)
}

// firstNewerInput returns the first changed input that is missing or modified after built:
// a build of a modified tree cannot contain it.
func firstNewerInput(root string, changed []string, built time.Time) (string, bool) {
	if built.IsZero() {
		return changed[0], true
	}
	for i := 0; i < len(changed) && i < maxBuildInputs; i++ {
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(changed[i])))
		if err != nil || info.ModTime().After(built) {
			return changed[i], true
		}
	}
	return "", false
}
