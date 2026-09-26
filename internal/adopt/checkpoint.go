package adopt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

const (
	checkpointScript = ".config/lefthook/scripts/checkpoint.py"
	checkpointCommon = ".config/lefthook/scripts/common.py"
	checkpointPolicy = ".config/agent/checkpoint.json"
)

var checkpointBranchPrefixes = []string{"checkpoint/", "audit/", "fix/", "feat/", "chore/", "docs/", "refactor/", "test/", "ci/"}

type checkpointSource struct {
	path string
	data []byte
}

// reconcileCheckpointBundle installs the exact shared evaluator only when the
// explicitly selected source root contains both required files. Existing files
// remain authoritative unless --force is explicitly selected. With vendored set, the
// repository's lefthook.yml extends the canonical policy, whose scripts are vendored from
// one reviewed Praetor commit with it: existing scripts stay authoritative even under
// --force, so policy and scripts never mix versions (BUG-858), and only missing ones are
// installed.
func reconcileCheckpointBundle(ctx context.Context, s *adoptSession, vendored bool) (bool, error) {
	if s.opts.LockSourceRoot == "" {
		return false, errors.New("no explicit verified checkpoint source root")
	}
	sources, err := readCheckpointSources(ctx, s.opts.LockSourceRoot)
	if err != nil {
		return false, err
	}
	for _, source := range sources {
		if err := installCheckpointSource(s, source, vendored); err != nil {
			return false, err
		}
	}
	policy, err := checkpointPolicyJSON(ctx, s)
	if err != nil {
		return false, err
	}
	_, err = s.scaffoldFile(scaffold{
		rel: checkpointPolicy, perm: filePerm, content: policy, force: false,
		created:  "Created local-only checkpoint policy for adopted repository",
		verified: "Existing checkpoint policy preserved",
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

func installCheckpointSource(s *adoptSession, source checkpointSource, vendored bool) error {
	written, err := s.scaffoldFile(scaffold{
		rel: source.path, perm: filePerm, content: source.data, force: !vendored,
		created:  "Installed shared checkpoint evaluator from verified source bundle",
		verified: "Existing shared checkpoint evaluator preserved",
	})
	if err != nil || written || s.opts.DryRun || vendored {
		return err
	}
	full, err := repoFile(s.repoPath, source.path)
	if err != nil {
		return err
	}
	actual, err := readRepoFile(full)
	if err != nil {
		return err
	}
	if string(actual) != string(source.data) {
		return fmt.Errorf("existing %s differs from the verified checkpoint evaluator", source.path)
	}
	return nil
}

func readCheckpointSources(ctx context.Context, root string) (result []checkpointSource, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	directory, err := contextopt.OpenDirectory(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("checkpoint source root: %w", err)
	}
	defer func() { err = errors.Join(err, directory.Close()) }()
	if err := validateCheckpointSourceRoot(root); err != nil {
		return nil, err
	}
	result = make([]checkpointSource, 0, 2)
	for _, name := range []string{checkpointScript, checkpointCommon} {
		source, readErr := readCheckpointSource(ctx, root, name)
		if readErr != nil {
			return nil, readErr
		}
		result = append(result, source)
	}
	return result, nil
}

func validateCheckpointSourceRoot(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("checkpoint source root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("checkpoint source root must be a directory")
	}
	return nil
}

func readCheckpointSource(ctx context.Context, root, name string) (checkpointSource, error) {
	if err := ctx.Err(); err != nil {
		return checkpointSource{}, err
	}
	path := filepath.Join(root, filepath.FromSlash(name))
	info, err := os.Lstat(path)
	if err != nil {
		return checkpointSource{}, fmt.Errorf("checkpoint source %s: %w", name, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return checkpointSource{}, fmt.Errorf("checkpoint source %s must be a regular file", name)
	}
	data, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return checkpointSource{}, fmt.Errorf("read checkpoint source %s: %w", name, err)
	}
	return checkpointSource{path: name, data: data}, nil
}

func checkpointPolicyJSON(ctx context.Context, s *adoptSession) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name := s.repoName
	owner := defaultOwner
	if filepath.Base(name) == name {
		owner = resolveOwner(ctx, s.repoPath)
		name = filepath.Base(s.repoPath)
	}
	policy := struct {
		Version    int      `json:"version"`
		Enabled    bool     `json:"enabled"`
		Minutes    int      `json:"commit_after_minutes"`
		Files      int      `json:"commit_after_files"`
		OnStop     bool     `json:"on_stop"`
		Publish    bool     `json:"publish"`
		Remote     string   `json:"remote"`
		Base       string   `json:"base"`
		Repository string   `json:"repository"`
		Prefixes   []string `json:"branch_prefixes"`
		RequirePR  bool     `json:"require_pr"`
	}{1, true, 30, 20, true, false, "origin", "main", owner + "/" + name, checkpointBranchPrefixes, false}
	return json.MarshalIndent(policy, "", "  ")
}

func checkpointFilesPresent(root string) bool {
	for _, name := range []string{checkpointScript, checkpointCommon, checkpointPolicy} {
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}
