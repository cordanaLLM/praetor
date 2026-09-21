package config

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

const registerManifestHead = "version: 1\n"

func loadRegisterManifest(t *testing.T, section string) (*Manifest, error) {
	t.Helper()
	return LoadManifest(writeManifest(t, registerManifestHead+section))
}

func TestLoadManifestRegisterPositive(t *testing.T) {
	m, err := loadRegisterManifest(t, `register:
  surfaces:
    forge: social
    docs: docs
    agent: internal
  tasks:
    architecture_synthesis: docs
    waiver_signoff: {register: social, max_tokens: 1024}
  evidence:
    inline_max_lines: 40
    inline_max_tokens: 1200
`)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if got := m.Register.Tasks["architecture_synthesis"]; got.Register != TextRegisterDocs || got.MaxTokens != 0 {
		t.Errorf("scalar row = %+v, want docs without a budget", got)
	}
	if got := m.Register.Tasks["waiver_signoff"]; got.Register != TextRegisterSocial || got.MaxTokens != 1024 {
		t.Errorf("mapping row = %+v, want social with 1024 tokens", got)
	}
	policy := m.EffectiveRegister()
	if policy.Evidence.InlineMaxLines != 40 || policy.Evidence.InlineMaxTokens != 1200 {
		t.Errorf("evidence bounds = %+v, want the tightened 40 / 1200", policy.Evidence)
	}
	if got := policy.Tasks["function_docstrings"].Register; got != TextRegisterDocs {
		t.Errorf("default row lost: function_docstrings = %q", got)
	}
}

func TestLoadManifestRegisterNegative(t *testing.T) {
	cases := map[string]struct{ section, want string }{
		"unknown register":       {"register: {surfaces: {forge: loud}}\n", `unsupported text register "loud"`},
		"non-string register":    {"register: {surfaces: {forge: 3}}\n", "text register must be a string enum"},
		"unknown surface":        {"register: {surfaces: {slack: social}}\n", `unknown register surface "slack"`},
		"misspelled section":     {"regsiter: {}\n", "regsiter"},
		"misspelled nested key":  {"register: {surface: {forge: social}}\n", "surface"},
		"unknown task field":     {"register: {tasks: {ci_debugging: {register: docs, budget: 300}}}\n", `"budget"`},
		"duplicated task field":  {"register: {tasks: {ci_debugging: {register: docs, register: social}}}\n", `"register"`},
		"row without a register": {"register: {tasks: {ci_debugging: {max_tokens: 512}}}\n", `register task "ci_debugging"`},
		"non-integer budget":     {"register: {tasks: {ci_debugging: {register: docs, max_tokens: many}}}\n", "must be an integer"},
		"budget below the floor": {"register: {tasks: {ci_debugging: {register: docs, max_tokens: 200}}}\n", `register max_tokens for "ci_debugging" must be 256..8192`},
		"budget above the cap":   {"register: {tasks: {ci_debugging: {register: docs, max_tokens: 8193}}}\n", `register max_tokens for "ci_debugging" must be 256..8192`},
		"explicit zero budget":   {"register: {tasks: {ci_debugging: {register: docs, max_tokens: 0}}}\n", "must be 256..8192"},
		"padded task label":      {"register: {tasks: {\" ci_debugging\": docs}}\n", `invalid register task label " ci_debugging"`},
		"zero evidence lines":    {"register: {evidence: {inline_max_lines: 0}}\n", "register evidence bound must be 1..58"},
		"loosened evidence":      {"register: {evidence: {inline_max_lines: 59}}\n", "register evidence bound must be 1..58"},
		"negative evidence":      {"register: {evidence: {inline_max_lines: -1}}\n", "register evidence bound must be 1..58"},
		"loosened token bound":   {"register: {evidence: {inline_max_tokens: 1501}}\n", "register evidence bound must be 1..1500"},
		"unknown evidence field": {"register: {evidence: {inline_max_bytes: 10}}\n", `"inline_max_bytes"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadRegisterManifest(t, tc.section)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestLoadManifestRegisterBoundary(t *testing.T) {
	m, err := loadRegisterManifest(t, "register: {}\n")
	if err != nil {
		t.Fatalf("empty section: %v", err)
	}
	if got := m.EffectiveRegister(); !reflect.DeepEqual(got, DefaultRegisterPolicy()) {
		t.Errorf("empty section = %+v, want the defaults", got)
	}
	for _, accepted := range []string{
		"register: {tasks: {ci_debugging: {register: docs, max_tokens: 256}}}\n",
		"register: {tasks: {ci_debugging: {register: docs, max_tokens: 8192}}}\n",
		"register: {evidence: {inline_max_lines: 1, inline_max_tokens: 1}}\n",
		"register: {evidence: {inline_max_lines: 58, inline_max_tokens: 1500}}\n",
		registerRows(MaxRegisterTaskRows),
	} {
		if _, err := loadRegisterManifest(t, accepted); err != nil {
			t.Errorf("section %.60q must load: %v", accepted, err)
		}
	}
	_, err = loadRegisterManifest(t, registerRows(MaxRegisterTaskRows+1))
	if err == nil || !strings.Contains(err.Error(), "register tasks exceed 64 rows") {
		t.Errorf("65 rows: error = %v, want the row bound", err)
	}
}

func registerRows(count int) string {
	var section strings.Builder
	section.WriteString("register:\n  tasks:\n")
	for i := 0; i < count; i++ {
		fmt.Fprintf(&section, "    task_%02d: internal\n", i)
	}
	return section.String()
}

func TestEffectiveRegister(t *testing.T) {
	var absent *Manifest
	if got := absent.EffectiveRegister(); !reflect.DeepEqual(got, DefaultRegisterPolicy()) {
		t.Errorf("nil manifest = %+v, want the defaults", got)
	}
	m := &Manifest{Register: &RegisterPolicy{
		Surfaces: map[RegisterSurface]TextRegister{SurfaceForge: TextRegisterDocs},
		Tasks:    map[string]RegisterTask{"waiver_signoff": {Register: TextRegisterInternal, MaxTokens: 512}},
		Evidence: EvidenceBounds{InlineMaxLines: 40, InlineMaxTokens: 9000},
	}}
	policy := m.EffectiveRegister()
	if policy.Surfaces[SurfaceForge] != TextRegisterDocs || policy.Surfaces[SurfaceAgent] != TextRegisterInternal {
		t.Errorf("surfaces = %+v, want forge replaced and agent kept", policy.Surfaces)
	}
	if got := policy.Tasks["waiver_signoff"]; got.Register != TextRegisterInternal || got.MaxTokens != 512 {
		t.Errorf("explicit row must win over the default: %+v", got)
	}
	if policy.Evidence.InlineMaxLines != 40 || policy.Evidence.InlineMaxTokens != EvidenceInlineMaxTokensDefault {
		t.Errorf("evidence = %+v, want 40 lines and an untouched token default (tighten only)", policy.Evidence)
	}
	if DefaultRegisterPolicy().Surfaces[SurfaceForge] != TextRegisterSocial {
		t.Error("EffectiveRegister mutated the defaults")
	}
}

func TestDefaultRegisterPolicyShipsNoBudget(t *testing.T) {
	policy := DefaultRegisterPolicy()
	if err := policy.validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
	for label, row := range policy.Tasks {
		if row.MaxTokens != 0 {
			t.Errorf("default row %s ships a %d-token budget; budgets come from measured data", label, row.MaxTokens)
		}
	}
}

func TestRegisterResolve(t *testing.T) {
	policy := DefaultRegisterPolicy()
	policy.Tasks["waiver_signoff"] = RegisterTask{Register: TextRegisterSocial, MaxTokens: 1024}
	cases := []struct {
		name    string
		surface RegisterSurface
		task    string
		want    Resolution
	}{
		{"forge ignores the task", SurfaceForge, "ci_debugging", Resolution{TextRegisterSocial, 0, "surfaces.forge", ""}},
		{"docs ignores the task", SurfaceDocs, "waiver_signoff", Resolution{TextRegisterDocs, 0, "surfaces.docs", ""}},
		{"agent without a row", SurfaceAgent, "ci_debugging", Resolution{TextRegisterInternal, 0, "surfaces.agent", ""}},
		{"agent with a row", SurfaceAgent, "waiver_signoff", Resolution{TextRegisterSocial, 1024, "tasks.waiver_signoff", ""}},
		{"empty surface is the agent surface", "", "architecture_synthesis", Resolution{TextRegisterDocs, 0, "tasks.architecture_synthesis", ""}},
		{"neither surface nor task", "", "", Resolution{TextRegisterInternal, 0, "surfaces.agent", ""}},
		{"unknown surface falls back", "slack", "", Resolution{TextRegisterInternal, 0, "surfaces.agent", ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := policy.Resolve(tc.surface, tc.task); got != tc.want {
				t.Fatalf("Resolve = %+v, want %+v", got, tc.want)
			}
		})
	}
	if got := (RegisterPolicy{}).Resolve(SurfaceForge, ""); got.Register != TextRegisterSocial {
		t.Errorf("zero policy must resolve through the defaults, got %+v", got)
	}
}

func TestValidateTaskLabels(t *testing.T) {
	policy := DefaultRegisterPolicy()
	known := []string{"architecture_synthesis", "function_docstrings", "commit_message_synthesis", "waiver_signoff"}
	if err := policy.ValidateTaskLabels(known); err != nil {
		t.Fatalf("declared labels: %v", err)
	}
	policy.Tasks["deploy_prod"] = RegisterTask{Register: TextRegisterDocs}
	err := policy.ValidateTaskLabels(known)
	if err == nil || !strings.Contains(err.Error(), `register task "deploy_prod" is not a declared target_tasks label`) {
		t.Fatalf("undeclared label: error = %v", err)
	}
	if err := (RegisterPolicy{}).ValidateTaskLabels(nil); err != nil {
		t.Fatalf("a policy without rows needs no labels: %v", err)
	}
}

func TestRenderRegisterBlockGolden(t *testing.T) {
	block, err := RenderRegisterBlock(DefaultRegisterPolicy())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	lines := strings.Split(block, "\n")
	if len(lines)+strings.Count(RegisterSectionPrefix, "\n") != MaxRegisterBlockLines {
		t.Fatalf("default block is %d lines plus its prefix, want exactly the budget %d", len(lines), MaxRegisterBlockLines)
	}
	if lines[0] != RegisterBlockStart || lines[len(lines)-1] != RegisterBlockEnd {
		t.Fatalf("block must open and close with its markers:\n%s", block)
	}
	for _, want := range []string{
		"| social | forge: issues, PR bodies, review comments, commit bodies | `social-text` skill:",
		"| docs | docs/, README, ADR bodies | complete without bloat:",
		"| internal | briefs, agent-to-agent traffic, research fan-outs, workflow returns | `caveman` skill:",
		"- Task rows: social = commit_message_synthesis, waiver_signoff; docs = architecture_synthesis, function_docstrings; every other label and any brief without one = internal.",
		"- Evidence above 58 lines or 1500 tokens leaves the message as a file",
		"`evidence: <path> sha256:<12 hex> lines:<n>`",
	} {
		if strings.Count(block, want) != 1 {
			t.Errorf("block must contain %q exactly once:\n%s", want, block)
		}
	}
	// The internal row names the caveman skill; the soft one-liner it replaced let agents
	// keep writing full prose. The config value stays "internal".
	if strings.Contains(block, "telegraphic:") || !strings.Contains(RegisterDirective(TextRegisterInternal), "`caveman` skill:") {
		t.Errorf("internal register must render the caveman form, not the old telegraphic line:\n%s", block)
	}
	// A vendor-named H2 would make the section private to one compiled target.
	if regexp.MustCompile(`(?m)^## `).MatchString(block) {
		t.Errorf("block must not carry a heading of its own:\n%s", block)
	}
	if strings.Contains(RegisterBlockHeading, "Claude") || !strings.HasPrefix(RegisterBlockHeading, "## ") {
		t.Errorf("heading %q must be a vendor-neutral H2", RegisterBlockHeading)
	}
	if RegisterSectionPrefix != RegisterBlockHeading+"\n\n" {
		t.Errorf("section prefix %q must be the heading and one blank line", RegisterSectionPrefix)
	}
	// The table must stand between blank lines, or the forge renders the next line as a row.
	if !strings.Contains(block, ".\n\n| Register |") || !strings.Contains(block, "verdict |\n\n- Task rows:") {
		t.Errorf("table must be surrounded by blank lines:\n%s", block)
	}
}

func TestRenderRegisterBlockFollowsThePolicy(t *testing.T) {
	policy := DefaultRegisterPolicy()
	policy.Surfaces[SurfaceForge] = TextRegisterDocs
	policy.Tasks["waiver_signoff"] = RegisterTask{Register: TextRegisterSocial, MaxTokens: 1024}
	policy.Tasks["ci_debugging"] = RegisterTask{Register: TextRegisterInternal}
	policy.Evidence = EvidenceBounds{InlineMaxLines: 40, InlineMaxTokens: 900}
	block, err := RenderRegisterBlock(policy)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"| social | task rows only |",
		"| docs | forge: issues, PR bodies, review comments, commit bodies; docs/, README, ADR bodies |",
		"Evidence above 40 lines or 900 tokens",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("block must contain %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "1024") || strings.Contains(block, "ci_debugging") {
		t.Errorf("block must print neither a task budget nor a row equal to the fallback:\n%s", block)
	}
}

func TestRenderRegisterBlockRejectsOverflow(t *testing.T) {
	policy := DefaultRegisterPolicy()
	policy.Tasks["smuggled\nline"] = RegisterTask{Register: TextRegisterDocs}
	if _, err := RenderRegisterBlock(policy); err == nil || !strings.Contains(err.Error(), "budget 15") {
		t.Fatalf("a label that adds a line must exceed the block budget, got %v", err)
	}
}

func TestRegisterDirective(t *testing.T) {
	for _, register := range []TextRegister{TextRegisterSocial, TextRegisterDocs, TextRegisterInternal} {
		directive := RegisterDirective(register)
		if !strings.HasPrefix(directive, "Text register "+string(register)+": ") || strings.Count(directive, "\n") != 0 {
			t.Errorf("directive for %s = %q", register, directive)
		}
	}
	if got := RegisterDirective(""); got != "" {
		t.Errorf("empty register must yield no directive, got %q", got)
	}
	if got := RegisterDirective("loud"); got != "" {
		t.Errorf("unknown register must yield no directive, got %q", got)
	}
}

func TestEvidencePointer(t *testing.T) {
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	got, err := EvidencePointer(".workingdir/evidence/run.log", digest, 412)
	if err != nil || got != "evidence: .workingdir/evidence/run.log sha256:0123456789ab lines:412" {
		t.Fatalf("pointer = %q, %v", got, err)
	}
	if got, err = EvidencePointer("run.log", strings.ToUpper(digest), 0); err != nil || !strings.HasSuffix(got, "sha256:0123456789ab lines:0") {
		t.Fatalf("zero lines and an upper-case digest: %q, %v", got, err)
	}
	if _, err = EvidencePointer("run.log", digest[:12], 1<<20); err != nil {
		t.Fatalf("exactly 12 hex digits and 1<<20 lines: %v", err)
	}
	for name, call := range map[string]func() (string, error){
		"short digest":   func() (string, error) { return EvidencePointer("run.log", "0123456789", 1) },
		"non-hex digest": func() (string, error) { return EvidencePointer("run.log", "0123456789zz", 1) },
		"negative lines": func() (string, error) { return EvidencePointer("run.log", digest, -1) },
		"empty path":     func() (string, error) { return EvidencePointer("", digest, 1) },
		"two-line path":  func() (string, error) { return EvidencePointer("a\nb", digest, 1) },
	} {
		if got, err := call(); err == nil {
			t.Errorf("%s: want an error, got %q", name, got)
		}
	}
}

func TestEmissionSurfacesPositive(t *testing.T) {
	m, err := loadRegisterManifest(t, `register:
  surfaces:
    context: internal
    mcp: docs
    hooks: internal
    prompts: social
    ledger: internal
`)
	if err != nil {
		t.Fatalf("all five emission surfaces must load: %v", err)
	}
	policy := m.EffectiveRegister()
	cases := []struct {
		surface  RegisterSurface
		want     Resolution
		enforced bool
	}{
		{SurfaceContext, Resolution{TextRegisterInternal, 0, "surfaces.context", ""}, true},
		{SurfaceMCP, Resolution{TextRegisterDocs, 0, "surfaces.mcp", ""}, false},
		{SurfaceHooks, Resolution{TextRegisterInternal, 0, "surfaces.hooks", ""}, true},
		{SurfacePrompts, Resolution{TextRegisterSocial, 0, "surfaces.prompts", ""}, false},
		{SurfaceLedger, Resolution{TextRegisterInternal, 0, "surfaces.ledger", ""}, true},
	}
	for _, tc := range cases {
		if got := policy.Resolve(tc.surface, "waiver_signoff"); got != tc.want {
			t.Errorf("Resolve(%s) = %+v, want %+v; a task row must not change an emission surface", tc.surface, got, tc.want)
		}
		if got, err := policy.LintEnforced(tc.surface); err != nil || got != tc.enforced {
			t.Errorf("LintEnforced(%s) = %v, %v; want %v", tc.surface, got, err, tc.enforced)
		}
	}
}

// TestContextSurfaceIsFixed: AGENTS.md has no opt-out from the caveman gate. surfaces.agent
// never reaches the context surface, writing internal for it is accepted, and writing any
// other register for it is rejected.
func TestContextSurfaceIsFixed(t *testing.T) {
	m, err := loadRegisterManifest(t, "register:\n  surfaces:\n    agent: docs\n")
	if err != nil {
		t.Fatal(err)
	}
	policy := m.EffectiveRegister()
	want := Resolution{Register: ContextRegister, Source: "surfaces.context"}
	if got := policy.Resolve(SurfaceContext, "waiver_signoff"); got != want {
		t.Errorf("Resolve(context) = %+v, want %+v whatever surfaces.agent says", got, want)
	}
	if enforced, err := policy.LintEnforced(SurfaceContext); err != nil || !enforced {
		t.Errorf("LintEnforced(context) = %v, %v; want true", enforced, err)
	}
	if got := DefaultRegisterPolicy().Resolve(SurfaceContext, ""); got != want {
		t.Errorf("default Resolve(context) = %+v, want %+v", got, want)
	}
	if _, err := loadRegisterManifest(t, "register:\n  surfaces:\n    context: internal\n"); err != nil {
		t.Errorf("context: internal must load: %v", err)
	}
	for _, register := range []string{"docs", "social"} {
		_, err := loadRegisterManifest(t, "register:\n  surfaces:\n    context: "+register+"\n")
		if err == nil || !strings.Contains(err.Error(), `register surface "context" is fixed to internal`) {
			t.Errorf("context: %s: error = %v, want the no-opt-out rejection", register, err)
		}
	}
}

// TestOperatorSurfaceIsFixed: a reply to the operator is named explicitly (register-gaps-
// 20260919.md) rather than left to fall through to EmissionDefaultRegister, and it has no
// opt-out, the same shape as SurfaceContext.
func TestOperatorSurfaceIsFixed(t *testing.T) {
	// Positive: operator resolves to OperatorRegister (docs) regardless of surfaces.agent or
	// the task row, is known, and is never caveman-enforced.
	m, err := loadRegisterManifest(t, "register:\n  surfaces:\n    agent: internal\n")
	if err != nil {
		t.Fatal(err)
	}
	policy := m.EffectiveRegister()
	want := Resolution{Register: OperatorRegister, Source: "surfaces.operator"}
	if got := policy.Resolve(SurfaceOperator, "waiver_signoff"); got != want {
		t.Errorf("Resolve(operator) = %+v, want %+v whatever surfaces.agent says", got, want)
	}
	if enforced, err := policy.LintEnforced(SurfaceOperator); err != nil || enforced {
		t.Errorf("LintEnforced(operator) = %v, %v; want false, the operator surface is never caveman", enforced, err)
	}
	if !KnownRegisterSurface(SurfaceOperator) {
		t.Error("KnownRegisterSurface(operator) = false, want true")
	}
	if got := DefaultRegisterPolicy().Resolve(SurfaceOperator, ""); got != want {
		t.Errorf("default Resolve(operator) = %+v, want %+v", got, want)
	}
	if _, err := loadRegisterManifest(t, "register:\n  surfaces:\n    operator: docs\n"); err != nil {
		t.Errorf("operator: docs must load: %v", err)
	}
	// Negative: writing internal or social for operator is rejected, the same no-opt-out
	// shape as writing docs or social for context.
	for _, register := range []string{"internal", "social"} {
		_, err := loadRegisterManifest(t, "register:\n  surfaces:\n    operator: "+register+"\n")
		if err == nil || !strings.Contains(err.Error(), `register surface "operator" is fixed to docs`) {
			t.Errorf("operator: %s: error = %v, want the no-opt-out rejection", register, err)
		}
	}
	// Boundary: a misspelled surface name is an unknown-surface error, not a silent
	// fallback, the same as every other surface.
	if enforced, err := DefaultRegisterPolicy().LintEnforced("operators"); err == nil || enforced {
		t.Errorf(`LintEnforced("operators") = %v, %v; want an unknown-surface error`, enforced, err)
	}
}

func TestEmissionSurfacesNegative(t *testing.T) {
	for name, tc := range map[string]struct{ section, want string }{
		"unknown register on an emission surface": {"register: {surfaces: {mcp: loud}}\n", `unsupported text register "loud"`},
		"misspelled emission surface":             {"register: {surfaces: {hook: internal}}\n", `unknown register surface "hook"`},
		"non-string emission register":            {"register: {surfaces: {ledger: 1}}\n", "text register must be a string enum"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadRegisterManifest(t, tc.section)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
	for _, surface := range []RegisterSurface{"", "slack", "Context"} {
		if enforced, err := DefaultRegisterPolicy().LintEnforced(surface); err == nil || enforced {
			t.Errorf("LintEnforced(%q) = %v, %v; want an unknown-surface error", surface, enforced, err)
		}
		if KnownRegisterSurface(surface) {
			t.Errorf("KnownRegisterSurface(%q) = true", surface)
		}
	}
}

func TestEmissionSurfacesBoundary(t *testing.T) {
	// Unset emission surfaces are internal, so the lint is on by default; context is fixed to
	// internal as well (TestContextSurfaceIsFixed).
	defaults := DefaultRegisterPolicy()
	for _, surface := range emissionSurfaces {
		want := Resolution{TextRegisterInternal, 0, "surfaces." + string(surface), ""}
		if got := defaults.Resolve(surface, ""); got != want {
			t.Errorf("default Resolve(%s) = %+v, want the internal default", surface, got)
		}
		if enforced, err := defaults.LintEnforced(surface); err != nil || !enforced {
			t.Errorf("default LintEnforced(%s) = %v, %v; want the lint on", surface, enforced, err)
		}
	}
	// surfaces.agent does not reach an emission surface: writing docs for the agent surface
	// must not switch the lint off for engine text. Only the surface's own key does.
	m := &Manifest{Register: &RegisterPolicy{Surfaces: map[RegisterSurface]TextRegister{SurfaceAgent: TextRegisterDocs}}}
	for _, surface := range []RegisterSurface{SurfaceMCP, SurfaceHooks, SurfacePrompts, SurfaceLedger} {
		if got := m.EffectiveRegister().Resolve(surface, ""); got != (Resolution{TextRegisterInternal, 0, "surfaces." + string(surface), ""}) {
			t.Errorf("%s with agent = docs resolves to %+v, want the internal default", surface, got)
		}
		if enforced, err := m.EffectiveRegister().LintEnforced(surface); err != nil || !enforced {
			t.Errorf("%s with agent = docs: LintEnforced = %v, %v; want the lint on", surface, enforced, err)
		}
	}
	// The block stays at its budget: emission surfaces are not rendered.
	policy := DefaultRegisterPolicy()
	policy.Surfaces[SurfaceMCP] = TextRegisterDocs
	withSurface, errWith := RenderRegisterBlock(policy)
	plain, errPlain := RenderRegisterBlock(DefaultRegisterPolicy())
	if errWith != nil || errPlain != nil || withSurface != plain {
		t.Errorf("an emission surface changed the rendered block (%v, %v)", errWith, errPlain)
	}
	// The three audience surfaces keep their meaning for LintEnforced.
	for surface, want := range map[RegisterSurface]bool{SurfaceForge: false, SurfaceDocs: false, SurfaceAgent: true} {
		if got, err := defaults.LintEnforced(surface); err != nil || got != want {
			t.Errorf("LintEnforced(%s) = %v, %v; want %v", surface, got, err, want)
		}
	}
}
