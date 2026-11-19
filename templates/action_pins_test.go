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
	"github.com/cordanaLLM/praetor/templates"
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

// TestTemplateActionBlockFunctions proves every admitted block through the renderer's real function map.
func TestTemplateActionBlockFunctions(t *testing.T) {
	for _, name := range []string{"hostedGateOn", "hostedGateScheduledOn", "hostedGateConcurrency", "hostedGateDraftStep"} {
		t.Run(name, func(t *testing.T) {
			body := "{{ " + name + " }}"
			rendered, err := templates.Render(name, body, templates.Context{})
			if err != nil {
				t.Fatal(err)
			}
			_, uses, err := util.ScanActionUses(rendered, 100)
			if err != nil || len(uses) != 0 || rendered == "" {
				t.Fatalf("block output = %q, uses = %+v, err = %v", rendered, uses, err)
			}
			_, uses, err = util.ScanTemplateActionUses(body, 100, "", "")
			if err != nil || len(uses) != 0 {
				t.Fatalf("proven block refused: %+v, %v", uses, err)
			}
		})
	}
}

// templateInventoryEntry fixes the base head's source identity and action reference.
type templateInventoryEntry struct {
	line, start, end int
	ref              string
}

// baseTemplateInventory records all committed YAML templates at head 848a7c8c7.
func baseTemplateInventory() map[string][]templateInventoryEntry {
	return map[string][]templateInventoryEntry{
		"flutter/analysis_options.yaml.tmpl": {},
		"flutter/ci-flutter.yml.tmpl": {
			{20, 786, 843, "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1  # v7.0.1"},
			{36, 1367, 1431, "subosito/flutter-action@1a449444c387b1966244ae4d4f8c696479add0b2  # v2.23.0"},
		},
		"go/.golangci.yml.tmpl": {},
		"go/ci-go.yml.tmpl": {
			{20, 786, 843, "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1  # v7.0.1"},
			{36, 1339, 1396, "actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e  # v7.0.0"},
			{52, 2277, 2331, "actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9  # v6.1.0"},
			{66, 3079, 3133, "actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9  # v6.1.0"},
		},
		"go/security-go.yml.tmpl": {
			{18, 640, 697, "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1  # v7.0.1"},
			{24, 892, 949, "actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e  # v7.0.0"},
			{36, 1497, 1551, "actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9  # v6.1.0"},
			{46, 1944, 1998, "actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9  # v6.1.0"},
		},
		"jvm/ci-jvm.yml.tmpl": {
			{27, 1344, 1401, "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1  # v7.0.1"},
			{45, 2101, 2160, "actions/setup-java@d1374f8ac74474200d16ef8ecee22c7ae7a51880  # v6.3.0"},
		},
		"node/ci-node.yml.tmpl": {
			{33, 1848, 1905, "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1  # v7.0.1"},
			{49, 2459, 2518, "actions/setup-node@949feb2413d6458794dcd2491c4babbbce0c15c1  # v7.1.0"},
			{77, 3510, 3569, "actions/setup-node@949feb2413d6458794dcd2491c4babbbce0c15c1  # v7.1.0"},
			{109, 4684, 4742, "oven-sh/setup-bun@0c5077e51419868618aeaa5fe8019c62421857d6  # v2.2.0"},
			{112, 4874, 4933, "actions/setup-node@949feb2413d6458794dcd2491c4babbbce0c15c1  # v7.1.0"},
			{133, 5639, 5698, "actions/setup-node@949feb2413d6458794dcd2491c4babbbce0c15c1  # v7.1.0"},
		},
		"osimage/.yamllint.yml.tmpl": {},
		"rust/ci-rust-library.yml.tmpl": {
			{15, 390, 447, "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1  # v7.0.1"},
		},
		"rust/ci-rust.yml.tmpl": {
			{24, 974, 1031, "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1  # v7.0.1"},
		},
	}
}

// TestTemplateActionInventoryMatchesBase keeps every committed template accepted with identical inventory.
func TestTemplateActionInventoryMatchesBase(t *testing.T) {
	for name, want := range baseTemplateInventory() {
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		_, uses, err := util.ScanTemplateActionUses(string(body), 100000)
		if err != nil || len(uses) != len(want) {
			t.Fatalf("%s: uses = %+v, err = %v; want %+v", name, uses, err, want)
		}
		for i, use := range uses {
			got := templateInventoryEntry{use.Line, use.Start, use.End, use.Ref}
			if got != want[i] {
				t.Errorf("%s: inventory[%d] = %+v, want %+v", name, i, got, want[i])
			}
		}
	}
}
