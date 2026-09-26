package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/forge"
	"gopkg.in/yaml.v3"
)

const maxSyncLabels = 1000

func validateSyncLabels(data []byte) (int, error) {
	var taxonomy struct {
		Version int           `yaml:"version"`
		Labels  []forge.Label `yaml:"labels"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&taxonomy); err != nil {
		return 0, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return 0, errors.New("label taxonomy requires exactly one document")
	}
	if taxonomy.Version != 1 || len(taxonomy.Labels) == 0 || len(taxonomy.Labels) > maxSyncLabels {
		return 0, fmt.Errorf("label taxonomy requires version 1 and 1-%d labels", maxSyncLabels)
	}
	if err := validateSyncLabelEntries(taxonomy.Labels); err != nil {
		return 0, err
	}
	return len(taxonomy.Labels), nil
}

func validateSyncLabelEntries(labels []forge.Label) error {
	seen := make(map[string]bool, len(labels))
	for i := 0; i < len(labels) && i < maxSyncLabels; i++ {
		label := labels[i]
		name := strings.TrimSpace(label.Name)
		if name == "" || name != label.Name || seen[name] {
			return errors.New("label names must be nonempty, trimmed and unique")
		}
		if color, err := hex.DecodeString(label.Color); err != nil || len(color) != 3 {
			return errors.New("label colors must contain exactly six hexadecimal digits")
		}
		seen[name] = true
	}
	return nil
}

// Parse through YAML after requiring JSON syntax: yaml.v3 rejects duplicate keys,
// including nested JSON objects. Comparing parsed documents ignores whitespace and
// object key order while preserving every declared rule, parameter and condition.
func validateSyncRuleset(data []byte, policy config.BranchProtectionPolicy, contexts []string) error {
	if !json.Valid(data) {
		return errors.New("ruleset must be a single valid JSON document")
	}
	var observed map[string]any
	if err := yaml.Unmarshal(data, &observed); err != nil {
		return err
	}
	expectedBytes, err := forge.RenderRepositoryRuleset(policy, contexts)
	if err != nil {
		return err
	}
	var expected map[string]any
	if err := yaml.Unmarshal(expectedBytes, &expected); err != nil {
		return err
	}
	if !reflect.DeepEqual(observed, expected) {
		return errors.New("ruleset differs from declared branch protection policy; review the existing file before reconciliation")
	}
	return nil
}

// verifySyncCompanions returns how many companion checks are missing or unverified. A
// verify function returns a non-empty reason for a valid file it could not verify.
func verifySyncCompanions(ctx context.Context, root, catalogRoot string, manifest *config.Manifest) (int, error) {
	checks := []struct {
		name   string
		label  string
		verify func() (string, error)
	}{
		{".standards.lock", "Lockfile", func() (string, error) {
			return verifySyncLockfile(ctx, root, catalogRoot, manifest)
		}},
		{"AGENTS.md", "Context harness and six compiled projections", func() (string, error) {
			return "", compiler.NewTranspiler().VerifyContext(ctx, filepath.Join(root, "AGENTS.md"), root)
		}},
	}
	incomplete := 0
	for _, check := range checks {
		_, exists, err := contextopt.ObserveSnapshot(ctx, filepath.Join(root, check.name))
		if err != nil {
			return incomplete, fmt.Errorf("%s observation failed: %w", check.name, err)
		}
		if !exists {
			fmt.Printf("  [MISSING] %s %s missing; verification incomplete.\n", check.label, check.name)
			incomplete++
			continue
		}
		reason, err := check.verify()
		if err != nil {
			return incomplete, fmt.Errorf("%s validation failed: %w", check.name, err)
		}
		if reason != "" {
			fmt.Printf("  [UNVERIFIED] %s %s: %s; verification incomplete.\n", check.label, check.name, reason)
			incomplete++
			continue
		}
		fmt.Printf("  [OK] %s %s verified\n", check.label, check.name)
	}
	return incomplete, nil
}

// verifySyncLockfile reports a well-formed lock whose content digests cannot be hashed
// as unverified instead of verified.
func verifySyncLockfile(ctx context.Context, root, catalogRoot string, manifest *config.Manifest) (string, error) {
	result, err := config.ValidateLockfileWithOptions(ctx, config.LockValidationOptions{Root: root, CatalogRoot: catalogRoot}, manifest)
	if err != nil || result.Verified() {
		return "", err
	}
	return fmt.Sprintf("pins and aggregate digest are valid, but %v; materialize it or pass --catalog-root", config.ErrLockUnverifiable), nil
}
