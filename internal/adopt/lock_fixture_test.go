package adopt

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"gopkg.in/yaml.v3"
)

const adoptFixtureVersion = "v9.8.7"

// newAdoptLockSource creates actual YAML sources and independently hashes their
// content. It does not use the lock builder being exercised by adoption.
func newAdoptLockSource(t *testing.T) string {
	t.Helper()
	return newCatalogLockSource(t, &config.Manifest{Version: 1,
		Profiles: []string{"framework", "template-seed", "native-gpu-systems", "app-service", "os-image"},
		Facets:   []string{"security:high", "api:public-contract", "docs:seo-portal", "agent:sandboxed", "custom:facet"}}, nil)
}

// newCatalogLockSource is newAdoptLockSource for manifest, with bodies naming the catalog text
// of an id; an id without one gets a synthetic fixture body.
func newCatalogLockSource(t *testing.T, manifest *config.Manifest, bodies map[string]string) string {
	t.Helper()
	root := t.TempDir()
	profiles, profileLines := writeAdoptSourceEntries(t, root, "profile", manifest.Profiles, bodies)
	facets, facetLines := writeAdoptSourceEntries(t, root, "facet", manifest.Facets, bodies)
	lines := append(profileLines, facetLines...)
	sort.Strings(lines)
	lock := map[string]any{"version": 1, "pinned_version": adoptFixtureVersion,
		"profiles": profiles, "facets": facets,
		"digest": fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(strings.Join(lines, "\n")+"\n")))}
	for name, value := range map[string]any{".standards.yaml": manifest, ".standards.lock": lock} {
		data, err := yaml.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(root, name), string(data))
	}
	if _, err := config.ValidateLockfile(context.Background(), root, manifest); err != nil {
		t.Fatalf("independent source fixture must validate: %v", err)
	}
	writeRegisterSkillSources(t, root)
	return root
}

// sourceCheckout is this repository's root, resolved when the package loads: a test that changes
// the working directory still finds the files the source bundle copies from it.
var sourceCheckout = func() string {
	root := filepath.Join("..", "..")
	if abs, err := filepath.Abs(root); err == nil {
		return abs
	}
	return root
}()

// writeRegisterSkillSources copies the register skill bundle this repository ships into the
// source bundle at root, as a Praetor checkout holds it, so adoption installs it (#235).
func writeRegisterSkillSources(t *testing.T, root string) {
	t.Helper()
	for _, name := range config.RegisterSkillBundle() {
		rel := filepath.FromSlash(compiler.CanonicalSkillRel(name))
		mustWrite(t, filepath.Join(root, rel), mustRead(t, filepath.Join(sourceCheckout, rel)))
	}
}

func writeAdoptSourceEntries(t *testing.T, root, kind string, ids []string, bodies map[string]string) ([]map[string]string, []string) {
	t.Helper()
	directory := filepath.Join(root, ".config", "archetypes")
	if kind == "facet" {
		directory = filepath.Join(directory, "facets")
	}
	var entries []map[string]string
	var lines []string
	for _, id := range ids {
		body, ok := bodies[id]
		if !ok {
			body = fmt.Sprintf("id: %q\nname: %q\n", id, "Adoption fixture "+id)
		}
		digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body)))
		mustWrite(t, filepath.Join(directory, strings.ReplaceAll(id, ":", "-")+".yaml"), body)
		entries = append(entries, map[string]string{"id": id, "version": adoptFixtureVersion, "digest": digest})
		lines = append(lines, kind+":"+id+"="+digest)
	}
	return entries, lines
}

func assertAdoptedLock(t *testing.T, root string) {
	t.Helper()
	manifest, err := config.LoadManifest(filepath.Join(root, ".standards.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.ValidateLockfile(context.Background(), root, manifest); err != nil {
		t.Fatalf("adoption must produce a valid digest lock: %v", err)
	}
	var lock struct {
		PinnedVersion string `yaml:"pinned_version"`
	}
	if err := yaml.Unmarshal([]byte(mustRead(t, filepath.Join(root, ".standards.lock"))), &lock); err != nil {
		t.Fatal(err)
	}
	// The version names the source catalog's content, never the source lock's declared
	// adoptFixtureVersion (#595); internal/config pins the exact digest.
	if !adoptedCatalogVersion.MatchString(lock.PinnedVersion) {
		t.Fatalf("lock version must name the source catalog, not copy %q: %q", adoptFixtureVersion, lock.PinnedVersion)
	}
}

// adoptedCatalogVersion is the shape of a lock version built from a source catalog.
var adoptedCatalogVersion = regexp.MustCompile(`^v0\.0\.0\+catalog\.[0-9a-f]{12}$`)
