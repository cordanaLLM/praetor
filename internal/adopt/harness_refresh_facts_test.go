// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/paperclip"
)

const (
	// pinnedReceiptSection pins a well-formed receipt key, as `praetorctl gate keygen` advises.
	pinnedReceiptSection = "receipt:\n  public_key: \"" +
		"abababababababababababababababababababababababababababababababab" + "\"\n"
	pinnedReceiptClaim   = "without `--dry-run` mints one; attach minted receipt to PR proposal"
	unpinnedReceiptClaim = "attach no receipt"
	rustInvariantClaim   = "Rust: zero `.unwrap()`"
	cargoManifest        = "[package]\nname = \"widget\"\nversion = \"0.1.0\"\n"
)

// adoptedGoWidget adopts a Go repository with plain adopt and returns it with the harness it
// wrote, which prescribes no receipt and names no Rust construct.
func adoptedGoWidget(t *testing.T) (string, string) {
	t.Helper()
	repo := newTestRepo(t, "widget")
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repo, false)); err != nil {
		t.Fatal(err)
	}
	harness := mustRead(t, filepath.Join(repo, paperclipFile))
	if !strings.Contains(harness, unpinnedReceiptClaim) || strings.Contains(harness, rustInvariantClaim) {
		t.Fatalf("fixture precondition: first adoption harness:\n%s", harness)
	}
	return repo, harness
}

// appendManifest appends text to the adopted manifest, as an operator editing it does.
func appendManifest(t *testing.T, repo, text string) {
	t.Helper()
	path := filepath.Join(repo, manifestFile)
	mustWrite(t, path, mustRead(t, path)+text)
}

// TestAdoptRefreshesHarnessAfterFactsChange: Positive. The harness a plain adopt wrote is
// still unmodified output after the operator pins receipt.public_key, as its receipt row
// advises, or adds a language; the next plain adopt refreshes it without --force to the row
// that requires receipts and the new language's clauses, and re-binds the source contract
// (docs/guides/adoption-verification.md, Paperclip harness).
func TestAdoptRefreshesHarnessAfterFactsChange(t *testing.T) {
	cases := map[string]struct {
		change func(t *testing.T, repo string)
		want   string
	}{
		"pin receipt key": {func(t *testing.T, repo string) { appendManifest(t, repo, pinnedReceiptSection) }, pinnedReceiptClaim},
		"Go gains Rust": {func(t *testing.T, repo string) {
			mustWrite(t, filepath.Join(repo, "Cargo.toml"), cargoManifest)
		}, rustInvariantClaim},
		"Go gains C with cleanup-goto exception": {func(t *testing.T, repo string) {
			mustWrite(t, filepath.Join(repo, "meson.build"), cMarkers["meson.build"])
			mustWrite(t, filepath.Join(repo, "docs", "cleanup-goto.md"), cleanupGotoDocument)
			appendManifest(t, repo, "hiss:\n  exceptions:\n    c_goto_cleanup: docs/cleanup-goto.md\n")
		}, cleanupGotoClause},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			repo, _ := adoptedGoWidget(t)
			tc.change(t, repo)
			report, err := Adopt(t.Context(), sourceAdoptOptions(t, repo, false))
			if err != nil {
				t.Fatal(err)
			}
			if got := mustRead(t, filepath.Join(repo, paperclipFile)); !strings.Contains(got, tc.want) {
				t.Fatalf("plain adopt kept the harness of the earlier facts:\n%s", got)
			}
			if rules := mustRead(t, filepath.Join(repo, ".paperclip", "rules.md")); !strings.Contains(strings.Join(strings.Fields(rules), " "), tc.want) {
				t.Fatalf("rules.md not refreshed beside harness.json:\n%s", rules)
			}
			if !strings.Contains(reportDetail(report, paperclipFile), "Refreshed unmodified earlier") {
				t.Fatalf("refresh not reported: %q", reportDetail(report, paperclipFile))
			}
			requirePassingSourceGate(t, repo, paperclipFile)
		})
	}
}

// TestAdoptKeepsHandEditedHarnessAfterFactsChange: Negative. A harness the operator edited
// stays operator-owned byte for byte when the key is pinned, and the contract binds to it. An
// edit to the HISS-04 function length alone, made in harness.json and rules.md alike, is such
// an edit too: the refresh key takes no number from the harness on disk.
func TestAdoptKeepsHandEditedHarnessAfterFactsChange(t *testing.T) {
	edits := map[string]func(t *testing.T, repo, harness string) string{
		"operating contract": func(_ *testing.T, _, harness string) string {
			return strings.Replace(harness, "prevent duplicate PRs", "prevent duplicate PRs; ping reviewer", 1)
		},
		"function length only": func(t *testing.T, repo, harness string) string {
			rulesPath := filepath.Join(repo, ".paperclip", "rules.md")
			mustWrite(t, rulesPath, editedFuncLOC(t, mustRead(t, rulesPath), `func\s+LOC <= `))
			return editedFuncLOC(t, harness, `func LOC \\u003c= `)
		},
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			repo, harness := adoptedGoWidget(t)
			edited := edit(t, repo, harness)
			if edited == harness {
				t.Fatal("fixture edit missed")
			}
			mustWrite(t, filepath.Join(repo, paperclipFile), edited)
			// The operator who edits the harness drops the contract bound to the generated
			// bytes, which adoption writes last, so the next run binds the edit instead of
			// refusing it.
			path := filepath.Join(repo, manifestFile)
			manifest := mustRead(t, path)
			cut := strings.Index(manifest, "\nregister:\n")
			if cut < 0 {
				t.Fatalf("fixture precondition: adopted manifest binds no register.sources:\n%s", manifest)
			}
			mustWrite(t, path, manifest[:cut+1]+pinnedReceiptSection)
			if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repo, false)); err != nil {
				t.Fatal(err)
			}
			if got := mustRead(t, filepath.Join(repo, paperclipFile)); got != edited {
				t.Fatalf("hand-edited harness rewritten:\n%s", got)
			}
			requirePassingSourceGate(t, repo, paperclipFile)
		})
	}
}

// editedFuncLOC rewrites the HISS-04 function length that prefix introduces in text to 45, as
// an operator's hand edit of the number alone would; rules.md may wrap the clause.
func editedFuncLOC(t *testing.T, text, prefix string) string {
	t.Helper()
	stated := regexp.MustCompile(`(` + prefix + `)[0-9]+`)
	if !stated.MatchString(text) {
		t.Fatalf("fixture precondition: no function length after %q in:\n%s", prefix, text)
	}
	return stated.ReplaceAllString(text, "${1}45")
}

// unconditionalPinnedRow is the pinned receipt row #523 wrote; a gate run with --dry-run mints
// nothing, so the row was replaced by the conditional one (#550).
const unconditionalPinnedRow = "Ed25519 Exit-0 Receipts: mint via `praetorctl gate run`; attach receipt to every PR proposal."

// writeUnconditionalPinnedHarness writes this release's harness for repo with the #523 pinned
// row in place of the current one, harness.json beside its rules.md, as the release before
// #550 wrote it, and returns harness.json.
func writeUnconditionalPinnedHarness(t *testing.T, repo string) string {
	t.Helper()
	h, _, err := paperclip.SynthesizeHarness(t.Context(), repo, hisscatalog.Facts{})
	if err != nil {
		t.Fatal(err)
	}
	for index, row := range h.OperatingContract {
		if strings.HasPrefix(row, "Ed25519 Exit-0 Receipts:") {
			h.OperatingContract[index] = unconditionalPinnedRow
		}
	}
	if err := paperclip.WriteHarness(h, repo); err != nil {
		t.Fatal(err)
	}
	return mustRead(t, filepath.Join(repo, paperclipFile))
}

// TestAdoptRefreshesUnconditionalReceiptRows: Boundary (#550). Plain adopt refreshes, without
// --force, an unmodified harness carrying either earlier unconditional receipt claim, key
// pinned: the 462e3f3a release's row and the #523 pinned row, each beside its rules.md and with
// the contract bound to it. Both rendered forms then carry the conditional row and neither
// claim, and the contract is re-bound to text that passes Caveman.
func TestAdoptRefreshesUnconditionalReceiptRows(t *testing.T) {
	cases := map[string]struct {
		write func(t *testing.T, repo string) string
		claim string
	}{
		"462e3f3a release": {func(t *testing.T, repo string) string {
			mustWrite(t, filepath.Join(repo, ".paperclip", "rules.md"), releasedHarness(t, "rules.md.golden"))
			harness := releasedHarness(t, "harness.json.golden")
			mustWrite(t, filepath.Join(repo, paperclipFile), harness)
			return harness
		}, "attach cryptographic execution receipts to all PR proposals"},
		"#523 pinned row": {writeUnconditionalPinnedHarness, "attach receipt to every PR proposal"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			repo := newTestRepo(t, "legacy")
			mustWrite(t, filepath.Join(repo, manifestFile), legacyManifest+pinnedReceiptSection)
			harness := tc.write(t, repo)
			bound, err := managedRegisterSources(t.Context(), []byte(harness))
			if err != nil || !strings.Contains(harness, tc.claim) {
				t.Fatalf("fixture precondition: %v\n%s", err, harness)
			}
			mustWrite(t, filepath.Join(repo, manifestFile), legacyManifest+pinnedReceiptSection+manifestSourcesYAML(t, bound))
			report, err := Adopt(t.Context(), sourceAdoptOptions(t, repo, false))
			if err != nil {
				t.Fatal(err)
			}
			forms := map[string]string{
				paperclipFile:         mustRead(t, filepath.Join(repo, paperclipFile)),
				".paperclip/rules.md": strings.Join(strings.Fields(mustRead(t, filepath.Join(repo, ".paperclip", "rules.md"))), " "),
			}
			for form, text := range forms {
				if !strings.Contains(text, pinnedReceiptClaim) || strings.Contains(text, tc.claim) {
					t.Fatalf("%s not refreshed to the conditional receipt row:\n%s", form, text)
				}
			}
			if !strings.Contains(reportDetail(report, paperclipFile), "Refreshed unmodified earlier") {
				t.Fatalf("refresh not reported: %q", reportDetail(report, paperclipFile))
			}
			if got := requirePassingSourceGate(t, repo, paperclipFile); got.SHA256 == bound.SHA256 {
				t.Fatal("contract still bound to the earlier harness digest")
			}
		})
	}
}

// TestAdoptHarnessRefreshFactBoundary: Boundary. Unchanged facts leave the harness byte for
// byte; pinning the key and adding a language in one step still refreshes, and removing the key
// again returns to the unpinned row, so every step of the round trip stays generated output.
func TestAdoptHarnessRefreshFactBoundary(t *testing.T) {
	repo, harness := adoptedGoWidget(t)
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repo, false)); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(repo, paperclipFile)); got != harness {
		t.Fatalf("unchanged facts rewrote the harness:\n%s", got)
	}
	appendManifest(t, repo, pinnedReceiptSection)
	mustWrite(t, filepath.Join(repo, "Cargo.toml"), cargoManifest)
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repo, false)); err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, filepath.Join(repo, paperclipFile))
	if !strings.Contains(got, pinnedReceiptClaim) || !strings.Contains(got, rustInvariantClaim) {
		t.Fatalf("combined fact change not refreshed:\n%s", got)
	}
	mustWrite(t, filepath.Join(repo, manifestFile), strings.Replace(mustRead(t, filepath.Join(repo, manifestFile)), pinnedReceiptSection, "", 1))
	if err := os.Remove(filepath.Join(repo, "Cargo.toml")); err != nil {
		t.Fatal(err)
	}
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repo, false)); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(repo, paperclipFile)); got != harness {
		t.Fatalf("round trip to the first facts did not restore the first harness:\n%s", got)
	}
}
