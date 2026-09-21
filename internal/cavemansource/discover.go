package cavemansource

import (
	"context"
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

const trackedProbeMaxBytes = 1 << 20

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
	var found []discoveredInput
	err = filepath.WalkDir(abs, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type().IsRegular() && matchesFormat(path, input.Format) {
			rel, relErr := filepath.Rel(rootAbs, path)
			if relErr != nil {
				return relErr
			}
			found = append(found, discoveredInput{input: input, path: filepath.ToSlash(rel)})
		}
		if len(found) > contextopt.MaxSources {
			return fmt.Errorf("source directory exceeds %d matching files", contextopt.MaxSources)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("directory %s contains zero %s source files", input.Path, input.Format)
	}
	return found, nil
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
	args := []string{"--literal-pathspecs", "ls-files", "-z", "--"}
	args = append(args, files...)
	result, err := util.RunGitProbe(ctx, root, trackedProbeMaxBytes, args...)
	if err != nil {
		return fmt.Errorf("list tracked caveman sources: %w", err)
	}
	tracked := make(map[string]bool, len(files))
	for _, file := range strings.Split(string(result.Stdout), "\x00") {
		tracked[filepath.ToSlash(file)] = file != ""
	}
	for _, file := range files {
		if !tracked[file] {
			return fmt.Errorf("caveman source %s is not tracked", file)
		}
	}
	return nil
}
