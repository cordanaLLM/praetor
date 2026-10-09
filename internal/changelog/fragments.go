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
	fragments, snapshots, err := loadFragmentSnapshots(ctx, repoPath, root)
	if err != nil {
		return nil, nil, err
	}
	for _, snapshot := range snapshots {
		files = append(files, filepath.Join(repoPath, FragmentDir, snapshot.Name))
	}
	return fragments, files, nil
}

func openFragments(ctx context.Context, repoPath string) (*os.Root, bool, error) {
	if _, absent, err := statFragmentDir(ctx, repoPath); err != nil || absent {
		return nil, absent, err
	}
	root, err := contextopt.OpenDirectory(ctx, filepath.Join(repoPath, FragmentDir))
	return root, false, err
}

// FragmentDirPresent reports whether repoPath keeps a changelog fragment directory: FragmentDir
// exists there as a directory and holds at least one regular file, a fragment or a placeholder
// such as FragmentPlaceholder. An empty directory does not count: git keeps no empty directory,
// so a fresh clone of the same commit has none, and the answer must be the same in both trees.
// A file or a symbolic link of that name is no fragment directory, since the fragment reader
// refuses to open it (openFragments). The text register renders the changelog fragment
// convention only for a repository that keeps one (config.FragmentConvention).
func FragmentDirPresent(ctx context.Context, repoPath string) (present bool, err error) {
	info, absent, err := statFragmentDir(ctx, repoPath)
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", FragmentDir, err)
	}
	if absent || !info.IsDir() {
		return false, nil
	}
	root, err := contextopt.OpenDirectory(ctx, filepath.Join(repoPath, FragmentDir))
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", FragmentDir, err)
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	entries, err := fragmentEntries(root)
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", FragmentDir, err)
	}
	return slices.ContainsFunc(entries, func(entry os.DirEntry) bool { return entry.Type().IsRegular() }), nil
}

// statFragmentDir is the one existence check of FragmentDir below repoPath, shared by the
// fragment reader and FragmentDirPresent. absent is true when nothing of that name exists.
func statFragmentDir(ctx context.Context, repoPath string) (info os.FileInfo, absent bool, err error) {
	if ctx == nil {
		return nil, false, errors.New("changelog fragments require a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	info, err = os.Lstat(filepath.Join(repoPath, FragmentDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, true, nil
	}
	return info, false, err
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

func loadFragmentSnapshots(ctx context.Context, repoPath string, root *os.Root) ([]Fragment, []fragmentSnapshot, error) {
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
			fragPath := filepath.Join(repoPath, FragmentDir, entry.Name())
			return nil, nil, fmt.Errorf("parse changelog fragment %s: %w", fragPath, err)
		}
		fragments = append(fragments, fragment)
		snapshots = append(snapshots, fragmentSnapshot{Name: entry.Name(), SHA256: contentHash(raw)})
	}
	return fragments, snapshots, nil
}

// ErrFragmentNotRoundTrippable reports an encoded fragment that this package's own reader
// cannot load back, or that loads back as different content.
var ErrFragmentNotRoundTrippable = errors.New("fragment does not survive its own encoding")

// verifyFragmentRoundTrip decodes freshly encoded fragment bytes and checks that the
// fields that reach the changelog come back unchanged, so an unwritable fragment is
// refused at creation instead of failing the next release.
func verifyFragmentRoundTrip(raw []byte, want Fragment) error {
	got, err := decodeFragment(raw)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrFragmentNotRoundTrippable, err)
	}
	if got.Title != want.Title || got.Issue != want.Issue || got.Breaking != want.Breaking {
		return fmt.Errorf("%w: decoded content differs from the fragment written", ErrFragmentNotRoundTrippable)
	}
	return nil
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
	var rawMap map[string]any
	if err := yaml.Unmarshal(raw, &rawMap); err != nil {
		return Fragment{}, err
	}
	if val, ok := rawMap["issue"]; ok {
		if val == nil {
			return Fragment{}, errors.New("issue cannot be empty")
		}
		strVal, isStr := val.(string)
		if !isStr {
			return Fragment{}, fmt.Errorf("invalid issue %v: must be string", val)
		}
		normIssue, err := normaliseIssue(strVal)
		if err != nil {
			return Fragment{}, err
		}
		fragment.Issue = normIssue
	}
	return fragment, nil
}

func fragmentFilename(name string) bool {
	return filepath.IsLocal(name) && filepath.Base(name) == name && (strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml"))
}
