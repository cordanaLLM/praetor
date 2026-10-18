package adopt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// releaseTokenGate names the gate on every line it prints.
const releaseTokenGate = "Release token"

// releaseTokenScope is what every verdict states about the evidence behind it.
const releaseTokenScope = "Read from the workflow files under .github/workflows; no release pull request was checked on the forge."

// releaseTokenGuide is the document explaining the passing shape and exceptions.
const releaseTokenGuide = "docs/guides/release-token.md"

// maxReleaseTokenFindings bounds the findings one verdict lists (HISS-02); the rest are counted.
const maxReleaseTokenFindings = 60

// ReleaseTokenOptions is what AuditReleaseToken reads.
type ReleaseTokenOptions struct {
	Root       string
	Exceptions []config.Exception
	Today      time.Time
}

// AuditReleaseToken is the release-token gate shared by the CLI and MCP audits.
// Every release-please step in .github/workflows must obtain its token from an earlier
// actions/create-github-app-token step in the same job, rather than using the default workflow token.
func AuditReleaseToken(ctx context.Context, opts ReleaseTokenOptions) (string, error) {
	if ctx == nil {
		return "", errors.New("[FAIL] " + releaseTokenGate + ": the audit requires a context")
	}
	entries := config.ExceptionsFor(opts.Exceptions, config.ExceptionRuleReleaseToken)
	if err := config.ValidateExceptions(entries, opts.Today); err != nil {
		return "", fmt.Errorf("[FAIL] %s: %w", releaseTokenGate, err)
	}
	findings, totalSteps, err := forge.MeasureReleaseTokens(ctx, opts.Root)
	if err != nil {
		return "", fmt.Errorf("[FAIL] %s: cannot read the workflows: %w", releaseTokenGate, err)
	}
	if totalSteps == 0 {
		if len(entries) > 0 {
			return "", errors.New(staleExceptionEntries(releaseTokenGate, config.ExceptionRuleReleaseToken, "step", entries, "no workflow holds a release-please step"))
		}
		return "[SKIP] " + releaseTokenGate + " not checked: no workflow under .github/workflows holds a release-please step.", nil
	}
	judged := judgeReleaseTokenFindings(findings, totalSteps, entries, opts.Today)
	if len(judged.unexcused) > 0 || len(judged.stale) > 0 {
		return "", errors.New(judged.failure())
	}
	return judged.pass(), nil
}

// releaseTokenJudgement categorizes release-token findings against declared exceptions.
type releaseTokenJudgement struct {
	totalSteps int
	excused    []forge.ReleaseTokenFinding
	unexcused  []forge.ReleaseTokenFinding
	live       map[string]config.Exception
	expired    map[string]config.Exception
	stale      []config.Exception
}

// judgeReleaseTokenFindings classifies findings into excused and unexcused, tracking stale and expired exceptions.
func judgeReleaseTokenFindings(findings []forge.ReleaseTokenFinding, totalSteps int, entries []config.Exception, today time.Time) releaseTokenJudgement {
	judged := releaseTokenJudgement{
		totalSteps: totalSteps,
		live:       map[string]config.Exception{},
		expired:    map[string]config.Exception{},
	}
	failing := map[string][]forge.ReleaseTokenFinding{}
	for i := 0; i < len(findings) && i < maxReleaseTokenFindings; i++ {
		wf := findings[i].Workflow
		failing[wf] = append(failing[wf], findings[i])
	}

	for i := 0; i < len(entries) && i < config.MaxExceptions; i++ {
		entry := entries[i]
		if len(failing[entry.Path]) == 0 {
			judged.stale = append(judged.stale, entry)
		} else if entry.Expired(today) {
			judged.expired[entry.Path] = entry
		} else {
			judged.live[entry.Path] = entry
		}
	}

	for i := 0; i < len(findings) && i < maxReleaseTokenFindings; i++ {
		f := findings[i]
		if _, ok := judged.live[f.Workflow]; ok {
			judged.excused = append(judged.excused, f)
		} else {
			judged.unexcused = append(judged.unexcused, f)
		}
	}
	return judged
}

// failure formats the error message for unexcused findings or stale exception entries.
func (j releaseTokenJudgement) failure() string {
	lines := make([]string, 0, len(j.unexcused)+4)
	for index := 0; index < len(j.unexcused) && index < maxReleaseTokenFindings; index++ {
		f := j.unexcused[index]
		lines = append(lines, fmt.Sprintf("[FAIL] %s: %s: %s.", releaseTokenGate, f.Where(), f.Problem))
	}
	if omitted := len(j.unexcused) - maxReleaseTokenFindings; omitted > 0 {
		lines = append(lines, fmt.Sprintf("[FAIL] %s: %d more release-please steps fail.", releaseTokenGate, omitted))
	}
	if len(j.unexcused) > 0 {
		lines = append(lines, j.unexcusedLines()...)
	}
	if len(j.stale) > 0 {
		lines = append(lines, staleExceptionEntries(releaseTokenGate, config.ExceptionRuleReleaseToken, "step", j.stale, "the workflows they name hold no release-please step with a finding"))
	}
	return strings.Join(lines, "\n")
}

// unexcusedLines reports expired entries and guidance for resolving or exempting failing steps.
func (j releaseTokenJudgement) unexcusedLines() []string {
	var lines []string
	seen := map[string]bool{}
	for index := 0; index < len(j.unexcused) && index < maxReleaseTokenFindings; index++ {
		path := j.unexcused[index].Workflow
		entry, expired := j.expired[path]
		if seen[path] || !expired {
			continue
		}
		seen[path] = true
		lines = append(lines, expiredExceptionEntry(releaseTokenGate, "the release-please step above in "+path+" fails",
			"give it a token from an actions/create-github-app-token step", entry))
	}
	days, ok := config.ExceptionCategoryDays("C")
	if !ok {
		days = 10
	}
	return append(lines, fmt.Sprintf("[FAIL] %s: no live exceptions entry declares the steps above; give each a token from an "+
		"actions/create-github-app-token step (%s), or, for a step that cannot yet, declare its workflow in the exceptions "+
		"list of .standards.yaml (rule %s, path %s/<workflow>, category C, a reason, and an expiry at most %d days ahead).",
		releaseTokenGate, releaseTokenGuide, config.ExceptionRuleReleaseToken, ghworkflow.Dir, days))
}

// pass formats the success report including any declared exemptions.
func (j releaseTokenJudgement) pass() string {
	var b strings.Builder
	stepWord := "step takes"
	if j.totalSteps != 1 {
		stepWord = "steps take"
	}
	fmt.Fprintf(&b, "[PASS] %s: %d release-please %s token from a GitHub App token step", releaseTokenGate, j.totalSteps, stepWord)
	if len(j.excused) > 0 {
		fmt.Fprintf(&b, "; %d excepted:", len(j.excused))
		j.writeExcused(&b)
	} else {
		b.WriteString(".")
	}
	b.WriteString("\n" + releaseTokenScope)
	return b.String()
}

// writeExcused lists each excused step under the entry that declares it.
func (j releaseTokenJudgement) writeExcused(b *strings.Builder) {
	var currentPath string
	for index := 0; index < len(j.excused) && index < maxReleaseTokenFindings; index++ {
		f := j.excused[index]
		if f.Workflow != currentPath {
			currentPath = f.Workflow
			live := j.live[f.Workflow]
			fmt.Fprintf(b, "\n  - %s, excepted until %s by the exceptions entry (rule %s, %s): %s",
				f.Workflow, live.Expires, live.Rule, live.Target(), live.Reason)
		}
		fmt.Fprintf(b, "\n    - %s: %s", f.Where(), f.Problem)
	}
	if omitted := len(j.excused) - maxReleaseTokenFindings; omitted > 0 {
		fmt.Fprintf(b, "\n  - %d more excepted findings", omitted)
	}
}
