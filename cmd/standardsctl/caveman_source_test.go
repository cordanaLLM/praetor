package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/cavemansource"
	"github.com/cordanaLLM/praetor/internal/config"
)

func TestCavemanSourceExtensionsPositive(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, ".standards.yaml", "version: 1\n")
	writeFixtureFile(t, root, "hooks/check.sh", "echo \"result: pass.\"\n")
	writeFixtureFile(t, root, "hooks/check.py", "print(\"block: path outside root.\")\n")
	hooks := filepath.Join(root, "hooks")
	out, err := runCavemanCLI(t, "", "check", "--root="+root, "--surface=hooks", "--ext=.sh,.py", hooks)
	if err != nil || strings.Count(out, ": PASS ") != 2 || !strings.Contains(out, "extraction=shell") || !strings.Contains(out, "extraction=python") {
		t.Fatalf("script directory: err=%v\n%s", err, out)
	}

	jsonPath := writeFixtureFile(t, root, "prompts/agent.json", `{"agent":{"review":{"prompt":"result: pass.\\nnext: stop."}}}`)
	out, err = runCavemanCLI(t, "", "check", "--root="+root, "--surface=prompts", "--selector=agent.*.prompt", jsonPath)
	if err != nil || !strings.Contains(out, ": PASS ") || !strings.Contains(out, "source_selector=$.agent.review.prompt") {
		t.Fatalf("JSON selector: err=%v\n%s", err, out)
	}
}

func TestCavemanSourceExtensionsNegative(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, ".standards.yaml", "version: 1\n")
	computed := writeFixtureFile(t, root, "hooks/computed.py", `print(f"block: {reason}")`+"\n")
	if _, err := runCavemanCLI(t, "", "check", "--root="+root, "--surface=hooks", computed); err == nil || !strings.Contains(err.Error(), "output unverified") {
		t.Fatalf("computed Python output accepted: %v", err)
	}

	emptyDir := filepath.Join(root, "empty")
	writeFixtureFile(t, emptyDir, "notes.txt", "not source")
	if _, err := runCavemanCLI(t, "", "check", "--root="+root, "--surface=hooks", "--ext=.py", emptyDir); err == nil || !strings.Contains(err.Error(), "matched zero files") {
		t.Fatalf("zero-match directory accepted: %v", err)
	}

	unknown := writeFixtureFile(t, root, "hooks/check.rb", `puts "result: pass."`)
	if _, err := runCavemanCLI(t, "", "check", "--root="+root, "--surface=hooks", unknown); err == nil || !strings.Contains(err.Error(), "unsupported source extension") {
		t.Fatalf("unknown source format accepted: %v", err)
	}

	writeFixtureFile(t, root, ".standards.yaml", "version: 1\nregister:\n  surfaces:\n    hooks: docs\n")
	static := writeFixtureFile(t, root, "hooks/static.py", `print("result: pass.")`+"\n")
	if _, err := runCavemanCLI(t, "", "check", "--root="+root, "--surface=hooks", static); err == nil || !strings.Contains(err.Error(), "has no Caveman verdict") {
		t.Fatalf("non-internal surface reported a green skip: %v", err)
	}
}

func TestCavemanConfiguredSourcesCoverage(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "prompts/agent.json", `{"messages":["result: pass.","next: stop."]}`)
	input := config.RegisterSourceInput{Path: "prompts/agent.json", Surface: config.SurfacePrompts,
		Kind: "message", Format: config.SourceFormatJSON, Selector: "messages.*"}
	coverage, err := cavemansource.ExtractInputs(context.Background(), root, []config.RegisterSourceInput{input})
	if err != nil {
		t.Fatal(err)
	}
	writeConfiguredSourceManifest(t, root, coverage, "messages.*", "")
	initGitFixture(t, root)

	out, err := runCavemanCLI(t, "", "check", "--root="+root, "--configured-sources")
	if err != nil || strings.Count(out, ": PASS ") != 2 {
		t.Fatalf("configured sources: err=%v\n%s", err, out)
	}

	writeConfiguredSourceManifest(t, root, coverage, "messages.0", "")
	if _, err = runCavemanCLI(t, "", "check", "--root="+root, "--configured-sources"); err == nil || !strings.Contains(err.Error(), "expected 2 values, extracted 1") {
		t.Fatalf("omitted declared value accepted: %v", err)
	}

	writeConfiguredSourceManifest(t, root, coverage, "messages.*", "        unknown: true\n")
	if _, err = runCavemanCLI(t, "", "check", "--root="+root, "--configured-sources"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown source schema field accepted: %v", err)
	}
}

func TestAuditCavemanConfiguredSources(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "prompts/agent.json", `{"messages":["result: pass."]}`)
	input := config.RegisterSourceInput{Path: "prompts/agent.json", Surface: config.SurfacePrompts,
		Kind: "message", Format: config.SourceFormatJSON, Selector: "messages.*"}
	coverage, err := cavemansource.ExtractInputs(context.Background(), root, []config.RegisterSourceInput{input})
	if err != nil {
		t.Fatal(err)
	}
	writeConfiguredSourceManifest(t, root, coverage, "messages.*", "")
	initGitFixture(t, root)
	manifest, err := config.LoadManifest(filepath.Join(root, ".standards.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := auditCavemanConfiguredSources(context.Background(), manifest, root); err != nil {
		t.Fatalf("configured audit rejected passing source: %v", err)
	}
	writeFixtureFile(t, root, "prompts/agent.json", `{"messages":["We are ready and it is complete."]}`)
	updated, err := cavemansource.ExtractInputs(context.Background(), root, []config.RegisterSourceInput{input})
	if err != nil {
		t.Fatal(err)
	}
	writeConfiguredSourceManifest(t, root, updated, "messages.*", "")
	manifest, err = config.LoadManifest(filepath.Join(root, ".standards.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := auditCavemanConfiguredSources(context.Background(), manifest, root); err == nil || !strings.Contains(err.Error(), "C9 grammar") {
		t.Fatalf("audit accepted non-Caveman runtime source: %v", err)
	}
}

func TestAuditCavemanConfiguredSourcesDeclarationBoundary(t *testing.T) {
	root := t.TempDir()
	manifest := &config.Manifest{Version: 1}
	if err := auditCavemanConfiguredSources(context.Background(), manifest, root); err != nil {
		t.Fatalf("absent register section rejected as configured policy: %v", err)
	}
	manifest.Register = &config.RegisterPolicy{}
	err := auditCavemanConfiguredSources(context.Background(), manifest, root)
	if err == nil || !strings.Contains(err.Error(), "declared register requires register.sources") {
		t.Fatalf("declared register omitted source contract: %v", err)
	}

	writeFixtureFile(t, root, ".standards.yaml", "version: 1\n")
	if _, err = runCavemanCLI(t, "", "check", "--root="+root, "--configured-sources"); err == nil ||
		!strings.Contains(err.Error(), "register.sources is not configured") {
		t.Fatalf("configured source gate accepted absent register: %v", err)
	}
	writeFixtureFile(t, root, ".standards.yaml", "version: 1\nregister:\n  surfaces:\n    hooks: internal\n")
	if _, err = runCavemanCLI(t, "", "check", "--root="+root, "--configured-sources"); err == nil ||
		!strings.Contains(err.Error(), "register.sources is not configured") {
		t.Fatalf("configured source gate accepted declared register omission: %v", err)
	}
}

func writeConfiguredSourceManifest(t *testing.T, root string, coverage cavemansource.Result, selector, extra string) {
	t.Helper()
	body := fmt.Sprintf(`version: 1
register:
  sources:
    expected: %d
    sha256: %s
    inputs:
      - path: prompts/agent.json
        surface: prompts
        kind: message
        format: json
        selector: %s
%s`, len(coverage.Sources), coverage.SHA256, selector, extra)
	writeFixtureFile(t, root, ".standards.yaml", body)
}
