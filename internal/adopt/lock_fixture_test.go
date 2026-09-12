package adopt

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"gopkg.in/yaml.v3"
)

const adoptFixtureVersion = "v9.8.7"

// newAdoptLockSource creates actual YAML sources and independently hashes their
// content. It does not use the lock builder being exercised by adoption.
func newAdoptLockSource(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	manifest := &config.Manifest{Version: 1,
		Profiles: []string{"framework", "template-seed", "native-gpu-systems"},
		Facets:   []string{"security:high", "api:public-contract", "docs:seo-portal", "agent:sandboxed", "custom:facet"}}
	profiles, profileLines := writeAdoptSourceEntries(t, root, "profile", manifest.Profiles)
	facets, facetLines := writeAdoptSourceEntries(t, root, "facet", manifest.Facets)
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
	return root
}

func writeAdoptSourceEntries(t *testing.T, root, kind string, ids []string) ([]map[string]string, []string) {
	t.Helper()
	directory := filepath.Join(root, ".config", "archetypes")
	if kind == "facet" {
		directory = filepath.Join(directory, "facets")
	}
	var entries []map[string]string
	var lines []string
	for _, id := range ids {
		body := fmt.Sprintf("id: %q\nname: %q\n", id, "Adoption fixture "+id)
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
	if lock.PinnedVersion != adoptFixtureVersion {
		t.Fatalf("lock version must come from the source bundle: %q", lock.PinnedVersion)
	}
}
