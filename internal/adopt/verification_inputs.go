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
	maxVerificationDepth      = 6
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
	result.files = make(map[string][]byte)
	result.pythonDirectories = make(map[string]bool)
	if ctx == nil {
		return result, errors.New("verification planning requires context")
	}
	ctx, cancel := context.WithTimeout(ctx, contextopt.MaxDuration)
	defer cancel()
	err = walkVerificationInputs(ctx, path, &result)
	return result, err
}

func walkVerificationInputs(ctx context.Context, path string, result *verificationInputs) error {
	queue := []string{"."}
	count, total := 0, 0
	for i := 0; i < len(queue) && i <= maxVerificationEntries; i++ {
		entries, err := readVerificationDirectory(ctx, filepath.Join(path, queue[i]))
		if err != nil {
			return err
		}
		count += len(entries)
		if count > maxVerificationEntries {
			return errors.New("verification discovery exceeds 4096 entries")
		}
		for _, entry := range entries {
			rel := filepath.ToSlash(filepath.Join(queue[i], entry.Name()))
			if err := visitVerificationInput(ctx, path, rel, entry, result, &total, &queue); err != nil {
				return err
			}
		}
	}
	return nil
}

func readVerificationDirectory(ctx context.Context, path string) (entries []fs.DirEntry, err error) {
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
	entries, err = dir.ReadDir(maxVerificationEntries + 1)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return entries, err
}

func visitVerificationInput(ctx context.Context, root, rel string, entry fs.DirEntry, result *verificationInputs, total *int, queue *[]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if entry.IsDir() && skipVerificationDirectory(entry.Name()) {
		return nil
	}
	if strings.ContainsAny(rel, "\r\n") {
		return errors.New("verification discovery refuses filenames containing line breaks")
	}
	if strings.Count(rel, "/") > maxVerificationDepth {
		return errors.New("verification discovery exceeds directory depth")
	}
	if entry.IsDir() {
		*queue = append(*queue, rel)
		return nil
	}
	if err := result.capture(ctx, root, rel, entry, total); err != nil {
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

func (v *verificationInputs) capture(ctx context.Context, root, rel string, entry fs.DirEntry, total *int) error {
	if strings.HasPrefix(rel, "tests/") && strings.HasPrefix(entry.Name(), "test") && strings.HasSuffix(rel, ".py") {
		if entry.Type()&fs.ModeSymlink != 0 {
			return errors.New("verification discovery refuses symlinked Python tests")
		}
		v.pythonDirectories[filepath.ToSlash(filepath.Dir(rel))] = true
		if len(v.pythonDirectories) > maxVerificationInputs {
			return errors.New("verification discovery exceeds 128 Python test directories")
		}
	}
	if !verificationMarker(rel) {
		return nil
	}
	if len(v.files) >= maxVerificationInputs {
		return errors.New("verification discovery exceeds 128 metadata files")
	}
	data, err := contextopt.ReadSnapshot(ctx, filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("verification input %s: %w", rel, err)
	}
	*total += len(data)
	if len(data) > maxVerificationInputBytes || *total > maxVerificationTotalBytes {
		return errors.New("verification metadata exceeds byte bounds")
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
