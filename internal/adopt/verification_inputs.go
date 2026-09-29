package adopt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	maxVerificationEntries    = util.DefaultDiscoveryEntries
	maxVerificationInputs     = 128
	maxVerificationInputBytes = 64 * 1024
	maxVerificationTotalBytes = 2 * 1024 * 1024
	// Public repositories commonly nest project metadata under src/test trees;
	// retain a scalar traversal bound while allowing those supported layouts.
	maxVerificationDepth = 32
)

type verificationInputs struct {
	files             map[string][]byte
	pythonDirectories map[string]bool
	cSources          cSourceObservation
	// unreadable holds, for each optional pytest configuration file the walk found but could not
	// read within its bounds, why it could not (pytestOptionalMarker).
	unreadable []string
}

func (v verificationInputs) has(path string) bool {
	_, ok := v.files[path]
	return ok
}

func loadVerificationInputs(ctx context.Context, path string) (result verificationInputs, err error) {
	return loadVerificationInputsWithLimits(ctx, path, DefaultVerificationLimits())
}

func loadVerificationInputsWithLimits(ctx context.Context, path string, requested VerificationLimits) (result verificationInputs, err error) {
	limits, normalizeErr := NormalizeVerificationLimits(&requested)
	if normalizeErr != nil {
		return result, normalizeErr
	}
	result.files = make(map[string][]byte)
	result.pythonDirectories = make(map[string]bool)
	if ctx == nil {
		return result, errors.New("verification planning requires context")
	}
	ctx, cancel := context.WithTimeout(ctx, contextopt.MaxDuration)
	defer cancel()
	err = walkVerificationInputs(ctx, path, &result, limits)
	return result, err
}

func walkVerificationInputs(ctx context.Context, path string, result *verificationInputs, limits VerificationLimits) error {
	queue := []string{"."}
	count, total := 0, 0
	for i := 0; i < len(queue) && i <= limits.MaxEntries; i++ {
		entries, err := readVerificationDirectory(ctx, filepath.Join(path, queue[i]), limits.MaxEntries)
		if err != nil {
			return err
		}
		count += len(entries)
		if count > limits.MaxEntries {
			return entriesBound.exceeded(limits.MaxEntries, fmt.Errorf("verification discovery exceeds %d entries", limits.MaxEntries))
		}
		for _, entry := range entries {
			rel := filepath.ToSlash(filepath.Join(queue[i], entry.Name()))
			if err := visitVerificationInput(ctx, path, rel, entry, result, &total, &queue, limits); err != nil {
				return err
			}
		}
	}
	return nil
}

func readVerificationDirectory(ctx context.Context, path string, entryLimit int) (entries []fs.DirEntry, err error) {
	root, err := contextopt.OpenDirectory(ctx, path)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	entries, err = dir.ReadDir(entryLimit + 1)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return entries, err
}

func visitVerificationInput(ctx context.Context, root, rel string, entry fs.DirEntry, result *verificationInputs, total *int, queue *[]string, limits VerificationLimits) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if entry.IsDir() && skipVerificationDirectory(entry.Name()) {
		return nil
	}
	if strings.ContainsAny(rel, "\r\n") {
		return errors.New("verification discovery refuses filenames containing line breaks")
	}
	if strings.Count(rel, "/") > limits.MaxDepth {
		return depthBound.exceeded(limits.MaxDepth, fmt.Errorf("verification discovery exceeds directory depth %d", limits.MaxDepth))
	}
	if entry.IsDir() {
		*queue = append(*queue, rel)
		return nil
	}
	if err := result.capture(ctx, root, rel, entry, total, limits); err != nil {
		return err
	}
	return nil
}

// skipVerificationDirectory reports a directory that holds no verification input of the
// repository's own. The scratch directories come from util.IsScratchDir: a .claude/worktrees
// tree alone holds whole copies of the checkout and ran the walk past its entry bound on an
// ordinary working checkout. The toolchain trees come from util.IsToolchainTreeDir: the C
// sources of a package Zig fetched into zig-pkg/ made a pure-Zig repository C/C++.
func skipVerificationDirectory(name string) bool {
	if util.IsScratchDir(name) || util.IsToolchainTreeDir(name) {
		return true
	}
	switch name {
	case ".git", "node_modules", "vendor", "bin", "obj", "build", "dist", "target", ".venv", "__pycache__":
		return true
	default:
		return false
	}
}

func (v *verificationInputs) capture(ctx context.Context, root, rel string, entry fs.DirEntry, total *int, limits VerificationLimits) error {
	v.cSources.observe(rel)
	if strings.HasPrefix(rel, "tests/") && strings.HasPrefix(entry.Name(), "test") && strings.HasSuffix(rel, ".py") {
		if entry.Type()&fs.ModeSymlink != 0 {
			return errors.New("verification discovery refuses symlinked Python tests")
		}
		v.pythonDirectories[filepath.ToSlash(filepath.Dir(rel))] = true
		if len(v.pythonDirectories) > limits.MaxFiles {
			return filesBound.exceeded(limits.MaxFiles, fmt.Errorf("verification discovery exceeds %d Python test directories", limits.MaxFiles))
		}
	}
	if !verificationMarker(rel) {
		return nil
	}
	return v.captureMarker(ctx, root, rel, entry, total, limits)
}

// captureMarker records the verification marker at rel within the file and byte bounds. A
// presence marker is recorded without its content, which no planner reads. An optional pytest
// configuration file that cannot be read, such as a symlink, a file that is not UTF-8 text or one
// past the byte bounds, is not pytest configuration: it is recorded as unreadable, for the
// report to name, and the walk goes on, as it did before tox.ini and setup.cfg were read (#594).
// A cancelled or expired walk still fails.
func (v *verificationInputs) captureMarker(ctx context.Context, root, rel string, entry fs.DirEntry, total *int, limits VerificationLimits) error {
	if len(v.files) >= limits.MaxFiles {
		return filesBound.exceeded(limits.MaxFiles, fmt.Errorf("verification discovery exceeds %d metadata files", limits.MaxFiles))
	}
	if presenceMarker(rel) {
		if !entry.Type().IsRegular() {
			return fmt.Errorf("verification discovery refuses %s that is not a regular file", rel)
		}
		v.files[rel] = nil
		return nil
	}
	data, err := readVerificationMarker(ctx, root, rel, *total, limits)
	if err != nil && ctx.Err() == nil && pytestOptionalMarker(rel) {
		v.unreadable = append(v.unreadable, err.Error())
		return nil
	}
	if err != nil {
		return err
	}
	*total += len(data)
	v.files[rel] = data
	return nil
}

// readVerificationMarker reads the verification marker at rel when it fits the per-file byte
// bound and, with total bytes already read, the total byte bound.
func readVerificationMarker(ctx context.Context, root, rel string, total int, limits VerificationLimits) ([]byte, error) {
	data, err := contextopt.ReadSnapshot(ctx, filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, fmt.Errorf("verification input %s: %w", rel, err)
	}
	if int64(len(data)) > limits.MaxFileBytes || int64(total)+int64(len(data)) > limits.MaxTotalBytes {
		return nil, fmt.Errorf("verification input %s: metadata exceeds byte bounds (file=%d total=%d)", rel, limits.MaxFileBytes, limits.MaxTotalBytes)
	}
	return data, nil
}

func verificationMarker(rel string) bool {
	if strings.HasSuffix(rel, ".csproj") || strings.HasSuffix(rel, ".sln") || strings.HasSuffix(rel, ".slnx") {
		return true
	}
	// The pytest configuration files include pyproject.toml, the Python runtime marker.
	if pytestConfigMarker(rel) {
		return true
	}
	switch rel {
	case "Makefile", "go.mod", "Cargo.toml", "package.json", "global.json", ".python-version",
		"meson.build", "core/meson.build", "CMakeLists.txt", "pom.xml", "build.gradle", "build.gradle.kts", "pubspec.yaml", zigBuildMarker:
		return true
	default:
		return false
	}
}

// presenceMarker reports a verification marker whose presence alone selects a plan step. The
// walk records it without reading it, so a build script of any size spends none of the byte
// budget: build.zig is Zig source, often longer than the metadata byte bound, and no planner
// reads it. pytest.toml, .pytest.toml, pytest.ini and .pytest.ini are pytest's configuration
// even when empty, so their content is not read either (pytestPresenceMarker).
func presenceMarker(rel string) bool {
	return rel == zigBuildMarker || pytestPresenceMarker(rel)
}
