// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Positive: the pin reads as the action at its tag and the reuse major that tag runs, and
// the digest-pinned reference combines the full commit SHA with the version comment.
func TestReuseActionPin(t *testing.T) {
	if got := ReuseActionRef(); got != "fsfe/reuse-action@"+ReuseActionVersion {
		t.Errorf("ReuseActionRef = %q", got)
	}
	if got := ReuseActionPinnedRef(); got != "fsfe/reuse-action@"+ReuseActionCommit+"  # "+ReuseActionVersion {
		t.Errorf("ReuseActionPinnedRef = %q", got)
	}
	checkHexSHA(t, ReuseActionCommit)
	if got := ReuseMajor(); got == "" || got == ReuseActionVersion || got[0] < '0' || got[0] > '9' {
		t.Errorf("ReuseMajor = %q, want the digits of %s", got, ReuseActionVersion)
	}
}

func checkHexSHA(t *testing.T, sha string) {
	t.Helper()
	if len(sha) != 40 {
		t.Errorf("commit SHA length = %d, want 40", len(sha))
		return
	}
	for _, c := range sha {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Errorf("commit SHA %q contains non-hex character %q", sha, c)
			return
		}
	}
}

// ReuseDeclared reads the two REUSE markers at the root. Positive: REUSE.toml or LICENSES/ alone
// declares REUSE. Negative: a root with neither does not, nor a nil context. Boundary: a
// REUSE.toml directory or a LICENSES file is no marker, and neither is a symlink to one.
func TestReuseDeclared_3D(t *testing.T) {
	tomlRoot := licensedRoot(t, map[string]string{ReuseFile: "x\n"})
	licensesRoot := licensedRoot(t, map[string]string{LicensesDir + "/MIT.txt": "x\n"})
	plain := licensedRoot(t, map[string]string{"README.md": "x\n"})
	wrongKinds := licensedRoot(t, map[string]string{ReuseFile + "/inner": "x\n", LicensesDir: "x\n"})
	for name, tc := range map[string]struct {
		root string
		want bool
	}{
		"REUSE.toml": {tomlRoot, true}, "LICENSES/": {licensesRoot, true},
		"neither": {plain, false}, "wrong kinds": {wrongKinds, false},
	} {
		got, err := ReuseDeclared(context.Background(), tc.root)
		if err != nil || got != tc.want {
			t.Errorf("%s: ReuseDeclared = %v, %v; want %v", name, got, err, tc.want)
		}
	}
	//nolint:staticcheck // SA1012: the nil context is the case under test.
	if _, err := ReuseDeclared(nil, tomlRoot); err == nil {
		t.Error("a nil context was accepted")
	}
	if runtime.GOOS == "windows" {
		t.Log("the symlink case needs symlink privileges Windows test hosts do not grant by default")
		return
	}
	linked := t.TempDir()
	if err := os.Symlink(filepath.Join(licensesRoot, LicensesDir), filepath.Join(linked, LicensesDir)); err != nil {
		t.Fatal(err)
	}
	if got, err := ReuseDeclared(context.Background(), linked); err != nil || got {
		t.Errorf("a symlinked LICENSES/: ReuseDeclared = %v, %v; want false", got, err)
	}
}

type renovatePackageRule struct {
	MatchManagers     []string `json:"matchManagers"`
	MatchFileNames    []string `json:"matchFileNames"`
	MatchPackageNames []string `json:"matchPackageNames"`
	AllowedVersions   string   `json:"allowedVersions"`
	GroupName         string   `json:"groupName"`
}

type renovateCustomManager struct {
	CustomType          string   `json:"customType"`
	ManagerFilePatterns []string `json:"managerFilePatterns"`
	MatchStrings        []string `json:"matchStrings"`
	DepNameTemplate     string   `json:"depNameTemplate"`
	DatasourceTemplate  string   `json:"datasourceTemplate"`
	VersioningTemplate  string   `json:"versioningTemplate"`
}

type renovateConfig struct {
	GitHubActions struct {
		ManagerFilePatterns []string `json:"managerFilePatterns"`
	} `json:"github-actions"`
	CustomManagers []renovateCustomManager `json:"customManagers"`
	PackageRules   []renovatePackageRule   `json:"packageRules"`
}

// Positive: renovate.json carries a regex custom manager for internal/supplychain/reuse_lint.go
// that extracts currentValue from ReuseActionVersion, currentDigest from ReuseActionCommit,
// depName fsfe/reuse-action, and datasource github-tags.
// Negative: github-actions.managerFilePatterns does not match reuse_lint.go, and a malformed
// source without either constant fails the regex.
func TestRenovateRegexMatchesReuseActionPin(t *testing.T) {
	config := readRenovateTestConfig(t)
	rel := "internal/supplychain/reuse_lint.go"
	assertNoGitHubActionsMatch(t, config.GitHubActions.ManagerFilePatterns, rel)
	manager := findCustomManagerForFile(config.CustomManagers, rel)
	if manager == nil {
		t.Fatalf("no custom manager matches %s", rel)
	}
	assertReuseManagerConfig(t, manager)

	re, err := regexp.Compile(manager.MatchStrings[0])
	if err != nil {
		t.Fatalf("regex compile: %v", err)
	}
	src, err := os.ReadFile("reuse_lint.go")
	if err != nil {
		t.Fatal(err)
	}
	assertRegexMatchesReuseSource(t, re, src)

	broken := strings.Replace(string(src), ReuseActionCommit, "", 1)
	if re.MatchString(broken) {
		t.Error("regex matched broken source missing commit SHA")
	}
}

// Positive: the REUSE gate packageRule in renovate.json narrows matching to fsfe/reuse-action
// and restricts allowedVersions to major-only tags (/^v\d+$/).
func TestRenovatePackageRule_NarrowsToReuseAction(t *testing.T) {
	config := readRenovateTestConfig(t)
	var found *renovatePackageRule
	for i := range config.PackageRules {
		rule := &config.PackageRules[i]
		if rule.GroupName == "REUSE gate actions" {
			found = rule
			break
		}
	}
	if found == nil {
		t.Fatal("packageRules missing 'REUSE gate actions' group")
	}
	if !slices.Equal(found.MatchPackageNames, []string{"fsfe/reuse-action"}) {
		t.Errorf("matchPackageNames = %v, want [fsfe/reuse-action]", found.MatchPackageNames)
	}
	if found.AllowedVersions != "/^v\\d+$/" {
		t.Errorf("allowedVersions = %q, want /^v\\d+$/", found.AllowedVersions)
	}
}

func readRenovateTestConfig(t *testing.T) renovateConfig {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "renovate.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config renovateConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	return config
}

func assertNoGitHubActionsMatch(t *testing.T, patterns []string, rel string) {
	t.Helper()
	for _, pattern := range patterns {
		expr := strings.Trim(pattern, "/")
		if regexp.MustCompile(expr).MatchString(rel) {
			t.Fatalf("github-actions.managerFilePatterns must not match %s", rel)
		}
	}
}

func assertReuseManagerConfig(t *testing.T, manager *renovateCustomManager) {
	t.Helper()
	if manager.CustomType != "regex" {
		t.Errorf("customType = %q, want regex", manager.CustomType)
	}
	if manager.DepNameTemplate != ReuseAction {
		t.Errorf("depNameTemplate = %q, want %s", manager.DepNameTemplate, ReuseAction)
	}
	if manager.DatasourceTemplate != "github-tags" {
		t.Errorf("datasourceTemplate = %q, want github-tags", manager.DatasourceTemplate)
	}
	if manager.VersioningTemplate != "regex:^v(?<major>\\d+)$" {
		t.Errorf("versioningTemplate = %q, want regex:^v(?<major>\\d+)$", manager.VersioningTemplate)
	}
	if len(manager.MatchStrings) == 0 {
		t.Fatal("custom manager has empty matchStrings")
	}
}

func assertRegexMatchesReuseSource(t *testing.T, re *regexp.Regexp, src []byte) {
	t.Helper()
	match := re.FindSubmatch(src)
	if match == nil {
		t.Fatal("regex did not match reuse_lint.go")
	}
	valIndex := re.SubexpIndex("currentValue")
	digestIndex := re.SubexpIndex("currentDigest")
	if valIndex < 0 || digestIndex < 0 {
		t.Fatal("regex missing currentValue or currentDigest capture groups")
	}
	if gotVal := string(match[valIndex]); gotVal != ReuseActionVersion {
		t.Errorf("currentValue = %q, want %q", gotVal, ReuseActionVersion)
	}
	if gotDigest := string(match[digestIndex]); gotDigest != ReuseActionCommit {
		t.Errorf("currentDigest = %q, want %q", gotDigest, ReuseActionCommit)
	}
}

func findCustomManagerForFile(managers []renovateCustomManager, rel string) *renovateCustomManager {
	for i := range managers {
		for _, pattern := range managers[i].ManagerFilePatterns {
			expr := strings.Trim(pattern, "/")
			if regexp.MustCompile(expr).MatchString(rel) {
				return &managers[i]
			}
		}
	}
	return nil
}
