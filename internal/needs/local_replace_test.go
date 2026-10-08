package needs

import (
	"os"
	"path/filepath"
	"testing"
)

// A requirement the go.mod replaces with a directory is the checkout's own module, so the scan
// does not count it as a third-party dependency; a requirement replaced by another module, or
// not replaced, stays (nested test-only modules such as tools/schemacheck require this module).
func TestParseGoMod_LocalReplaceIsNotAThirdPartyDependency(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/self\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "tools", "nested")
	if err := os.MkdirAll(filepath.Join(root, "third_party", "dep"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "third_party", "dep", "go.mod"), []byte("module example.com/other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "go.mod")
	manifest := "module example.com/nested\n\ngo 1.27\n\nrequire (\n\texample.com/self v0.0.0\n\texample.com/lib v1.0.0\n\texample.com/forked v1.0.0\n\texample.com/dep v0.0.0\n)\n\nreplace example.com/self => ../..\n\nreplace example.com/forked => example.com/fork v1.0.1\n\nreplace example.com/dep => ../../third_party/dep\n"
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, err := parseGoMod(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := parsed.directDeps["example.com/self"]; present {
		t.Errorf("the locally replaced module is counted as a dependency: %v", parsed.directDeps)
	}
	for _, kept := range []string{"example.com/lib", "example.com/forked", "example.com/dep"} {
		if _, present := parsed.directDeps[kept]; !present {
			t.Errorf("%s was dropped: %v", kept, parsed.directDeps)
		}
	}
}

// Imports of a locally replaced module are dropped with its requirement, whole-path or by prefix;
// a module whose name only starts the same way stays.
func TestDropLocalModuleImports(t *testing.T) {
	imports := map[string]struct{}{
		"example.com/self": {}, "example.com/self/internal/x": {}, "example.com/selfish": {}, "example.com/lib": {},
	}
	dropLocalModuleImports(imports, map[string]bool{"example.com/self": true})
	if len(imports) != 2 {
		t.Fatalf("imports = %v, want example.com/selfish and example.com/lib", imports)
	}
	for _, kept := range []string{"example.com/selfish", "example.com/lib"} {
		if _, present := imports[kept]; !present {
			t.Errorf("%s was dropped", kept)
		}
	}
	dropLocalModuleImports(imports, nil)
	if len(imports) != 2 {
		t.Fatalf("no local modules must drop nothing, got %v", imports)
	}
}
