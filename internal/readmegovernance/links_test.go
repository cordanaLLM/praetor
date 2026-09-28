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

// forgeAgentsLinks are the AGENTS.md links each known forge host renders for acme/widgets,
// spelled out rather than read from the code under test.
var forgeAgentsLinks = map[string]string{
	"github.com":   "https://github.com/acme/widgets/blob/HEAD/AGENTS.md",
	"gitlab.com":   "https://gitlab.com/acme/widgets/-/blob/HEAD/AGENTS.md",
	"codeberg.org": "https://codeberg.org/acme/widgets/src/branch/HEAD/AGENTS.md",
	"gitea.com":    "https://gitea.com/acme/widgets/src/branch/HEAD/AGENTS.md",
}

// Positive: on every known forge the HISS badge links AGENTS.md on the default branch of the
// manifest's repository by that forge's absolute URL, with or without the documentation
// contract; LinkedHost reads the host back, and the rendering is idempotent. A fork's
// identity renders the fork's URL.
func TestReconcilePositiveLinksAgentsOnTheKnownForge(t *testing.T) {
	for host, want := range forgeAgentsLinks {
		for _, documentation := range []bool{false, true} {
			state := State{BaselineKnown: true, DocumentationEnabled: documentation,
				RepositoryOwner: "acme", RepositoryName: "widgets", RepositoryHost: host}
			out, _, err := Reconcile("# Widgets\n", state)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, hissBadgeLink+"\n") || !strings.Contains(out, "[praetor-hiss-agents]: "+want+"\n") {
				t.Fatalf("%+v: rendered block lacks the AGENTS.md link %q:\n%s", state, want, out)
			}
			if relative := relativeLinks(blockLinks(t, out)); len(relative) != 0 {
				t.Fatalf("%+v: block links repository-relative paths %q", state, relative)
			}
			if got := LinkedHost(out, "acme", "widgets"); got != host {
				t.Fatalf("%+v: LinkedHost = %q", state, got)
			}
			if again, changed, err := Reconcile(out, state); err != nil || changed || again != out {
				t.Fatalf("%+v: not idempotent: changed=%v err=%v", state, changed, err)
			}
		}
	}
	fork := State{BaselineKnown: true, RepositoryOwner: "fork-owner", RepositoryName: "widgets", RepositoryHost: "github.com"}
	if out, _, err := Reconcile("# Widgets\n", fork); err != nil ||
		!strings.Contains(out, "https://github.com/fork-owner/widgets/blob/HEAD/AGENTS.md\n") {
		t.Fatalf("fork identity does not link the fork's AGENTS.md: %v\n%s", err, out)
	}
}

// Negative: the block the previous renderer wrote links AGENTS.md by a relative path, which
// the link check above detects, and audit reports it stale. A host outside the known forges,
// no host, and a partial or unsafe identity never produce a link, and LinkedHost reads a link
// into another repository or onto an unknown host as none, so audit reports such a block
// stale. The documentation contract still refuses a partial or unsafe identity.
func TestReconcileNegativeRejectsRelativeLinksAndUnknownForges(t *testing.T) {
	previous := "# Widgets\n\n" + previousBlock + "\n"
	if relative := relativeLinks(blockLinks(t, previous)); len(relative) != 1 || relative[0] != "AGENTS.md" {
		t.Fatalf("link check misses the previous block's relative link: %q", relative)
	}
	if err := Verify(previous, State{BaselineKnown: true}); !errors.Is(err, ErrStale) {
		t.Fatalf("previous block verified: %v", err)
	}
	for _, state := range []State{
		{BaselineKnown: true, RepositoryOwner: "acme", RepositoryName: "widgets", RepositoryHost: "git.example.org"},
		{BaselineKnown: true, RepositoryOwner: "acme", RepositoryName: "widgets"},
		{BaselineKnown: true, RepositoryOwner: "acme", RepositoryHost: "github.com"},
		{BaselineKnown: true, RepositoryOwner: "acme/evil", RepositoryName: "widgets", RepositoryHost: "gitlab.com"},
		{BaselineKnown: true, RepositoryOwner: "acme", RepositoryName: "widgets)evil", RepositoryHost: "github.com"},
	} {
		out, _, err := Reconcile("# Widgets\n", state)
		if err != nil || strings.Contains(out, hissAgentsRef) || !strings.Contains(out, "\n"+hissBadgeImage+"\n") {
			t.Fatalf("%+v: rendered a link or failed: %v\n%s", state, err, out)
		}
	}
	linked, _, err := Reconcile("# Widgets\n", State{BaselineKnown: true, RepositoryOwner: "acme", RepositoryName: "widgets", RepositoryHost: "gitlab.com"})
	if err != nil {
		t.Fatal(err)
	}
	foreignHost := strings.Replace(linked, "https://gitlab.com/", "https://git.example.org/", 1)
	for name, tc := range map[string]struct{ content, owner string }{
		"another repository": {linked, "example"},
		"unknown host":       {foreignHost, "acme"},
	} {
		host := LinkedHost(tc.content, tc.owner, "widgets")
		state := State{BaselineKnown: true, RepositoryOwner: tc.owner, RepositoryName: "widgets", RepositoryHost: host}
		if host != "" || !errors.Is(Verify(tc.content, state), ErrStale) {
			t.Fatalf("%s: LinkedHost = %q, and the block must verify as stale", name, host)
		}
	}
	for _, identity := range [][2]string{{"acme", ""}, {"acme/evil", "widgets"}, {"acme", "widgets)evil"}} {
		state := State{BaselineKnown: true, DocumentationEnabled: true, RepositoryOwner: identity[0], RepositoryName: identity[1]}
		if _, _, err := Reconcile("# Widgets\n", state); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("identity %q/%q under documentation rendered: %v", identity[0], identity[1], err)
		}
	}
}

// Boundary: without a known forge the badge is an unlinked image, so no state renders a
// repository-relative link or a guessed forge URL, with the documentation contract too (its
// workflow badge is the only link); plain Reconcile refreshes the previous block in place,
// keeping the text around it, with no --force equivalent: the whole marker region is
// Praetor's. A README without a block, or with broken markers, has no linked host.
func TestReconcileBoundaryUnlinkedBadgeAndPreviousBlockRefresh(t *testing.T) {
	state := State{BaselineKnown: true}
	out, _, err := Reconcile("# Widgets\n", state)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "\n"+hissBadgeImage+"\n") || strings.Contains(out, hissAgentsRef) {
		t.Fatalf("block without a known forge must render the badge unlinked:\n%s", out)
	}
	if links := blockLinks(t, out); len(links) != 1 || len(relativeLinks(links)) != 0 {
		t.Fatalf("block without a known forge links %q, want only the badge image", links)
	}
	documentation := State{BaselineKnown: true, DocumentationEnabled: true, RepositoryOwner: "acme", RepositoryName: "widgets"}
	if docs, _, err := Reconcile("# Widgets\n", documentation); err != nil || strings.Contains(docs, hissAgentsRef) {
		t.Fatalf("documentation contract without a known forge linked the HISS badge: %v\n%s", err, docs)
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
	for _, content := range []string{"# Widgets\n", Start + "\n" + Start + "\n" + End + "\n", "a\r\nb\n"} {
		if host := LinkedHost(content, "acme", "widgets"); host != "" {
			t.Fatalf("LinkedHost(%q) = %q, want none", content, host)
		}
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
		t.Skip("mkdocs is not on PATH; TestReconcilePositiveLinksAgentsOnTheKnownForge asserts the link shape instead")
	}
	for name, state := range map[string]State{
		"unlinked": {BaselineKnown: true},
		"documentation": {BaselineKnown: true, DocumentationEnabled: true, RepositoryOwner: "acme", RepositoryName: "widgets",
			RepositoryHost: "github.com"},
		"gitlab": {BaselineKnown: true, RepositoryOwner: "acme", RepositoryName: "widgets", RepositoryHost: "gitlab.com"},
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
