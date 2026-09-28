package readmegovernance

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// previousBlock is the block the renderer before #506 wrote for State{BaselineKnown: true}:
// the HISS badge linked AGENTS.md by a repository-relative path.
const previousBlock = Start + "\n" +
	"[![HISS Adopted][praetor-hiss-badge]](AGENTS.md)\n\n" +
	"Praetor manages this repository's declared governance policy. This managed\n" +
	"block records adoption state; it is not a verification certificate.\n\n" +
	"**Verification**: `make verify-all` runs the repository's configured\n" +
	"verification cascade.\n\n" +
	"**HISS Audit**: `praetorctl audit` enforces policy, generated-surface\n" +
	"integrity, and the debt ratchet.\n\n" +
	"**Context Sync**: `praetorctl compile-context --verify` verifies every\n" +
	"generated agent context against `AGENTS.md`.\n\n" +
	"**Debt Baseline**: `.standards-baseline.json` anchors the debt ratchet at\n" +
	"0 recorded infractions; audit forbids growth.\n\n" +
	"[praetor-hiss-badge]: https://img.shields.io/badge/Standards-HISS%20Adopted-blue\n" +
	End

// linkDestination captures the destination of an inline link or image, "](dest)", and of a
// link reference definition, "[label]: dest".
var linkDestination = regexp.MustCompile(`\]\(([^)\s]+)[^)]*\)|(?m)^\[[^\]]+\]:\s+(\S+)`)

// blockLinks returns every link destination inside the managed block of md.
func blockLinks(t *testing.T, md string) []string {
	t.Helper()
	start, end := strings.Index(md, Start), strings.Index(md, End)
	if start < 0 || end < start {
		t.Fatalf("no managed block:\n%s", md)
	}
	var links []string
	for _, match := range linkDestination.FindAllStringSubmatch(md[start:end], -1) {
		links = append(links, match[1]+match[2])
	}
	return links
}

// relativeLinks returns the destinations of links that are not absolute https URLs.
func relativeLinks(links []string) []string {
	var relative []string
	for _, link := range links {
		if !strings.HasPrefix(link, "https://") {
			relative = append(relative, link)
		}
	}
	return relative
}

// Positive: with an identity, documentation gate or not, the HISS badge links AGENTS.md on
// the default branch of that repository by absolute URL, and a fork's identity renders the
// fork's URL; the rendering is idempotent.
func TestReconcilePositiveLinksAgentsByAbsoluteURL(t *testing.T) {
	for _, identity := range [][2]string{{"acme", "widgets"}, {"fork-owner", "widgets"}} {
		for _, docs := range []bool{false, true} {
			state := State{BaselineKnown: true, DocumentationEnabled: docs, RepositoryOwner: identity[0], RepositoryName: identity[1]}
			out, _, err := Reconcile("# Widgets\n", state)
			if err != nil {
				t.Fatal(err)
			}
			want := "[praetor-hiss-agents]: https://github.com/" + identity[0] + "/" + identity[1] + "/blob/HEAD/AGENTS.md\n"
			if !strings.Contains(out, hissBadgeLink+"\n") || !strings.Contains(out, want) {
				t.Fatalf("%+v: rendered block lacks the absolute AGENTS.md link %q:\n%s", state, want, out)
			}
			if relative := relativeLinks(blockLinks(t, out)); len(relative) != 0 {
				t.Fatalf("%+v: block links repository-relative paths %q", state, relative)
			}
			if again, changed, err := Reconcile(out, state); err != nil || changed || again != out {
				t.Fatalf("%+v: not idempotent: changed=%v err=%v", state, changed, err)
			}
		}
	}
}

// Negative: the block the previous renderer wrote links AGENTS.md by a relative path, which
// the link check above detects; audit reports it stale; and a partial or unsafe identity is
// refused even without the documentation gate, where it now feeds the AGENTS.md link.
func TestReconcileNegativeRejectsRelativeLinksAndPartialIdentity(t *testing.T) {
	previous := "# Widgets\n\n" + previousBlock + "\n"
	if relative := relativeLinks(blockLinks(t, previous)); len(relative) != 1 || relative[0] != "AGENTS.md" {
		t.Fatalf("link check misses the previous block's relative link: %q", relative)
	}
	if err := Verify(previous, State{BaselineKnown: true}); !errors.Is(err, ErrStale) {
		t.Fatalf("previous block verified: %v", err)
	}
	for _, identity := range [][2]string{{"acme", ""}, {"", "widgets"}, {"acme/evil", "widgets"}, {"acme", "widgets)evil"}} {
		state := State{BaselineKnown: true, RepositoryOwner: identity[0], RepositoryName: identity[1]}
		if _, _, err := Reconcile("# Widgets\n", state); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("identity %q/%q without documentation rendered: %v", identity[0], identity[1], err)
		}
	}
}

// Boundary: without an identity the badge is an unlinked image, so no state renders a
// repository-relative link; plain Reconcile refreshes the previous block in place, keeping
// the text around it, with no --force equivalent: the whole marker region is Praetor's.
func TestReconcileBoundaryUnlinkedBadgeAndPreviousBlockRefresh(t *testing.T) {
	state := State{BaselineKnown: true}
	out, _, err := Reconcile("# Widgets\n", state)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "\n"+hissBadgeImage+"\n") || strings.Contains(out, hissAgentsRef) {
		t.Fatalf("identity-free block must render the badge unlinked:\n%s", out)
	}
	if links := blockLinks(t, out); len(links) != 1 || len(relativeLinks(links)) != 0 {
		t.Fatalf("identity-free block links %q, want only the badge image", links)
	}
	previous := "# Widgets\n\n" + previousBlock + "\n\nHuman tail.\n"
	refreshed, changed, err := Reconcile(previous, state)
	if err != nil || !changed {
		t.Fatalf("previous block not refreshed: changed=%v err=%v", changed, err)
	}
	if want := strings.Replace(out, End+"\n", End+"\n\nHuman tail.\n", 1); refreshed != want {
		t.Fatalf("refresh differs from a fresh rendering:\n%s\nwant:\n%s", refreshed, want)
	}
	if err := Verify(refreshed, state); err != nil {
		t.Fatalf("refreshed block fails audit: %v", err)
	}
}

// mkdocsBuildTimeout bounds one strict portal build (HISS-02).
const mkdocsBuildTimeout = 2 * time.Minute

// buildPortal writes readme as the index page of a minimal MkDocs portal, the way a
// documentation site includes its README, and runs a strict build. It returns the build's
// combined output and error.
func buildPortal(t *testing.T, mkdocs, readme string) (string, error) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mkdocs.yml"), []byte("site_name: Portal\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "index.md"), []byte(readme), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), mkdocsBuildTimeout)
	defer cancel()
	// --strict without -q: quiet mode hides the warnings strict mode counts.
	cmd := exec.CommandContext(ctx, mkdocs, "build", "--strict", "--config-file", filepath.Join(root, "mkdocs.yml"),
		"--site-dir", filepath.Join(root, "site"))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Positive and negative against the real consumer: a strict MkDocs build of a portal whose
// index page is the README passes for every rendering and aborts on the previous block's
// relative AGENTS.md link. Hosts without mkdocs on PATH skip it; the link-shape assertions
// above hold the same contract there.
func TestRenderedBlockBuildsInStrictMkDocsPortal(t *testing.T) {
	mkdocs, err := exec.LookPath("mkdocs")
	if err != nil {
		t.Skip("mkdocs is not on PATH; TestReconcilePositiveLinksAgentsByAbsoluteURL asserts the link shape instead")
	}
	for name, state := range map[string]State{
		"no identity":   {BaselineKnown: true},
		"identity":      {BaselineKnown: true, RepositoryOwner: "acme", RepositoryName: "widgets"},
		"documentation": {BaselineKnown: true, DocumentationEnabled: true, RepositoryOwner: "acme", RepositoryName: "widgets"},
	} {
		readme, _, err := Reconcile("# Widgets\n\nHuman text.\n", state)
		if err != nil {
			t.Fatal(err)
		}
		if out, err := buildPortal(t, mkdocs, readme); err != nil {
			t.Fatalf("%s: strict portal build failed: %v\n%s", name, err, out)
		}
	}
	out, err := buildPortal(t, mkdocs, "# Widgets\n\n"+previousBlock+"\n")
	if err == nil || !strings.Contains(out, "'AGENTS.md'") {
		t.Fatalf("strict portal build accepted the previous block's relative link: %v\n%s", err, out)
	}
}
