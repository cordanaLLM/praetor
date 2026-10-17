// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// licenceHeaderLines is how far into an emitted script its licence line may stand: behind the
// shebang, within the header an adopter's licence-header gate reads (#1281).
const licenceHeaderLines = 5

// licenceLine is the comment line that names the licence of a shell or Python file. The launcher
// may end it in its carriage-return guard, " #".
var licenceLine = regexp.MustCompile(`^# SPDX-License-Identifier: [A-Za-z0-9.+-]+( (AND|OR|WITH) [A-Za-z0-9.+-]+)*( #)?$`)

// hasLicenceLine reports whether one of the first licenceHeaderLines lines of text, in either
// line-ending style, is its licence line.
func hasLicenceLine(text string) bool {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for _, line := range lines[:min(len(lines), licenceHeaderLines)] {
		if licenceLine.MatchString(line) {
			return true
		}
	}
	return false
}

// emittedScripts returns every shell and Python fixture under testdata/emitted by its path.
func emittedScripts(t *testing.T) map[string]string {
	t.Helper()
	scripts := map[string]string{}
	err := filepath.WalkDir(emittedFixtureRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".sh") && !strings.HasSuffix(path, ".py") {
			return err
		}
		data, err := os.ReadFile(path)
		scripts[filepath.ToSlash(path)] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return scripts
}

// Positive: every shell and Python file adoption renders carries its licence line, so an
// adopter's header gate passes on files it must not edit (#1281).
func TestEmittedScripts_Positive_CarryTheLicenceLine(t *testing.T) {
	scripts := emittedScripts(t)
	for _, rel := range []string{engineLauncherFile, evasionHookFile} {
		if _, ok := scripts[emittedFixtureRoot+"/"+rel]; !ok {
			t.Fatalf("no emitted fixture for %s; the walk would pass on nothing", rel)
		}
	}
	for rel, text := range emittedHookRenderings(t) {
		if strings.HasSuffix(rel, ".sh") || strings.HasSuffix(rel, ".py") {
			scripts["rendering of "+rel] = text
		}
	}
	for path, text := range scripts {
		if !hasLicenceLine(text) {
			t.Errorf("%s has no SPDX-License-Identifier line in its first %d lines", path, licenceHeaderLines)
		}
	}
}

// Negative: a rendering without the line, with the line below the header, or with a misspelt
// tag fails the check.
func TestEmittedScripts_Negative_HeaderlessRenderingFails(t *testing.T) {
	without := func(text string) string {
		return regexp.MustCompile(`(?m)^# SPDX-License-Identifier: .*\n`).ReplaceAllString(text, "")
	}
	late := strings.Repeat("#\n", licenceHeaderLines) + "# SPDX-License-Identifier: EUPL-1.2\n"
	for name, text := range map[string]string{
		"launcher without the line":    without(engineLauncherScript),
		"interceptor without the line": without(buildBlockEvasionPY()),
		"line below the header":        "#!/bin/sh\n" + late,
		"misspelt tag":                 "#!/bin/sh\n# SPDX-Licence-Identifier: EUPL-1.2\n",
		"no expression":                "#!/bin/sh\n# SPDX-License-Identifier:\n",
	} {
		if hasLicenceLine(text) {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Boundary: the line counts behind a shebang, at the last header line, in a CRLF checkout and
// with the launcher's trailing guard.
func TestEmittedScripts_Boundary_LicenceLineForms(t *testing.T) {
	last := "#!/bin/sh\n" + strings.Repeat("#\n", licenceHeaderLines-2) + "# SPDX-License-Identifier: EUPL-1.2\n"
	for name, text := range map[string]string{
		"behind a shebang":      "#!/usr/bin/env python3\n# SPDX-License-Identifier: EUPL-1.2\n",
		"last header line":      last,
		"CRLF checkout":         strings.ReplaceAll(engineLauncherScript, "\n", "\r\n"),
		"carriage-return guard": "#!/bin/sh\n# SPDX-License-Identifier: EUPL-1.2 #\n",
		"compound expression":   "# SPDX-License-Identifier: EUPL-1.2 AND MIT\n",
	} {
		if !hasLicenceLine(text) {
			t.Errorf("%s: refused", name)
		}
	}
}
