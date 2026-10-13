package flavors

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// #1279: tag_pattern was declared for every flavor and decoded, yet nothing read it. It left
// the struct and the repository's own config; a config that still declares it loads, plans the
// same, and says the key is ignored.

func loadFlavorFixture(t *testing.T, body string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flavors.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

// Positive: no Flavor field decodes tag_pattern, and the repository's own config declares no
// ignored key, so its plan carries no note.
func TestFlavor_Positive_NoFieldDecodesAnIgnoredKey(t *testing.T) {
	typ := reflect.TypeOf(Flavor{})
	for i := 0; i < typ.NumField(); i++ {
		for _, key := range IgnoredKeys {
			if tag := typ.Field(i).Tag.Get("yaml"); strings.Split(tag, ",")[0] == key {
				t.Fatalf("Flavor.%s decodes %s, a key nothing reads", typ.Field(i).Name, key)
			}
		}
	}
	cfg, err := LoadConfig("../../.config/flavors.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Ignored) != 0 {
		t.Fatalf(".config/flavors.yaml still declares an ignored key: %v", cfg.Ignored)
	}
}

const withPattern = `version: 1
flavors:
  edge: {source_ref: "refs/heads/main", tag_pattern: "v{{version}}-rc.{{rc_number}}", update_frequency: manual}
  bleeding: {source_ref: "refs/heads/main", tag_pattern: "v{{version}}-bleeding.{{commit_short}}"}
  latest: {source_ref: "refs/tags/v*"}
`

// Negative: a config that still declares tag_pattern loads, names each flavor that declares
// it, in flavor order, and plans exactly what the same config without the key plans.
func TestLoadConfig_Negative_DeclaredTagPatternIsReportedAndDecidesNothing(t *testing.T) {
	cfg := loadFlavorFixture(t, withPattern)
	want := []string{
		`flavor "bleeding" declares tag_pattern, which ` + ignoredKeyNote,
		`flavor "edge" declares tag_pattern, which ` + ignoredKeyNote,
	}
	if !reflect.DeepEqual(cfg.Ignored, want) {
		t.Fatalf("Ignored = %q, want %q", cfg.Ignored, want)
	}
	without := strings.NewReplacer(`, tag_pattern: "v{{version}}-rc.{{rc_number}}"`, "", `, tag_pattern: "v{{version}}-bleeding.{{commit_short}}"`, "").Replace(withPattern)
	if without == withPattern {
		t.Fatal("fixture replacement did not apply")
	}
	plain := loadFlavorFixture(t, without)
	resolve := func(ref string) (string, bool) { return "c0ffee", ref == "refs/heads/main" }
	current := map[string]string{"edge": "old", "bleeding": "old"}
	if got, base := PlanTransitions(cfg, current, resolve), PlanTransitions(plain, current, resolve); !reflect.DeepEqual(got, base) {
		t.Fatalf("tag_pattern changed the plan:\n%+v\nwant\n%+v", got, base)
	}
}

// Boundary: an empty tag_pattern is still a declared key and is named; a flavor without it, and
// a config without flavors, carry no note.
func TestLoadConfig_Boundary_EmptyTagPatternCountsAsDeclared(t *testing.T) {
	cfg := loadFlavorFixture(t, "version: 1\nflavors:\n  edge: {source_ref: refs/heads/main, tag_pattern: \"\"}\n  latest: {source_ref: \"refs/tags/v*\"}\n")
	if len(cfg.Ignored) != 1 || !strings.HasPrefix(cfg.Ignored[0], `flavor "edge" declares tag_pattern`) {
		t.Fatalf("Ignored = %q, want one note for edge", cfg.Ignored)
	}
	if empty := loadFlavorFixture(t, "version: 1\n"); len(empty.Ignored) != 0 {
		t.Fatalf("a config without flavors carries notes: %q", empty.Ignored)
	}
}
