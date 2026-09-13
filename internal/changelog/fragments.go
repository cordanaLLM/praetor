package changelog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"gopkg.in/yaml.v3"
)

const maxFragmentEntries = 10000

type fragmentSnapshot struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

func loadFragmentsContext(ctx context.Context, repoPath string) (fragments []Fragment, files []string, err error) {
	root, absent, err := openFragments(ctx, repoPath)
	if err != nil || absent {
		return nil, nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	fragments, snapshots, err := loadFragmentSnapshots(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	for _, snapshot := range snapshots {
		files = append(files, filepath.Join(repoPath, "changelog.d", snapshot.Name))
	}
	return fragments, files, nil
}

func openFragments(ctx context.Context, repoPath string) (*os.Root, bool, error) {
	if ctx == nil {
		return nil, false, errors.New("changelog fragments require a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	dir := filepath.Join(repoPath, "changelog.d")
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return nil, true, nil
	} else if err != nil {
		return nil, false, err
	}
	root, err := contextopt.OpenDirectory(ctx, dir)
	return root, false, err
}

func fragmentEntries(root *os.Root) ([]os.DirEntry, error) {
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	entries, readErr := dir.ReadDir(maxFragmentEntries + 1)
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	if err := errors.Join(readErr, dir.Close()); err != nil {
		return nil, err
	}
	if len(entries) > maxFragmentEntries {
		return nil, fmt.Errorf("changelog fragment inventory exceeds %d entries", maxFragmentEntries)
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, nil
}

func loadFragmentSnapshots(ctx context.Context, root *os.Root) ([]Fragment, []fragmentSnapshot, error) {
	entries, err := fragmentEntries(root)
	if err != nil {
		return nil, nil, err
	}
	var fragments []Fragment
	var snapshots []fragmentSnapshot
	totalBytes := 0
	for _, entry := range entries {
		if entry.IsDir() || !fragmentFilename(entry.Name()) {
			continue
		}
		raw, err := contextopt.ReadRootSnapshot(ctx, root, entry.Name())
		if err != nil {
			return nil, nil, err
		}
		totalBytes += len(raw)
		if totalBytes > contextopt.MaxSourceBytes {
			return nil, nil, errors.New("aggregate changelog fragments exceed 1 MiB")
		}
		fragment, err := decodeFragment(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("parse changelog fragment %s: %w", entry.Name(), err)
		}
		fragments = append(fragments, fragment)
		snapshots = append(snapshots, fragmentSnapshot{Name: entry.Name(), SHA256: contentHash(raw)})
	}
	return fragments, snapshots, nil
}

func decodeFragment(raw []byte) (Fragment, error) {
	var fragment Fragment
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fragment); err != nil {
		return Fragment{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Fragment{}, errors.Join(errors.New("fragment must contain exactly one YAML document"), err)
	}
	fragment.Type = FragmentType(strings.ToLower(string(fragment.Type)))
	if _, ok := sectionTitles[fragment.Type]; !ok || strings.TrimSpace(fragment.Title) == "" {
		return Fragment{}, errors.New("fragment requires a known type and nonempty title")
	}
	return fragment, nil
}

func fragmentFilename(name string) bool {
	return filepath.IsLocal(name) && filepath.Base(name) == name && (strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml"))
}
