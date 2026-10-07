// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// radarAcceptance is the #818 acceptance fixture internal/radar ships: a registry and one planted
// file per source.
var radarAcceptance = filepath.Join("..", "..", "internal", "radar", "testdata", "acceptance")

// radarRepo builds a repository whose manifest declares the acceptance registry under
// .config/radar.yaml and returns its root.
func radarRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	registry, err := os.ReadFile(filepath.Join(radarAcceptance, "radar.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, ".standards.yaml", "version: 1\nradar:\n  registry: .config/radar.yaml\n")
	writeFixtureFile(t, root, ".config/radar.yaml", string(registry))
	return root
}

// collectArgs are the arguments of a collect over the acceptance fixture ending at now.
func collectArgs(root, fixture, now, out string) []string {
	return []string{"collect", "--path=" + root, "--fixture=" + fixture, "--now=" + now, "--days=7", "--out=" + out}
}

// Positive: validate passes the declared registry and a --registry file; collect over the
// acceptance fixture writes a digest listing the new repository, the push, the paper and the
// release, and names the read path.
func TestRadarCommand_Positive_ValidateAndCollect(t *testing.T) {
	root := radarRepo(t)
	out, err := captureStdout(t, func() error { return dispatchCommand("radar", []string{"validate", "--path=" + root}) })
	if err != nil || !strings.Contains(out, "[PASS] radar registry .config/radar.yaml: 3 sources (feed: 2, github_repo: 1)") {
		t.Fatalf("validate = %q, %v", out, err)
	}
	direct := filepath.Join(radarAcceptance, "radar.yaml")
	if out, err := captureStdout(t, func() error { return dispatchCommand("radar", []string{"validate", "--registry", direct}) }); err != nil ||
		!strings.Contains(out, "[PASS] radar registry "+direct) {
		t.Fatalf("validate --registry = %q, %v", out, err)
	}
	digestPath := filepath.Join(t.TempDir(), "digest.md")
	out, err = captureStdout(t, func() error {
		return dispatchCommand("radar", collectArgs(root, radarAcceptance, "2026-10-07", digestPath))
	})
	if err != nil || !strings.Contains(out, "Read path: planted fixtures in") || !strings.Contains(out, "[PASS] wrote "+digestPath+": 4 items from 3 sources") {
		t.Fatalf("collect = %q, %v", out, err)
	}
	digest, err := os.ReadFile(digestPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"created repository example-org/new-parser", "pushed to main", "Linear-time byte-pair merging", "[v2.0.0]"} {
		if !strings.Contains(string(digest), want) {
			t.Errorf("digest lacks %q:\n%s", want, digest)
		}
	}
}

// Negative: an invalid registry names the field; a manifest without a radar section, a collect
// without --fixture, --out or --now, an invalid day count, a fixture that is no directory, stray
// positionals and an unknown subcommand all fail; when every source fails the digest is still
// written, naming each, and the command fails.
func TestRadarCommand_Negative_Refusals(t *testing.T) {
	root := t.TempDir()
	bad := writeFixtureFile(t, root, "bad.yaml", "version: 1\nsources:\n  - id: a\n    kind: query\n    url: https://example.org/a\n    why: x\n")
	if err := dispatchCommand("radar", []string{"validate", "--registry=" + bad}); err == nil || !strings.Contains(err.Error(), "sources[0].kind") {
		t.Errorf("invalid registry: %v", err)
	}
	writeFixtureFile(t, root, ".standards.yaml", "version: 1\n")
	if err := dispatchCommand("radar", []string{"validate", "--path=" + root}); err == nil || !strings.Contains(err.Error(), "declares no radar section") {
		t.Errorf("no radar section: %v", err)
	}
	repo := radarRepo(t)
	out := filepath.Join(t.TempDir(), "digest.md")
	for name, args := range map[string][]string{
		"no fixture":   {"collect", "--path=" + repo, "--now=2026-10-07", "--out=" + out},
		"no out":       {"collect", "--path=" + repo, "--fixture=" + radarAcceptance, "--now=2026-10-07"},
		"no now":       {"collect", "--path=" + repo, "--fixture=" + radarAcceptance, "--out=" + out},
		"zero days":    {"collect", "--path=" + repo, "--fixture=" + radarAcceptance, "--now=2026-10-07", "--days=0", "--out=" + out},
		"file fixture": {"collect", "--path=" + repo, "--fixture=" + bad, "--now=2026-10-07", "--out=" + out},
		"positional":   {"validate", "--path=" + repo, "extra"},
		"unknown":      {"publish"},
	} {
		if err := dispatchCommand("radar", args); err == nil {
			t.Errorf("%s: radar %v succeeded", name, args)
		}
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("a refused collect wrote %s: %v", out, err)
	}
	err := dispatchCommand("radar", collectArgs(repo, t.TempDir(), "2026-10-07", out))
	if err == nil || !strings.Contains(err.Error(), "every radar source failed (3 of 3)") {
		t.Fatalf("all sources failed: %v", err)
	}
	digest, readErr := os.ReadFile(out)
	if readErr != nil || strings.Count(string(digest), " is missing: file does not exist") != 3 {
		t.Errorf("digest of a failed run = %q, %v", digest, readErr)
	}
}

// Boundary: no arguments and --help print usage; a window with nothing new writes an empty
// digest; one failing source, a planted feed with a document type declaration, is named in the
// digest while the command succeeds.
func TestRadarCommand_Boundary_EmptyAndPartial(t *testing.T) {
	for _, args := range [][]string{{}, {"--help"}} {
		out, err := captureStdout(t, func() error { return dispatchCommand("radar", args) })
		if err != nil || !strings.Contains(out, "Usage: praetorctl radar") {
			t.Errorf("radar %v = %q, %v", args, out, err)
		}
	}
	repo := radarRepo(t)
	empty := filepath.Join(t.TempDir(), "empty.md")
	out, err := captureStdout(t, func() error { return dispatchCommand("radar", collectArgs(repo, radarAcceptance, "2026-11-07", empty)) })
	if info, statErr := os.Stat(empty); err != nil || statErr != nil || info.Size() != 0 || !strings.Contains(out, "wrote an empty digest") {
		t.Fatalf("unchanged window: %q, %v, %v", out, err, statErr)
	}
	fixture := t.TempDir()
	for _, name := range []string{"example-org-activity.xml", "example-project.json"} {
		data, err := os.ReadFile(filepath.Join(radarAcceptance, name))
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, fixture, name, string(data))
	}
	writeFixtureFile(t, fixture, "example-papers.xml", `<!DOCTYPE rss [<!ENTITY x "boom">]><rss/>`)
	partial := filepath.Join(t.TempDir(), "partial.md")
	out, err = captureStdout(t, func() error { return dispatchCommand("radar", collectArgs(repo, fixture, "2026-10-07", partial)) })
	if err != nil || !strings.Contains(out, "1 of 3 sources failed and are named in the digest: example-papers") {
		t.Fatalf("one failing source: %q, %v", out, err)
	}
	digest, err := os.ReadFile(partial)
	if err != nil || !strings.Contains(string(digest), "- example-papers: radar feed: document type declarations are refused") {
		t.Errorf("partial digest = %q, %v", digest, err)
	}
}
