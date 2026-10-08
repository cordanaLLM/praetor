// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package generated

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/readmegovernance"
)

// memoryTree is a Tree held in memory.
type memoryTree map[string]string

func (m memoryTree) Files(context.Context) ([]string, error) {
	files := make([]string, 0, len(m))
	for rel := range m {
		files = append(files, rel)
	}
	sort.Strings(files)
	return files, nil
}

func (m memoryTree) Read(_ context.Context, rel string) ([]byte, bool, error) {
	content, held := m[rel]
	if !held {
		return nil, false, nil
	}
	return []byte(content), true, nil
}

// praetorTree holds one file of every built-in artefact and the files they require.
func praetorTree() memoryTree {
	return memoryTree{
		"AGENTS.md":                        "# Agents\n" + config.RegisterBlockStart + "\nregister\n" + config.RegisterBlockEnd + "\n",
		"CLAUDE.md":                        "compiled\n",
		".claude/agents/praetor-a.md":      "persona\n",
		".claude/agents/notes.txt":         "not a persona\n",
		".standards-baseline.json":         "{}\n",
		"README.md":                        "# Readme\n" + readmegovernance.Start + "\nblock\n" + readmegovernance.End + "\n",
		".needs.yaml":                      "needs: []\n",
		"tools/figures/build.mjs":          "// engine\n",
		"docs/assets/figures/a.json":       "{}\n",
		"docs/assets/figures/a.svg":        "<svg/>\n",
		"cmd/standardsctl/main.go":         "package main\n",
		".devcontainer/Dockerfile.praetor": "FROM scratch\n",
		".devcontainer/devcontainer.json":  "{}\n",
		"internal/managedasset/testdata/shipped/markdown.sha256": "abc  x\n",
		"internal/codexhook/events_gen.go":                       "package codexhook\n",
	}
}

func assertArtefactsActive(t *testing.T, artefacts []Artefact) []string {
	t.Helper()
	var names []string
	for _, artefact := range artefacts {
		names = append(names, artefact.Name)
		if !artefact.Active || artefact.Origin != OriginBuiltin {
			t.Errorf("%s: active=%v origin=%s reason=%q", artefact.Name, artefact.Active, artefact.Origin, artefact.Reason)
		}
	}
	return names
}

func assertSetJSON(t *testing.T, set *Set) {
	t.Helper()
	data, err := json.Marshal(set)
	if err != nil {
		t.Fatalf("json = %v", err)
	}
	s := string(data)
	if !strings.Contains(s, `"regeneration":{"branch_prefix":"regen/","title_type":"chore(generated)"}`) ||
		!strings.Contains(s, `"command":["praetorctl","baseline","--record"]`) {
		t.Fatalf("json = %s", s)
	}
}

// Positive (#696): in a Praetor source checkout every built-in artefact applies, in render
// order, with the files it selects; the set marshals to the JSON a merge train reads.
func TestResolve_Positive_BuiltinsApplyInAPraetorCheckout(t *testing.T) {
	set, err := Resolve(context.Background(), nil, praetorTree())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{NameProjections, NameRegisterBlock, NameBaseline, NameReadmeBlock, NameNeeds, NameFigures, NameShippedTexts, NameClientTypes, NameDevContainer}
	names := assertArtefactsActive(t, set.Artefacts)
	if strings.Join(names, ";") != strings.Join(want, ";") {
		t.Fatalf("render order = %q, want %q", names, want)
	}
	if files := strings.Join(set.Artefacts[0].Files, ","); files != ".claude/agents/praetor-a.md,CLAUDE.md" {
		t.Fatalf("projection files = %q", files)
	}
	if set.Marker.BranchPrefix != config.DefaultRegenerationBranchPrefix || set.Marker.TitleType != config.DefaultRegenerationTitleType {
		t.Fatalf("marker = %+v", set.Marker)
	}
	assertSetJSON(t, set)
}

// Positive (#696): the built-in declarations satisfy the schema an adopter's own entries must
// satisfy, and the projection paths follow agent_clients as compile-context does.
func TestBuiltinArtefacts_Positive_SatisfyTheSchemaAndFollowClients(t *testing.T) {
	builtins, err := builtinArtefacts(&config.Manifest{})
	if err != nil {
		t.Fatal(err)
	}
	decls := make([]config.GeneratedArtefact, 0, len(builtins))
	for _, b := range builtins {
		decls = append(decls, b.decl)
	}
	if err := config.ValidateGenerated(&config.GeneratedPolicy{Artefacts: decls}); err != nil {
		t.Fatalf("a built-in declaration breaks the schema: %v", err)
	}
	claudeOnly, err := builtinArtefacts(&config.Manifest{AgentClients: []string{"claude"}})
	if err != nil {
		t.Fatal(err)
	}
	if paths := strings.Join(claudeOnly[0].decl.Paths, ","); strings.Contains(paths, ".windsurfrules") || !strings.Contains(paths, "CLAUDE.md") {
		t.Fatalf("claude-only projection paths = %q", paths)
	}
	if _, err := builtinArtefacts(&config.Manifest{AgentClients: []string{"no-such-client"}}); err == nil {
		t.Fatal("an unknown agent client must fail as compile-context fails")
	}
}

func assertInactiveReasons(t *testing.T, set *Set, wantReasons map[string]string) {
	t.Helper()
	reasons := map[string]string{}
	for _, artefact := range set.Artefacts {
		if !artefact.Active {
			reasons[artefact.Name] = artefact.Reason
		}
	}
	for name, want := range wantReasons {
		if !strings.Contains(reasons[name], want) {
			t.Errorf("%s: reason %q, want %q", name, reasons[name], want)
		}
	}
	if len(reasons) != len(wantReasons) {
		t.Errorf("inactive = %q", reasons)
	}
}

func assertResolveRefused(t *testing.T, manifest *config.Manifest, tree Tree, wantErr string) {
	t.Helper()
	_, err := Resolve(context.Background(), manifest, tree)
	if err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("Resolve with error %q: got %v", wantErr, err)
	}
}

// Negative (#696): outside a Praetor checkout the Praetor-only artefacts do not apply, and an
// artefact without its required file, its selected files or its block markers does not apply,
// each with its reason. A declined name that is no built-in artefact, and a declared artefact
// that takes a built-in name, are refused.
func TestResolve_Negative_ReasonsAndRefusals(t *testing.T) {
	tree := praetorTree()
	delete(tree, "cmd/standardsctl/main.go")
	delete(tree, ".needs.yaml")
	tree["README.md"] = "# Readme without a block\n"
	set, err := Resolve(context.Background(), nil, tree)
	if err != nil {
		t.Fatal(err)
	}
	wantReasons := map[string]string{
		NameShippedTexts: "not a Praetor source checkout", NameDevContainer: "not a Praetor source checkout",
		NameClientTypes: "not a Praetor source checkout",
		NameNeeds:       ".needs.yaml is absent", NameReadmeBlock: "no file carries its block markers",
	}
	assertInactiveReasons(t, set, wantReasons)
	declined := &config.Manifest{Generated: &config.GeneratedPolicy{Decline: []string{"debt baselin"}}}
	assertResolveRefused(t, declined, tree, `"debt baselin" names no built-in artefact`)
	clash := &config.Manifest{Generated: &config.GeneratedPolicy{Artefacts: []config.GeneratedArtefact{
		{Name: NameBaseline, Paths: []string{"x"}, Command: []string{"m"}, Sources: []string{"s"}}}}}
	assertResolveRefused(t, clash, tree, "takes the name of the built-in artefact")
	broken := memoryTree{"README.md": readmegovernance.Start + "\n"}
	assertResolveRefused(t, nil, broken, "README.md")
}

func assertGlobSelectionBounds(t *testing.T) {
	t.Helper()
	files := make([]string, 0, MaxArtefactFiles+1)
	for index := 0; index < MaxArtefactFiles; index++ {
		files = append(files, fmt.Sprintf("out/%05d.txt", index))
	}
	globs := newGlobSet([]string{"out/*.txt"})
	if selected, err := globs.selectFrom(files, "out"); err != nil || len(selected) != MaxArtefactFiles {
		t.Fatalf("exactly %d files: %d, %v", MaxArtefactFiles, len(selected), err)
	}
	if _, err := globs.selectFrom(append(files, "out/overflow.txt"), "out"); err == nil || !strings.Contains(err.Error(), "more than 4096 files") {
		t.Fatalf("one file over the bound: %v", err)
	}
}

func assertGlobPathDepthBounds(t *testing.T) {
	t.Helper()
	deep := strings.Repeat("d/", maxPathSegments) + "x.txt"
	if newGlobSet([]string{"**/*.txt"}).match(deep) {
		t.Fatal("a path deeper than the segment bound must match no glob")
	}
	if !newGlobSet([]string{"**/*.txt"}).match(strings.Repeat("d/", maxPathSegments-1) + "x.txt") {
		t.Fatal("a path at the segment bound must still match")
	}
}

// Boundary (#696): a declined built-in artefact is listed as declined and not resolved; a glob
// that selects exactly MaxArtefactFiles files passes and one more fails; a path deeper than the
// segment bound matches no glob.
func TestResolve_Boundary_DeclineAndGlobBounds(t *testing.T) {
	manifest := &config.Manifest{Generated: &config.GeneratedPolicy{Decline: []string{NameDevContainer}}}
	set, err := Resolve(context.Background(), manifest, praetorTree())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(set.Declined, ",") != NameDevContainer || len(set.Artefacts) != 8 {
		t.Fatalf("declined = %q, artefacts = %d", set.Declined, len(set.Artefacts))
	}
	assertGlobSelectionBounds(t)
	assertGlobPathDepthBounds(t)
}
