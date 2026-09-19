// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package workstation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// BuildFunc builds one binary from checkout into destination (a full file path). The real
// implementation (goBuild) runs `go build -trimpath`; tests substitute a fake that writes
// fixture bytes instead, so Install's lock, backup, swap and manifest logic is exercised
// without paying for a real compiler invocation on every run.
type BuildFunc func(ctx context.Context, checkout, name, destination string) error

// Options configures Install. Checkout and BinDir must be clean absolute paths.
// ManifestPath defaults to config.DefaultInstallManifestPath. Now, Build and GOOS are
// injected seams: they default to time.Now, the real `go build` invocation, and
// runtime.GOOS.
type Options struct {
	Checkout            string
	BinDir              string
	ManifestPath        string
	FleetSettings       config.SettingsDocument
	WorkstationSettings config.SettingsDocument
	Now                 func() time.Time
	Build               BuildFunc
	GOOS                string
}

func (o Options) resolved() Options {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Build == nil {
		o.Build = goBuild
	}
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	return o
}

// Result is what Install reports.
type Result struct {
	Manifest     config.InstallManifest `json:"manifest"`
	ManifestPath string                 `json:"manifest_path"`
}

// Install builds the three engine binaries from Checkout and places them atomically in
// BinDir, then writes the install manifest. It holds the exclusive bin-directory lock for
// its whole run: a concurrent install or update is refused (ErrLockHeld), never merged. A
// build or placement failure restores every target this run already touched from a backup
// taken before anything was written.
func Install(ctx context.Context, opts Options) (result Result, err error) {
	if ctx == nil {
		return Result{}, errors.New("workstation: install requires a context")
	}
	opts, err = validateInstallOptions(opts)
	if err != nil {
		return Result{}, err
	}
	// #nosec G301 -- BinDir is the operator's PATH bin directory; 0755 keeps it listable
	// and its binaries executable by other processes, matching standard bin-directory
	// permissions (e.g. ~/.local/bin).
	if mkErr := os.MkdirAll(opts.BinDir, 0o755); mkErr != nil {
		return Result{}, fmt.Errorf("workstation: create bin directory %s: %w", opts.BinDir, mkErr)
	}
	release, err := acquireLock(opts.BinDir)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if relErr := release(); relErr != nil {
			err = errors.Join(err, relErr)
		}
	}()
	return install(ctx, opts)
}

func validateInstallOptions(opts Options) (Options, error) {
	opts = opts.resolved()
	if !filepath.IsAbs(opts.Checkout) {
		return opts, errors.New("workstation: checkout must be an absolute path")
	}
	if !filepath.IsAbs(opts.BinDir) {
		return opts, errors.New("workstation: bin directory must be an absolute path")
	}
	if opts.ManifestPath == "" {
		path, err := config.DefaultInstallManifestPath()
		if err != nil {
			return opts, fmt.Errorf("workstation: resolve default manifest path: %w", err)
		}
		opts.ManifestPath = path
	}
	return opts, nil
}

func install(ctx context.Context, opts Options) (Result, error) {
	states, err := inspectTargets(opts.BinDir)
	if err != nil {
		return Result{}, err
	}
	var backupDir string
	if !allAbsent(states) {
		if backupDir, err = backupTargets(opts.BinDir, states); err != nil {
			return Result{}, err
		}
	}
	digests, err := placeAll(ctx, opts, states, backupDir)
	if err != nil {
		return Result{}, err
	}
	commit, err := engineCommit(ctx, opts.Checkout)
	if err != nil {
		return Result{}, err
	}
	manifest, err := writeManifest(ctx, opts, commit, digests, backupDir, states)
	if err != nil {
		return Result{}, err
	}
	return Result{Manifest: manifest, ManifestPath: opts.ManifestPath}, nil
}

// placeAll builds each binary into a scratch directory, then swaps it and its alias into
// binDir in turn. Any failure rolls back everything this call already placed.
func placeAll(ctx context.Context, opts Options, states map[string]targetState, backupDir string) (map[string]string, error) {
	buildDir, err := os.MkdirTemp("", "praetor-workstation-build-")
	if err != nil {
		return nil, fmt.Errorf("workstation: create build directory: %w", err)
	}
	defer os.RemoveAll(buildDir) //nolint:errcheck // best-effort scratch cleanup; the binaries are already placed by the time this runs

	digests := make(map[string]string, len(binaryNames))
	var done []string
	for _, name := range binaryNames {
		digest, placeErr := placeOne(ctx, opts, name, buildDir, states)
		if placeErr != nil {
			return nil, rollbackAndWrap(opts, backupDir, states, done, placeErr)
		}
		digests[name] = digest
		done = append(done, name, binaryAliases[name])
	}
	return digests, nil
}

// placeOne builds, hashes and swaps in one binary plus its alias symlink.
func placeOne(ctx context.Context, opts Options, name, buildDir string, states map[string]targetState) (string, error) {
	built := filepath.Join(buildDir, name)
	if err := opts.Build(ctx, opts.Checkout, name, built); err != nil {
		return "", fmt.Errorf("workstation: build %s: %w", name, err)
	}
	digest, builtMode, err := hashFile(built)
	if err != nil {
		return "", err
	}
	mode, err := installMode(builtMode, name, states)
	if err != nil {
		return "", err
	}
	if err := stageAndSwap(opts.GOOS, opts.BinDir, name, func(path string) error {
		return copyFileMode(built, path, mode)
	}); err != nil {
		return "", err
	}
	alias := binaryAliases[name]
	if err := stageAndSwap(opts.GOOS, opts.BinDir, alias, func(path string) error {
		return os.Symlink(name, path)
	}); err != nil {
		return "", err
	}
	return digest, nil
}

// installMode computes the permission ceiling for name: the built binary's own mode
// (capped at 0755), narrowed by any existing regular file at name or its alias, exactly as
// scripts/dev_install.py did. A host that had chmod'd an installed binary down to
// non-executable keeps meaning that: the new build inherits the same restriction rather
// than silently widening it back.
//
// On Windows this whole computation is skipped: os.FileInfo.Mode() there is synthesised
// from the read-only attribute alone, so built.Perm() for an ordinary freshly built binary
// already comes back 0o666 -- no execute bit for os.Stat to report, whether or not anything
// pre-existing narrows it further. Every install refused itself as "existing permissions
// prevent an executable installation" on the Windows leg of the portability matrix (#135),
// not because any operator restricted anything, but because Windows never reports an
// execute bit through Mode() to begin with; executability there comes from the .exe
// extension. This reads runtime.GOOS rather than the caller-supplied Options.GOOS: the
// latter is a simulated label the test suite also sets to "linux" while actually running on
// the real Windows filesystem (it drives path-formatting choices like symlink vs. copy, not
// what os.Stat can report), so it does not answer what this check needs to know.
func installMode(built os.FileMode, name string, states map[string]targetState) (os.FileMode, error) {
	if runtime.GOOS == goosWindows {
		return built.Perm() & 0o755, nil
	}
	mode := built.Perm() & 0o755
	for _, target := range []string{name, binaryAliases[name]} {
		if state := states[target]; state.kind == targetFile {
			mode &= state.mode
		}
	}
	if mode&0o111 == 0 {
		return 0, fmt.Errorf("workstation: existing permissions on %s prevent an executable installation", name)
	}
	return mode, nil
}

func rollbackAndWrap(opts Options, backupDir string, states map[string]targetState, done []string, cause error) error {
	failures := restoreFromBackup(opts.GOOS, opts.BinDir, backupDir, states, done)
	if len(failures) > 0 {
		return fmt.Errorf("workstation: install failed: %w; rollback also failed: %v; backup kept at %s", cause, failures, backupDir)
	}
	if backupDir != "" {
		return fmt.Errorf("workstation: install failed: %w; rolled back; backup kept at %s", cause, backupDir)
	}
	return fmt.Errorf("workstation: install failed: %w; rolled back", cause)
}

// engineCommit reads checkout's exact HEAD commit; config.WriteInstallManifest's own
// validation is the final check that it is a well-formed 40-character id.
func engineCommit(ctx context.Context, checkout string) (string, error) {
	out, err := util.RunGit(ctx, checkout, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("workstation: resolve checkout HEAD in %s: %w", checkout, err)
	}
	return out, nil
}

func writeManifest(ctx context.Context, opts Options, commit string, digests map[string]string,
	backupDir string, states map[string]targetState) (config.InstallManifest, error) {
	fleet, err := describeSettingsDocument(ctx, opts.FleetSettings)
	if err != nil {
		return config.InstallManifest{}, err
	}
	workstationDoc, err := describeSettingsDocument(ctx, opts.WorkstationSettings)
	if err != nil {
		return config.InstallManifest{}, err
	}
	previous := previousInstall(ctx, opts, backupDir, states)
	manifest := buildManifest(opts.Now(), commit, opts.BinDir, digests, previous, fleet, workstationDoc)
	if err := config.WriteInstallManifest(opts.ManifestPath, manifest); err != nil {
		return config.InstallManifest{}, fmt.Errorf("workstation: write install manifest: %w", err)
	}
	return manifest, nil
}

func describeSettingsDocument(ctx context.Context, doc config.SettingsDocument) (*config.InstalledDocument, error) {
	if doc.Path == "" {
		return nil, nil
	}
	data, err := contextopt.ReadSnapshot(ctx, doc.Path)
	if err != nil {
		return nil, fmt.Errorf("workstation: read %s settings %s: %w", doc.Origin, doc.Path, err)
	}
	return settingsDocument(doc, data), nil
}

// previousInstall names the install this run replaces, for a later rollback (W2). It is
// nil on a genuinely first install (every target was absent) and also when a target
// existed but no valid prior manifest could be read: there is a backup on disk either way,
// but only a readable manifest gives a commit worth recording.
func previousInstall(ctx context.Context, opts Options, backupDir string, states map[string]targetState) *config.InstalledPrevious {
	if allAbsent(states) {
		return nil
	}
	prior, err := config.ReadInstallManifest(ctx, opts.ManifestPath)
	if err != nil {
		return nil
	}
	return &config.InstalledPrevious{EngineCommit: prior.EngineCommit, Backup: backupDir}
}

// goBuild is the real BuildFunc: `go build -trimpath`, which still embeds Go's VCS stamp
// (so `praetorctl version` reports the built commit) because -trimpath strips source paths
// from the binary, not the module's recorded VCS metadata.
func goBuild(ctx context.Context, checkout, name, destination string) error {
	pkg, ok := buildPackages[name]
	if !ok {
		return fmt.Errorf("workstation: no build package recorded for %s", name)
	}
	out, err := util.RunCommand(ctx, checkout, "go", "build", "-trimpath", "-o", destination, pkg)
	if err != nil {
		return fmt.Errorf("workstation: go build %s: %w: %s", pkg, err, out)
	}
	return nil
}
