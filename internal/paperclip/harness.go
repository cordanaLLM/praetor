package paperclip

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

const (
	manifestFile = ".standards.yaml"
	paperclipDir = ".paperclip"
	harnessFile  = "harness.json"
	rulesFile    = "rules.md"
	// filePerm is the mode of the written harness files.
	filePerm os.FileMode = 0o644
	// dirPerm is the mode of the .paperclip directory.
	dirPerm os.FileMode = 0o755
)

// Harness represents the Paperclip agent runtime configuration.
type Harness struct {
	Version           int      `json:"version"`
	Platform          string   `json:"platform"`
	OperatingContract []string `json:"operating_contract"`
	AGitPushFormat    string   `json:"agit_push_format"`
	Invariants        []string `json:"invariants"`
}

// SynthesizeHarness generates a Paperclip agent harness embedding fleet contracts. The
// repository identity lookup (a git subprocess) runs under the caller's context.
func SynthesizeHarness(ctx context.Context, repoPath string) (*Harness, error) {
	if ctx == nil {
		return nil, fmt.Errorf("paperclip: context cannot be nil")
	}
	contract := []string{
		"Pushing a branch is NOT shipping: an open PR is required, but still not shipped work until merged.",
		"Rebase onto main immediately: run git fetch origin && git rebase origin/main before proposing.",
		"Rule 0 Terminal Disposition: every run must end with a structured disposition (in_review or blocked).",
		"Ed25519 Exit-0 Receipts: attach cryptographic execution receipts to all PR proposals.",
		"Timeout Resilience: timeout is not failure; re-check open PRs before retrying to prevent duplicate PRs.",
		// A Paperclip run reports to an orchestrating agent, so its product is internal text.
		config.RegisterDirective(config.TextRegisterInternal),
	}

	invariants := []string{
		"HISS-01: Acyclic DAG control flow (no recursion)",
		"HISS-02: Scalar upper bounds on all loops; context timeout on all I/O",
		"HISS-04: McCabe Cyclomatic <= 10, Cognitive <= 15, Func LOC <= 75",
		"HISS-07: Zero .unwrap() / .expect(); all errors handled or wrapped",
		"HISS-10: Zero-warning tolerance across compiler, linters, and formatters",
		"HISS-15: 3D testing mandatory (Positive, Negative, Boundary >= 2 checks/dim)",
		"HISS-16: Canonical AGENTS.md compiled to vendor harnesses",
	}

	return &Harness{
		Version:           1,
		Platform:          resolvePlatform(ctx, repoPath),
		OperatingContract: contract,
		AGitPushFormat:    "git push origin HEAD:refs/for/main -o topic=<issue-id>",
		Invariants:        invariants,
	}, nil
}

// resolvePlatform derives owner/name from the manifest, then the git identity, then
// the directory basename.
func resolvePlatform(ctx context.Context, repoPath string) string {
	if platform, ok := manifestPlatform(repoPath); ok {
		return platform
	}
	owner, repo, err := util.ResolveRepoIdentity(ctx, repoPath)
	if err == nil && owner != "" && repo != "" {
		return fmt.Sprintf("%s/%s", owner, repo)
	}
	return fmt.Sprintf("cordanaLLM/%s", filepath.Base(repoPath))
}

// manifestPlatform reads owner/name from .standards.yaml when present and complete.
func manifestPlatform(repoPath string) (string, bool) {
	manifestPath, err := util.ConfinePath(repoPath, manifestFile)
	if err != nil {
		return "", false
	}
	// #nosec G304 -- manifestPath is confined to repoPath by ConfinePath.
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", false
	}
	var m struct {
		Repository struct {
			Owner string `yaml:"owner"`
			Name  string `yaml:"name"`
		} `yaml:"repository"`
	}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return "", false
	}
	if m.Repository.Owner == "" || m.Repository.Name == "" {
		return "", false
	}
	return fmt.Sprintf("%s/%s", m.Repository.Owner, m.Repository.Name), true
}

// WriteHarness writes .paperclip/harness.json and .paperclip/rules.md into repoPath.
// Both targets are confined to repoPath so a symlinked .paperclip cannot redirect them.
func WriteHarness(h *Harness, repoPath string) error {
	if h == nil {
		return fmt.Errorf("paperclip: harness cannot be nil")
	}
	dir, err := util.ConfinePath(repoPath, paperclipDir)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", paperclipDir, err)
	}
	if err := util.MkdirSecure(dir, dirPerm); err != nil {
		return fmt.Errorf("create %s dir: %w", paperclipDir, err)
	}

	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal harness: %w", err)
	}
	jsonPath, err := util.ConfinePath(repoPath, filepath.Join(paperclipDir, harnessFile))
	if err != nil {
		return fmt.Errorf("resolve %s: %w", harnessFile, err)
	}
	if err := util.WriteFileSecure(jsonPath, append(data, '\n'), filePerm); err != nil {
		return fmt.Errorf("write %s: %w", jsonPath, err)
	}

	mdPath, err := util.ConfinePath(repoPath, filepath.Join(paperclipDir, rulesFile))
	if err != nil {
		return fmt.Errorf("resolve %s: %w", rulesFile, err)
	}
	if err := util.WriteFileSecure(mdPath, []byte(renderRules(h)), filePerm); err != nil {
		return fmt.Errorf("write %s: %w", mdPath, err)
	}
	return nil
}

// renderRules renders the human-readable operating rules of a harness.
func renderRules(h *Harness) string {
	md := fmt.Sprintf("# Paperclip Operating Rules (%s)\n\n## Operating Contract\n", h.Platform)
	for _, c := range h.OperatingContract {
		md += fmt.Sprintf("- %s\n", c)
	}
	md += fmt.Sprintf("\n## AGit Push Protocol\n```bash\n%s\n```\n\n## High-Integrity Invariants\n", h.AGitPushFormat)
	for _, inv := range h.Invariants {
		md += fmt.Sprintf("- %s\n", inv)
	}
	return md
}

// LoadHarness reads and validates a Paperclip harness configuration.
func LoadHarness(path string) (*Harness, error) {
	ctx, cancel := context.WithTimeout(context.Background(), contextopt.MaxDuration)
	defer cancel()
	return LoadHarnessContext(ctx, path)
}

// LoadHarnessContext validates bounded configuration without following symlinks.
func LoadHarnessContext(ctx context.Context, path string) (*Harness, error) {
	data, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read harness file: %w", err)
	}

	var h Harness
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, fmt.Errorf("parse harness json: %w", err)
	}

	if h.Version != 1 {
		return nil, fmt.Errorf("invalid harness: expected version 1")
	}
	if err := validateHarnessValues([]string{h.Platform, h.AGitPushFormat}); err != nil {
		return nil, fmt.Errorf("invalid harness identity or push format: %w", err)
	}
	for _, values := range [][]string{h.OperatingContract, h.Invariants} {
		if err := validateHarnessValues(values); err != nil {
			return nil, fmt.Errorf("invalid harness contract or invariants: %w", err)
		}
	}

	return &h, nil
}

func validateHarnessValues(values []string) error {
	if len(values) == 0 || len(values) > 64 {
		return fmt.Errorf("expected 1..64 values")
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" || len(value) > 4096 {
			return fmt.Errorf("values must be nonempty and at most 4096 bytes")
		}
	}
	return nil
}
