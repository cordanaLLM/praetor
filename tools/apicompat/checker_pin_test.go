// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apicompat

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/semver"
)

// minXTools is the floor of golang.org/x/tools the checker's build module must require: the
// first release measured to read the export data Go 1.27.2 writes (version 5), which the
// v0.49.0 the checker's own go.mod asks for does not (#1049).
const minXTools = "v0.51.0"

const (
	checkerModuleName = "github.com/joelanford/go-apidiff"
	xtoolsModuleName  = "golang.org/x/tools"
)

// directRequirements maps each module go.mod requires directly (no indirect marker) to its
// version, read through internal/gomanifest the way the go command reads the file (HISS-19).
func directRequirements(goMod string) map[string]string {
	direct := map[string]string{}
	inBlock := false
	for _, raw := range strings.Split(goMod, "\n") {
		line, ok := gomanifest.RequirementLine(raw, &inBlock)
		if !ok || gomanifest.IsIndirect(line) {
			continue
		}
		if requirement, parsed := gomanifest.ParseRequirement(line); parsed {
			direct[requirement.Path] = requirement.Version
		}
	}
	return direct
}

// buildModuleConst returns the text of one raw-string constant of the gate source.
func buildModuleConst(source []byte, name string) (string, error) {
	match := regexp.MustCompile(`(?s)\t` + name + " = `(.*?)`").FindSubmatch(source)
	if match == nil {
		return "", fmt.Errorf("the gate defines no %s", name)
	}
	return string(match[1]), nil
}

// checkBuildModule reports why the build module text does not pin the checker: go.mod must
// require go-apidiff and golang.org/x/tools directly at exact versions, x/tools at minXTools or
// newer, go.sum must hold both modules' hashes, and tools.go must import both.
func checkBuildModule(goMod, goSum, tools string) error {
	versions := directRequirements(goMod)
	for _, module := range []string{checkerModuleName, xtoolsModuleName} {
		version, found := versions[module]
		if _, precision, parsed := semver.ParseTag(version); !found || !parsed || precision != 3 {
			return fmt.Errorf("go.mod does not require %s directly at an exact version", module)
		}
		for _, sum := range []string{module + " " + version + " h1:", module + " " + version + "/go.mod h1:"} {
			if !strings.Contains(goSum, sum) {
				return fmt.Errorf("go.sum lacks a %q line", sum)
			}
		}
	}
	have, _, haveOK := semver.ParseTag(versions[xtoolsModuleName])
	floor, _, floorOK := semver.ParseTag(minXTools)
	if !haveOK || !floorOK || semver.Compare(have, floor) < 0 {
		return fmt.Errorf("go.mod requires %s %s, below %s", xtoolsModuleName, versions[xtoolsModuleName], minXTools)
	}
	for _, imported := range []string{`_ "` + checkerModuleName + `"`, `_ "` + xtoolsModuleName + `/go/packages"`} {
		if !strings.Contains(tools, imported) {
			return fmt.Errorf("tools.go does not import %s", imported)
		}
	}
	if len(gomanifest.ParseManifest([]byte(goMod)).Replaces) > 0 {
		return errors.New("go.mod holds a replace directive")
	}
	return nil
}

// Positive: the checker is built from a pinned build module whose requirements are exact, x/tools
// at the floor or newer, with go.sum and tools.go covering them, and the gate no longer installs
// module@version. Negative: an x/tools below minXTools (the version that read every Go 1.27.2
// package as empty), a missing requirement, an emptied go.sum and an emptied tools.go are each
// refused. Boundary: the floor itself passes.
func TestGatePinsItsChecker(t *testing.T) {
	source, err := Read(GateFile)
	if err != nil {
		t.Fatal(err)
	}
	var goMod, goSum, tools string
	for name, into := range map[string]*string{"checkerGoMod": &goMod, "checkerGoSum": &goSum, "checkerTools": &tools} {
		if *into, err = buildModuleConst(source, name); err != nil {
			t.Fatal(err)
		}
	}
	if err := checkBuildModule(goMod, goSum, tools); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(source, []byte(`"install"`)) || bytes.Contains(source, []byte("checkerModule")) {
		t.Fatal("the gate still installs the checker as module@version instead of building the pinned build module")
	}
	xtools := directRequirements(goMod)[xtoolsModuleName]
	withXTools := func(version string) (string, string) {
		return strings.Replace(goMod, xtoolsModuleName+" "+xtools, xtoolsModuleName+" "+version, 1),
			strings.ReplaceAll(goSum, xtoolsModuleName+" "+xtools, xtoolsModuleName+" "+version)
	}
	belowMod, belowSum := withXTools("v0.49.0")
	floorMod, floorSum := withXTools(minXTools)
	refused := map[string][3]string{
		"x/tools below the floor": {belowMod, belowSum, tools},
		"checker not required":    {strings.Replace(goMod, "\t"+checkerModuleName, "\t// "+checkerModuleName, 1), goSum, tools},
		"go.sum emptied":          {goMod, "", tools},
		"tools.go emptied":        {goMod, goSum, "package tools\n"},
	}
	for name, m := range refused {
		if err := checkBuildModule(m[0], m[1], m[2]); err == nil {
			t.Errorf("%s: the build module was accepted", name)
		}
	}
	if err := checkBuildModule(floorMod, floorSum, tools); err != nil {
		t.Errorf("x/tools at the floor was refused: %v", err)
	}
}
