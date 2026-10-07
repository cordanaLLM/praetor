package adopt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	checkpointScript = ".config/lefthook/scripts/checkpoint.py"
	checkpointCommon = ".config/lefthook/scripts/common.py"
	// checkpointLauncher starts the interpreter of the checkpoint jobs. It is the canonical
	// policy's launcher: it tries a fixed list of candidates and runs the first that answers a
	// version probe as Python 3 (#339). The generated lefthook.yml calls it by this path
	// (lefthookPythonCommand).
	checkpointLauncher = ".config/lefthook/python.sh"
	checkpointPolicy   = ".config/agent/checkpoint.json"
)

// checkpointBundle lists the files adoption copies from the verified source root, in the
// order it installs them.
var checkpointBundle = []string{checkpointScript, checkpointCommon, checkpointLauncher}

var checkpointBranchPrefixes = []string{"checkpoint/", "audit/", "fix/", "feat/", "chore/", "docs/", "refactor/", "test/", "ci/"}

type checkpointSource struct {
	path string
	data []byte
}

// priorCheckpointDigests are the digests (priorRendering) of every text a Praetor release
// shipped at each checkpoint bundle path, keyed by path and then to the release that shipped
// it; the current texts are among them. Audit does not read the bundle, so --force keeps an
// edited script: these texts are what adoption refreshes to the verified source bundle without
// --force (#239). testdata/checkpoint reproduces each digest, and
// TestPriorCheckpointDigests_Boundary_CurrentSourcesRecorded fails until a changed script is
// recorded here (checkpoint_prior_test.go).
var priorCheckpointDigests = map[string]map[string]string{
	checkpointScript: {
		"ca53c59946b809a991a8c0d3d8e7ce164786fb4282e48865d47b5b7a7bbd4338": "platform promotion (#14)",
		"df7029ac02521544a9d32b4e75105512cdc047e28746d476b98f291e87c71a34": "Windows suite (#149)",
		"57aa9d7a711627bf64b74d23d12e8725e0980bb96d140a289c8ee376b77f9498": "register-sourced hook text (#487)",
		"8ca7e7926028b518902006ced9f59cce952eee5e056cd690c39a43455c2d0278": "neutrality sweep (#509)",
		"00bb0ecb2e3c257d23cb2719c7c5513461e0386c3e9795d259f273283bc6f8cd": "re-run supersedes its earlier run (#786)",
		"2c65c03d940b25ccfa90041d90aabb57ed400c5253158c798163bddfc32e523c": "draft gate refusal reads as draft pending (#815)",
	},
	checkpointCommon: {
		"61ccf1481d43073ca29aab6c6e6b78cc6f5cb4a86bf1e7603b8b0e01d02f71a9": "platform promotion (#14)",
		"28ba4c3481f26f90eb48b371d4a757e16e965280ad1352dfad8c412562d19dbd": "Windows gate (#102)",
		"d4cf017e548ad619f75869f8cda22b3c03d2e5da02f176dec685df99dc5e0a4a": "Windows suite (#149)",
		"e3eb30a9b9a0b276d8e55214bbdb560c981d232e3c0d55506d68ba1996097f7a": "devcontainer LF pin (#285)",
		"c0c49adebaa1e0cb98b187b2e55520369d9c363674d73e88825f3c0e0606a567": "cross-platform matrix (#346)",
		"3327c281a4ddd8d2bafae75ad83a0d84b07fcdf6219ece3ff6e0928d86e9afb0": "hook Git config isolation (#427)",
		"10bd958f9e08481a39e593c2fa662e14badf033b6ed09a3cd35ba36c97fd2136": "split command output (#436)",
		"2464805e827368e22b3299f8296c3a6c06f100576e0ec248281f3ac607e3a9ed": "hook client roots (#461)",
		"a414804b60c86153208c0265b824a759e01c06b43ab4810d57ffd20c5d5b30aa": "Platform Neutrality gaps (#469)",
		"c1572868510a383605b92cd80fad267c1a0b3d14e2bfd3dadd4d29db1aa5907a": "neutrality sweep (#509)",
	},
	checkpointLauncher: {
		"8b6266ea16912a362c2e26d9a936887ca955ecf68f69744c935f2259d1a2c8dc": "proven interpreter candidates (#339)",
	},
}

// reconcileCheckpointBundle installs the exact shared evaluator only when the explicitly
// selected source root contains every file of the bundle (checkpointBundle). An existing file that differs from the
// source bundle is kept, --force included, and leaves the lifecycle unavailable; one holding an
// earlier Praetor text of it is refreshed (installCheckpointSources). With vendored set, the
// repository's lefthook.yml extends the canonical policy, whose scripts are vendored from one
// reviewed Praetor commit with it: existing scripts stay as they are, never refreshed, so
// policy and scripts never mix versions (BUG-858), and only missing ones are installed.
func reconcileCheckpointBundle(ctx context.Context, s *adoptSession, vendored bool) (bool, error) {
	if s.opts.LockSourceRoot == "" {
		return false, errors.New("no explicit verified checkpoint source root")
	}
	sources, err := readCheckpointSources(ctx, s.opts.LockSourceRoot)
	if err != nil {
		return false, err
	}
	// The policy names the repository it publishes to, so it is resolved before anything is
	// installed: an unresolved identity installs no half of the lifecycle.
	policy, err := checkpointPolicyJSON(ctx, s)
	if err != nil {
		return false, err
	}
	if err := installCheckpointSources(ctx, s, sources, vendored); err != nil {
		return false, err
	}
	_, err = s.scaffoldFile(ctx, scaffold{
		rel: checkpointPolicy, perm: filePerm, content: policy,
		created:  "Created local-only checkpoint policy for adopted repository",
		verified: "Existing checkpoint policy preserved",
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// installCheckpointSources installs every evaluator file. Without vendored the bundle is one
// unit: a file adoption keeps (keptCheckpointSource) is found before any file is written,
// reported, and leaves the lifecycle unavailable, so no file of the bundle is installed or
// refreshed beside one that stays. With vendored set, each file goes on its own.
func installCheckpointSources(ctx context.Context, s *adoptSession, sources []checkpointSource, vendored bool) error {
	if !vendored {
		kept, err := keptCheckpointSource(ctx, s, sources)
		if err != nil {
			return err
		}
		if kept >= 0 {
			return installCheckpointSource(ctx, s, sources[kept], false)
		}
	}
	for i := 0; i < len(sources) && i < maxCheckpointSources; i++ {
		if err := installCheckpointSource(ctx, s, sources[i], vendored); err != nil {
			return err
		}
	}
	return nil
}

// maxCheckpointSources bounds a walk over the checkpoint bundle's files (HISS-02).
const maxCheckpointSources = 8

// keptCheckpointSource returns the index of the first bundle file adoption keeps, or -1 when
// there is none: a file that exists and holds neither its verified source nor an earlier Praetor
// text of it (priorCheckpointDigests), line endings aside, or one that cannot be read to tell.
func keptCheckpointSource(ctx context.Context, s *adoptSession, sources []checkpointSource) (int, error) {
	for i := 0; i < len(sources) && i < maxCheckpointSources; i++ {
		full, err := repoFile(s.repoPath, sources[i].path)
		if err != nil {
			return 0, err
		}
		actual, exists, readErr := contextopt.ObserveSnapshot(ctx, full)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, ctxErr
		}
		if keptCheckpointText(actual, exists, readErr, sources[i]) {
			return i, nil
		}
	}
	return -1, nil
}

// keptCheckpointText reports whether adoption keeps the bundle file it observed as actual: one
// that exists and is neither source's verified text nor an earlier Praetor text of source's
// path, in one consistent line-ending style, or one it could not read (readErr), which
// installCheckpointSource then reports as unverified.
func keptCheckpointText(actual []byte, exists bool, readErr error, source checkpointSource) bool {
	if readErr != nil {
		return true
	}
	if !exists {
		return false
	}
	if current, err := util.CanonicalTextEquivalent(actual, source.data); err == nil && current {
		return false
	}
	return !isPriorRendering(actual, priorCheckpointDigests[source.path])
}

// installCheckpointSource installs one evaluator file. An existing copy that holds an earlier
// Praetor text of it is refreshed to the verified bundle; one that differs otherwise, or cannot
// be compared with it, is kept, --force included, and leaves the lifecycle unavailable. With
// vendored set, an existing copy belongs to the vendored canonical bundle and stays as it is.
func installCheckpointSource(ctx context.Context, s *adoptSession, source checkpointSource, vendored bool) error {
	sc := scaffold{
		rel: source.path, perm: filePerm, content: source.data,
		created:   "Installed shared checkpoint evaluator from verified source bundle",
		verified:  "Existing shared checkpoint evaluator preserved",
		refreshed: "Refreshed an unedited earlier Praetor checkpoint evaluator to the verified source bundle",
	}
	if !vendored {
		sc.prior = priorCheckpointDigests[source.path]
	}
	state, err := s.scaffoldFile(ctx, sc)
	if err != nil || vendored {
		return err
	}
	switch state {
	case scaffoldDrifted:
		return fmt.Errorf("existing %s differs from the verified checkpoint evaluator", source.path)
	case scaffoldUnverified:
		return fmt.Errorf("existing %s could not be compared with the verified checkpoint evaluator", source.path)
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
	result = make([]checkpointSource, 0, len(checkpointBundle))
	for _, name := range checkpointBundle {
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

// errCheckpointIdentity refuses a checkpoint policy for a repository whose identity adoption
// could not resolve: the evaluator validates the named repository against the origin remote,
// and a guessed name fails that check on every checkpoint.
var errCheckpointIdentity = errors.New("repository identity unresolved; the checkpoint policy must name the repository it publishes to")

func checkpointPolicyJSON(ctx context.Context, s *adoptSession) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !s.identity.resolved() {
		return nil, errCheckpointIdentity
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
	}{1, true, 30, 20, true, false, "origin", "main", s.identity.coordinate(), checkpointBranchPrefixes, false}
	return json.MarshalIndent(policy, "", "  ")
}

func checkpointFilesPresent(root string) bool {
	for _, name := range slices.Concat(checkpointBundle, []string{checkpointPolicy}) {
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}
