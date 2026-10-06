package compiler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/util"
)

// contextFileName is the file name of a canonical context file: the root AGENTS.md and every
// nested one below it.
const contextFileName = "AGENTS.md"

// MaxNestedContextFiles bounds the tracked nested AGENTS.md files one caveman gate run reads
// (HISS-02). The adopter repository in #311 tracks 219. A listing above the cap is refused with
// ErrNestedContextCap, naming both numbers; it is never truncated.
const MaxNestedContextFiles = 1024

// nestedContextPathBytes is the path length the listing's byte bound budgets for each file, so
// the count cap, not the byte bound, refuses a repository with too many nested files.
const nestedContextPathBytes = 1024

// nestedContextPathspec selects every tracked AGENTS.md at any depth, the root one included;
// nestedContextPaths drops that one.
const nestedContextPathspec = ":(glob)**/" + contextFileName

// ErrNestedContextProse is returned when a tracked nested AGENTS.md breaks the caveman lint. A
// nested AGENTS.md is canonical agent context as the root one is, so it is judged the same way
// (CheckContextText) under the same fixed, no-opt-out rule (ADR-0010 decision 11, #311).
var ErrNestedContextProse = errors.New("nested AGENTS.md fails the caveman lint")

// ErrNestedContextCap is returned when a repository tracks more than MaxNestedContextFiles
// nested AGENTS.md files.
var ErrNestedContextCap = errors.New("too many tracked nested AGENTS.md files")

// LintCanonicalPersonas runs the caveman lint plus AgentTextCeiling over every
// canonical persona under .agents/agents. Personas sit under config.SurfaceContext by its
// own doc comment ("AGENTS.md, the compiled vendor files, personas and skills";
// internal/config/register.go), so they carry the same fixed, no-opt-out rule as AGENTS.md
// itself (ADR-0010 decision 11) rather than an emission surface a manifest can opt out of.
// It returns the number of personas linted.
func LintCanonicalPersonas(ctx context.Context, rootDir string) (int, error) {
	names, err := listCanonicalAgents(ctx, rootDir)
	if err != nil {
		return 0, err
	}
	for i := 0; i < len(names) && i < maxAgentProjections; i++ {
		data, err := readCanonicalAgent(ctx, rootDir, names[i])
		if err != nil {
			return 0, err
		}
		label := filepath.Join(CanonicalAgentsRel, names[i])
		if _, err := LintAgentText(label, string(data)); err != nil {
			return 0, err
		}
	}
	return len(names), nil
}

// LintCanonicalSkillFiles runs the caveman lint plus AgentTextCeiling over every
// canonical skill's SKILL.md under .agents/skills, on the same SurfaceContext basis as
// LintCanonicalPersonas. It returns the number of skills linted.
func LintCanonicalSkillFiles(ctx context.Context, rootDir string) (int, error) {
	names, err := listCanonicalSkills(ctx, rootDir)
	if err != nil {
		return 0, err
	}
	for i := 0; i < len(names) && i < maxSkillProjections; i++ {
		data, err := readCanonicalSkill(ctx, rootDir, names[i])
		if err != nil {
			return 0, err
		}
		label := filepath.Join(CanonicalSkillsRel, names[i], SkillEntryName)
		if _, err := LintAgentText(label, string(data)); err != nil {
			return 0, err
		}
	}
	return len(names), nil
}

// ListNestedContextFiles returns every nested AGENTS.md git tracks below rootDir: slash paths
// relative to rootDir, sorted, rootDir's own AGENTS.md left out. It is the one enumerator of
// nested context files. It asks the index (git ls-files through util.RunGitProbe, under the
// probe's deadline and ctx's), so an untracked or ignored file is never listed, and git enters
// neither a nested repository nor a submodule. A rootDir outside every Git work tree tracks
// nothing and lists none. More than MaxNestedContextFiles files is ErrNestedContextCap.
func ListNestedContextFiles(ctx context.Context, rootDir string) ([]string, error) {
	return listNestedContextFiles(ctx, rootDir, MaxNestedContextFiles)
}

// listNestedContextFiles is ListNestedContextFiles under the cap limit.
func listNestedContextFiles(ctx context.Context, rootDir string, limit int) ([]string, error) {
	inRepository, err := util.GitWorktreePresent(ctx, rootDir)
	if err != nil {
		return nil, fmt.Errorf("list nested %s: detect Git work tree: %w", contextFileName, err)
	}
	if !inRepository {
		return nil, nil
	}
	result, err := util.RunGitProbe(ctx, rootDir, (limit+2)*nestedContextPathBytes,
		"ls-files", "-z", "--cached", "--deduplicate", "--", nestedContextPathspec)
	if err != nil {
		return nil, fmt.Errorf("list nested %s: git ls-files in %s: %w: %.512s", contextFileName, rootDir, err,
			bytes.TrimSpace(result.Stderr))
	}
	return nestedContextPaths(result.Stdout, limit)
}

// nestedContextPaths parses a NUL-terminated ls-files listing into the sorted nested AGENTS.md
// paths it holds. The pathspec matches by pattern; the base name is compared here as well, so
// the answer does not depend on a platform's case folding.
func nestedContextPaths(out []byte, limit int) ([]string, error) {
	records := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	var paths []string
	for i := 0; i < len(records); i++ { // bounded by the probe's byte cap
		rel := records[i]
		if rel != contextFileName && path.Base(rel) == contextFileName {
			paths = append(paths, rel)
		}
	}
	if len(paths) > limit {
		return nil, fmt.Errorf("%w: %d tracked, more than the cap of %d one caveman gate run reads",
			ErrNestedContextCap, len(paths), limit)
	}
	slices.Sort(paths)
	return paths, nil
}

// nestedContextVerdict collects the caveman verdicts over the nested AGENTS.md files.
type nestedContextVerdict struct {
	linted int
	failed int
	quoted []string
}

// record adds the verdict on one file: finding is empty for a pass.
func (v *nestedContextVerdict) record(finding string) {
	v.linted++
	if finding == "" {
		return
	}
	v.failed++
	if len(v.quoted) < maxQuotedLintFindings {
		v.quoted = append(v.quoted, finding)
	}
}

// err returns ErrNestedContextProse with the failed count and the first findings, or nil.
func (v nestedContextVerdict) err() error {
	if v.failed == 0 {
		return nil
	}
	more := ""
	if extra := v.failed - len(v.quoted); extra > 0 {
		more = fmt.Sprintf(" (+%d more files)", extra)
	}
	return fmt.Errorf("%w: %d of %d files: %s%s", ErrNestedContextProse, v.failed, v.linted,
		strings.Join(v.quoted, " | "), more)
}

// lintNestedContexts runs the context caveman gate over every nested AGENTS.md below rootDir
// (ListNestedContextFiles) except source, the AGENTS.md LintContext judges. Each file is judged
// as the root one is (CheckContextText, kind context), so 'praetorctl caveman check
// --kind=context <file>' reproduces the verdict. Every file is read before the verdict, so one
// run names each failing file. A listed file absent from the working tree, as a sparse checkout
// or an unstaged deletion leaves it, holds no text and is not counted. It returns the number of
// files linted.
func lintNestedContexts(ctx context.Context, rootDir, source string) (int, error) {
	paths, err := ListNestedContextFiles(ctx, rootDir)
	if err != nil {
		return 0, err
	}
	skip := absolutePath(source)
	var verdict nestedContextVerdict
	for i := 0; i < len(paths) && i < MaxNestedContextFiles; i++ {
		if skip != "" && absolutePath(filepath.Join(rootDir, filepath.FromSlash(paths[i]))) == skip {
			continue
		}
		present, finding, err := lintNestedContext(ctx, rootDir, paths[i])
		if err != nil {
			return 0, err
		}
		if present {
			verdict.record(finding)
		}
	}
	return verdict.linted, verdict.err()
}

// lintNestedContext reads the nested AGENTS.md rel below rootDir through the confined reader,
// which refuses a symlink at any component, and returns whether it exists and the quoted
// findings of a failed lint, empty for a pass.
func lintNestedContext(ctx context.Context, rootDir, rel string) (present bool, finding string, err error) {
	if err := ctx.Err(); err != nil {
		return false, "", err
	}
	data, err := readConfinedText(ctx, rootDir, rel)
	if errors.Is(err, fs.ErrNotExist) {
		return false, "", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("read nested %s: %w", rel, err)
	}
	report, _ := CheckContextText(string(data), caveman.Options{Kind: caveman.KindContext})
	if report.Passed() {
		return true, "", nil
	}
	return true, describeLintFindings(filepath.FromSlash(rel), report.Findings), nil
}

// absolutePath returns p made absolute, or "" when p is empty or does not resolve.
func absolutePath(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	return abs
}

// agentSourceLint counts the canonical agent sources one caveman gate run judged beside the
// root AGENTS.md.
type agentSourceLint struct {
	nested, personas, skills int
}

// summary states the counts behind a pass, so a clean run can be told apart from one that read
// nothing.
func (l agentSourceLint) summary() string {
	return fmt.Sprintf("%d nested %s, %d personas and %d skills passed the caveman lint (personas and skills <= %d prose words each)",
		l.nested, contextFileName, l.personas, l.skills, AgentTextCeiling)
}

// lintAgentSources runs the caveman gate over every canonical agent source below rootDir other
// than source, the root AGENTS.md LintContext judges: every tracked nested AGENTS.md
// (lintNestedContexts), every persona (LintCanonicalPersonas) and every skill
// (LintCanonicalSkillFiles). Every surface runs, and one error returns per failed surface, so
// one run names each fix. compile-context, compile-context --verify (lintAgentText), the CLI
// audit and the MCP standards_audit (AuditAgentSources) all run it.
func lintAgentSources(ctx context.Context, rootDir, source string) (agentSourceLint, []error) {
	var lint agentSourceLint
	var nestedErr, personaErr, skillErr error
	lint.nested, nestedErr = lintNestedContexts(ctx, rootDir, source)
	lint.personas, personaErr = LintCanonicalPersonas(ctx, rootDir)
	lint.skills, skillErr = LintCanonicalSkillFiles(ctx, rootDir)
	failures := slices.DeleteFunc([]error{nestedErr, personaErr, skillErr}, func(err error) bool { return err == nil })
	return lint, failures
}

// AuditAgentSources is the audit gate over the canonical agent sources beside the root
// AGENTS.md at source: every tracked nested AGENTS.md, persona and skill below rootDir
// (lintAgentSources). `praetorctl audit` and the MCP standards_audit both run it, so both return
// the same verdict: the [PASS] line with the counts, or a [FAIL] error joining every failed
// surface. Like LintContext it reads no manifest and has no opt-out.
func AuditAgentSources(ctx context.Context, rootDir, source string) (string, error) {
	lint, failures := lintAgentSources(ctx, rootDir, source)
	if len(failures) > 0 {
		return "", fmt.Errorf("[FAIL] Agent source caveman lint: %w", errors.Join(failures...))
	}
	return "[PASS] Caveman lint verified (" + lint.summary() + ").", nil
}
