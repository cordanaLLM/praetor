// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package templates_test

import (
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/cordanaLLM/praetor/internal/semver"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Only local ./ actions and docker:// references are exempt from commit pinning.
// Share the workflow reader with supplychain and adopt; enforce the emitted comment style here.
func actionPinFindings(text string) []string {
	_, uses, err := util.ScanActionUses(text, 100000)
	if err != nil {
		return []string{err.Error()}
	}
	var findings []string
	for _, use := range uses {
		if strings.HasPrefix(use.Ref, "./") || strings.HasPrefix(use.Ref, "docker://") {
			continue
		}
		ref, release, separated := strings.Cut(use.Ref, "  # ")
		_, versioned := semver.Parse(release)
		if !use.SHAPinned || !separated || !versioned || !strings.HasPrefix(release, "v") || ref != use.Pin.Action+"@"+use.Pin.SHA {
			findings = append(findings, fmt.Sprintf("line %d: uses: %s needs a full commit digest and '  # v<semver>'", use.Line+1, use.Ref))
		}
	}
	return findings
}

// templateActionPinFindings walks every .yml.tmpl template and every committed rendering in tree
// and returns each uses: line that is not pinned to a full commit digest with its release comment.
func templateActionPinFindings(tree fs.FS) ([]string, error) {
	var findings []string
	err := fs.WalkDir(tree, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || (!strings.HasSuffix(name, ".yml.tmpl") && !strings.HasPrefix(name, renderedRoot+"/")) {
			return nil
		}
		body, err := fs.ReadFile(tree, name)
		if err != nil {
			return err
		}
		for _, finding := range actionPinFindings(string(body)) {
			findings = append(findings, name+":"+finding)
		}
		return nil
	})
	return findings, err
}

// TestTemplateActionsAreDigestPinned fails for any emitted action that floats on a tag or branch (#1409).
func TestTemplateActionsAreDigestPinned(t *testing.T) {
	findings, err := templateActionPinFindings(os.DirFS("."))
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		t.Error(finding)
	}
}

// TestTemplateActionPinFixtures plants each accepted and refused uses: form in a template and a rendering.
func TestTemplateActionPinFixtures(t *testing.T) {
	const digest = "3d3c42e5aac5ba805825da76410c181273ba90b1"
	for _, name := range []string{"fixture/ci.yml.tmpl", renderedRoot + "/fixture/ci/default.yml"} {
		for _, tc := range []struct {
			ref  string
			want int
		}{
			{"actions/checkout@v4", 1},
			{"actions/checkout@main", 1},
			{"actions/checkout@" + digest + "  # v7.0.1", 0},
			{"actions/checkout@" + digest, 1},
			{"actions/checkout@" + digest + " # v7.0.1", 1},
			{"actions/checkout@" + digest + "  # v7", 1},
			{"actions/checkout@" + digest + "  # 7.0.1", 1},
			{"actions/checkout@" + digest + "  # v07.0.1", 1},
			{"actions/checkout@" + digest[:39] + "  # v7.0.1", 1},
			{"actions/checkout@" + strings.ToUpper(digest) + "  # v7.0.1", 1},
			{"./.github/actions/local", 0},
			{"docker://alpine:3", 0},
		} {
			t.Run(name+"/"+tc.ref, func(t *testing.T) {
				tree := fstest.MapFS{name: {Data: []byte("steps:\n  - uses: " + tc.ref + "\n")}}
				got, err := templateActionPinFindings(tree)
				if err != nil || len(got) != tc.want {
					t.Fatalf("findings = %v, err = %v; want %d", got, err, tc.want)
				}
			})
		}
	}
}
