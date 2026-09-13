package dogfood

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

func prepareDiscovery(ctx context.Context, opts *DiscoveryOptions) (DiscoveryPolicy, string, []DiscoveryCase, string, error) {
	var empty DiscoveryPolicy
	if err := ctx.Err(); err != nil {
		return empty, "", nil, "", err
	}
	if err := validateDiscoveryOptions(opts); err != nil {
		return empty, "", nil, "", err
	}
	policy, policySum, err := loadDiscoveryPolicy(ctx, opts.PolicyPath)
	if err != nil {
		return empty, "", nil, "", err
	}
	if opts.Path != "" {
		opts.Path, err = filepath.Abs(opts.Path)
		if err != nil {
			return empty, "", nil, "", err
		}
		if err := validateSuitePath(opts.Path); err != nil {
			return empty, "", nil, "", err
		}
		return policy, policySum, []DiscoveryCase{{ID: "case-001", Repository: "local:" + opts.Path, Status: "planned"}}, "", nil
	}
	cases, configSum, err := loadDiscoveryCohort(ctx, opts.ConfigPath)
	return policy, policySum, cases, configSum, err
}

func validateDiscoveryOptions(opts *DiscoveryOptions) error {
	if opts.Stage != "plan" && opts.Stage != "observe" {
		return errors.New("discovery stage must be plan or observe")
	}
	if (opts.ConfigPath == "") == (opts.Path == "") {
		return errors.New("discovery requires exactly one of config_path or path")
	}
	if opts.PolicyPath == "" || opts.ArtifactDir == "" {
		return errors.New("discovery requires policy_path and artifact_dir")
	}
	return validateDiscoveryExecution(opts)
}

func validateDiscoveryExecution(opts *DiscoveryOptions) error {
	if opts.Concurrency == 0 {
		opts.Concurrency = MaxDiscoveryWorkers
	}
	if opts.Concurrency < 1 || opts.Concurrency > MaxDiscoveryWorkers {
		return errors.New("discovery concurrency must be 1..4")
	}
	if opts.Stage == "observe" && opts.ConfigPath != "" && !opts.AllowRemote {
		return errors.New("public discovery requires server remote opt-in")
	}
	return nil
}

func loadDiscoveryCohort(ctx context.Context, path string) ([]DiscoveryCase, string, error) {
	data, err := readSuiteConfig(ctx, path)
	if err != nil {
		return nil, "", err
	}
	_, err = suiteObject(data, []string{"version", "public_repositories"})
	if err != nil {
		return nil, "", err
	}
	var config struct {
		Version            int      `json:"version"`
		PublicRepositories []string `json:"public_repositories"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, "", err
	}
	if config.Version != 1 || len(config.PublicRepositories) < 1 || len(config.PublicRepositories) > MaxDiscoveryRepositories {
		return nil, "", errors.New("discovery cohort requires version 1 and 1..128 repositories")
	}
	cases, err := discoveryCohortCases(config.PublicRepositories)
	return cases, discoveryDigest(data), err
}

func discoveryCohortCases(repositories []string) ([]DiscoveryCase, error) {
	cases := make([]DiscoveryCase, 0, len(repositories))
	seen := make(map[string]bool)
	for i := 0; i < len(repositories) && i < MaxDiscoveryRepositories; i++ {
		source, err := parsePublicSource(repositories[i])
		if err != nil {
			return nil, err
		}
		if len(source.sha) != 40 || seen[strings.ToLower(source.url)] {
			return nil, errors.New("discovery requires distinct GitHub repositories with immutable 40-hex commit pins")
		}
		seen[strings.ToLower(source.url)] = true
		cases = append(cases, DiscoveryCase{ID: fmt.Sprintf("case-%03d", i+1), Repository: source.url, RequestedSHA: source.sha, Status: "planned"})
	}
	return cases, nil
}

func loadDiscoveryPolicy(ctx context.Context, path string) (DiscoveryPolicy, string, error) {
	var policy DiscoveryPolicy
	data, err := readSuiteConfig(ctx, path)
	if err != nil {
		return policy, "", err
	}
	fields, err := suiteObject(data, []string{"version", "rules"}, "input_limits")
	if err != nil {
		return policy, "", err
	}
	var rules []json.RawMessage
	if err := json.Unmarshal(fields["rules"], &rules); err != nil {
		return policy, "", err
	}
	if len(rules) < 1 || len(rules) > 128 {
		return policy, "", errors.New("discovery policy requires 1..128 rules")
	}
	for i := 0; i < len(rules) && i < 128; i++ {
		if _, err := suiteObject(rules[i], []string{"key", "title", "kind", "matches", "analyzer"}); err != nil {
			return policy, "", err
		}
	}
	if err := json.Unmarshal(data, &policy); err != nil {
		return policy, "", err
	}
	policy.InputLimits, err = decodeInputLimits(fields["input_limits"])
	if err != nil {
		return policy, "", err
	}
	return policy, discoveryDigest(data), ValidateDiscoveryPolicy(policy)
}

func discoveryDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
