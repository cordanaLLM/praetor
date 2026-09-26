package cavemansource

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/testsupport"
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

func installSourceGitIndex(t *testing.T, data []byte) {
	t.Helper()
	bin := t.TempDir()
	source := `package main
import ("fmt"; "os")
func main() {
	required := []string{"--literal-pathspecs", "ls-files", "--stage", "-z", "--"}
	found := false
	for start := 1; start+len(required) <= len(os.Args); start++ {
		match := true
		for index := range required { if os.Args[start+index] != required[index] { match = false; break } }
		if match { found = true; break }
	}
	if !found { os.Exit(2) }
	fmt.Print(` + strconv.Quote(string(data)) + `)
}
`
	testsupport.BuildExecutable(t, bin, "git", source)
	t.Setenv("PATH", bin)
}

func regularTrackedIndex(paths ...string) []byte {
	entries := make([]string, 0, len(paths))
	for _, path := range paths {
		entries = append(entries, "100644 "+strings.Repeat("a", 40)+" 0\t"+path+"\x00")
	}
	return []byte(strings.Join(entries, ""))
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
	installSourceGitIndex(t, regularTrackedIndex("hooks/check.sh", "hooks/check.py", "prompts/agents.json", "prompts/mcp.yaml"))
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
	installSourceGitIndex(t, nil)
	declared := &config.RegisterSources{Expected: 1, SHA256: result.SHA256, Inputs: []config.RegisterSourceInput{input}}
	_, err = ExtractDeclared(context.Background(), root, declared)
	if err == nil || !strings.Contains(err.Error(), "not tracked") {
		t.Fatalf("untracked source accepted: %v", err)
	}
	if _, err = ExtractDeclaredContent(context.Background(), root, declared); err != nil {
		t.Fatalf("pre-commit declared content rejected: %v", err)
	}
	declared.SHA256 = "sha256:" + strings.Repeat("0", 64)
	if _, err = ExtractDeclaredContent(context.Background(), root, declared); err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("stale pre-commit declaration accepted: %v", err)
	}
}

func TestExtractYAMLRejectsDuplicateMappingKeys(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "prompts/duplicate.yaml", "prompt: 'result: first.'\nprompt: 'result: second.'\n")
	input := config.RegisterSourceInput{Path: "prompts/duplicate.yaml", Surface: config.SurfacePrompts,
		Kind: "message", Format: config.SourceFormatYAML, Selector: "prompt"}
	if _, err := ExtractInputs(context.Background(), root, []config.RegisterSourceInput{input}); err == nil ||
		!strings.Contains(err.Error(), "unique strings") {
		t.Fatalf("duplicate YAML mapping key accepted: %v", err)
	}
}

func TestExtractShellMessagesWithStderrRedirection(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "hooks/check.sh", "echo \"result: pass.\">&2\nprintf '%s\\n' 'next: stop.' 1>&2\n")
	result, err := ExtractInputs(context.Background(), root, []config.RegisterSourceInput{
		sourceInput("hooks/check.sh", config.SourceFormatShell, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sources) != 2 || result.Sources[0].Text != "result: pass.\n" || result.Sources[1].Text != "next: stop.\n" {
		t.Fatalf("stderr messages not decoded: %+v", result.Sources)
	}
}

func TestExtractPythonMessageWrittenToStderr(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "hooks/check.py", "print(\"result: pass.\", file=sys.stderr)\nsys.stderr.write(f\"block: {reason}.\")\nprint(\"next: \" + action)\nsys.stderr.write(\n    f\"input: {path}.\\n\"\n    f\"block: {reason}.\\n\"\n)\n")
	result, err := ExtractInputs(context.Background(), root, []config.RegisterSourceInput{
		sourceInput("hooks/check.py", config.SourceFormatPython, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sources) != 4 || result.Sources[0].Text != "result: pass." || result.Sources[1].Text != "block: {value}." ||
		result.Sources[2].Text != "next: {value}" ||
		result.Sources[3].Text != "input: {value}.\nblock: {value}.\n" {
		t.Fatalf("stderr message not decoded: %+v", result.Sources)
	}
}

func TestExtractPythonHexadecimalEscapes(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "hooks/check.py", `print("result: \xE9 \u20AC \U0001F642")`+"\n")
	input := sourceInput("hooks/check.py", config.SourceFormatPython, "")
	result, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
	if err != nil || len(result.Sources) != 1 || result.Sources[0].Text != "result: é € 🙂" {
		t.Fatalf("Python hexadecimal escapes: sources=%+v err=%v", result.Sources, err)
	}

	for _, escape := range []string{`\uD800`, `\U00110000`, `\UFFFFFFFF`} {
		writeSourceFile(t, root, "hooks/check.py", `print("result: `+escape+`")`+"\n")
		if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil ||
			!strings.Contains(err.Error(), "invalid Python hexadecimal escape") {
			t.Errorf("invalid Python hexadecimal escape %s accepted: %v", escape, err)
		}
	}
}

func TestExtractPythonComputedOutputRequiresClassification(t *testing.T) {
	root := t.TempDir()
	input := sourceInput("hooks/check.py", config.SourceFormatPython, "")
	writeSourceFile(t, root, "hooks/check.py", "print(json.dumps(result))\n")
	if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil ||
		!strings.Contains(err.Error(), "computed output requires") {
		t.Fatalf("unclassified computed output accepted: %v", err)
	}
	writeSourceFile(t, root, "hooks/check.py", "print(json.dumps(result))  # caveman:not-applicable structured-protocol\n")
	result, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
	if err != nil || len(result.Sources) != 1 || result.Sources[0].NotApplicable != "structured-protocol" ||
		result.Applicable != 0 || result.NotApplicable != 1 {
		t.Fatalf("classified protocol output was not coverage-bound: sources=%+v err=%v", result.Sources, err)
	}
}

func TestExtractPythonClassifiesStdoutProtocolBytes(t *testing.T) {
	root := t.TempDir()
	input := sourceInput("hooks/check.py", config.SourceFormatPython, "")
	writeSourceFile(t, root, "hooks/check.py", "sys.stdout.buffer.write(b\"POLICY_OK\\n\")  # caveman:not-applicable protocol-marker\n")
	result, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
	if err != nil || len(result.Sources) != 1 || result.Sources[0].NotApplicable != "protocol-marker" {
		t.Fatalf("classified stdout protocol bytes were not coverage-bound: sources=%+v err=%v", result.Sources, err)
	}
}

func TestExtractGoStaticStringTable(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "mcp/text.go", `package mcp

var runtimeText = [...]string{
	"result: pass.",
	"next: stop.",
}
`)
	input := sourceInput("mcp/text.go", config.SourceFormatGo, "runtimeText.*")
	result, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
	if err != nil || len(result.Sources) != 2 || result.Sources[0].Text != "result: pass." || result.Sources[1].Text != "next: stop." {
		t.Fatalf("Go runtime text table: sources=%+v err=%v", result.Sources, err)
	}
	writeSourceFile(t, root, "mcp/text.go", "package mcp\nvar runtimeText = [...]string{\"result: \" + status}\n")
	if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil || !strings.Contains(err.Error(), "static string") {
		t.Fatalf("computed Go runtime text accepted: %v", err)
	}
	writeSourceFile(t, root, "mcp/text.go", "package mcp\nvar runtimeText = [...]string{\"result: pass.\"}\n")
	for _, selector := range []string{"runtimeText.+0", "runtimeText.-0", "runtimeText.00"} {
		input.Selector = selector
		if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil ||
			!strings.Contains(err.Error(), "matched zero") {
			t.Errorf("non-canonical Go index %q accepted: %v", selector, err)
		}
	}
}

func TestExtractGoMCPRuntimeCensus(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "mcp/tools.go", `package main
func tools() {
	mcp.NewReadOnlyTool("inspect", "goal: inspect.", schema, handler)
	_ = mcp.ToolInputSchema{Properties: map[string]mcp.PropertySchema{
		"path": {Description: "input: path."},
	}}
	mcp.ErrorResult("block: invalid input.")
	mcp.TextResult(fmt.Sprintf("result: %s.", status))
	fmt.Fprintf(&out, "next: inspect %s.\n", path)
	out.WriteString("result: complete.\n")
	mcpTextResult(data, mcpTextStructuredJSON)
}
`)
	descriptions := sourceInput("mcp", config.SourceFormatGo, "mcp.descriptions")
	descriptions.Surface = config.SurfaceMCP
	result, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{descriptions})
	if err != nil || len(result.Sources) != 2 {
		t.Fatalf("MCP descriptions: sources=%+v err=%v", result.Sources, err)
	}
	outputs := sourceInput("mcp", config.SourceFormatGo, "mcp.outputs")
	outputs.Surface = config.SurfaceMCP
	result, err = ExtractInputs(t.Context(), root, []config.RegisterSourceInput{outputs})
	classified := 0
	for _, source := range result.Sources {
		if source.NotApplicable != "" {
			classified++
		}
	}
	if err != nil || len(result.Sources) != 5 || classified != 1 {
		t.Fatalf("MCP outputs: sources=%+v err=%v", result.Sources, err)
	}
}

func TestExtractGoMCPRuntimeRejectsResultAlias(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "mcp/tools.go", `package main
func tools() {
	result := mcp.TextResult
	result(message)
}
`)
	input := sourceInput("mcp", config.SourceFormatGo, "mcp.outputs")
	input.Surface = config.SurfaceMCP
	if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil ||
		!strings.Contains(err.Error(), "result function reference") {
		t.Fatalf("MCP result alias bypass accepted: %v", err)
	}
}

func TestExtractGoMCPRuntimeRejectsBuilderBypasses(t *testing.T) {
	fixtures := map[string]string{
		"write":              `b.Write([]byte("hidden"))`,
		"fprintln":           `fmt.Fprintln(&b, "hidden")`,
		"io write":           `io.WriteString(&b, "hidden")`,
		"alias":              `alias := &b; _ = alias`,
		"fake text":          `mcpComposedTextResult(fake.Text())`,
		"direct cast":        `mcpComposedTextResult(mcpGovernedText(message))`,
		"helper raw storage": `helper(&b, message)`,
	}
	for name, bypass := range fixtures {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			helper := ""
			if name == "helper raw storage" {
				helper = "func helper(b *mcpTextBuilder, message string) { b.value.WriteString(message) }\n"
			}
			body := "package main\n" + helper + "func tools() {\nvar b mcpTextBuilder\n" + bypass + "\n}\n"
			writeSourceFile(t, root, "mcp/tools.go", body)
			input := sourceInput("mcp", config.SourceFormatGo, "mcp.outputs")
			input.Surface = config.SurfaceMCP
			if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil ||
				!strings.Contains(err.Error(), "bypass") && !strings.Contains(err.Error(), "requires text") {
				t.Fatalf("builder bypass accepted: %v", err)
			}
		})
	}
}

type goMCPFixture struct {
	name         string
	source       string
	wantError    string
	wantSources  int
	wantExcluded int
}

func TestExtractGoMCPRuntimeRejectsCompileValidReferenceBypasses(t *testing.T) {
	fixtures := append(rejectedBuilderReferenceFixtures(), rejectedOutputReferenceFixtures()...)
	fixtures = append(fixtures, rejectedGovernedBindingFixtures()...)
	root := t.TempDir()
	writeCompileValidGoFixtures(t, root, fixtures)
	for index, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			input := sourceInput(fmt.Sprintf("case-%02d/main.go", index), config.SourceFormatGo, "mcp.outputs")
			input.Surface = config.SurfaceMCP
			if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil ||
				!strings.Contains(err.Error(), fixture.wantError) {
				t.Fatalf("compile-valid bypass accepted or misclassified: %v", err)
			}
		})
	}
}

func TestExtractGoMCPRuntimeAcceptsCompileValidGovernedControls(t *testing.T) {
	fixtures := acceptedOutputReferenceFixtures()
	root := t.TempDir()
	writeCompileValidGoFixtures(t, root, fixtures)
	for index, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			input := sourceInput(fmt.Sprintf("case-%02d/main.go", index), config.SourceFormatGo, "mcp.outputs")
			input.Surface = config.SurfaceMCP
			result, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
			if err != nil || len(result.Sources) != fixture.wantSources || result.NotApplicable != fixture.wantExcluded {
				t.Fatalf("governed control: result=%+v err=%v", result, err)
			}
		})
	}
}

func TestExtractGoMCPRuntimeBindsCrossFileGovernedCallsToObjects(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "go.mod", "module github.com/cordanaLLM/praetor\n\ngo 1.24\n")
	writeSourceFile(t, root, "internal/mcp/mcp.go", `package mcp
type ToolResult struct{}
func TextResult(string) *ToolResult { return &ToolResult{} }
`)
	writeSourceFile(t, root, "mcp/helpers.go", goGovernedFixture(`func Safe() mcpGovernedText {
	return mcpTextf("result: pass.")
}`))
	writeSourceFile(t, root, "mcp/output.go", `package fixture
import "github.com/cordanaLLM/praetor/internal/mcp"
func exercise() *mcp.ToolResult { return mcpComposedTextResult(Safe()) }
`)
	input := sourceInput("mcp", config.SourceFormatGo, "mcp.outputs")
	input.Surface = config.SurfaceMCP
	if result, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err != nil ||
		result.Applicable != 1 || result.NotApplicable != 1 || len(result.Sources) != 2 {
		t.Fatalf("cross-file governed function rejected: result=%+v err=%v", result, err)
	}

	writeSourceFile(t, root, "mcp/output.go", `package fixture
import "github.com/cordanaLLM/praetor/internal/mcp"
func exercise(Safe func() mcpGovernedText) *mcp.ToolResult {
	return mcpComposedTextResult(Safe())
}
`)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("compile cross-file object-identity fixture: %v\n%s", err, output)
	}
	if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil ||
		!strings.Contains(err.Error(), "requires text from") {
		t.Fatalf("local function value borrowed package helper trust: %v", err)
	}
}

func TestExtractGoMCPRuntimeRejectsCrossFileResultHelperMutation(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "go.mod", "module github.com/cordanaLLM/praetor\n\ngo 1.24\n")
	writeSourceFile(t, root, "internal/mcp/mcp.go", `package mcp
type ContentItem struct { Type string; Text string }
type ToolResult struct { Content []ContentItem; IsError bool }
func TextResult(text string) *ToolResult {
	return &ToolResult{Content: []ContentItem{{Type: "text", Text: text}}}
}
`)
	writeSourceFile(t, root, "mcp/result.go", `package fixture
import "github.com/cordanaLLM/praetor/internal/mcp"
var sharedResult = mcp.TextResult("result: pass.")
func output() *mcp.ToolResult { return sharedResult }
`)
	writeSourceFile(t, root, "mcp/mutate.go", `package fixture
import "encoding/json"
func mutate(payload []byte) { _ = json.Unmarshal(payload, sharedResult) }
`)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("compile cross-file result mutation fixture: %v\n%s", err, output)
	}
	input := sourceInput("mcp", config.SourceFormatGo, "mcp.outputs")
	input.Surface = config.SurfaceMCP
	if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil ||
		!strings.Contains(err.Error(), "post-construction mcp result escape") {
		t.Fatalf("cross-file result helper mutation accepted: %v", err)
	}
}

func TestExtractGoMCPRuntimePackageResultGlobals(t *testing.T) {
	result := `package fixture
import "github.com/cordanaLLM/praetor/internal/mcp"
func output(ok bool) (*mcp.ToolResult, error) {
	if !ok {
		return nil, nil
	}
	return mcp.TextResult("result: pass."), nil
}
`
	cases := []struct {
		name, result, sibling, reject string
	}{
		{"returned nil stays predeclared", result, `package fixture
func consume(values ...any) {}
func exercise() { consume(nil) }
`, ""},
		{"same-file global returned by sibling escapes", result + `func current() *mcp.ToolResult { return cached }
`, `package fixture
import (
	"encoding/json"
	"github.com/cordanaLLM/praetor/internal/mcp"
)
var cached = pick()
func pick() *mcp.ToolResult { return nil }
func mutate(payload []byte) { _ = json.Unmarshal(payload, cached) }
`, "post-construction mcp result escape"},
		{"package declaration shadows nil", result, `package fixture
var nil = 0
`, "shadows predeclared identifier nil"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeSourceFile(t, root, "mcp/result.go", tc.result)
			writeSourceFile(t, root, "mcp/sibling.go", tc.sibling)
			input := sourceInput("mcp", config.SourceFormatGo, "mcp.outputs")
			input.Surface = config.SurfaceMCP
			_, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
			if tc.reject == "" && err != nil {
				t.Fatalf("predeclared nil treated as a result value: %v", err)
			}
			if tc.reject != "" && (err == nil || !strings.Contains(err.Error(), tc.reject)) {
				t.Fatalf("want %q, got %v", tc.reject, err)
			}
		})
	}
}

func TestExtractGoMCPResultExpressionDepthBoundary(t *testing.T) {
	for _, tc := range []struct {
		wrappers int
		reject   bool
	}{{maxGoSelectorDepth - 1, false}, {maxGoSelectorDepth, true}} {
		t.Run(fmt.Sprintf("wrappers-%d", tc.wrappers), func(t *testing.T) {
			root := t.TempDir()
			argument := strings.Repeat("(", tc.wrappers) + "1" + strings.Repeat(")", tc.wrappers)
			writeSourceFile(t, root, "mcp/depth.go", `package fixture
func consume(values ...any) {}
func exercise() { consume(`+argument+`); mcp.TextResult("result: pass.") }
`)
			input := sourceInput("mcp", config.SourceFormatGo, "mcp.outputs")
			input.Surface = config.SurfaceMCP
			_, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
			if tc.reject != (err != nil) || tc.reject && !strings.Contains(err.Error(), "exceeds depth") {
				t.Fatalf("wrappers=%d reject=%t err=%v", tc.wrappers, tc.reject, err)
			}
		})
	}
}

func rejectedBuilderReferenceFixtures() []goMCPFixture {
	templateHelper := `func (b *mcpTextBuilder) Template(template string, args ...any) {
	fmt.Fprintf(&b.value, template, args...)
}`
	return []goMCPFixture{
		{"builder selector shadows trusted helper", goBuilderFixture(`"fmt"`, `func allowBuilder(*mcpTextBuilder) {}
type builderSink struct { allowBuilder func(*mcpTextBuilder, string) }`, `func exercise(sink builderSink, message string) {
	var b mcpTextBuilder; sink.allowBuilder(&b, message)
	var out strings.Builder; fmt.Fprint(&out, "result: pass.")
}`), "storage access bypasses", 0, 0},
		{"package closure shadows trusted builder helper", goBuilderFixture(`"fmt"`, `func allowBuilder(*mcpTextBuilder) {}
type builderSink struct { allowBuilder func(*mcpTextBuilder, string) }
var sink = builderSink{allowBuilder: func(b *mcpTextBuilder, message string) {
	fmt.Fprint(&b.value, message)
}}`, `func exercise(message string) {
	var b mcpTextBuilder; sink.allowBuilder(&b, message)
	var out strings.Builder; fmt.Fprint(&out, "result: pass.")
}`), "closure", 0, 0},
		{"builder value-copy alias", goBuilderFixture(`"fmt"`, "", `func exercise(message string) {
	var b mcpTextBuilder; alias := b; fmt.Fprint(&alias.value, message)
}`), "aliasing mcpTextBuilder", 0, 0},
		{"builder var value-copy alias", goBuilderFixture(`"fmt"`, "", `func exercise(message string) {
	var b mcpTextBuilder; var alias = b; fmt.Fprint(&alias.value, message)
}`), "aliasing mcpTextBuilder", 0, 0},
		{"global builder value-copy alias", goBuilderFixture(`"fmt"`, "", `var b mcpTextBuilder
var alias = b
func exercise(message string) { fmt.Fprint(&alias.value, message) }
`), "aliasing mcpTextBuilder", 0, 0},
		{"builder tuple value-copy alias", goBuilderFixture(`"fmt"`, "", `func exercise(message string) {
	var b mcpTextBuilder; alias, other := b, 0; _ = other; fmt.Fprint(&alias.value, message)
}`), "aliasing mcpTextBuilder", 0, 0},
		{"dereferenced builder selector", goBuilderFixture(`"fmt"`, "", `func exercise(b *mcpTextBuilder, message string) {
	fmt.Fprint(&(*b).value, message)
}`), "storage access bypasses", 0, 0},
		{"addressed builder selector", goBuilderFixture(`"fmt"`, "", `func exercise(message string) {
	var b mcpTextBuilder; fmt.Fprint(&(&b).value, message)
}`), "storage access bypasses", 0, 0},
		{"nested selector indirection", goBuilderFixture(`"fmt"`, "", `func exercise(message string) {
	var b mcpTextBuilder; fmt.Fprint(&(((*(&b))).value), message)
}`), "storage access bypasses", 0, 0},
		{"builder method value", goBuilderFixture(`"fmt"`, templateHelper, `func exercise(message string) {
	var b mcpTextBuilder; emit := b.Template; emit(message)
}`), "function reference bypasses", 0, 0},
		{"builder method expression", goBuilderFixture(`"fmt"`, templateHelper, `func exercise(message string) {
	var b mcpTextBuilder; emit := (*mcpTextBuilder).Template; emit(&b, message)
}`), "function reference bypasses", 0, 0},
		{"builder return by value", goBuilderFixture("", "", `func leak(b mcpTextBuilder) mcpTextBuilder {
	return b
}`), "returning mcpTextBuilder", 0, 0},
		{"builder pass by value", goBuilderFixture("", "", `func consume(mcpTextBuilder) {}
func exercise() { var b mcpTextBuilder; consume(b) }
`), "storage access bypasses", 0, 0},
		{"builder storage method value", goBuilderFixture("", "", `func exercise(message string) {
	var b mcpTextBuilder; write := (&b).value.WriteString; _, _ = write(message)
}`), "function reference bypasses", 0, 0},
	}
}

func rejectedOutputReferenceFixtures() []goMCPFixture {
	return []goMCPFixture{
		{"direct tool result composite beside governed output", `package fixture
import wire "github.com/cordanaLLM/praetor/internal/mcp"
func exercise(message string) *wire.ToolResult {
	wire.TextResult("result: pass.")
	return &wire.ToolResult{Content: []wire.ContentItem{{Type: "text", Text: message}}}
}
`, "direct mcp.ToolResult", 0, 0},
		{"tool result aliases hide direct composites", `package fixture
import wire "github.com/cordanaLLM/praetor/internal/mcp"
type ResultAlias = wire.ToolResult
type ItemAlias = wire.ContentItem
func exercise(message string) *wire.ToolResult {
	wire.TextResult("result: pass.")
	return &ResultAlias{Content: []ItemAlias{{Type: "text", Text: message}}}
}
`, "mcp.ToolResult type reference", 0, 0},
		{"named tool result wrapper hides direct composite", `package fixture
import wire "github.com/cordanaLLM/praetor/internal/mcp"
type ResultWrapper wire.ToolResult
type ItemWrapper wire.ContentItem
func exercise(message string) *wire.ToolResult {
	wire.TextResult("result: pass.")
	item := ItemWrapper{Type: "text", Text: message}
	result := &ResultWrapper{Content: []wire.ContentItem{wire.ContentItem(item)}}
	return (*wire.ToolResult)(result)
}
`, "mcp.ToolResult type reference", 0, 0},
		{"new and field assignment hide direct result construction", `package fixture
import wire "github.com/cordanaLLM/praetor/internal/mcp"
func exercise(message string) *wire.ToolResult {
	wire.TextResult("result: pass.")
	result := new(wire.ToolResult)
	item := new(wire.ContentItem)
	item.Type = "text"
	item.Text = message
	result.Content = append(result.Content, *item)
	return result
}
`, "mcp.ToolResult type reference", 0, 0},
		{"zero values and field assignment hide direct result construction", `package fixture
import wire "github.com/cordanaLLM/praetor/internal/mcp"
func exercise(message string) *wire.ToolResult {
	wire.TextResult("result: pass.")
	var result wire.ToolResult
	var item wire.ContentItem
	item.Type = "text"
	item.Text = message
	result.Content = append(result.Content, item)
	return &result
}
`, "mcp.ToolResult type reference", 0, 0},
		{"canonical result text mutated after construction", `package fixture
import wire "github.com/cordanaLLM/praetor/internal/mcp"
func exercise(message string) *wire.ToolResult {
	result := wire.TextResult("result: pass.")
	result.Content[0].Text = message
	return result
}
`, "post-construction mcp result mutation", 0, 0},
		{"canonical result text mutated through slice alias", `package fixture
import wire "github.com/cordanaLLM/praetor/internal/mcp"
func exercise(message string) *wire.ToolResult {
	result := wire.TextResult("result: pass.")
	alias := result
	items := alias.Content
	items[0].Text = message
	return result
}
`, "post-construction mcp result mutation", 0, 0},
		{"canonical result text mutated through pointer aliases", `package fixture
import wire "github.com/cordanaLLM/praetor/internal/mcp"
func exercise(message string) *wire.ToolResult {
	result := wire.TextResult("result: pass.")
	item := &result.Content[0]
	text := &item.Text
	*text = message
	return result
}
`, "post-construction mcp result mutation", 0, 0},
		{"canonical result mutated through callback indirection", `package fixture
import wire "github.com/cordanaLLM/praetor/internal/mcp"
func mutate(setText func(string), message string) { setText(message) }
func exercise(message string) *wire.ToolResult {
	result := wire.TextResult("result: pass.")
	mutate(func(text string) { result.Content[0].Text = text }, message)
	return result
}
`, "post-construction mcp result mutation", 0, 0},
		{"canonical result escapes to mutating helper", `package fixture
import (
	"encoding/json"
	wire "github.com/cordanaLLM/praetor/internal/mcp"
)
func exercise(message string) *wire.ToolResult {
	result := wire.TextResult("result: pass.")
	payload, _ := json.Marshal(map[string]any{"content": []any{map[string]any{"type": "text", "text": message}}})
	_ = json.Unmarshal(payload, result)
	return result
}
`, "post-construction mcp result escape", 0, 0},
		{"wrapped canonical result escapes through returned alias", `package fixture
import (
	"encoding/json"
	wire "github.com/cordanaLLM/praetor/internal/mcp"
)
func makeResult() *wire.ToolResult { return wire.TextResult("result: pass.") }
func exercise(payload []byte) *wire.ToolResult {
	result := makeResult()
	alias := result
	_ = json.Unmarshal(payload, result)
	return alias
}
`, "post-construction mcp result escape", 0, 0},
		{"canonical result escapes through selector assignment", `package fixture
import wire "github.com/cordanaLLM/praetor/internal/mcp"
type resultBox struct { value any }
func exercise() *wire.ToolResult {
	result := wire.TextResult("result: pass.")
	var box resultBox
	box.value = result
	return result
}
`, "post-construction mcp result escape", 0, 0},
		{"canonical result escapes through keyed composite", `package fixture
import wire "github.com/cordanaLLM/praetor/internal/mcp"
func exercise() *wire.ToolResult {
	result := wire.TextResult("result: pass.")
	_ = struct{ value any }{value: result}
	return result
}
`, "post-construction mcp result escape", 0, 0},
		{"selector collision cannot borrow governed helper name", `package fixture
import (
	"fmt"
	"github.com/cordanaLLM/praetor/internal/mcp"
	"github.com/cordanaLLM/praetor/other"
)
type mcpGovernedText = other.Text
func mcpTextf(template string, args ...any) mcpGovernedText {
	return mcpGovernedText(fmt.Sprintf(template, args...))
}
func mcpComposedTextResult(text mcpGovernedText) *mcp.ToolResult {
	return mcp.TextResult(string(text))
}
func Safe() mcpGovernedText { return mcpTextf("result: pass.") }
func exercise() *mcp.ToolResult { return mcpComposedTextResult(other.Safe()) }
`, "requires text from", 0, 0},
		{"result function value", `package fixture
import wire "github.com/cordanaLLM/praetor/internal/mcp"
func exercise(message string) { emit := wire.TextResult; _ = emit(message) }
`, "function reference bypasses", 0, 0},
		{"fmt function value", `package fixture
import ( "fmt"; "io" )
func exercise(out io.Writer, message string) { emit := fmt.Fprint; _, _ = emit(out, message) }
`, "function reference bypasses", 0, 0},
		{"io function value", `package fixture
import "io"
func exercise(out io.Writer, message string) { emit := io.WriteString; _, _ = emit(out, message) }
`, "function reference bypasses", 0, 0},
		{"http function value", `package fixture
import "net/http"
func exercise(w http.ResponseWriter, message string) { emit := http.Error; emit(w, message, 500) }
`, "function reference bypasses", 0, 0},
		{"write string var method value", `package fixture
import "strings"
func exercise(message string) { var out strings.Builder; var write = out.WriteString; _, _ = write(message) }
`, "function reference bypasses", 0, 0},
		{"write string reassigned method value", `package fixture
import "strings"
func exercise(message string) { var out strings.Builder; var write func(string) (int, error); write = out.WriteString; _, _ = write(message) }
`, "function reference bypasses", 0, 0},
		{"write string grouped method value", `package fixture
import "strings"
func exercise(message string) { var out strings.Builder; var ( write = out.WriteString ); _, _ = write(message) }
`, "function reference bypasses", 0, 0},
		{"write string method expression", `package fixture
import "strings"
func exercise(out *strings.Builder, message string) { (*strings.Builder).WriteString(out, message) }
`, "function reference bypasses", 0, 0},
		{"fmt import alias", `package fixture
import ( f "fmt"; "io" )
func exercise(out io.Writer, message string) { f.Fprint(out, message) }
`, "unclassified dynamic", 0, 0},
		{"io import alias", `package fixture
import stream "io"
func exercise(out stream.Writer, message string) { stream.WriteString(out, message) }
`, "explicit classification", 0, 0},
		{"http import alias", `package fixture
import web "net/http"
func exercise(w web.ResponseWriter, message string) { web.Error(w, message, web.StatusBadRequest) }
`, "explicit protocol or untrusted classification", 0, 0},
	}
}

func rejectedGovernedBindingFixtures() []goMCPFixture {
	return []goMCPFixture{
		{"typed governed declaration with literal initializer", goGovernedFixture(`func exercise() *mcp.ToolResult {
	var text mcpGovernedText = "hidden"
	return mcpComposedTextResult(text)
}`), "assigned ungoverned text", 0, 0},
		{"governed value reassigned a literal", goGovernedFixture(`func exercise() *mcp.ToolResult {
	text := mcpTextf("result: pass.")
	text = "hidden"
	return mcpComposedTextResult(text)
}`), "assigned ungoverned text", 0, 0},
		{"governed function returns a literal", goGovernedFixture(`func hidden() mcpGovernedText {
	return "This sentence is hidden from Caveman."
}
func exercise() *mcp.ToolResult { return mcpComposedTextResult(hidden()) }`), "returns ungoverned text", 0, 0},
		{"global governed declaration initialized by a literal", goGovernedFixture(`var hidden mcpGovernedText = "This sentence is hidden from Caveman."
func hiddenText() mcpGovernedText { return hidden }
func exercise() *mcp.ToolResult { return mcpComposedTextResult(hiddenText()) }`), "assigned ungoverned text", 0, 0},
		{"governed return observes earlier unsafe binding", goGovernedFixture(`func hidden(stop bool) mcpGovernedText {
	text := mcpGovernedText("This sentence is hidden from Caveman.")
	if stop { return text }
	text = mcpTextf("result: pass.")
	return text
}
func exercise() *mcp.ToolResult { return mcpComposedTextResult(hidden(true)) }`), "returns ungoverned text", 0, 0},
		{"governed function value is not package trust", goGovernedFixture(`func safe() mcpGovernedText { return mcpTextf("result: pass.") }
func exercise() *mcp.ToolResult {
	producer := safe
	var text mcpGovernedText
	text = producer()
	return mcpComposedTextResult(text)
}`), "assigned ungoverned text", 0, 0},
	}
}

func acceptedOutputReferenceFixtures() []goMCPFixture {
	classification := `type mcpTextClassification uint8
const ( mcpTextStructuredJSON mcpTextClassification = iota + 1; mcpTextUntrusted; mcpTextProtocol )
func mcpClassifiedText(text string, _ mcpTextClassification) string { return text }`
	templateHelper := `func (b *mcpTextBuilder) Template(template string, args ...any) {
	fmt.Fprintf(&b.value, template, args...)
}`
	return []goMCPFixture{
		{"aliased fmt direct call", `package fixture
import ( f "fmt"; "io" )
func exercise(out io.Writer, status string) { f.Fprintf(out, "result: %s.", status) }
`, "", 1, 0},
		{"parenthesized result direct call", `package fixture
import wire "github.com/cordanaLLM/praetor/internal/mcp"
func exercise() { (wire.TextResult)("result: pass.") }
`, "", 1, 0},
		{"aliased classified transports", `package fixture
import ( stream "io"; web "net/http" )
` + classification + `
func exercise(out stream.Writer, w web.ResponseWriter) {
	stream.WriteString(out, mcpClassifiedText(": keepalive\n\n", mcpTextProtocol))
	web.Error(w, mcpClassifiedText("Bad Request", mcpTextProtocol), web.StatusBadRequest)
}
`, "", 2, 2},
		{"direct builder helper", goBuilderFixture(`"fmt"`, templateHelper, `func exercise(status string) {
	var b mcpTextBuilder; b.Template("result: %s.", status)
}`), "", 1, 0},
		{"global direct builder helper", goBuilderFixture(`"fmt"`, templateHelper, `var b mcpTextBuilder
func exercise(status string) { b.Template("result: %s.", status) }
`), "", 1, 0},
		{"parenthesized write string direct call", `package fixture
import "strings"
func exercise() { var out strings.Builder; (out.WriteString)("result: pass.") }
`, "", 1, 0},
		{"parenthesized aliased package calls", `package fixture
import ( f "fmt"; stream "io"; web "net/http" )
` + classification + `
func exercise(out stream.Writer, w web.ResponseWriter, status string) {
	(f.Fprintf)(out, "result: %s.", status)
	(stream.WriteString)(out, mcpClassifiedText(": keepalive\n\n", mcpTextProtocol))
	(web.Error)(w, mcpClassifiedText("Bad Request", mcpTextProtocol), web.StatusBadRequest)
}
`, "", 3, 2},
		{"typed governed binding assigned trusted text", goGovernedFixture(`func exercise() *mcp.ToolResult {
	var text mcpGovernedText
	text = mcpTextf("result: pass.")
	return mcpComposedTextResult(text)
}`), "", 2, 1},
		{"governed function returns trusted text", goGovernedFixture(`func safe() mcpGovernedText {
	return mcpTextf("result: pass.")
}
func exercise() *mcp.ToolResult { return mcpComposedTextResult(safe()) }`), "", 2, 1},
		{"global governed declaration initialized by trusted text", goGovernedFixture(`var safeText = mcpTextf("result: pass.")
func safe() mcpGovernedText { return safeText }
func exercise() *mcp.ToolResult { return mcpComposedTextResult(safe()) }`), "", 2, 1},
		{"canonical result metadata mutation", `package fixture
import wire "github.com/cordanaLLM/praetor/internal/mcp"
func exercise(failed bool) *wire.ToolResult {
	result := wire.TextResult("result: pass.")
	result.IsError = failed
	return result
}
`, "", 1, 0},
		{"canonical result content read", `package fixture
import wire "github.com/cordanaLLM/praetor/internal/mcp"
func exercise() *wire.ToolResult {
	result := wire.TextResult("result: pass.")
	_ = len(result.Content)
	return result
}
`, "", 1, 0},
		{"canonical result alias return", `package fixture
import wire "github.com/cordanaLLM/praetor/internal/mcp"
func exercise() *wire.ToolResult {
	result := wire.TextResult("result: pass.")
	alias := result
	return alias
}
`, "", 1, 0},
	}
}

func goBuilderFixture(extraImports, helpers, exercise string) string {
	return "package fixture\nimport (\n\"strings\"\n" + extraImports + "\n)\n" +
		"type mcpTextBuilder struct { value strings.Builder }\n" + helpers + "\n" + exercise + "\n"
}

func goGovernedFixture(exercise string) string {
	return `package fixture
import (
	"fmt"
	"github.com/cordanaLLM/praetor/internal/mcp"
)
type mcpGovernedText string
func mcpTextf(template string, args ...any) mcpGovernedText {
	return mcpGovernedText(fmt.Sprintf(template, args...))
}
func mcpComposedTextResult(text mcpGovernedText) *mcp.ToolResult {
	return mcp.TextResult(string(text))
}
` + exercise + "\n"
}

func writeCompileValidGoFixtures(t *testing.T, root string, fixtures []goMCPFixture) {
	t.Helper()
	writeSourceFile(t, root, "go.mod", "module github.com/cordanaLLM/praetor\n\ngo 1.24\n")
	writeSourceFile(t, root, "internal/mcp/mcp.go", `package mcp
type ContentItem struct { Type string; Text string }
type ToolResult struct { Content []ContentItem; IsError bool }
func TextResult(string) *ToolResult { return &ToolResult{} }
func ErrorResult(string) *ToolResult { return &ToolResult{} }
`)
	writeSourceFile(t, root, "other/other.go", `package other
type Text string
func Safe() Text { return Text("This sentence is hidden from Caveman.") }
`)
	for index, fixture := range fixtures {
		writeSourceFile(t, root, fmt.Sprintf("case-%02d/main.go", index), fixture.source)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("compile adversarial Go fixtures: %v\n%s", err, output)
	}
}

func TestCoverageDigestBindsDiscoveredFileIdentity(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "mcp/output.go", `package fixture
import "github.com/cordanaLLM/praetor/internal/mcp"
func output() { mcp.TextResult("result: pass.") }
`)
	input := sourceInput("mcp", config.SourceFormatGo, "mcp.outputs")
	input.Surface = config.SurfaceMCP
	before, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
	if err != nil {
		t.Fatal(err)
	}
	writeSourceFile(t, root, "mcp/no_output.go", "package fixture\n")
	after, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Sources) != len(after.Sources) || before.SHA256 == after.SHA256 {
		t.Fatalf("discovered file identity missing from coverage digest: before=%s after=%s sources=%d/%d",
			before.SHA256, after.SHA256, len(before.Sources), len(after.Sources))
	}
}

func TestExtractGoMCPResultAliasDepthBoundary(t *testing.T) {
	root := t.TempDir()
	input := sourceInput("mcp/output.go", config.SourceFormatGo, "mcp.outputs")
	input.Surface = config.SurfaceMCP
	writeSourceFile(t, root, input.Path, resultAliasChainSource(maxGoSelectorDepth))
	result, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
	if err != nil || result.Applicable != 1 {
		t.Fatalf("exact result alias bound rejected: result=%+v err=%v", result, err)
	}
	writeSourceFile(t, root, input.Path, resultAliasChainSource(maxGoSelectorDepth+1))
	if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil ||
		!strings.Contains(err.Error(), "alias chain exceeds") {
		t.Fatalf("result alias bound plus one accepted: %v", err)
	}
}

func resultAliasChainSource(aliasCount int) string {
	var source strings.Builder
	source.WriteString(`package fixture
import "github.com/cordanaLLM/praetor/internal/mcp"
func makeResult() *mcp.ToolResult { return mcp.TextResult("result: pass.") }
func output() *mcp.ToolResult {
	result := makeResult()
`)
	previous := "result"
	for index := 0; index < aliasCount; index++ {
		name := fmt.Sprintf("alias%d", index)
		fmt.Fprintf(&source, "\t%s := %s\n", name, previous)
		previous = name
	}
	fmt.Fprintf(&source, "\treturn %s\n}\n", previous)
	return source.String()
}

func TestExtractGoMCPRuntimeClassifiesTransportOutputs(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "mcp/transport.go", `package main
func transport() {
	fmt.Fprintf(out, mcpClassifiedText("%s\n", mcpTextProtocol), data)
	io.WriteString(w, mcpClassifiedText(": keepalive\n\n", mcpTextProtocol))
	http.Error(w, mcpClassifiedText("Bad Request", mcpTextProtocol), 400)
	http.Error(w, mcpClassifiedText(err.Error(), mcpTextUntrusted), 500)
}
`)
	input := sourceInput("mcp/transport.go", config.SourceFormatGo, "mcp.outputs")
	input.Surface = config.SurfaceMCP
	result, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
	if err != nil || result.Applicable != 0 || result.NotApplicable != 4 || len(result.Sources) != 4 {
		t.Fatalf("transport classification: result=%+v err=%v", result, err)
	}
	for _, source := range result.Sources {
		if source.NotApplicable != "protocol" && source.NotApplicable != "untrusted-passthrough" {
			t.Errorf("broad transport classification accepted: %+v", source)
		}
	}
}

func TestExtractGoMCPRuntimeRejectsTransportBypasses(t *testing.T) {
	fixtures := map[string]string{
		"http static":     `http.Error(w, "Bad Request", 400)`,
		"http dynamic":    `http.Error(w, err.Error(), 500)`,
		"io write":        `io.WriteString(w, ": keepalive\n\n")`,
		"broad forwarded": `http.Error(w, mcpClassifiedText(message, mcpTextForwarded), 500)`,
	}
	for name, body := range fixtures {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeSourceFile(t, root, "mcp/transport.go", "package main\nfunc transport() {\n"+body+"\n}\n")
			input := sourceInput("mcp/transport.go", config.SourceFormatGo, "mcp.outputs")
			input.Surface = config.SurfaceMCP
			if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil ||
				!strings.Contains(err.Error(), "classification") {
				t.Fatalf("transport bypass accepted: %v", err)
			}
		})
	}
}

func TestExtractGoMCPRuntimeRejectsImportAndHelperSpoofs(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "mcp/alias.go", `package main
import x "github.com/cordanaLLM/praetor/internal/mcp"
func tools() { x.TextResult(message) }
`)
	input := sourceInput("mcp/alias.go", config.SourceFormatGo, "mcp.outputs")
	input.Surface = config.SurfaceMCP
	if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil ||
		!strings.Contains(err.Error(), "unclassified dynamic") {
		t.Fatalf("aliased mcp import bypass accepted: %v", err)
	}
	writeSourceFile(t, root, "mcp/alias.go", `package main
import . "github.com/cordanaLLM/praetor/internal/mcp"
func tools() { TextResult(message) }
`)
	if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil ||
		!strings.Contains(err.Error(), "dot import") {
		t.Fatalf("dot-import mcp bypass accepted: %v", err)
	}
	writeSourceFile(t, root, "mcp/alias.go", `package main
func mcpTextResult(text string, class mcpTextClassification) *mcp.ToolResult {
	return mcp.TextResult(other)
}
`)
	if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil ||
		!strings.Contains(err.Error(), "fixed implementation") {
		t.Fatalf("spoofed classification helper accepted: %v", err)
	}
	writeSourceFile(t, root, "mcp/alias.go", `package main
func mcpTextResult(text any, _ mcpTextClassification) *mcp.ToolResult {
	return mcp.TextResult(text)
}
`)
	if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil ||
		!strings.Contains(err.Error(), "fixed implementation") {
		t.Fatalf("spoofed helper signature accepted: %v", err)
	}
	writeSourceFile(t, root, "mcp/alias.go", `package main
type mcpTextBuilder struct{ value strings.Builder }
func (b *mcpTextBuilder) Template(template string, args ...any) {
	b.value.WriteString(template)
}
func tools() { var b mcpTextBuilder; b.Template("result: hidden.") }
`)
	if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil ||
		!strings.Contains(err.Error(), "fixed implementation") {
		t.Fatalf("spoofed builder helper accepted: %v", err)
	}
}

func TestExtractGoMCPDescriptionsIgnoreUnrelatedFields(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "mcp/tools.go", `package main
func tools() {
	_ = otherSchema{Description: "not agent text"}
	_ = mcp.ToolInputSchema{Properties: map[string]mcp.PropertySchema{
		"path": {Description: "input: path."},
	}}
}
`)
	input := sourceInput("mcp", config.SourceFormatGo, "mcp.descriptions")
	input.Surface = config.SurfaceMCP
	result, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
	if err != nil || len(result.Sources) != 1 || result.Sources[0].Text != "input: path." {
		t.Fatalf("unrelated Description field entered census: sources=%+v err=%v", result.Sources, err)
	}
}

func TestCoverageDigestIgnoresPhysicalLineMovement(t *testing.T) {
	root := t.TempDir()
	input := sourceInput("hooks/check.py", config.SourceFormatPython, "")
	writeSourceFile(t, root, "hooks/check.py", `print("result: pass.")`+"\n")
	before, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
	if err != nil {
		t.Fatal(err)
	}
	writeSourceFile(t, root, "hooks/check.py", "# moved by a comment\n"+`print("result: pass.")`+"\n")
	after, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
	if err != nil {
		t.Fatal(err)
	}
	if before.SHA256 != after.SHA256 {
		t.Fatalf("physical line movement changed semantic digest: %s != %s", before.SHA256, after.SHA256)
	}
}

func TestExtractGoMCPRuntimeRejectsDynamicText(t *testing.T) {
	for name, body := range map[string]string{
		"tool description":     `mcp.NewReadOnlyTool("inspect", description, schema, handler)`,
		"property description": `_ = mcp.ToolInputSchema{Properties: map[string]mcp.PropertySchema{"path": {Description: description}}}`,
		"tool output":          `mcp.TextResult(message)`,
		"builder output":       `out.WriteString(message)`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeSourceFile(t, root, "mcp/tools.go", "package main\nfunc tools() {\n"+body+"\n}\n")
			selector := "mcp.outputs"
			if strings.Contains(name, "description") {
				selector = "mcp.descriptions"
			}
			input := sourceInput("mcp", config.SourceFormatGo, selector)
			input.Surface = config.SurfaceMCP
			if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil ||
				!strings.Contains(err.Error(), "unclassified dynamic") {
				t.Fatalf("dynamic MCP text accepted: %v", err)
			}
		})
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

func TestExtractInputCountBoundary(t *testing.T) {
	root := t.TempDir()
	values := make(map[string]string, config.MaxRegisterSourceInputs)
	inputs := make([]config.RegisterSourceInput, config.MaxRegisterSourceInputs)
	for index := range inputs {
		key := fmt.Sprintf("message_%02d", index)
		values[key] = "result: pass."
		inputs[index] = sourceInput("prompts/messages.json", config.SourceFormatJSON, key)
	}
	writeJSONSource(t, root, "prompts/messages.json", values)
	result, err := ExtractInputs(t.Context(), root, inputs)
	if err != nil || len(result.Sources) != config.MaxRegisterSourceInputs {
		t.Fatalf("exact %d inputs: values=%d err=%v", config.MaxRegisterSourceInputs, len(result.Sources), err)
	}
	above := append(append([]config.RegisterSourceInput(nil), inputs...), inputs[0])
	if _, err = ExtractInputs(t.Context(), root, above); err == nil || !strings.Contains(err.Error(), "1..64 rows") {
		t.Fatalf("%d inputs accepted: %v", config.MaxRegisterSourceInputs+1, err)
	}
}

func TestExtractDiscoveredFileCountBoundary(t *testing.T) {
	root := t.TempDir()
	for index := 0; index < contextopt.MaxSources; index++ {
		writeSourceFile(t, root, fmt.Sprintf("hooks/check-%02d.py", index), `print("result: pass.")`+"\n")
	}
	input := sourceInput("hooks", config.SourceFormatPython, "")
	result, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
	if err != nil || len(result.Sources) != contextopt.MaxSources {
		t.Fatalf("exact %d discovered files: values=%d err=%v", contextopt.MaxSources, len(result.Sources), err)
	}
	writeSourceFile(t, root, "hooks/check-64.py", `print("result: pass.")`+"\n")
	if _, err = ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil || !strings.Contains(err.Error(), "exceeds 64") {
		t.Fatalf("%d discovered files accepted: %v", contextopt.MaxSources+1, err)
	}
}

func TestExtractRejectsSymlinkedSourcePaths(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "targets/check.py", `print("result: pass.")`+"\n")
	if err := os.Symlink(filepath.Join(root, "targets", "check.py"), filepath.Join(root, "linked.py")); err != nil {
		index := []byte("120000 " + strings.Repeat("a", 40) + " 0\tlinked.py\x00")
		if indexErr := validateTrackedSourceIndex(index, []string{"linked.py"}); indexErr == nil ||
			!strings.Contains(indexErr.Error(), "indexed symlink") {
			t.Fatalf("alternate indexed-symlink coverage failed after filesystem symlink error %v: %v", err, indexErr)
		}
		return
	}
	input := sourceInput("linked.py", config.SourceFormatPython, "")
	if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil ||
		!strings.Contains(err.Error(), "symlink") {
		t.Fatalf("direct symlink source accepted: %v", err)
	}

	if err := os.Mkdir(filepath.Join(root, "hooks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "targets", "check.py"), filepath.Join(root, "hooks", "check.py")); err != nil {
		t.Fatal(err)
	}
	input = sourceInput("hooks", config.SourceFormatPython, "")
	if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil ||
		!strings.Contains(err.Error(), "symlink") {
		t.Fatalf("directory symlink source accepted: %v", err)
	}

	if err := os.Symlink(filepath.Join(root, "targets"), filepath.Join(root, "linked-dir")); err != nil {
		t.Fatal(err)
	}
	input = sourceInput("linked-dir/check.py", config.SourceFormatPython, "")
	if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil ||
		!strings.Contains(err.Error(), "symlink") {
		t.Fatalf("intermediate symlink source accepted: %v", err)
	}
}

type syntheticSourceInfo struct {
	name string
	mode fs.FileMode
}

func (i syntheticSourceInfo) Name() string       { return i.name }
func (i syntheticSourceInfo) Size() int64        { return 0 }
func (i syntheticSourceInfo) Mode() fs.FileMode  { return i.mode }
func (i syntheticSourceInfo) ModTime() time.Time { return time.Time{} }
func (i syntheticSourceInfo) IsDir() bool        { return i.mode.IsDir() }
func (i syntheticSourceInfo) Sys() any           { return nil }

func TestSourceSymlinkChecksWithoutHostSymlinks(t *testing.T) {
	root := t.TempDir()
	for name, symlinkPath := range map[string]string{
		"direct":       filepath.Join(root, "linked.py"),
		"intermediate": filepath.Join(root, "linked-dir"),
	} {
		t.Run(name, func(t *testing.T) {
			requested := symlinkPath
			if name == "intermediate" {
				requested = filepath.Join(symlinkPath, "check.py")
			}
			lstat := func(path string) (fs.FileInfo, error) {
				mode := fs.FileMode(0)
				if filepath.Clean(path) == filepath.Clean(symlinkPath) {
					mode = fs.ModeSymlink
				}
				return syntheticSourceInfo{name: filepath.Base(path), mode: mode}, nil
			}
			if err := rejectSourceSymlinksWith(root, requested, lstat); err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("synthetic %s symlink accepted: %v", name, err)
			}
		})
	}
	walker := sourceDirectoryWalker{ctx: t.Context()}
	entry := fs.FileInfoToDirEntry(syntheticSourceInfo{name: "check.py", mode: fs.ModeSymlink})
	if err := walker.visit(filepath.Join(root, "hooks", "check.py"), entry, nil); err == nil ||
		!strings.Contains(err.Error(), "symlink") {
		t.Fatalf("synthetic directory-child symlink accepted: %v", err)
	}
}

func TestExtractDeclaredRejectsIndexedSymlink(t *testing.T) {
	root := t.TempDir()
	path := "hooks/check.py"
	writeSourceFile(t, root, path, `print("result: pass.")`+"\n")
	input := sourceInput(path, config.SourceFormatPython, "")
	result, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
	if err != nil {
		t.Fatal(err)
	}
	index := []byte("120000 " + strings.Repeat("a", 40) + " 0\t" + path + "\x00")
	installSourceGitIndex(t, index)
	declared := &config.RegisterSources{Expected: result.Applicable, NotApplicable: result.NotApplicable,
		SHA256: result.SHA256, Inputs: []config.RegisterSourceInput{input}}
	if _, err := ExtractDeclared(t.Context(), root, declared); err == nil || !strings.Contains(err.Error(), "indexed symlink") {
		t.Fatalf("indexed symlink accepted: %v", err)
	}
}

func TestExtractDeclaredFailsClosedWithoutGit(t *testing.T) {
	root := t.TempDir()
	path := "hooks/check.py"
	writeSourceFile(t, root, path, `print("result: pass.")`+"\n")
	input := sourceInput(path, config.SourceFormatPython, "")
	result, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	declared := &config.RegisterSources{Expected: result.Applicable, NotApplicable: result.NotApplicable,
		SHA256: result.SHA256, Inputs: []config.RegisterSourceInput{input}}
	if _, err := ExtractDeclared(t.Context(), root, declared); err == nil ||
		!strings.Contains(err.Error(), "list tracked caveman sources") {
		t.Fatalf("missing Git did not fail closed: %v", err)
	}
}

func TestTrackedSourceIndexModesAndBounds(t *testing.T) {
	path := "hooks/check.py"
	regular := string(regularTrackedIndex(path))
	for name, fixture := range map[string]struct {
		data []byte
		want string
	}{
		"regular":              {[]byte(regular), ""},
		"executable":           {[]byte(strings.Replace(regular, "100644", "100755", 1)), ""},
		"symlink":              {[]byte("120000 " + strings.Repeat("a", 40) + " 0\t" + path + "\x00"), "indexed symlink"},
		"gitlink":              {[]byte("160000 " + strings.Repeat("a", 40) + " 0\t" + path + "\x00"), "unsupported index mode"},
		"conflict stage one":   {[]byte("100644 " + strings.Repeat("a", 40) + " 1\t" + path + "\x00"), "malformed entry"},
		"conflict stage two":   {[]byte("100644 " + strings.Repeat("a", 40) + " 2\t" + path + "\x00"), "malformed entry"},
		"conflict stage three": {[]byte("100644 " + strings.Repeat("a", 40) + " 3\t" + path + "\x00"), "malformed entry"},
		"malformed":            {[]byte("100644 missing-fields\x00"), "malformed entry"},
		"malformed object":     {[]byte("100644 xyz 0\t" + path + "\x00"), "malformed entry"},
		"missing":              {nil, "not tracked"},
		"unexpected":           {regularTrackedIndex("other.py"), "unexpected path"},
		"duplicate":            {[]byte(regular + regular), "repeats"},
		"truncated":            {[]byte(strings.TrimSuffix(regular, "\x00")), "truncated framing"},
		"empty record":         {[]byte(regular + "\x00"), "empty entry"},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateTrackedSourceIndex(fixture.data, []string{path})
			if fixture.want == "" && err != nil {
				t.Fatalf("regular tracked source rejected: %v", err)
			}
			if fixture.want != "" && (err == nil || !strings.Contains(err.Error(), fixture.want)) {
				t.Fatalf("tracked index entry: got %v, want %q", err, fixture.want)
			}
		})
	}
	paths := make([]string, maxTrackedIndexEntries)
	for index := range paths {
		paths[index] = fmt.Sprintf("hooks/check-%02d.py", index)
	}
	if err := validateTrackedSourceIndex(regularTrackedIndex(paths...), paths); err != nil {
		t.Fatalf("exact tracked index bound rejected: %v", err)
	}
	abovePaths := append(append([]string(nil), paths...), "hooks/check-over.go")
	if err := validateTrackedSourceIndex(regularTrackedIndex(abovePaths...), abovePaths); err == nil ||
		!strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("over-bound tracked index accepted: %v", err)
	}
}

func TestExtractTableValueBoundary(t *testing.T) {
	root := t.TempDir()
	values := make([]string, config.MaxRegisterSourceTableValues)
	for index := range values {
		values[index] = "result: pass."
	}
	writeJSONSource(t, root, "prompts/messages.json", map[string]any{"messages": values})
	input := sourceInput("prompts/messages.json", config.SourceFormatJSON, "messages.*")
	result, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
	if err != nil || len(result.Sources) != config.MaxRegisterSourceTableValues {
		t.Fatalf("exact %d table values: values=%d err=%v", config.MaxRegisterSourceTableValues, len(result.Sources), err)
	}
	values = append(values, "next: stop.")
	writeJSONSource(t, root, "prompts/messages.json", map[string]any{"messages": values})
	if _, err = ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil || !strings.Contains(err.Error(), "exceeds 256") {
		t.Fatalf("%d table values accepted: %v", config.MaxRegisterSourceTableValues+1, err)
	}
}

// TestExtractOutputCountBoundary pins the repository aggregate separately from the table
// bound: one shell hook may emit more values than one table holds, up to the aggregate.
func TestExtractOutputCountBoundary(t *testing.T) {
	root := t.TempDir()
	line := "echo \"result: pass.\"\n"
	input := sourceInput("hooks/emit.sh", config.SourceFormatShell, "")
	writeSourceFile(t, root, "hooks/emit.sh", strings.Repeat(line, config.MaxRegisterSourceOutputs))
	result, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
	if err != nil || result.Applicable != config.MaxRegisterSourceOutputs {
		t.Fatalf("exact %d outputs: values=%d err=%v", config.MaxRegisterSourceOutputs, result.Applicable, err)
	}
	writeSourceFile(t, root, "hooks/emit.sh", strings.Repeat(line, config.MaxRegisterSourceOutputs+1))
	if _, err = ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil ||
		!strings.Contains(err.Error(), "exceed 16384 applicable values") {
		t.Fatalf("%d outputs accepted: %v", config.MaxRegisterSourceOutputs+1, err)
	}
}

func TestExtractFileByteBoundary(t *testing.T) {
	root := t.TempDir()
	prefix := "echo \"result: pass.\"\n#"
	exact := prefix + strings.Repeat("x", contextopt.MaxSourceBytes-len(prefix))
	writeSourceFile(t, root, "hooks/exact.sh", exact)
	input := sourceInput("hooks/exact.sh", config.SourceFormatShell, "")
	if result, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err != nil || len(result.Sources) != 1 {
		t.Fatalf("exact %d-byte file rejected: values=%d err=%v", contextopt.MaxSourceBytes, len(result.Sources), err)
	}
	writeSourceFile(t, root, "hooks/exact.sh", exact+"x")
	if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil || !strings.Contains(err.Error(), "at most 1048576 bytes") {
		t.Fatalf("%d-byte file accepted: %v", contextopt.MaxSourceBytes+1, err)
	}
}

func TestExtractValueByteBoundary(t *testing.T) {
	root := t.TempDir()
	input := sourceInput("prompts/message.json", config.SourceFormatJSON, "message")
	writeJSONSource(t, root, "prompts/message.json", map[string]string{"message": strings.Repeat("x", maxSelectionBytes)})
	if result, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err != nil || len(result.Sources) != 1 {
		t.Fatalf("exact %d-byte value rejected: values=%d err=%v", maxSelectionBytes, len(result.Sources), err)
	}
	writeJSONSource(t, root, "prompts/message.json", map[string]string{"message": strings.Repeat("x", maxSelectionBytes+1)})
	if _, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil || !strings.Contains(err.Error(), "1..65536 bytes") {
		t.Fatalf("%d-byte value accepted: %v", maxSelectionBytes+1, err)
	}
}

func TestExtractSelectedByteBoundary(t *testing.T) {
	root := t.TempDir()
	for index := 0; index < contextopt.MaxSourceBytes/maxSelectionBytes; index++ {
		writeJSONSource(t, root, fmt.Sprintf("prompts/message-%02d.json", index),
			map[string]string{"message": strings.Repeat("x", maxSelectionBytes)})
	}
	input := sourceInput("prompts", config.SourceFormatJSON, "message")
	result, err := ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
	if err != nil || len(result.Sources) != contextopt.MaxSourceBytes/maxSelectionBytes {
		t.Fatalf("exact %d selected bytes: values=%d err=%v", contextopt.MaxSourceBytes, len(result.Sources), err)
	}
	writeJSONSource(t, root, "prompts/message-extra.json", map[string]string{"message": "x"})
	if _, err = ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input}); err == nil || !strings.Contains(err.Error(), "selections exceed 1048576") {
		t.Fatalf("%d selected bytes accepted: %v", contextopt.MaxSourceBytes+1, err)
	}
}

func writeJSONSource(t *testing.T, root, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeSourceFile(t, root, path, string(data))
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
