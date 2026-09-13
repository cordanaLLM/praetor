package devcontainer

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestBootstrapBuildPreservesSelectedSourceVersion(t *testing.T) {
	root := bootstrapSourceFixture(t)
	// A mutable fixture symbol makes accidental linker rewriting observable even
	// though the production CLI currently declares its version as a constant.
	writeBootstrapFile(t, root, "cmd/standardsctl/main.go", "package main\nimport \"fmt\"\nvar version = \"selected-source-v42\"\nfunc main() { fmt.Println(version) }\n")
	bundle, err := PrepareBundle(t.Context(), "app", nil, nil, BootstrapOptions{SourceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	command := bootstrapBuildCommand(t, renderBootstrapDockerfile(bundle.Spec()))
	if _, err := util.RunCommandBytes(t.Context(), root, "/bin/sh", 4096, "-c", command); err != nil {
		t.Fatal(err)
	}
	result, err := util.RunCommandBytes(t.Context(), root, filepath.Join(root, "praetorctl"), 4096)
	if err != nil || string(result.Stdout) != "selected-source-v42\n" {
		t.Fatalf("generated build changed the selected source version: %q %v", result.Stdout, err)
	}
}

func bootstrapBuildCommand(t *testing.T, dockerfile string) string {
	t.Helper()
	for _, line := range strings.Split(dockerfile, "\n") {
		if strings.HasPrefix(line, "RUN /usr/local/go/bin/go build ") {
			command := strings.TrimPrefix(line, "RUN ")
			command = strings.Replace(command, "/usr/local/go/bin/go", "go", 1)
			return strings.Replace(command, "/out/praetorctl", "praetorctl", 1)
		}
	}
	t.Fatal("generated Dockerfile omitted the CLI build")
	return ""
}
