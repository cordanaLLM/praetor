package adopt

import (
	"context"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

const (
	// harnessEndMarker terminates every harness praetor writes so that a later
	// --force update can locate the boundary to repository-specific instructions.
	harnessEndMarker = "<!-- praetor:harness:end -->"
	// harnessSeparator separates the harness from repository-specific instructions.
	harnessSeparator = "\n---\n"
	// harnessFooterHeading starts the last section of the generated harness.
	harnessFooterHeading = "## Primary Verification Commands"
	// verifyCommand is the single verification entrypoint advertised everywhere.
	verifyCommand = "make verify-all"
	// codeFence delimits the shell block of the harness footer.
	codeFence = "```"
)

const agentHarnessTemplate = `<!-- markdownlint-disable MD013 MD025 -->
# {{ .RepoName }} Agent Operating Harness

Run verification before concluding any turn:

` + "```bash\n{{ .VerifyCmd }}\n```\n\n```mermaid\n" + `flowchart LR
    AGENT["Autonomous Agent"] --> CHECK["{{ .VerifyCmd }}"]
    CHECK --> AUDIT["standardsctl audit"]
    CHECK --> COMPILER["standardsctl compile-context --verify"]
    CHECK --> GATE{"All checks Pass?"}
    GATE -- Yes --> RECEIPT["Ed25519 Exit-0 Receipt"]
    GATE -- No --> DISTILL["SARIF Diagnostic Distillation (<= 1500 tokens)"]
` + "```\n\n"

const agentHarnessFooterTemplate = harnessFooterHeading + `

` + "```bash\n" + `# Fast local test suite
{{ .TestCmd }}

# Recompile and verify cross-agent context outputs
standardsctl compile-context --verify

# Audit repository against declared HISS-16 standards
standardsctl audit

# Run all formatting, linting, and security gates
{{ .VerifyCmd }}
` + "```\n"

// buildAgentHarness renders the canonical harness, terminated by harnessEndMarker.
func buildAgentHarness(repoName, arch string, plan *VerificationPlan) (string, error) {
	tCtx := TemplateContext{
		RepoName:  repoName,
		Archetype: arch,
		VerifyCmd: verifyCommand,
		TestCmd:   verificationTestText(plan),
		Runtime:   strings.Join(plan.Runtimes, ", "),
	}
	header, err := RenderTemplate("harness_header", agentHarnessTemplate, tCtx)
	if err != nil {
		return "", fmt.Errorf("render harness header: %w", err)
	}
	footer, err := RenderTemplate("harness_footer", agentHarnessFooterTemplate, tCtx)
	if err != nil {
		return "", fmt.Errorf("render harness footer: %w", err)
	}
	return header + buildAgentHarnessDirectives() + footer + "\n" + harnessEndMarker + "\n", nil
}

func buildAgentHarnessDirectives() string {
	return `## Core Directives & Invariants (Modernized NASA JPL Power-of-10)

| Invariant | Scope | NASA Rule | Enforcement Mechanism | Failure Action |
| :--- | :--- | :--- | :--- | :--- |
| **HISS-01** | Control Flow | Rule 1 | Recursion strictly prohibited; call graph must be DAG; zero ` + "`goto`" + `. | Immediate build failure |
| **HISS-02** | Loops & I/O | Rule 2 | Scalar upper bound on all loops; explicit ` + "`context.Context`" + ` timeout on all I/O. | Semgrep / AST error |
| **HISS-03** | Memory | Rule 3 | Zero dynamic heap allocation (` + "`malloc` / `free`" + `) in hot simulation/tick loops. | Allocation audit sweep |
| **HISS-04** | Complexity | Rule 4 | Function length $\le 60$ LOC, McCabe Cyclomatic $\le 10$, Statements $\le 50$. | AST sweep blocker |
| **HISS-07** | Error Handling | Rule 7 | Zero ` + "`.unwrap()` / `.expect()`" + `; all errors handled or wrapped with context. | Linter / Compiler error |
| **HISS-08** | Determinism | Rule 8 | Zero dynamic execution (` + "`eval` / `exec`" + `); zero banned unsafe libc (` + "`gets` / `strcpy` / `sprintf`" + `). | AST / Linter error |
| **HISS-09** | Reference Safety | Rule 9 | Mandatory ` + "`// SAFETY:`" + ` proofs for all pointer arithmetic and ` + "`unsafe`" + ` blocks. | AST check blocker |
| **HISS-10** | Warning Hygiene | Rule 10 | Zero-warning tolerance across compiler, linter, and format sweeps. | Exit code 1 |
| **HISS-15** | 3D Testing | Rule 5 | Positive, negative, and boundary tests mandatory for all public interfaces. | CI coverage gate |
| **HISS-16** | Context Integrity | Fleet | Single canonical ` + "`AGENTS.md`" + `; vendor files compiled via ` + "`standardsctl compile-context`" + `. | Pre-commit blocker |

## Operational Rules

1. **Act on Verified State**:
   Read source files and run real commands before hypothesizing or editing. Never guess flag names, library signatures, or repo configurations from memory.

2. **Lead with Output**:
   Provide direct answers, diffs, and commands. Avoid filler preambles, "Based on", restatements, or conversational chatter.

3. **Context Transpiler First**:
   Never edit ` + "`CLAUDE.md`" + `, ` + "`.cursor/rules/*.mdc`" + `, ` + "`.windsurfrules`" + `, or ` + "`.github/copilot-instructions.md`" + ` manually. Make all agent instruction updates in ` + "`AGENTS.md`" + ` and execute:

   ` + "```bash\n   standardsctl compile-context\n   ```\n\n" + `4. **SARIF Diagnostic Distillation**:
   When reporting compiler or linter errors, distill output to $\le 1,500$ tokens ($< 60$ lines). Print the top 3 root-cause failures with file/line pointers and write full SARIF logs to ephemeral storage.

5. **No Evasion Tolerated**:
   Do not attempt ` + "`--no-verify`" + `, ` + "`LEFTHOOK=0`" + `, or modifying ` + "`.git/hooks`" + `. All pull requests are authoritatively re-checked in an ephemeral isolated sandbox by ` + "`cordana-standards[bot]`" + `.

6. **Anti-Loop Interception**:
   If the same AST diff and error category repeats $\ge 3$ times, halt execution immediately. Re-evaluate the underlying design instead of making micro-textual retries.

`
}

// hasHarness reports whether content already carries a praetor harness.
func hasHarness(content string) bool {
	return strings.Contains(content, "Agent Operating Harness") ||
		strings.Contains(content, "## Core Directives & Invariants")
}

// splitHarnessTail returns the repository-specific instructions that follow an existing
// harness. It recognises, in order, the end marker written by current versions, the
// footer of harnesses written before the marker existed, and a bare "---" separator.
// ok is false when no boundary can be identified.
func splitHarnessTail(existing string) (tail string, ok bool) {
	if idx := strings.Index(existing, harnessEndMarker); idx >= 0 {
		return trimSeparator(existing[idx+len(harnessEndMarker):]), true
	}
	if idx := strings.Index(existing, harnessFooterHeading); idx >= 0 {
		rest := existing[idx:]
		open := strings.Index(rest, codeFence)
		if open < 0 {
			return "", false
		}
		closing := strings.Index(rest[open+len(codeFence):], codeFence)
		if closing < 0 {
			return "", false
		}
		end := open + len(codeFence) + closing + len(codeFence)
		return trimSeparator(rest[end:]), true
	}
	if parts := strings.SplitN(existing, harnessSeparator, 2); len(parts) == 2 {
		return trimSeparator(parts[1]), true
	}
	return "", false
}

// trimSeparator drops surrounding whitespace and one leading "---" separator line.
func trimSeparator(tail string) string {
	tail = strings.TrimSpace(tail)
	if strings.HasPrefix(tail, "---") {
		tail = strings.TrimSpace(strings.TrimPrefix(tail, "---"))
	}
	return tail
}

func reconcileAgentHarness(_ context.Context, s *adoptSession) error {
	agentsContent, err := resolveAgentsContent(s)
	if err != nil {
		return err
	}
	return transpileAgentTargets(s, agentsContent)
}

func resolveAgentsContent(s *adoptSession) (string, error) {
	full, err := repoFile(s.repoPath, agentsFile)
	if err != nil {
		return "", err
	}
	harness, err := buildAgentHarness(s.repoName, s.arch, s.verification)
	if err != nil {
		return "", err
	}
	if !fileExists(full) {
		if err := validateHarnessProjection(harness); err != nil {
			return "", err
		}
		if err := s.write(full, []byte(harness), filePerm); err != nil {
			return "", err
		}
		s.report.recordCreated(agentsFile, "Synthesized canonical Praetor Agent Operating Harness and HISS-16 invariants")
		return harness, nil
	}
	existingBytes, err := readRepoFile(full)
	if err != nil {
		return "", err
	}
	return mergeExistingAgentsContent(s, full, string(existingBytes), harness)
}

func validateHarnessProjection(content string) error {
	if _, err := compiler.NewTranspiler().CompileContent(content); err != nil {
		return fmt.Errorf("context composition cannot produce valid projections: %w", err)
	}
	return nil
}

// mergeExistingAgentsContent prepends the harness to a foreign AGENTS.md, leaves an
// existing harness alone without Force, and with Force replaces only the harness part
// while keeping everything after its boundary. When the boundary of an existing harness
// cannot be identified the file is left untouched and an error is recorded rather than
// silently discarding repository instructions.
func mergeExistingAgentsContent(s *adoptSession, full, existing, harness string) (string, error) {
	if !hasHarness(existing) {
		merged := harness + harnessSeparator + "\n" + existing
		if err := validateHarnessProjection(merged); err != nil {
			return "", err
		}
		if err := s.write(full, []byte(merged), filePerm); err != nil {
			return "", err
		}
		s.report.recordReconciledAs(agentsFile, actionMerge, "Merged Praetor Agent Operating Harness & HISS-16 directives above existing instructions")
		return merged, nil
	}
	if !s.opts.Force {
		s.report.recordReconciled(agentsFile, "Existing Praetor Agent Operating Harness preserved; command synchronization not verified")
		if existing != harness {
			s.report.addWarning("Existing AGENTS.md was preserved; review its commands against the verification plan or use --force to refresh a recognized harness boundary.")
		}
		return existing, nil
	}
	tail, ok := splitHarnessTail(existing)
	if !ok {
		s.report.addError("%s: cannot locate the end of the existing harness; file left untouched (separate repository instructions from the harness with a '---' line and re-run)", agentsFile)
		s.report.recordReconciled(agentsFile, "Existing harness left untouched: boundary to repository instructions not found")
		return existing, nil
	}
	merged := strings.TrimSpace(harness) + "\n"
	if tail != "" {
		merged = strings.TrimSpace(harness) + "\n" + harnessSeparator + "\n" + tail + "\n"
	}
	if err := validateHarnessProjection(merged); err != nil {
		return "", err
	}
	if err := s.write(full, []byte(merged), filePerm); err != nil {
		return "", err
	}
	s.report.recordReconciled(agentsFile, "Updated Praetor Agent Operating Harness while preserving repository-specific instructions")
	return merged, nil
}

// transpileAgentTargets compiles AGENTS.md into every vendor context file. A compile
// failure is fatal: HISS-16 guarantees that the vendor files mirror AGENTS.md.
func transpileAgentTargets(s *adoptSession, agentsContent string) error {
	res, err := compiler.NewTranspiler().CompileContent(agentsContent)
	if err != nil {
		return fmt.Errorf("context compilation: %w", err)
	}
	for i := 0; i < len(res.Files) && i < maxTranspileTargets; i++ {
		f := res.Files[i]
		full, err := repoFile(s.repoPath, f.RelativePath)
		if err != nil {
			return err
		}
		existed := fileExists(full)
		if err := s.write(full, []byte(f.Content), filePerm); err != nil {
			return err
		}
		if existed {
			s.report.recordReconciled(f.RelativePath, fmt.Sprintf("Synchronized vendor context target (%d LOC)", f.LineCount))
			continue
		}
		s.report.recordCreated(f.RelativePath, fmt.Sprintf("Compiled vendor context target (%d LOC)", f.LineCount))
	}
	return nil
}
