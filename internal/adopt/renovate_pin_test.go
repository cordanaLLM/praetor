// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type pinManagerFixture struct {
	CustomType, DatasourceTemplate, PackageNameTemplate, CurrentValueTemplate, DepNameTemplate string
	MatchStrings, ManagerFilePatterns                                                          []string
}

// TestRenovateWorkflowPinManager pins reconciliation and idempotency of the Renovate custom regex manager for workflow pins.
func TestRenovateWorkflowPinManager(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "renovate.json"), "{}\n")
	s := &adoptSession{repoPath: root, report: &AdoptReport{}}
	if err := reconcileRenovateIgnore(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	before := mustRead(t, filepath.Join(root, "renovate.json"))
	var config struct {
		CustomManagers []pinManagerFixture `json:"customManagers"`
	}
	if err := json.Unmarshal([]byte(before), &config, json.MatchCaseInsensitiveNames(true)); err != nil {
		t.Fatal(err)
	}
	if len(config.CustomManagers) != 1 {
		t.Fatalf("missing workflow pin custom manager: %s", before)
	}
	manager := config.CustomManagers[0]
	if manager.CustomType != "regex" || manager.DatasourceTemplate != "git-refs" || manager.CurrentValueTemplate != "HEAD" ||
		manager.PackageNameTemplate != "https://github.com/cordanaLLM/praetor" || manager.DepNameTemplate == "cordanaLLM/praetor" {
		t.Fatalf("manager cannot update the pin independently of engine suppression: %+v", manager)
	}
	assertRenovatePinPatterns(t, manager)
	if err := reconcileRenovateIgnore(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if after := mustRead(t, filepath.Join(root, "renovate.json")); before != after {
		t.Fatal("manager rendering is not idempotent")
	}
}

// assertRenovatePinPatterns verifies regex patterns capture exact commit SHAs and reject comments, branches, or invalid SHAs.
func assertRenovatePinPatterns(t *testing.T, manager pinManagerFixture) {
	t.Helper()
	for i, name := range []string{"PRAETOR_COMMIT", "PRAETOR_REF"} {
		pattern := regexp.MustCompile(manager.MatchStrings[i])
		pin := strings.Repeat("a", 40)
		line := "  " + name + ": '" + pin + "' # keep\r\n"
		matches := pattern.FindAllStringSubmatch("env:\r\n"+line+line, -1)
		if len(matches) != 2 || matches[0][pattern.SubexpIndex("currentDigest")] != pin {
			t.Fatalf("manager does not capture adjacent %s declarations", name)
		}
		if pattern.MatchString("# "+name+": "+pin) || pattern.MatchString(name+": main\n") {
			t.Fatal("manager matches comments or a branch")
		}
	}
	if regexp.MustCompile(manager.MatchStrings[0]).MatchString("PRAETOR_COMMIT: abcdef1\n") {
		t.Fatal("manager matches an invalid abbreviated PRAETOR_COMMIT")
	}
}

// TestRenovatePinManagerBoundaryAndRefusals pins manager array bounds and refusal of malformed customManagers configurations.
func TestRenovatePinManagerBoundaryAndRefusals(t *testing.T) {
	input := []byte(`{"customManagers":[` + strings.Repeat(`{},`, maxRenovateRules-2) + `{}]}`)
	merged, changed, err := mergeRenovatePinManager(input)
	if err != nil || !changed {
		t.Fatalf("last manager slot rejected: changed=%v err=%v", changed, err)
	}
	if _, changed, err := mergeRenovatePinManager(merged); err != nil || changed {
		t.Fatalf("manager at bound not idempotent: changed=%v err=%v", changed, err)
	}
	for _, input := range []string{
		`{"customManagers":null}`, `{"customManagers":{}}`,
		`{"customManagers":[` + strings.Repeat(`{},`, maxRenovateRules-1) + `{}]}`,
		`{"customManagers":[{"description":"` + renovatePinDescription + `"},{"description":"` + renovatePinDescription + `"}]}`,
	} {
		if _, _, err := mergeRenovatePinManager([]byte(input)); err == nil {
			t.Fatal("unsafe custom manager configuration accepted")
		}
	}
}

// TestRenovatePinManagerPreservesAdopterManager pins retention of adopter customManagers when adding the workflow pin manager.
func TestRenovatePinManagerPreservesAdopterManager(t *testing.T) {
	own := `{"customType":"regex","managerFilePatterns":["versions.txt"],"matchStrings":["version=(?<currentValue>.*)"],"depNameTemplate":"node","datasourceTemplate":"node-version"}`
	merged, _, err := mergeRenovatePinManager([]byte(`{"customManagers":[` + own + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		CustomManagers []jsontext.Value `json:"customManagers"`
	}
	if err := json.Unmarshal(merged, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.CustomManagers) != 2 || !sameJSON(config.CustomManagers[0], jsontext.Value(own)) {
		t.Fatal("adopter manager changed")
	}
}

// TestRenovatePinManagerDuplicateNamesCollection pins refusal of duplicate managers and verifies error messages name customManagers.
func TestRenovatePinManagerDuplicateNamesCollection(t *testing.T) {
	input := `{"customManagers":[{"description":"` + renovatePinDescription + `"},{"description":"` + renovatePinDescription + `"}]}`
	out, changed, err := mergeRenovatePinManager([]byte(input))
	if err == nil || !strings.Contains(err.Error(), "customManagers holds two entries") || strings.Contains(err.Error(), "packageRules") {
		t.Fatalf("duplicate manager diagnostic names wrong collection: %v", err)
	}
	if changed || out != nil {
		t.Fatal("duplicate managers produced a rewrite")
	}
}

// TestRenovatePinManagerDocumentSafety pins refusal of mixed line endings, non-objects, and duplicate members in Renovate JSON.
func TestRenovatePinManagerDocumentSafety(t *testing.T) {
	for _, tc := range []struct{ name, input, reason string }{
		{"mixed endings", "{\r\n\"extends\": []\n}\r\n", "its line endings are mixed"},
		{"not object", "[]", "pin manager requires an object"},
		{"duplicate members", `{"customManagers":[],"customManagers":[]}`, "pin manager requires an object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, changed, err := mergeRenovatePinManager([]byte(tc.input))
			if err == nil || !strings.Contains(err.Error(), tc.reason) || changed || out != nil {
				t.Fatalf("unsafe document not refused without output: changed=%v out=%q err=%v", changed, out, err)
			}
		})
	}
}

// TestRenovatePinManagerPreservesCRLF pins preservation of CRLF line endings across pin manager reconciliation.
func TestRenovatePinManagerPreservesCRLF(t *testing.T) {
	input := []byte("{\r\n  \"customManagers\": []\r\n}\r\n")
	out, changed, err := mergeRenovatePinManager(input)
	if err != nil || !changed || strings.Contains(strings.ReplaceAll(string(out), "\r\n", ""), "\n") {
		t.Fatalf("CRLF document not preserved: changed=%v out=%q err=%v", changed, out, err)
	}
	if next, changed, err := mergeRenovatePinManager(out); err != nil || changed || string(next) != string(out) {
		t.Fatalf("CRLF rerun changed bytes: changed=%v err=%v", changed, err)
	}
}

// TestRenovateEngineDocumentLineEndings pins preservation of LF and CRLF line endings in engine rule configuration.
func TestRenovateEngineDocumentLineEndings(t *testing.T) {
	for _, tc := range []struct{ name, input string }{
		{"LF", "{\n}\n"},
		{"CRLF", "{\r\n}\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, changed, err := mergeRenovateEngineRule(t.Context(), []byte(tc.input))
			if err != nil || !changed {
				t.Fatalf("engine document merge failed: changed=%v err=%v", changed, err)
			}
			if strings.Contains(tc.input, "\r\n") && strings.Contains(strings.ReplaceAll(string(out), "\r\n", ""), "\n") {
				t.Fatalf("engine merge lost CRLF: %q", out)
			}
			if _, changed, err := mergeRenovateEngineRule(t.Context(), out); err != nil || changed {
				t.Fatalf("engine rerun changed document: changed=%v err=%v", changed, err)
			}
		})
	}
}

// TestRenovateEngineRejectsMixedEndings pins refusal of documents with inconsistent mixed line endings.
func TestRenovateEngineRejectsMixedEndings(t *testing.T) {
	out, changed, err := mergeRenovateEngineRule(t.Context(), []byte("{\r\n}\n"))
	if err == nil || !strings.Contains(err.Error(), "its line endings are mixed") || changed || out != nil {
		t.Fatalf("mixed engine document not refused: changed=%v out=%q err=%v", changed, out, err)
	}
}
