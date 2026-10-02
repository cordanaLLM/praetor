// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	slashpath "path"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// GitHub Actions workflows.
//
// A run: block is a shell script that runs with the job's token, often with the repository's
// most sensitive commands, so the shell rules apply to it as to a script file (#618). The
// scanner reads every document GitHub runs as a workflow, a YAML file directly in
// .github/workflows, through the one workflow model the forge audits read too
// (internal/ghworkflow), and resolves each run: step's shell the way GitHub does: the step's
// shell:, the job's and then the workflow's defaults.run.shell, then the runner's default
// (ghworkflow.StepShell). A block whose shell the shell scanner reads (sh, bash, dash, ash) is
// scanned with the shell rules (scanShellLines). Its ${{ }} expressions are blanked first,
// keeping its line breaks: GitHub substitutes them before the shell starts, so their text is
// not shell. A finding is reported against the workflow file: in a literal block (run: |) at
// the line it sits on, in any other style at the line the value starts on, since folding and
// escapes lose the mapping.
//
// Every other block is counted in ScanCoverage.UnscannedRunBlocks under its shell (pwsh,
// python, cmd, the command of a custom template) or as unresolved when the shell is decided only
// when the workflow runs (an expression, or a runner whose labels name no single operating
// system). So is a block of a read shell that the shell scanner declines, one past
// maxWorkflowRunBlocks in its file and one longer than maxRunBlockBytes. None of them reads as
// clean. A document the workflow model refuses (malformed, or past its job or step bounds) is
// declined whole and counted as unscanned github-actions source.
//
// A run: block has no interpreter line: GitHub starts its shell itself (bash -e, bash
// --noprofile --norc -eo pipefail for shell: bash, sh -e), so HISS-07's strict-mode rule, which
// holds a script with an interpreter line to set -eu, is not decided for a block.

const (
	// workflowLanguageName is the language the coverage record names a workflow file under.
	workflowLanguageName = "github-actions"
	// unresolvedShell labels a block whose shell is decided only when the workflow runs.
	unresolvedShell = "unresolved"
	// customShell labels a block whose custom shell command cannot serve as a coverage key.
	customShell = "custom"
	// maxWorkflowRunBlocks bounds the run: blocks scanned in one workflow file (HISS-02).
	maxWorkflowRunBlocks = 512
	// maxRunBlockBytes bounds one run: block (HISS-02). The bound is praetor's own, not a GitHub
	// limit. GitHub's 21,000-character limit is the maximum length of an expression; it reaches a
	// run: value only through the ${{ }} expressions the value holds.
	maxRunBlockBytes = 64 << 10
)

// workflowExpression is one ${{ ... }} expression, possibly spanning lines.
var workflowExpression = regexp.MustCompile(`(?s)\$\{\{.*?\}\}`)

// shellLabel matches a shell name fit to key the coverage record.
var shellLabel = regexp.MustCompile(`^[a-z0-9][a-z0-9._+-]*$`)

// FixturePath returns the slash-separated path below a scan root at which a file named name is
// read as language: a GitHub Actions workflow only directly in .github/workflows, a file of any
// other language wherever it sits, so at the root. The HISS-20 corpus replay stages each
// fixture there (internal/hisscoverage).
func FixturePath(language, name string) string {
	if language == workflowLanguageName {
		return slashpath.Join(ghworkflow.Dir, name)
	}
	return name
}

// workflowLanguage reads the run: blocks of GitHub Actions workflows with the shell rules.
type workflowLanguage struct{}

// handles claims no extension: a YAML file is a workflow only where GitHub reads one.
func (workflowLanguage) handles(string) bool { return false }

func (workflowLanguage) name() string { return workflowLanguageName }

func (workflowLanguage) candidate(rel, _ string) bool { return ghworkflow.IsWorkflowPath(rel) }

// claims takes every candidate: GitHub runs every YAML document in .github/workflows, so one the
// model cannot read is unscanned workflow source, not an unrelated file.
func (workflowLanguage) claims(sourceFile) bool { return true }

func (workflowLanguage) fileBound() int { return ghworkflow.MaxFiles }

func (workflowLanguage) scan(src sourceFile, rep *ScanReport, opts ScanOptions) bool {
	spec, err := ghworkflow.Parse(src.data)
	if err != nil {
		return false
	}
	steps, err := ghworkflow.RunSteps(&spec)
	if err != nil {
		return false
	}
	file := &ScanReport{}
	for i := 0; i < len(steps); i++ {
		shell := ghworkflow.StepShell(&spec, steps[i].Job, steps[i].Step)
		if i >= maxWorkflowRunBlocks || !scanRunBlock(src.rel, steps[i].Step, shell, file, opts) {
			rep.Coverage.recordUnscannedRunBlock(runBlockLabel(shell))
		}
	}
	recordInLineOrder(rep, file)
	return true
}

// scanRunBlock applies the shell rules to one step's run: block, started by shell, and records
// its findings in file at their workflow lines. It reports false for a block it did not read: a
// shell the shell scanner does not read, a block past the byte bound or holding a NUL byte, and
// one the shell scanner declined.
func scanRunBlock(rel string, step *ghworkflow.Step, shell string, file *ScanReport, opts ScanOptions) bool {
	if !shellLanguages[shell] || len(step.Run) > maxRunBlockBytes || strings.IndexByte(step.Run, 0) >= 0 {
		return false
	}
	block := &ScanReport{}
	script := sourceFile{rel: rel, data: []byte(blankExpressions(step.Run))}
	if !scanShellLines(rel, script.lfLines(), shellInterp{known: true, bash: shell == "bash"}, block, opts) {
		return false
	}
	for _, v := range block.Violations {
		recordViolation(file, v.RuleID, v.FilePath, runBlockLine(step, v.LineNumber), v.Symbol, v.Message)
	}
	return true
}

// blankExpressions replaces every ${{ ... }} expression with spaces, keeping its line breaks so
// the script's lines still match the workflow's.
func blankExpressions(script string) string {
	return workflowExpression.ReplaceAllStringFunc(script, func(expression string) string {
		return strings.Map(func(r rune) rune {
			if r == '\n' {
				return r
			}
			return ' '
		}, expression)
	})
}

// runBlockLine maps line n of a step's script to the workflow line it sits on: in a literal
// block the line after the indicator is line 1, and any other style is reported at the line its
// value starts on.
func runBlockLine(step *ghworkflow.Step, n int) int {
	if step.RunLiteral {
		return step.RunLine + n
	}
	return step.RunLine
}

// runBlockLabel names an unscanned block's shell for the coverage record: the shell itself,
// unresolved when the workflow alone does not decide it, and custom for a command that does not
// fit a coverage key.
func runBlockLabel(shell string) string {
	switch {
	case shell == "":
		return unresolvedShell
	case len(shell) > maxCoverageExtensionBytes || !shellLabel.MatchString(shell):
		return customShell
	}
	return shell
}
