package cavemansource

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

func sourceInput(path string, format config.RegisterSourceFormat, selector string) config.RegisterSourceInput {
	return config.RegisterSourceInput{Path: path, Surface: config.SurfaceHooks, Kind: "message", Format: format, Selector: selector}
}

func writeSourceFile(t *testing.T, root, path, body string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runSourceGit(t *testing.T, root string, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, output)
	}
}

func trackSourceFiles(t *testing.T, root string, paths ...string) {
	t.Helper()
	runSourceGit(t, root, "init", "-q")
	runSourceGit(t, root, append([]string{"add", "--"}, paths...)...)
}

func TestExtractDeclaredPositive(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "hooks/check.sh", "echo \"block: path outside root.\"\necho \"result: token << static.\"\nprintf '%s\\n' 'result: pass.'\nprintf \"%s\\n\" \"goal: inspect.\"\ncat <<'EOF'\nnext: stop.\nEOF\n")
	writeSourceFile(t, root, "hooks/check.py", "print(\"line one\\nline two\")\n")
	writeSourceFile(t, root, "prompts/agents.json", `{"agents":{"review":{"prompt":"goal: inspect\nreturn: verdict"}}}`)
	writeSourceFile(t, root, "prompts/mcp.yaml", "tools:\n  - description: 'result: bounded scan.'\n")
	inputs := []config.RegisterSourceInput{
		sourceInput("hooks/check.sh", config.SourceFormatShell, ""),
		sourceInput("hooks/check.py", config.SourceFormatPython, ""),
		{Path: "prompts/agents.json", Surface: config.SurfacePrompts, Kind: "brief", Format: config.SourceFormatJSON, Selector: "agents.*.prompt"},
		{Path: "prompts/mcp.yaml", Surface: config.SurfaceMCP, Kind: "message", Format: config.SourceFormatYAML, Selector: "tools.*.description"},
	}
	got, err := ExtractInputs(context.Background(), root, inputs)
	if err != nil || len(got.Sources) != 8 {
		t.Fatalf("ExtractInputs() = %+v, %v", got, err)
	}
	trackSourceFiles(t, root, "hooks/check.sh", "hooks/check.py", "prompts/agents.json", "prompts/mcp.yaml")
	declared := &config.RegisterSources{Expected: len(got.Sources), SHA256: got.SHA256, Inputs: inputs}
	verified, err := ExtractDeclared(context.Background(), root, declared)
	if err != nil || verified.SHA256 != got.SHA256 {
		t.Fatalf("ExtractDeclared() = %+v, %v", verified, err)
	}
	texts := make([]string, len(verified.Sources))
	for index := range verified.Sources {
		texts[index] = verified.Sources[index].Text
	}
	joined := strings.Join(texts, "|")
	for _, want := range []string{"line one\nline two", "goal: inspect\nreturn: verdict", "goal: inspect.\n", "result: bounded scan."} {
		if !strings.Contains(joined, want) {
			t.Errorf("decoded runtime text missing %q: %q", want, joined)
		}
	}
}

func TestExtractDeclaredNegative(t *testing.T) {
	for name, fixture := range map[string]struct {
		format config.RegisterSourceFormat
		body   string
		want   string
	}{
		"python interpolation":   {config.SourceFormatPython, `print(f"block: {reason}")`, "output unverified"},
		"python concatenation":   {config.SourceFormatPython, `print("block: " + reason)`, "output unverified"},
		"python named escape":    {config.SourceFormatPython, `print("block: \N{ESCAPE}")`, "named Python Unicode escapes"},
		"shell interpolation":    {config.SourceFormatShell, `echo "block: $reason"`, "output unverified"},
		"shell command position": {config.SourceFormatShell, `if true; then echo "$reason"; fi`, "command position unsupported"},
		"heredoc expansion":      {config.SourceFormatShell, "cat <<EOF\nblock: \\$reason\nEOF", "output unverified"},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			ext := ".py"
			if fixture.format == config.SourceFormatShell {
				ext = ".sh"
			}
			path := "hooks/check" + ext
			writeSourceFile(t, root, path, fixture.body+"\n")
			_, err := ExtractInputs(context.Background(), root, []config.RegisterSourceInput{sourceInput(path, fixture.format, "")})
			if err == nil || !strings.Contains(err.Error(), fixture.want) {
				t.Fatalf("unsupported runtime expression accepted: %v", err)
			}
		})
	}

	root := t.TempDir()
	writeSourceFile(t, root, "prompts/agents.json", `{"agents":{}}`)
	input := config.RegisterSourceInput{Path: "prompts/agents.json", Surface: config.SurfacePrompts, Kind: "message", Format: config.SourceFormatJSON, Selector: "agents.*.prompt"}
	if _, err := ExtractInputs(context.Background(), root, []config.RegisterSourceInput{input}); err == nil || !strings.Contains(err.Error(), "matched zero") {
		t.Fatalf("empty selector accepted: %v", err)
	}

	writeSourceFile(t, root, "hooks/check.py", `print("result: pass.")`)
	input = sourceInput("hooks/check.py", config.SourceFormatPython, "")
	result, err := ExtractInputs(context.Background(), root, []config.RegisterSourceInput{input})
	if err != nil {
		t.Fatal(err)
	}
	trackSourceFiles(t, root, "prompts/agents.json")
	declared := &config.RegisterSources{Expected: 1, SHA256: result.SHA256, Inputs: []config.RegisterSourceInput{input}}
	if _, err = ExtractDeclared(context.Background(), root, declared); err == nil || !strings.Contains(err.Error(), "not tracked") {
		t.Fatalf("untracked source accepted: %v", err)
	}
}

func TestExtractLineBoundary(t *testing.T) {
	root := t.TempDir()
	exact := strings.Repeat("x\n", maxSelectionLines-1) + "x"
	writeSourceFile(t, root, "prompts/exact.yaml", "prompt: |\n  "+strings.ReplaceAll(exact, "\n", "\n  ")+"\n")
	input := config.RegisterSourceInput{Path: "prompts/exact.yaml", Surface: config.SurfacePrompts, Kind: "message", Format: config.SourceFormatYAML, Selector: "prompt"}
	got, err := ExtractInputs(context.Background(), root, []config.RegisterSourceInput{input})
	if err != nil || len(got.Sources) != 1 || logicalLines(got.Sources[0].Text) != maxSelectionLines {
		t.Fatalf("exact %d-line value rejected: lines=%d err=%v", maxSelectionLines, logicalLines(got.Sources[0].Text), err)
	}
	above := exact + "\nx"
	writeSourceFile(t, root, "prompts/exact.yaml", "prompt: |\n  "+strings.ReplaceAll(above, "\n", "\n  ")+"\n")
	if _, err = ExtractInputs(context.Background(), root, []config.RegisterSourceInput{input}); err == nil || !strings.Contains(err.Error(), "exceeds 1024 lines") {
		t.Fatalf("1025-line value accepted: %v", err)
	}

	writeSourceFile(t, root, "hooks/empty.py", "value = 'no output'\n")
	if _, err = ExtractInputs(context.Background(), root, []config.RegisterSourceInput{sourceInput("hooks/empty.py", config.SourceFormatPython, "")}); err == nil || !strings.Contains(err.Error(), "zero runtime") {
		t.Fatalf("zero extracted values accepted: %v", err)
	}
}

func TestDecodePrintfEscapesRuntime(t *testing.T) {
	got, err := decodePrintfEscapes(`\\\n\e\0123`)
	if err != nil || got != "\\\n\x1bS" {
		t.Fatalf("decodePrintfEscapes() = %q, %v", got, err)
	}
	if _, err := decodePrintfEscapes(`before\cafter`); err == nil || !strings.Contains(err.Error(), "early termination") {
		t.Fatalf("printf early termination accepted: %v", err)
	}
}
