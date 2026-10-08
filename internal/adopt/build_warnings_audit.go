package adopt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// buildWarningsGate names the gate on every line it prints.
const buildWarningsGate = "Build warnings (HISS-10)"

// buildWarningsScope is what every verdict states about the evidence behind it.
const buildWarningsScope = "Read from the run: steps of the workflow files under .github/workflows; what a make target, " +
	"a script, an action, a preset or a build file sets was not read, and no build was run."

// buildWarningsGuide is the document that lists each toolchain's form and what the gate reads.
const buildWarningsGuide = "docs/guides/build-warnings.md"

// maxBuildWarningsLanes bounds the lanes one verdict lists (HISS-02); the rest are counted.
const maxBuildWarningsLanes = 60

// buildToolchains is the order a verdict counts the lanes of each toolchain in.
var buildToolchains = [...]string{
	forge.ToolchainGCCClang, forge.ToolchainMSVC, forge.ToolchainCMake, forge.ToolchainMeson,
	forge.ToolchainCargo, forge.ToolchainRustc, forge.ToolchainGo, forge.ToolchainUnreadCompiler,
}

// BuildWarningsOptions is what AuditBuildWarnings reads: the repository root, the manifest's
// exceptions list (the gate reads the entries of config.ExceptionRuleBuildWarnings) and the day
// those entries' expiry is judged against.
type BuildWarningsOptions struct {
	Root       string
	Exceptions []config.Exception
	Today      time.Time
}

// AuditBuildWarnings is the HISS-10 build-warnings gate the CLI and MCP audits share. Every
// command of a workflow's run: steps that compiles C, C++, Rust or Go code is a build lane
// (forge.MeasureBuildWarnings), and each lane must build with the warnings-as-errors form its
// toolchain documents: a lane without it fails, naming its workflow, job, step and line, what it
// lacks and the form to add (#816). Without the form a native lane can print thousands of
// warnings and still pass.
//
// A live entry of the exceptions list with rule HISS-10 that names a workflow declares that
// workflow's failing lanes instead: the gate prints them with the entry's reason and expiry and
// passes. An expired entry fails like a missing one, and an entry naming a workflow without a
// failing lane is stale and fails until it is removed (AGENTS.md rule 14). A repository whose
// workflows build none of these languages skips the gate and says so.
func AuditBuildWarnings(ctx context.Context, opts BuildWarningsOptions) (string, error) {
	if ctx == nil {
		return "", errors.New("[FAIL] " + buildWarningsGate + ": the audit requires a context")
	}
	entries := config.ExceptionsFor(opts.Exceptions, config.ExceptionRuleBuildWarnings)
	if err := config.ValidateExceptions(entries, opts.Today); err != nil {
		return "", fmt.Errorf("[FAIL] %s: %w", buildWarningsGate, err)
	}
	measured, err := forge.MeasureBuildWarnings(ctx, opts.Root, forge.BuildWarningsInputs{
		CargoLints: flavor.CargoWarningsLints(ctx, opts.Root),
	})
	if err != nil {
		return "", fmt.Errorf("[FAIL] %s: cannot read the workflows: %w", buildWarningsGate, err)
	}
	if len(measured.Lanes) == 0 {
		if len(entries) > 0 {
			return "", errors.New(staleBuildWarningsEntries(entries, "no workflow step builds C, C++, Rust or Go code"))
		}
		return "[SKIP] " + buildWarningsGate + " not checked: no run: step under .github/workflows builds C, C++, Rust " +
			"or Go code, so no lane needs warnings as errors.", nil
	}
	judged := judgeBuildLanes(measured.Lanes, entries, opts.Today)
	if len(judged.unexcused) > 0 || len(judged.stale) > 0 {
		return "", errors.New(judged.failure())
	}
	return judged.pass(), nil
}

// buildLaneJudgement sorts the lanes against the HISS-10 entries: fatal lanes, failing lanes a
// live entry excuses (by workflow path, in lane order), failing lanes nothing excuses, the
// expired entry of a workflow with such lanes, and the entries that excuse no lane.
type buildLaneJudgement struct {
	fatal     []forge.BuildLane
	excused   []forge.BuildLane
	unexcused []forge.BuildLane
	live      map[string]config.Exception
	expired   map[string]config.Exception
	stale     []config.Exception
}

// judgeBuildLanes judges lanes against entries on today.
func judgeBuildLanes(lanes []forge.BuildLane, entries []config.Exception, today time.Time) buildLaneJudgement {
	judged := buildLaneJudgement{live: map[string]config.Exception{}, expired: map[string]config.Exception{}}
	failing := map[string]bool{}
	for index := 0; index < len(lanes) && index < maxBuildLanes; index++ {
		if !lanes[index].Fatal {
			failing[laneWorkflow(lanes[index])] = true
		}
	}
	// ValidateExceptions refuses a repeated entry, so each workflow has at most one.
	for index := 0; index < len(entries) && index < config.MaxExceptions; index++ {
		entry := entries[index]
		switch {
		case !failing[entry.Path]:
			judged.stale = append(judged.stale, entry)
		case entry.Expired(today):
			judged.expired[entry.Path] = entry
		default:
			judged.live[entry.Path] = entry
		}
	}
	for index := 0; index < len(lanes) && index < maxBuildLanes; index++ {
		judged.place(lanes[index])
	}
	return judged
}

// maxBuildLanes bounds the lanes one judgement reads (HISS-02): every command of every step of
// every job of every workflow can be a lane.
const maxBuildLanes = 1 << 20

// place sorts one lane into the judgement.
func (j *buildLaneJudgement) place(lane forge.BuildLane) {
	_, excused := j.live[laneWorkflow(lane)]
	switch {
	case lane.Fatal:
		j.fatal = append(j.fatal, lane)
	case excused:
		j.excused = append(j.excused, lane)
	default:
		j.unexcused = append(j.unexcused, lane)
	}
}

// laneWorkflow is the repository path of a lane's workflow, the path an entry names.
func laneWorkflow(lane forge.BuildLane) string {
	return ghworkflow.Dir + "/" + lane.Workflow
}

// failure is the gate's [FAIL] report: one line per lane nothing excuses, why no entry declares
// them, and the stale entries.
func (j buildLaneJudgement) failure() string {
	lines := make([]string, 0, len(j.unexcused)+4)
	for index := 0; index < len(j.unexcused) && index < maxBuildWarningsLanes; index++ {
		lane := j.unexcused[index]
		lines = append(lines, fmt.Sprintf("[FAIL] %s: %s: %s builds without warnings as errors: %s. Add: %s.",
			buildWarningsGate, lane.Where(), lane.Toolchain, lane.Detail, lane.Remedy))
	}
	if omitted := len(j.unexcused) - maxBuildWarningsLanes; omitted > 0 {
		lines = append(lines, fmt.Sprintf("[FAIL] %s: %d more lanes build without warnings as errors.", buildWarningsGate, omitted))
	}
	if len(j.unexcused) > 0 {
		lines = append(lines, j.unexcusedLines()...)
	}
	if len(j.stale) > 0 {
		lines = append(lines, staleBuildWarningsEntries(j.stale, "the workflows they name hold no lane that builds without warnings as errors"))
	}
	return strings.Join(lines, "\n")
}

// unexcusedLines says why no entry declares the failing lanes: an expired entry for each
// workflow that has one, then the two ways out.
func (j buildLaneJudgement) unexcusedLines() []string {
	var lines []string
	seen := map[string]bool{}
	for index := 0; index < len(j.unexcused) && index < maxBuildLanes; index++ {
		path := laneWorkflow(j.unexcused[index])
		entry, expired := j.expired[path]
		if seen[path] || !expired {
			continue
		}
		seen[path] = true
		lines = append(lines, expiredExceptionEntry(buildWarningsGate, "the lanes above in "+path+" fail",
			"build them with warnings as errors", entry))
	}
	return append(lines, fmt.Sprintf("[FAIL] %s: no live exceptions entry declares the lanes above; build each with its "+
		"toolchain's warnings-as-errors form (%s), or, for a lane that cannot yet, declare its workflow in the exceptions "+
		"list of .standards.yaml (rule %s, path %s/<workflow>, a reason, and an expiry at most %d days ahead).",
		buildWarningsGate, buildWarningsGuide, config.ExceptionRuleBuildWarnings, ghworkflow.Dir, config.MaxExceptionDays))
}

// pass is the gate's [PASS] report: the fatal lanes counted by toolchain, every excused lane
// under the entry that declares it, and the scope of the measurement.
func (j buildLaneJudgement) pass() string {
	var b strings.Builder
	fmt.Fprintf(&b, "[PASS] %s: %d build lanes fail on a warning%s", buildWarningsGate, len(j.fatal), toolchainCounts(j.fatal))
	if len(j.excused) > 0 {
		fmt.Fprintf(&b, "; %d excepted:", len(j.excused))
		j.writeExcused(&b)
	} else {
		b.WriteString(".")
	}
	b.WriteString("\n" + buildWarningsScope)
	return b.String()
}

// writeExcused lists each excused lane under the entry that declares it.
func (j buildLaneJudgement) writeExcused(b *strings.Builder) {
	var entry string
	for index := 0; index < len(j.excused) && index < maxBuildWarningsLanes; index++ {
		lane := j.excused[index]
		path := laneWorkflow(lane)
		if path != entry {
			entry = path
			live := j.live[path]
			fmt.Fprintf(b, "\n  - %s, excepted until %s by the exceptions entry (rule %s, %s): %s",
				path, live.Expires, live.Rule, live.Target(), live.Reason)
		}
		fmt.Fprintf(b, "\n    - %s: %s builds without warnings as errors: %s", lane.Where(), lane.Toolchain, lane.Detail)
	}
	if omitted := len(j.excused) - maxBuildWarningsLanes; omitted > 0 {
		fmt.Fprintf(b, "\n  - %d more excepted lanes", omitted)
	}
}

// toolchainCounts renders " (<toolchain> <n>, ...)" for lanes in buildToolchains order, or "".
func toolchainCounts(lanes []forge.BuildLane) string {
	counts := map[string]int{}
	for index := 0; index < len(lanes) && index < maxBuildLanes; index++ {
		counts[lanes[index].Toolchain]++
	}
	var parts []string
	for index := 0; index < len(buildToolchains); index++ {
		if count := counts[buildToolchains[index]]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", buildToolchains[index], count))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// staleBuildWarningsEntries is the [FAIL] line for HISS-10 entries that excuse no lane.
func staleBuildWarningsEntries(entries []config.Exception, why string) string {
	return staleExceptionEntries(buildWarningsGate, config.ExceptionRuleBuildWarnings, "lane", entries, why)
}
