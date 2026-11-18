// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package templates_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/cordanaLLM/praetor/internal/semver"
	"github.com/cordanaLLM/praetor/internal/supplychain"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Only local ./ actions and docker:// references are exempt from commit pinning.
// Share the workflow reader with supplychain and adopt; enforce the emitted comment style here.
func actionPinFindings(text string) []string {
	_, uses, err := util.ScanActionUses(text, 100000)
	return actionPinUseFindings(uses, err)
}

// actionPinUseFindings applies pinning and local-action rules to structurally decoded uses values.
func actionPinUseFindings(uses []util.ActionUse, err error) []string {
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
		renderedYAML := strings.HasPrefix(name, renderedRoot+"/") && (path.Ext(name) == ".yml" || path.Ext(name) == ".yaml")
		if entry.IsDir() || (!strings.HasSuffix(name, ".yml.tmpl") && !renderedYAML) {
			return nil
		}
		body, err := fs.ReadFile(tree, name)
		if err != nil {
			return err
		}
		_, uses, scanErr := util.ScanActionUses(string(body), 100000)
		if strings.HasSuffix(name, ".tmpl") {
			_, uses, scanErr = util.ScanTemplateActionUses(string(body), 100000)
		}
		for _, finding := range actionPinUseFindings(uses, scanErr) {
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

// TestTemplateActionPinYAMLKeyForms enforces the same pins for quoted keys and flow mappings.
func TestTemplateActionPinYAMLKeyForms(t *testing.T) {
	const digest = "3d3c42e5aac5ba805825da76410c181273ba90b1"
	for _, name := range []string{"fixture/ci.yml.tmpl", renderedRoot + "/fixture/ci/default.yml"} {
		for _, form := range []string{
			`- "uses": %s`, "- 'uses': %s", "- {uses: %s}",
			`- {"uses": %s}`, "- {'uses': %s}",
			`- "uses" : %s`, "- { uses : %s }",
			"- {name: Checkout, uses: %s}", "- {uses: %s, name: Checkout}",
		} {
			for _, tc := range []struct {
				ref, comment string
				want         int
			}{
				{"actions/checkout@v4", "", 1},
				{"actions/checkout@" + digest, "  # v7.0.1", 0},
				{"actions/checkout@" + digest, "", 1},
				{"actions/checkout@" + digest[:39], "  # v7.0.1", 1},
			} {
				t.Run(name+"/"+form+"/"+tc.ref+tc.comment, func(t *testing.T) {
					step := fmt.Sprintf(form, tc.ref) + tc.comment
					tree := fstest.MapFS{name: {Data: []byte("steps:\n  " + step + "\n")}}
					got, err := templateActionPinFindings(tree)
					if err != nil || len(got) != tc.want {
						t.Fatalf("findings = %v, err = %v; want %d", got, err, tc.want)
					}
				})
			}
		}
	}
}

// TestTemplateActionPinQuotedScalar refuses floating actions beside uses-like scalar content,
// non-scalar mapping keys, and multiline unrendered template constructs.
func TestTemplateActionPinQuotedScalar(t *testing.T) {
	for _, text := range []string{
		"steps:\n  - {uses: actions/checkout@v4, name: \"example, uses: ./local\"}\n",
		"steps:\n  - &k uses: ./local\n  - ? *k\n    : actions/checkout@main\n",
	} {
		if findings := actionPinFindings(text); len(findings) != 1 {
			t.Errorf("%q: findings = %v; want one finding", text, findings)
		}
	}
	for _, body := range []string{
		"steps:\n  - &k uses: ./local\n  - ? *k\n    : actions/checkout@main\n",
		"<% if .Enabled %>\nsteps:\n  - {\"u\\x73es\":\n      actions/checkout@main }\n<% end %>\n",
	} {
		tree := fstest.MapFS{"fixture/ci.yml.tmpl": {Data: []byte(body)}}
		if findings, err := templateActionPinFindings(tree); err != nil || len(findings) != 1 {
			t.Errorf("%q: findings = %v, err = %v; want one finding", body, findings, err)
		}
	}
}

// TestActionInventoryTemplates keeps the credits caller compatible with the shared structural reader.
func TestActionInventoryTemplates(t *testing.T) {
	root := t.TempDir()
	ctx := actionInventoryFixtureContext(t)
	if _, err := util.RunGit(ctx, root, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"ci.yml.tmpl":        "<% if .Enabled %>\nsteps:\n  - uses: actions/checkout@v4\n<% end %>\n",
		"settings.toml.tmpl": "<% /*\nEvery built-in rule; parser refuses an empty list.\n*/ %>\n[settings]\n",
	} {
		dir := filepath.Join(root, "templates", "fixture")
		// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- Private fixture directory requires owner write and traversal.
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	items, err := supplychain.ReadCreditInventory(ctx, root)
	if err != nil || len(items) != 1 || items[0].ID != "actions/checkout" {
		t.Fatalf("inventory = %+v, %v; want the template's checkout action", items, err)
	}
}

// actionInventoryFixtureContext isolates local git with the lane's three permitted barriers.
func actionInventoryFixtureContext(t *testing.T) context.Context {
	t.Helper()
	var environment []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			environment = append(environment, entry)
		}
	}
	environment = append(environment, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	deadline, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	ctx, err := util.WithCommandEnvironment(deadline, environment)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}
