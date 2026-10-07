// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package radar

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// sourceYAML renders one registry entry.
func sourceYAML(id string, kind Kind, rawURL, why string) string {
	return fmt.Sprintf("  - id: %q\n    kind: %q\n    url: %q\n    why: %q\n", id, kind, rawURL, why)
}

// registryYAML renders a version-1 registry holding entries.
func registryYAML(entries ...string) string {
	return "version: 1\nsources:\n" + strings.Join(entries, "")
}

// validFeed and validRepo are well-formed entries of the two supported kinds.
var (
	validFeed = sourceYAML("example-blog", KindFeed, "https://example.org/feed.xml", "Release notes of a parser we vendor")
	validRepo = sourceYAML("example-project", KindGitHubRepo, "https://github.com/example-org/example-project", "Upstream of our tokenizer")
)

// Positive: a registry with one source of each supported kind parses and loads from a file.
func TestRegistry_Positive_ParseAndLoad(t *testing.T) {
	registry, err := ParseRegistry([]byte(registryYAML(validFeed, validRepo)))
	if err != nil {
		t.Fatalf("ParseRegistry: %v", err)
	}
	if len(registry.Sources) != 2 || registry.Sources[0].Kind != KindFeed || registry.Sources[1].ID != "example-project" {
		t.Fatalf("sources = %+v", registry.Sources)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "radar.yaml"), []byte(registryYAML(validFeed)), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRegistry(dir, "radar.yaml")
	if err != nil || len(loaded.Sources) != 1 {
		t.Fatalf("LoadRegistry = %+v, %v", loaded, err)
	}
}

// registryRefusals are registries the parser must refuse, each with the text the error must
// carry: the field it names, or the reason.
var registryRefusals = []struct {
	name, body, want string
	sentinel         error
}{
	{"duplicate id", registryYAML(validFeed, validFeed), "sources[1].id: \"example-blog\" duplicates sources[0].id", nil},
	{"unknown kind", registryYAML(sourceYAML("a", "carrier_pigeon", "https://example.org/a", "why")), "sources[0].kind", ErrUnknownKind},
	{"planned kind", registryYAML(sourceYAML("a", "github_owner", "https://github.com/example-org/x", "why")), "sources[0].kind", ErrUnsupportedKind},
	{"missing kind", registryYAML(sourceYAML("a", "", "https://example.org/a", "why")), "sources[0].kind: required", nil},
	{"plain http", registryYAML(sourceYAML("a", KindFeed, "http://example.org/feed.xml", "why")), "sources[0].url", nil},
	{"query", registryYAML(sourceYAML("a", KindFeed, "https://example.org/feed?q=x", "why")), "sources[0].url", nil},
	{"credentials", registryYAML(sourceYAML("a", KindFeed, "https://user:pw@example.org/feed", "why")), "sources[0].url", nil},
	{"repo off github", registryYAML(sourceYAML("a", KindGitHubRepo, "https://gitlab.com/example-org/x", "why")), "https://github.com/<owner>/<repository>", nil},
	{"repo too deep", registryYAML(sourceYAML("a", KindGitHubRepo, "https://github.com/example-org/x/releases", "why")), "sources[0].url", nil},
	{"id with slash", registryYAML(sourceYAML("a/b", KindFeed, "https://example.org/a", "why")), "sources[0].id", nil},
	{"missing why", registryYAML(sourceYAML("a", KindFeed, "https://example.org/a", " ")), "sources[0].why: required", nil},
	{"two-line why", registryYAML(sourceYAML("a", KindFeed, "https://example.org/a", "one\ntwo")), "sources[0].why", nil},
	{"mention in why", registryYAML(sourceYAML("a", KindFeed, "https://example.org/a", "ask @someone")), "sources[0].why", ErrPersonSource},
	{"unknown key", "version: 1\nsources:\n  - id: a\n    kind: feed\n    url: https://example.org/a\n    why: x\n    licence: MIT\n", "licence", nil},
	{"wrong version", "version: 2\nsources:\n" + validFeed, "version: 2", nil},
	{"missing version", "sources:\n" + validFeed, "version: 0", nil},
	{"empty file", "", "radar registry", nil},
	{"two documents", registryYAML(validFeed) + "---\nversion: 1\n", "radar registry", nil},
}

// personURLs are source URLs that address one account, each refused with ErrPersonSource.
var personURLs = []struct {
	kind   Kind
	rawURL string
}{
	{KindFeed, "https://github.com/someone"},
	{KindFeed, "https://github.com/someone.atom"},
	{KindFeed, "https://gitlab.com/someone"},
	{KindFeed, "https://gist.github.com/someone"},
	{KindFeed, "https://mastodon.example/@someone"},
	{KindFeed, "https://medium.com/@someone/feed"},
	{KindFeed, "https://example.org/~someone/feed.xml"},
	{KindFeed, "https://example.org/author/someone/feed"},
	{KindFeed, "https://example.org/Users/someone.rss"},
	{KindFeed, "https://www.linkedin.com/in/someone"},
	{KindFeed, "https://orcid.org/0000-0002-1825-0097"},
	{KindFeed, "https://arxiv.org/a/someone_1"},
	{KindFeed, "https://dblp.org/pid/00/0000.xml"},
	{KindGitHubRepo, "https://github.com/someone/someone"},
	{KindGitHubRepo, "https://github.com/someone"},
	// The www., mobile., m. and country spellings of a listed domain are the same service.
	{KindFeed, "https://www.x.com/someone"},
	{KindFeed, "https://mobile.twitter.com/someone"},
	{KindFeed, "https://m.facebook.com/someone"},
	{KindFeed, "https://de.linkedin.com/in/someone"},
	{KindFeed, "https://www.orcid.org/0000-0002-1825-0097"},
	{KindFeed, "https://www.github.com/someone"},
	{KindFeed, "https://www.github.com/someone.atom"},
	{KindFeed, "https://hf.co/someone"},
	{KindFeed, "https://www.arxiv.org/a/someone_1"},
	{KindFeed, "https://export.arxiv.org/a/someone_1.atom"},
	// A per-account subdomain of a hosting service is the account's site, at any depth.
	{KindFeed, "https://someone.github.io/feed.xml"},
	{KindFeed, "https://someone.gitlab.io/blog/atom.xml"},
	{KindFeed, "https://someone.substack.com/feed"},
	{KindFeed, "https://someone.medium.com/feed"},
	{KindFeed, "https://someone.wordpress.com/feed"},
	{KindFeed, "https://en.someone.wordpress.com/feed"},
	{KindFeed, "https://someone.blogspot.com/feeds/posts/default"},
	{KindFeed, "https://someone.bsky.social/rss"},
	{KindFeed, "https://example-org.github.io/releases.xml"},
	// Every subdomain of a listed profile domain is the same service.
	{KindFeed, "https://m.linkedin.com/in/someone"},
	{KindFeed, "https://mobile.x.com/someone"},
	{KindFeed, "https://uk.linkedin.com/company/example"},
}

// nonPersonURLs are source URLs that share text with a refused domain without being on it, or
// sit on a listed domain in a shape that is not an account; each loads.
var nonPersonURLs = []string{
	"https://notx.com/someone",
	"https://x.com.example.org/someone",
	"https://github.com.example.org/someone",
	"https://mygithub.com/someone",
	"https://www.github.com/example-org/example-project/releases.atom",
	"https://export.arxiv.org/rss/cs.CL",
	"https://dblp.org/db/conf/example.xml",
	"https://github.io/feed.xml",
	"https://medium.com/feed/tag/tokenizers",
	"https://substack.com/feed.xml",
	"https://notgithub.io/feed.xml",
	"https://github.io.example.org/feed.xml",
	"https://example.org/someone.github.io/feed.xml",
}

// Negative: a duplicate id, an unknown or planned kind, a non-HTTPS or malformed URL, an account
// URL, an unknown key, a licence note among them, a wrong version, an empty or multi-document
// file and an oversized registry, field or file are all refused, each naming the field.
func TestRegistry_Negative_Refusals(t *testing.T) {
	for _, tc := range registryRefusals {
		_, err := ParseRegistry([]byte(tc.body))
		if err == nil || !strings.Contains(err.Error(), tc.want) || (tc.sentinel != nil && !errors.Is(err, tc.sentinel)) {
			t.Errorf("%s: err = %v, want %q (%v)", tc.name, err, tc.want, tc.sentinel)
		}
	}
	for _, tc := range personURLs {
		_, err := ParseRegistry([]byte(registryYAML(sourceYAML("a", tc.kind, tc.rawURL, "why"))))
		if !errors.Is(err, ErrPersonSource) || !strings.Contains(err.Error(), "sources[0].url") {
			t.Errorf("%s (%s): err = %v, want ErrPersonSource on the url", tc.rawURL, tc.kind, err)
		}
	}
	oversize := registryYAML(validFeed) + "# " + strings.Repeat("x", MaxRegistryBytes) + "\n"
	if _, err := ParseRegistry([]byte(oversize)); err == nil || !strings.Contains(err.Error(), "exceed the cap") {
		t.Errorf("oversize registry: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "radar.yaml"), []byte(oversize), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRegistry(dir, "radar.yaml"); !errors.Is(err, util.ErrFileTooLarge) {
		t.Errorf("oversize file: %v, want util.ErrFileTooLarge", err)
	}
	if _, err := LoadRegistry(dir, "missing.yaml"); err == nil {
		t.Error("a missing registry loaded")
	}
	long := registryYAML(sourceYAML("a", KindFeed, "https://example.org/a", strings.Repeat("w", MaxWhyBytes+1)))
	if _, err := ParseRegistry([]byte(long)); err == nil || !strings.Contains(err.Error(), "sources[0].why") {
		t.Errorf("oversize why: %v", err)
	}
	longURL := "https://example.org/" + strings.Repeat("p", MaxURLBytes)
	if _, err := ParseRegistry([]byte(registryYAML(sourceYAML("a", KindFeed, longURL, "why")))); err == nil ||
		!strings.Contains(err.Error(), "sources[0].url") {
		t.Errorf("oversize url: %v", err)
	}
	var none *Registry
	if none.Validate() == nil {
		t.Error("a nil registry validated")
	}
}

// Boundary: a domain matches on whole labels only, so a host that merely contains a listed name,
// or carries it as a leading label, loads; a listed domain loads where the path is no account;
// every subdomain of a listed domain matches, up to the longest host a DNS name allows.
func TestRegistry_Boundary_DomainLabels(t *testing.T) {
	for _, rawURL := range nonPersonURLs {
		if _, err := ParseRegistry([]byte(registryYAML(sourceYAML("a", KindFeed, rawURL, "why")))); err != nil {
			t.Errorf("%s: %v, want it to load", rawURL, err)
		}
	}
	// The cost the radar guide names: a single segment on any subdomain of a code host is refused,
	// a product subdomain included.
	product := "https://about.gitlab.com/atom.xml"
	if _, err := ParseRegistry([]byte(registryYAML(sourceYAML("a", KindFeed, product, "why")))); !errors.Is(err, ErrPersonSource) {
		t.Errorf("%s: %v, want ErrPersonSource", product, err)
	}
	// The longest host a DNS name allows, 253 bytes, is 124 one-letter labels above x.com.
	deep := strings.Repeat("a.", 124) + "x.com"
	_, err := ParseRegistry([]byte(registryYAML(sourceYAML("a", KindFeed, "https://"+deep+"/feed.xml", "why"))))
	if len(deep) != 253 || !errors.Is(err, ErrPersonSource) {
		t.Errorf("a %d-byte subdomain of x.com: %v, want ErrPersonSource", len(deep), err)
	}
}

// manySources renders count distinct feed entries.
func manySources(count int) string {
	entries := make([]string, count)
	for i := range entries {
		entries[i] = sourceYAML(fmt.Sprintf("feed-%03d", i), KindFeed, fmt.Sprintf("https://example.org/feed-%d.xml", i), "why")
	}
	return registryYAML(entries...)
}

// Boundary: an empty source list is refused and one source is enough; an id of exactly
// MaxIDBytes and a why of exactly MaxWhyBytes load and one byte more is refused; exactly
// MaxSources sources load and one more is refused; ids differing only in case collide.
func TestRegistry_Boundary_Limits(t *testing.T) {
	if _, err := ParseRegistry([]byte("version: 1\nsources: []\n")); !errors.Is(err, ErrNoSources) {
		t.Errorf("empty sources: %v, want ErrNoSources", err)
	}
	if _, err := ParseRegistry([]byte(registryYAML(validRepo))); err != nil {
		t.Errorf("one source: %v", err)
	}
	maxID := strings.Repeat("i", MaxIDBytes)
	if _, err := ParseRegistry([]byte(registryYAML(sourceYAML(maxID, KindFeed, "https://example.org/a", "why")))); err != nil {
		t.Errorf("%d-byte id: %v", MaxIDBytes, err)
	}
	if _, err := ParseRegistry([]byte(registryYAML(sourceYAML(maxID+"i", KindFeed, "https://example.org/a", "why")))); err == nil ||
		!strings.Contains(err.Error(), "sources[0].id") {
		t.Errorf("%d-byte id: %v", MaxIDBytes+1, err)
	}
	maxWhy := strings.Repeat("w", MaxWhyBytes)
	if _, err := ParseRegistry([]byte(registryYAML(sourceYAML("a", KindFeed, "https://example.org/a", maxWhy)))); err != nil {
		t.Errorf("%d-byte why: %v", MaxWhyBytes, err)
	}
	if registry, err := ParseRegistry([]byte(manySources(MaxSources))); err != nil || len(registry.Sources) != MaxSources {
		t.Errorf("%d sources: %v", MaxSources, err)
	}
	if _, err := ParseRegistry([]byte(manySources(MaxSources + 1))); err == nil || !strings.Contains(err.Error(), "exceed the cap") {
		t.Errorf("%d sources: %v", MaxSources+1, err)
	}
	folded := registryYAML(sourceYAML("Example", KindFeed, "https://example.org/a", "why"), sourceYAML("example", KindFeed, "https://example.org/b", "why"))
	if _, err := ParseRegistry([]byte(folded)); err == nil || !strings.Contains(err.Error(), "ids compare without case") {
		t.Errorf("ids differing in case: %v", err)
	}
}
