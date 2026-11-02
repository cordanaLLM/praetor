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
