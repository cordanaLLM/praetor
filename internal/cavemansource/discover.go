package cavemansource

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	trackedProbeMaxBytes   = 1 << 20
	maxTrackedIndexEntries = contextopt.MaxSources
)

type discoveredInput struct {
	input config.RegisterSourceInput
	path  string
}

func discoverInputs(ctx context.Context, root string, inputs []config.RegisterSourceInput) ([]discoveredInput, []string, error) {
	if len(inputs) == 0 || len(inputs) > config.MaxRegisterSourceInputs {
		return nil, nil, fmt.Errorf("caveman source inputs require 1..%d rows", config.MaxRegisterSourceInputs)
	}
	items := make([]discoveredInput, 0, len(inputs))
	for index := range inputs {
		found, err := discoverInput(ctx, root, inputs[index])
		if err != nil {
			return nil, nil, fmt.Errorf("caveman source input %d: %w", index, err)
		}
		items = append(items, found...)
		if len(items) > config.MaxRegisterSourceInputs*contextopt.MaxSources {
			return nil, nil, errors.New("caveman source discovery exceeds bounded work items")
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].path+"\x00"+items[i].input.Selector < items[j].path+"\x00"+items[j].input.Selector
	})
	files := uniqueDiscoveredFiles(items)
	if len(files) == 0 || len(files) > contextopt.MaxSources {
		return nil, nil, fmt.Errorf("caveman source discovery requires 1..%d files", contextopt.MaxSources)
	}
	return items, files, nil
}

func discoverInput(ctx context.Context, root string, input config.RegisterSourceInput) ([]discoveredInput, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	abs, err := confinedSourcePath(root, input.Path)
	if err != nil {
		return nil, err
	}
	if err := rejectSourceSymlinks(root, abs); err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", input.Path, err)
	}
	if !info.IsDir() {
		if !matchesFormat(input.Path, input.Format) {
			return nil, fmt.Errorf("%s extension does not match format %s", input.Path, input.Format)
		}
		return []discoveredInput{{input: input, path: input.Path}}, nil
	}
	return walkSourceDirectory(ctx, root, abs, input)
}

func walkSourceDirectory(ctx context.Context, root, abs string, input config.RegisterSourceInput) ([]discoveredInput, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	walker := sourceDirectoryWalker{ctx: ctx, root: rootAbs, input: input}
	err = filepath.WalkDir(abs, walker.visit)
	if err != nil {
		return nil, err
	}
	if len(walker.found) == 0 {
		return nil, fmt.Errorf("directory %s contains zero %s source files", input.Path, input.Format)
	}
	return walker.found, nil
}

type sourceDirectoryWalker struct {
	ctx   context.Context
	root  string
	input config.RegisterSourceInput
	found []discoveredInput
}

func (w *sourceDirectoryWalker) visit(path string, entry fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if entry.Type()&fs.ModeSymlink != 0 {
		return fmt.Errorf("caveman source directory contains symlink %s", path)
	}
	if !entry.Type().IsRegular() || !matchesFormat(path, w.input.Format) || sourceTestFile(path, w.input.Format) {
		return nil
	}
	return w.add(path)
}

func (w *sourceDirectoryWalker) add(path string) error {
	rel, err := filepath.Rel(w.root, path)
	if err != nil {
		return err
	}
	w.found = append(w.found, discoveredInput{input: w.input, path: filepath.ToSlash(rel)})
	if len(w.found) > contextopt.MaxSources {
		return fmt.Errorf("source directory exceeds %d matching files", contextopt.MaxSources)
	}
	return nil
}

func rejectSourceSymlinks(root, abs string) error {
	return rejectSourceSymlinksWith(root, abs, os.Lstat)
}

func rejectSourceSymlinksWith(root, abs string, lstat func(string) (fs.FileInfo, error)) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(rootAbs, abs)
	if err != nil {
		return err
	}
	parts := strings.Split(filepath.Clean(rel), string(os.PathSeparator))
	if len(parts) > 256 {
		return errors.New("caveman source path exceeds component bound")
	}
	current := rootAbs
	for index := 0; index < len(parts) && index < 256; index++ {
		current = filepath.Join(current, parts[index])
		info, inspectErr := lstat(current)
		if errors.Is(inspectErr, os.ErrNotExist) {
			return nil
		}
		if inspectErr != nil {
			return fmt.Errorf("inspect caveman source %s: %w", rel, inspectErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("caveman source path traverses symlink %s", rel)
		}
	}
	return nil
}

func sourceTestFile(path string, format config.RegisterSourceFormat) bool {
	base := filepath.Base(path)
	return format == config.SourceFormatGo && strings.HasSuffix(base, "_test.go") ||
		format == config.SourceFormatPython && strings.HasPrefix(base, "test_") ||
		format == config.SourceFormatPython && strings.HasSuffix(base, "_test.py")
}

func confinedSourcePath(root, rel string) (string, error) {
	abs, err := util.ConfinePath(root, filepath.FromSlash(rel))
	if err != nil {
		return "", fmt.Errorf("caveman source %s: %w", rel, err)
	}
	return abs, nil
}

func matchesFormat(path string, format config.RegisterSourceFormat) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch format {
	case config.SourceFormatShell:
		return ext == ".sh"
	case config.SourceFormatPython:
		return ext == ".py"
	case config.SourceFormatGo:
		return ext == ".go"
	case config.SourceFormatJSON:
		return ext == ".json"
	case config.SourceFormatYAML:
		return ext == ".yaml" || ext == ".yml"
	}
	return false
}

func uniqueDiscoveredFiles(items []discoveredInput) []string {
	files := make([]string, 0, len(items))
	for index := range items {
		if index == 0 || items[index].path != items[index-1].path {
			files = append(files, items[index].path)
		}
	}
	return files
}

func requireTracked(ctx context.Context, root string, files []string) error {
	args := []string{"--literal-pathspecs", "ls-files", "--stage", "-z", "--"}
	args = append(args, files...)
	result, err := util.RunGitProbe(ctx, root, trackedProbeMaxBytes, args...)
	if err != nil {
		return fmt.Errorf("list tracked caveman sources: %w", err)
	}
	return validateTrackedSourceIndex(result.Stdout, files)
}

func validateTrackedSourceIndex(data []byte, files []string) error {
	entries, err := splitTrackedSourceIndex(data)
	if err != nil {
		return err
	}
	if len(files) == 0 || len(files) > contextopt.MaxSources || len(entries) > maxTrackedIndexEntries {
		return fmt.Errorf("tracked caveman source index exceeds %d entries", maxTrackedIndexEntries)
	}
	requested, err := trackedSourceRequest(files)
	if err != nil {
		return err
	}
	tracked, err := trackedSourceEntries(entries, requested)
	if err != nil {
		return err
	}
	for _, file := range files {
		if !tracked[filepath.ToSlash(file)] {
			return fmt.Errorf("caveman source %s is not tracked", file)
		}
	}
	return nil
}

func trackedSourceRequest(files []string) (map[string]bool, error) {
	requested := make(map[string]bool, len(files))
	for _, file := range files {
		normalized := filepath.ToSlash(file)
		if requested[normalized] {
			return nil, fmt.Errorf("tracked caveman source request repeats %s", normalized)
		}
		requested[normalized] = true
	}
	return requested, nil
}

func trackedSourceEntries(entries []string, requested map[string]bool) (map[string]bool, error) {
	tracked := make(map[string]bool, len(entries))
	for _, raw := range entries {
		path, parseErr := parseTrackedSourceIndexEntry(raw)
		if parseErr != nil {
			return nil, parseErr
		}
		if !requested[path] {
			return nil, fmt.Errorf("tracked caveman source index contains unexpected path %s", path)
		}
		if tracked[path] {
			return nil, fmt.Errorf("tracked caveman source index repeats %s", path)
		}
		tracked[path] = true
	}
	return tracked, nil
}

func splitTrackedSourceIndex(data []byte) ([]string, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if data[len(data)-1] != 0 {
		return nil, errors.New("tracked caveman source index has truncated framing")
	}
	entries := strings.Split(string(data[:len(data)-1]), "\x00")
	for _, entry := range entries {
		if entry == "" {
			return nil, errors.New("tracked caveman source index contains empty entry")
		}
	}
	return entries, nil
}

func parseTrackedSourceIndexEntry(entry string) (string, error) {
	metadata, path, ok := strings.Cut(entry, "\t")
	fields := strings.Fields(metadata)
	if !ok || len(fields) != 3 || !validGitObjectID(fields[1]) || fields[2] != "0" || path == "" {
		return "", errors.New("tracked caveman source index contains malformed entry")
	}
	normalized := filepath.ToSlash(path)
	switch fields[0] {
	case "100644", "100755":
		return normalized, nil
	case "120000":
		return "", fmt.Errorf("caveman source %s is an indexed symlink", normalized)
	default:
		return "", fmt.Errorf("caveman source %s has unsupported index mode %s", normalized, fields[0])
	}
}

func validGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
