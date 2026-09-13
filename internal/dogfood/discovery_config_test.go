package dogfood

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDiscoveryCohortBoundsAndPins(t *testing.T) {
	root := t.TempDir()
	makeConfig := func(name string, value any) string {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	valid := func(count int) map[string]any {
		repos := make([]string, count)
		for i := range repos {
			repos[i] = fmt.Sprintf("https://github.com/example/repo%d#%s", i, strings.Repeat("a", 40))
		}
		return map[string]any{"version": 1, "public_repositories": repos}
	}
	for _, tc := range []struct {
		name    string
		value   any
		wantErr bool
	}{
		{"one", valid(1), false}, {"maximum", valid(128), false}, {"empty", valid(0), true},
		{"overbound", valid(129), true}, {"version", map[string]any{"version": 2, "public_repositories": []string{"https://github.com/example/repo#a" + strings.Repeat("a", 39)}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cases, _, err := loadDiscoveryCohort(context.Background(), makeConfig(tc.name+".json", tc.value))
			if (err != nil) != tc.wantErr || !tc.wantErr && len(cases) == 0 {
				t.Fatalf("cases=%d err=%v", len(cases), err)
			}
		})
	}
	for _, repos := range [][]string{
		{"https://github.com/example/repo#" + strings.Repeat("a", 40), "https://github.com/EXAMPLE/REPO#" + strings.Repeat("b", 40)},
		{"https://github.com/example/repo#" + strings.Repeat("a", 64)},
		{"https://github.com/example/repo"},
		{"https://user:secret@github.com/example/repo#" + strings.Repeat("a", 40)},
	} {
		_, _, err := loadDiscoveryCohort(context.Background(), makeConfig("invalid-"+fmt.Sprint(len(repos))+".json", map[string]any{"version": 1, "public_repositories": repos}))
		if err == nil {
			t.Fatalf("invalid cohort accepted: %v", repos)
		}
	}
}

func TestLoadDiscoveryPolicyStrictSchema(t *testing.T) {
	root := t.TempDir()
	valid := `{"version":1,"rules":[{"key":"hiss:go","title":"Go","kind":"scanner_extension","matches":[".go"],"analyzer":"hiss"}]}`
	for _, raw := range []string{
		valid,
		`{"version":1,"rules":[{"key":"hiss:go","title":"Go","kind":"scanner_extension","matches":[".go"],"analyzer":"hiss","extra":true}]}`,
		`{"version":1,"rules":[{"key":"hiss:go","title":"Go","kind":"scanner_extension","matches":[".go"],"analyzer":"hiss"},{"key":"hiss:go","title":"Again","kind":"scanner_extension","matches":[".rs"],"analyzer":"hiss"}]}`,
		`{"version":1,"rules":[{"key":"hiss:go","title":"Go","kind":"scanner_extension","matches":["../go"],"analyzer":"hiss"}]}`,
	} {
		path := filepath.Join(root, fmt.Sprintf("policy-%d.json", len(raw)))
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := loadDiscoveryPolicy(context.Background(), path)
		if (raw == valid) != (err == nil) {
			t.Fatalf("policy result raw=%s err=%v", raw, err)
		}
	}
}

func TestDefaultDiscoveryPolicyNativeCoverage(t *testing.T) {
	policyPath := filepath.Join("..", "..", ".config", "dogfood", "discovery-policy.json")
	policy, _, err := loadDiscoveryPolicy(context.Background(), policyPath)
	if err != nil {
		t.Fatalf("load default policy: %v", err)
	}
	rules := make(map[string]DiscoveryRule, len(policy.Rules))
	for _, rule := range policy.Rules {
		rules[rule.Key] = rule
	}
	for key, want := range map[string][]string{
		"hiss:native":         {".cxx", ".cu", ".hip"},
		"hiss:cuda-headers":   {".cuh"},
		"hiss:apple-native":   {".metal", ".mm"},
		"hiss:shaders":        {".comp", ".glsl"},
		"needs:native":        {"CMakeLists.txt", "meson.build"},
		"verification:native": {"CMakeLists.txt", "meson.build"},
	} {
		rule, ok := rules[key]
		if !ok {
			t.Fatalf("default policy missing %q", key)
		}
		got := make(map[string]bool, len(rule.Matches))
		for _, match := range rule.Matches {
			got[match] = true
		}
		for _, match := range want {
			if !got[match] {
				t.Errorf("default policy %q missing %q: %v", key, match, rule.Matches)
			}
		}
	}
}

func TestRunDiscoveryLocalPlanIsOfflineAndContextBound(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "fixture.go"), []byte("package fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(root, "policy.json")
	if err := os.WriteFile(policy, []byte(`{"version":1,"rules":[{"key":"hiss:go","title":"Go","kind":"scanner_extension","matches":[".go"],"analyzer":"hiss"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := RunDiscovery(context.Background(), DiscoveryOptions{Path: root, PolicyPath: policy, ArtifactDir: filepath.Join(root, "plan"), Stage: "plan", Concurrency: 1})
	if err != nil || report.Status != "planned" || report.Complete || report.Verified {
		t.Fatalf("offline plan: %+v err=%v", report, err)
	}
	if _, err := os.Stat(filepath.Join(root, "plan/report.json")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = RunDiscovery(ctx, DiscoveryOptions{Path: root, PolicyPath: policy, ArtifactDir: filepath.Join(root, "cancel"), Stage: "plan", Concurrency: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled discovery err=%v", err)
	}
}
