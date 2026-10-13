// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
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

// thirdPartyNotices is the layout #1244 measured: LICENSES/ holds the texts of third-party notices
// a build ships, named as their upstream names them, and the repository never used the reuse tool.
var thirdPartyNotices = map[string]string{
	LicensesDir + "/CC-BY-SA-4.0.txt": "Creative Commons Attribution-ShareAlike 4.0\n",
	LicensesDir + "/OFL-1.1.txt":      "SIL Open Font License 1.1\n",
	LicensesDir + "/goldmark-MIT.txt": "MIT License, goldmark\n",
	LicensesDir + "/fastdom-MIT.txt":  "MIT License, fastdom\n",
	"LICENSING.md":                    "# Licensing\n",
	"site/index.md":                   "# Site\n",
}

// withFiles returns files plus extra.
func withFiles(files map[string]string, extra map[string]string) map[string]string {
	merged := make(map[string]string, len(files)+len(extra))
	for rel, text := range files {
		merged[rel] = text
	}
	for rel, text := range extra {
		merged[rel] = text
	}
	return merged
}

// ReuseDeclared reads the two declaration files REUSE Specification 3.3 names for the whole tree.
// Positive: REUSE.toml or .reuse/dep5 declares REUSE, beside LICENSES/ or alone. Negative:
// LICENSES/ alone, the measured third-party layout included, a root with nothing, and a nil
// context. Boundary: either declaration file as a directory, LICENSES as a file, and a symlink to a
// declaration file are no declaration.
func TestReuseDeclared_3D(t *testing.T) {
	tomlRoot := licensedRoot(t, map[string]string{ReuseFile: "x\n"})
	for name, tc := range map[string]struct {
		files map[string]string
		want  bool
	}{
		"REUSE.toml":                       {map[string]string{ReuseFile: "x\n"}, true},
		".reuse/dep5":                      {map[string]string{ReuseDep5File: "Format: x\n"}, true},
		"REUSE.toml beside LICENSES/":      {withFiles(thirdPartyNotices, map[string]string{ReuseFile: "x\n"}), true},
		"LICENSES/ with third-party texts": {thirdPartyNotices, false},
		"LICENSES/ with one SPDX text":     {map[string]string{LicensesDir + "/MIT.txt": "x\n"}, false},
		"nothing":                          {map[string]string{"README.md": "x\n"}, false},
		"declarations as directories":      {map[string]string{ReuseFile + "/inner": "x\n", ReuseDep5File + "/inner": "x\n"}, false},
		"LICENSES as a file":               {map[string]string{LicensesDir: "x\n"}, false},
	} {
		got, err := ReuseDeclared(context.Background(), licensedRoot(t, tc.files))
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
	if err := os.Symlink(filepath.Join(tomlRoot, ReuseFile), filepath.Join(linked, ReuseFile)); err != nil {
		t.Fatal(err)
	}
	if got, err := ReuseDeclared(context.Background(), linked); err != nil || got {
		t.Errorf("a symlinked REUSE.toml: ReuseDeclared = %v, %v; want false", got, err)
	}
}

// gitRoot returns licensedRoot(files) initialised as a Git work tree, so the files are the
// repository's own by git's answer.
func gitRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := licensedRoot(t, files)
	command := exec.Command("git", "init", "-q", root)
	command.Env = testsupport.HermeticGitEnv(t)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return root
}

// REUSE-IgnoreStart: the strings below hold licence lines as test data, not this file's licence.

// headerLabelled is a repository that labels its files the REUSE way by headers alone: a source
// with the tag, a binary with a .license companion, and the texts under LICENSES/.
var headerLabelled = map[string]string{
	LicensesDir + "/MIT.txt":  "MIT License\n",
	"src/main.go":             "// SPDX-FileCopyrightText: 2026 Example\n// SPDX-License-Identifier: MIT\n\npackage main\n",
	"assets/logo.png":         "\x89PNG\n",
	"assets/logo.png.license": "SPDX-FileCopyrightText: 2026 Example\nSPDX-License-Identifier: MIT\n",
	"README.md":               "# Example\n",
}

// Positive: a repository labelling its files by headers alone is named, with the count of
// labelled files; the line says the gate is not rendered and how to keep it (#1244).
func TestReuseHeadersOnlyNotice_Positive(t *testing.T) {
	notice, err := ReuseHeadersOnlyNotice(context.Background(), gitRoot(t, headerLabelled))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"REUSE by headers only, no REUSE.toml", "the REUSE gate is not rendered",
		"add REUSE.toml to keep it", "(2 file(s) carry SPDX-License-Identifier headers or .license companions"} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice lacks %q: %q", want, notice)
		}
	}
}

// Negative: no line for a repository that declares REUSE, for the measured third-party layout,
// whose files carry no header, and for headers without LICENSES/.
func TestReuseHeadersOnlyNotice_Negative(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"REUSE.toml":                withFiles(headerLabelled, map[string]string{ReuseFile: "version = 1\n"}),
		".reuse/dep5":               withFiles(headerLabelled, map[string]string{ReuseDep5File: "Format: x\n"}),
		"third-party notices":       thirdPartyNotices,
		"headers without LICENSES/": {"src/main.go": "// SPDX-License-Identifier: MIT\npackage main\n"},
	} {
		notice, err := ReuseHeadersOnlyNotice(context.Background(), gitRoot(t, files))
		if err != nil || notice != "" {
			t.Errorf("%s: notice %q, %v; want none", name, notice, err)
		}
	}
}

// Boundary: a tag in a license text under LICENSES/, in a file git ignores, or past the first
// chunk reuse reads does not count, and outside a Git work tree there is no answer to count by.
func TestReuseHeadersOnlyNotice_Boundary(t *testing.T) {
	late := strings.Repeat("x", reuseHeaderBytes) + "\n// SPDX-License-Identifier: MIT\n"
	for name, files := range map[string]map[string]string{
		"tag in a license text": {LicensesDir + "/MIT.txt": "SPDX-License-Identifier: MIT\n"},
		"tag in an ignored file": {LicensesDir + "/MIT.txt": "MIT\n", ".gitignore": "build/\n",
			"build/out.go": "// SPDX-License-Identifier: MIT\n"},
		"tag past the first chunk": {LicensesDir + "/MIT.txt": "MIT\n", "big.txt": late},
	} {
		notice, err := ReuseHeadersOnlyNotice(context.Background(), gitRoot(t, files))
		if err != nil || notice != "" {
			t.Errorf("%s: notice %q, %v; want none", name, notice, err)
		}
	}
	notice, err := ReuseHeadersOnlyNotice(context.Background(), licensedRoot(t, headerLabelled))
	if err != nil || notice != "" {
		t.Errorf("outside a work tree: notice %q, %v; want none", notice, err)
	}
	edge := withFiles(map[string]string{LicensesDir + "/MIT.txt": "MIT\n"},
		map[string]string{"edge.txt": strings.Repeat("x", reuseHeaderBytes-len("SPDX-License-Identifier:")) + "SPDX-License-Identifier:"})
	if notice, err := ReuseHeadersOnlyNotice(context.Background(), gitRoot(t, edge)); err != nil || !strings.Contains(notice, "(1 file(s)") {
		t.Errorf("a tag ending at the last byte read: notice %q, %v; want one file", notice, err)
	}
}

// REUSE-IgnoreEnd

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
