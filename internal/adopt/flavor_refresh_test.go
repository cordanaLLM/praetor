// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"strings"
	"testing"
)

// #567: a plain adoption refreshes the rustfmt.toml an earlier release scaffolded, which named
// edition 2024 whatever the crates declared, to the edition Cargo.toml gives the workspace.

// earlierRustfmt is the rustfmt.toml earlier releases scaffolded into every Rust repository.
const earlierRustfmt = "edition = \"2024\"\nmax_width = 100\nnewline_style = \"Unix\"\nuse_small_heuristics = \"Default\"\n"

// rustWorkspace is a Cargo workspace on edition 2021 holding rustfmt, or no rustfmt.toml when
// rustfmt is empty.
func rustWorkspace(rustfmt string) map[string]string {
	files := map[string]string{
		"Cargo.toml":             "[workspace]\nmembers = [\"crates/core\"]\n\n[workspace.package]\nedition = \"2021\"\n",
		"crates/core/Cargo.toml": "[package]\nname = \"core\"\nedition.workspace = true\n",
	}
	if rustfmt != "" {
		files["rustfmt.toml"] = rustfmt
	}
	return files
}

// Positive: the earlier scaffold is refreshed to the workspace edition and reconciled as an
// earlier Praetor text, neither created nor warned about.
func TestReconcileWorkingDirAndFlavor_Positive_RefreshesTheEarlierRustfmt(t *testing.T) {
	s := flavorSession(t, false, rustWorkspace(earlierRustfmt))
	if err := reconcileWorkingDirAndFlavor(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(earlierRustfmt, "2024", "2021", 1)
	if got := mustRead(t, filepath.Join(s.repoPath, "rustfmt.toml")); got != want {
		t.Fatalf("rustfmt.toml = %q, want %q", got, want)
	}
	detail := findActionDetail(s.report.ActionDetails, "rustfmt.toml")
	if !contains(s.report.ReconciledFiles, "rustfmt.toml") || contains(s.report.CreatedFiles, "rustfmt.toml") ||
		detail != "Refreshed an earlier Praetor text to the current rust-systems flavor template" {
		t.Fatalf("refresh not reported as a reconcile: detail %q, report %+v", detail, s.report)
	}
	if len(s.report.Warnings) != 0 || len(s.report.Errors) != 0 {
		t.Fatalf("unexpected warnings %v errors %v", s.report.Warnings, s.report.Errors)
	}
}

// Negative: an edited scaffold is the repository's and stays, recorded as a kept file only.
func TestReconcileWorkingDirAndFlavor_Negative_EditedRustfmtIsKept(t *testing.T) {
	edited := earlierRustfmt + "imports_granularity = \"Crate\"\n"
	s := flavorSession(t, false, rustWorkspace(edited))
	if err := reconcileWorkingDirAndFlavor(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(s.repoPath, "rustfmt.toml")); got != edited {
		t.Fatalf("the edited rustfmt.toml was rewritten: %q", got)
	}
	if contains(s.report.ReconciledFiles, "rustfmt.toml") ||
		!strings.HasPrefix(findActionDetail(s.report.ActionDetails, "rustfmt.toml"), "Existing file kept") {
		t.Fatalf("an edited rustfmt.toml was reported refreshed: %+v", s.report)
	}
}

// Boundary: a workspace without rustfmt.toml gets one on its edition, recorded as created.
func TestReconcileWorkingDirAndFlavor_Boundary_FreshRustfmtNamesTheWorkspaceEdition(t *testing.T) {
	s := flavorSession(t, false, rustWorkspace(""))
	if err := reconcileWorkingDirAndFlavor(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(s.repoPath, "rustfmt.toml")); !strings.HasPrefix(got, "edition = \"2021\"\n") {
		t.Fatalf("rustfmt.toml does not follow the workspace edition: %q", got)
	}
	if !contains(s.report.CreatedFiles, "rustfmt.toml") || contains(s.report.ReconciledFiles, "rustfmt.toml") {
		t.Fatalf("a fresh rustfmt.toml was not reported created: %+v", s.report)
	}
}
