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
)

const (
	maxVerificationEntries    = 4096
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
			return fmt.Errorf("verification discovery exceeds %d entries", limits.MaxEntries)
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
		return fmt.Errorf("verification discovery exceeds directory depth %d", limits.MaxDepth)
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

func skipVerificationDirectory(name string) bool {
	switch name {
	case ".git", ".workingdir", ".workingdir2", "node_modules", "vendor", "bin", "obj", "build", "dist", "target", ".venv", "__pycache__":
		return true
	default:
		return false
	}
}

func (v *verificationInputs) capture(ctx context.Context, root, rel string, entry fs.DirEntry, total *int, limits VerificationLimits) error {
	if strings.HasPrefix(rel, "tests/") && strings.HasPrefix(entry.Name(), "test") && strings.HasSuffix(rel, ".py") {
		if entry.Type()&fs.ModeSymlink != 0 {
			return errors.New("verification discovery refuses symlinked Python tests")
		}
		v.pythonDirectories[filepath.ToSlash(filepath.Dir(rel))] = true
		if len(v.pythonDirectories) > limits.MaxFiles {
			return fmt.Errorf("verification discovery exceeds %d Python test directories", limits.MaxFiles)
		}
	}
	if !verificationMarker(rel) {
		return nil
	}
	if len(v.files) >= limits.MaxFiles {
		return fmt.Errorf("verification discovery exceeds %d metadata files", limits.MaxFiles)
	}
	data, err := contextopt.ReadSnapshot(ctx, filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("verification input %s: %w", rel, err)
	}
	*total += len(data)
	if int64(len(data)) > limits.MaxFileBytes || int64(*total) > limits.MaxTotalBytes {
		return fmt.Errorf("verification metadata exceeds byte bounds (file=%d total=%d)", limits.MaxFileBytes, limits.MaxTotalBytes)
	}
	v.files[rel] = data
	return nil
}

func verificationMarker(rel string) bool {
	if strings.HasSuffix(rel, ".csproj") || strings.HasSuffix(rel, ".sln") || strings.HasSuffix(rel, ".slnx") {
		return true
	}
	switch rel {
	case "Makefile", "go.mod", "Cargo.toml", "package.json", "global.json", "pyproject.toml", "pytest.ini", ".pytest.ini", ".python-version",
		"meson.build", "core/meson.build", "CMakeLists.txt", "pom.xml", "build.gradle", "build.gradle.kts", "pubspec.yaml":
		return true
	default:
		return false
	}
}
