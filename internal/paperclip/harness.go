package paperclip

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

// Harness represents the Paperclip agent runtime configuration.
type Harness struct {
	Version           int      `json:"version"`
	Platform          string   `json:"platform"`
	OperatingContract []string `json:"operating_contract"`
	AGitPushFormat    string   `json:"agit_push_format"`
	Invariants        []string `json:"invariants"`
}

// SynthesizeHarness generates a Paperclip agent harness embedding fleet contracts.
func SynthesizeHarness(repoPath string) (*Harness, error) {
	contract := []string{
		"Pushing a branch is NOT shipping: an open PR is required, but still not shipped work until merged.",
		"Rebase onto main immediately: run git fetch origin && git rebase origin/main before proposing.",
		"Rule 0 Terminal Disposition: every run must end with a structured disposition (in_review or blocked).",
		"Ed25519 Exit-0 Receipts: attach cryptographic execution receipts to all PR proposals.",
		"Timeout Resilience: timeout is not failure; re-check open PRs before retrying to prevent duplicate PRs.",
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

	platform := resolvePlatform(repoPath)

	return &Harness{
		Version:           1,
		Platform:          platform,
		OperatingContract: contract,
		AGitPushFormat:    "git push origin HEAD:refs/for/main -o topic=<issue-id>",
		Invariants:        invariants,
	}, nil
}

func resolvePlatform(repoPath string) string {
	manifestPath := filepath.Join(repoPath, ".standards.yaml")
	if data, err := os.ReadFile(manifestPath); err == nil {
		var m struct {
			Repository struct {
				Owner string `yaml:"owner"`
				Name  string `yaml:"name"`
			} `yaml:"repository"`
		}
		if err := yaml.Unmarshal(data, &m); err == nil {
			if m.Repository.Owner != "" && m.Repository.Name != "" {
				return fmt.Sprintf("%s/%s", m.Repository.Owner, m.Repository.Name)
			}
		}
	}

	cmd := exec.Command("git", "-C", repoPath, "config", "--get", "remote.origin.url")
	if out, err := cmd.Output(); err == nil {
		url := strings.TrimSpace(string(out))
		if owner, repo := util.ExtractOwnerAndRepo(url); owner != "" && repo != "" {
			return fmt.Sprintf("%s/%s", owner, repo)
		}
	}

	base := filepath.Base(repoPath)
	parent := filepath.Base(filepath.Dir(repoPath))
	if parent != "" && parent != "." && parent != "/" && parent != "dev" {
		return fmt.Sprintf("%s/%s", parent, base)
	}
	return fmt.Sprintf("cordanaLLM/%s", base)
}

// WriteHarness writes .paperclip/harness.json and .paperclip/rules.md into repoPath.
func WriteHarness(h *Harness, repoPath string) error {
	paperclipDir := filepath.Join(repoPath, ".paperclip")
	if err := os.MkdirAll(paperclipDir, 0755); err != nil {
		return fmt.Errorf("create .paperclip dir: %w", err)
	}

	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal harness: %w", err)
	}

	jsonPath := filepath.Join(paperclipDir, "harness.json")
	if err := os.WriteFile(jsonPath, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("write %s: %w", jsonPath, err)
	}

	mdContent := fmt.Sprintf("# Paperclip Operating Rules (%s)\n\n## Operating Contract\n", h.Platform)
	for _, c := range h.OperatingContract {
		mdContent += fmt.Sprintf("- %s\n", c)
	}
	mdContent += fmt.Sprintf("\n## AGit Push Protocol\n```bash\n%s\n```\n\n## High-Integrity Invariants\n", h.AGitPushFormat)
	for _, inv := range h.Invariants {
		mdContent += fmt.Sprintf("- %s\n", inv)
	}

	mdPath := filepath.Join(paperclipDir, "rules.md")
	if err := os.WriteFile(mdPath, []byte(mdContent), 0644); err != nil {
		return fmt.Errorf("write %s: %w", mdPath, err)
	}

	return nil
}

// LoadHarness reads and validates a Paperclip harness configuration.
func LoadHarness(path string) (*Harness, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read harness file: %w", err)
	}

	var h Harness
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, fmt.Errorf("parse harness json: %w", err)
	}

	if h.Platform == "" || len(h.OperatingContract) == 0 {
		return nil, fmt.Errorf("invalid harness: missing platform or operating contract")
	}

	return &h, nil
}
