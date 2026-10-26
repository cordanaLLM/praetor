package forge

import (
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
)

const (
	// maxReusableLevels is GitHub's nesting limit for reusable workflows: "You can connect up
	// to ten levels of workflows" (docs.github.com, Reusing workflow configurations, read
	// 2026-10-13), the calling workflow and nine called below it. A call deeper than that is
	// refused, as GitHub refuses to run it.
	maxReusableLevels = 10
	// maxReusableCalls bounds the calls one job's expansion follows, nested ones included:
	// GitHub allows "a maximum of 50 unique reusable workflows from a single workflow file"
	// (same page), and one workflow may be called by several of its jobs (HISS-02).
	maxReusableCalls = 50 * maxJobsPerFile
	// maxReusableContexts bounds the contexts one calling job expands to (HISS-02).
	maxReusableContexts = maxJobsPerFile * maxMatrixLegs
	// reusableContextSeparator joins the calling job's context and the called job's: the forge
	// reports one check per job of the called workflow, named "<caller> / <called job>"
	// (#1212, check runs of plurano/shared-ci pull request 5: "Licence / REUSE lint").
	reusableContextSeparator = " / "
	maxExprTokens            = 64
	maxExprStack             = 32
	// PluranoHoristesLicenceAction is the reusable workflow path called in plurano-owned repositories.
	PluranoHoristesLicenceAction = "plurano/horistes/.github/workflows/licence.yml"
	// PluranoHoristesLicenceWorkflowSHA is the commit SHA of plurano/horistes .github/workflows/licence.yml
	// pinned in the hosted REUSE gate for plurano-owned repositories (read 2026-10-19).
	PluranoHoristesLicenceWorkflowSHA = "5a2700ef8898bc377f0f9d13d5c95abf54f0fa4c"
	// PluranoHoristesLicencePinDate names when PluranoHoristesLicenceWorkflowSHA was read.
	PluranoHoristesLicencePinDate = "2026-10-19"
	// PluranoHoristesSecretScanAction is the reusable workflow path called for secret scanning in plurano-owned repositories.
	PluranoHoristesSecretScanAction = "plurano/horistes/.github/workflows/secret-scan.yml" // #nosec G101 -- a workflow path; "Secret" names the scan, no credential
	// PluranoHoristesSecretScanWorkflowSHA is the commit SHA of plurano/horistes .github/workflows/secret-scan.yml
	// pinned for secret scanning (read 2026-10-26).
	PluranoHoristesSecretScanWorkflowSHA = "c736fac69552061fb795ac49951c5284a3f5d041" // #nosec G101 -- a public commit id, no credential
	// PluranoHoristesSecretScanPinDate names when PluranoHoristesSecretScanWorkflowSHA was read.
	PluranoHoristesSecretScanPinDate = "2026-10-26"
	// PluranoHoristesReleasePleaseAction is the reusable workflow path called for release pull requests and releases in plurano-owned repositories.
	PluranoHoristesReleasePleaseAction = "plurano/horistes/.github/workflows/release-please.yml"
	// PluranoHoristesReleasePleaseWorkflowSHA is the commit SHA of plurano/horistes .github/workflows/release-please.yml
	// pinned for release management (read 2026-10-26).
	PluranoHoristesReleasePleaseWorkflowSHA = "c9632c6f9a06ccd560ffe4e1637649bcf19221db"
	// PluranoHoristesReleasePleasePinDate names when PluranoHoristesReleasePleaseWorkflowSHA was read.
	PluranoHoristesReleasePleasePinDate = "2026-10-26"
	// PluranoHoristesHookCopyAction is the reusable workflow path called for hook copy drift checks in plurano-owned repositories.
	PluranoHoristesHookCopyAction = "plurano/horistes/.github/workflows/hook-copy.yml"
	// PluranoHoristesHookCopyWorkflowSHA is the commit SHA of plurano/horistes .github/workflows/hook-copy.yml
	// pinned for hook copy drift verification (read 2026-10-26).
	PluranoHoristesHookCopyWorkflowSHA = "c9632c6f9a06ccd560ffe4e1637649bcf19221db"
	// PluranoHoristesHookCopyPinDate names when PluranoHoristesHookCopyWorkflowSHA was read.
	PluranoHoristesHookCopyPinDate = "2026-10-26"
)

// pinnedReusableWorkflow is a known external reusable workflow the engine pins.
type pinnedReusableWorkflow struct {
	sha     string
	pinName string
	yaml    string
}

// horistesLicenceWorkflowYAML mirrors plurano/horistes .github/workflows/licence.yml at
// PluranoHoristesLicenceWorkflowSHA (read 2026-10-19), limited to inputs and the job name
// needed to compute check contexts.
const horistesLicenceWorkflowYAML = `on:
  workflow_call:
    inputs:
      runner:
        type: string
        default: ubuntu-26.04
      root:
        type: string
        default: .
jobs:
  reuse:
    name: ${{ inputs.root == '.' && 'REUSE lint' || format('REUSE lint ({0})', inputs.root) }}
`

// horistesSecretScanWorkflowYAML mirrors plurano/horistes .github/workflows/secret-scan.yml at
// PluranoHoristesSecretScanWorkflowSHA (read 2026-10-26), limited to inputs and the job name
// needed to compute check contexts.
// #nosec G101 -- a public workflow mirror (inputs and the job name), no credential
const horistesSecretScanWorkflowYAML = `on:
  workflow_call:
    inputs:
      runner:
        type: string
        default: ubuntu-26.04
jobs:
  scan:
    name: Secret scan
`

// horistesReleasePleaseWorkflowYAML mirrors plurano/horistes .github/workflows/release-please.yml at
// PluranoHoristesReleasePleaseWorkflowSHA (read 2026-10-26), limited to inputs and the job name
// needed to compute check contexts.
const horistesReleasePleaseWorkflowYAML = `on:
  workflow_call:
    inputs:
      runner:
        type: string
        required: false
        default: ubuntu-26.04
      app_client_id:
        type: string
        required: true
      config_file:
        type: string
        required: false
        default: release-please-config.json
      manifest_file:
        type: string
        required: false
        default: .release-please-manifest.json
jobs:
  release-please:
    name: Release please
`

// horistesHookCopyWorkflowYAML mirrors plurano/horistes .github/workflows/hook-copy.yml at
// PluranoHoristesHookCopyWorkflowSHA (read 2026-10-26), limited to inputs and the job name
// needed to compute check contexts.
const horistesHookCopyWorkflowYAML = `on:
  workflow_call:
    inputs:
      runner:
        type: string
        required: false
        default: ubuntu-26.04
      hooks_path:
        type: string
        required: false
        default: .config/hooks
jobs:
  check:
    name: Hook copy
`

// knownPinnedExternalWorkflows maps external workflow action paths to their pinned specification.
var knownPinnedExternalWorkflows = map[string]pinnedReusableWorkflow{
	PluranoHoristesLicenceAction: {
		sha:     PluranoHoristesLicenceWorkflowSHA,
		pinName: "forge.PluranoHoristesLicenceWorkflowSHA",
		yaml:    horistesLicenceWorkflowYAML,
	},
	PluranoHoristesSecretScanAction: {
		sha:     PluranoHoristesSecretScanWorkflowSHA,
		pinName: "forge.PluranoHoristesSecretScanWorkflowSHA",
		yaml:    horistesSecretScanWorkflowYAML,
	},
	PluranoHoristesReleasePleaseAction: {
		sha:     PluranoHoristesReleasePleaseWorkflowSHA,
		pinName: "forge.PluranoHoristesReleasePleaseWorkflowSHA",
		yaml:    horistesReleasePleaseWorkflowYAML,
	},
	PluranoHoristesHookCopyAction: {
		sha:     PluranoHoristesHookCopyWorkflowSHA,
		pinName: "forge.PluranoHoristesHookCopyWorkflowSHA",
		yaml:    horistesHookCopyWorkflowYAML,
	},
}

// calledWorkflows holds the repository's workflow documents by file name, so a job that calls
// a reusable workflow of this repository (localWorkflowPrefix) reads the jobs it runs. Each
// document is parsed once, on its first call.
type calledWorkflows struct {
	data   map[string][]byte
	parsed map[string]*workflowSpec
}

// newCalledWorkflows indexes files by name. A caller that selects some workflows indexes all of
// them, so a selected workflow can call one that is not selected.
func newCalledWorkflows(files []workflowFile) calledWorkflows {
	called := calledWorkflows{data: make(map[string][]byte, len(files)), parsed: map[string]*workflowSpec{}}
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		called.data[files[i].Name] = files[i].Data
	}
	return called
}

// spec returns the reusable workflow uses names, which job, the calling job, calls. A workflow
// of another repository is refused unless it matches a known external reusable workflow the engine
// pins at the pinned commit SHA. So is a local name that is no workflow of this repository
// declaring workflow_call.
func (c calledWorkflows) spec(job, uses string) (*workflowSpec, error) {
	trimmed := strings.TrimSpace(uses)
	name, local := strings.CutPrefix(trimmed, localWorkflowPrefix)
	if !local {
		return c.externalSpec(job, trimmed)
	}
	if spec, ok := c.parsed[name]; ok {
		return spec, nil
	}
	// Files are indexed by name, so a call into a subdirectory, where GitHub runs no
	// reusable workflow, finds none.
	data, ok := c.data[name]
	if !ok {
		return nil, fmt.Errorf("job %q calls %s, which is no workflow in %s", job, uses, plannedWorkflowDir)
	}
	var spec workflowSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("job %q calls %s: parse: %w", job, uses, err)
	}
	if _, reusable := eventTrigger(&spec.On, workflowCallEvent); !reusable {
		return nil, fmt.Errorf("job %q calls %s, which declares no %s trigger", job, uses, workflowCallEvent)
	}
	if len(spec.Jobs) > maxJobsPerFile {
		return nil, fmt.Errorf("job %q calls %s, which exceeds %d jobs", job, uses, maxJobsPerFile)
	}
	c.parsed[name] = &spec
	return &spec, nil
}

// externalSpec resolves an external reusable workflow against the engine's pinned table.
func (c calledWorkflows) externalSpec(job, uses string) (*workflowSpec, error) {
	pin, pinned := util.ParseSHAPin(uses)
	entry, known := knownPinnedExternalWorkflows[pin.Action]
	if known && pinned {
		if pin.SHA == entry.sha {
			if spec, ok := c.parsed[uses]; ok {
				return spec, nil
			}
			var spec workflowSpec
			if err := yaml.Unmarshal([]byte(entry.yaml), &spec); err != nil {
				return nil, fmt.Errorf("job %q calls %s: parse: %w", job, uses, err)
			}
			c.parsed[uses] = &spec
			return &spec, nil
		}
		return nil, fmt.Errorf("job %q calls the reusable workflow %s at %s; only the pinned commit %s is supported "+
			"(move the engine pin (%s) first)", job, pin.Action, pin.SHA, entry.sha, entry.pinName)
	}

	return nil, fmt.Errorf("job %q calls the reusable workflow %s of another repository; its jobs, and so the "+
		"check contexts \"<job> / <called job>\" the forge reports for it, cannot be read here, and a required "+
		"check context that no run reports blocks the branch permanently", job, uses)
}

// reusableCall is one call still to expand: the contexts of the job that makes it, the
// workflow it calls, the calling job's id for messages, the level the called workflow
// runs at (the workflow a trigger starts is level 1), and the with inputs passed by the caller.
type reusableCall struct {
	prefixes []string
	uses     string
	job      string
	level    int
	with     map[string]any
}

// reusableCallContexts returns the check contexts a job that calls a reusable workflow reports
// inside the repository named identity (#1212). The forge reports none under the calling job's
// own name: it reports one check per job of the called workflow, "<caller> / <called job>". So
// the caller's contexts (jobCheckContexts: its name, its id, or one per matrix leg) are joined
// with those of every job of the called workflow that is itself required there (requiredJob,
// less what a proven aggregate covers, as for a workflow a trigger starts), each named the same
// way, matrix legs included. A called job that calls another workflow is followed through a
// work list, never by recursion (HISS-01), down to GitHub's limit of maxReusableLevels.
func reusableCallContexts(id string, job workflowJob, called calledWorkflows, identity string) ([]string, error) {
	callers, err := jobCheckContexts(id, job)
	if err != nil {
		return nil, err
	}
	queue := []reusableCall{{prefixes: callers, uses: job.Uses, job: id, level: 2, with: job.With}}
	var contexts []string
	for next := 0; next < len(queue); next++ {
		if next >= maxReusableCalls {
			return nil, fmt.Errorf("job %q: its reusable workflow calls exceed %d", id, maxReusableCalls)
		}
		found, calls, err := expandReusableCall(queue[next], called, identity)
		if err != nil {
			return nil, err
		}
		contexts = append(contexts, found...)
		if len(contexts) > maxReusableContexts {
			return nil, fmt.Errorf("job %q: its reusable workflow calls report more than %d contexts", id, maxReusableContexts)
		}
		queue = append(queue, calls...)
	}
	return contexts, nil
}

// expandReusableCall returns the contexts the jobs of one called workflow report and the calls
// its jobs make in turn, one level deeper.
func expandReusableCall(call reusableCall, called calledWorkflows, identity string) ([]string, []reusableCall, error) {
	spec, err := called.spec(call.job, call.uses)
	if err != nil {
		return nil, nil, err
	}
	ids := sortedJobIDs(spec.Jobs)
	covered := aggregateCoveredJobs(spec, ids)
	var contexts []string
	var calls []reusableCall
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		job := spec.Jobs[ids[i]]
		if covered[ids[i]] || !requiredJob(&job, identity) {
			continue
		}
		names, err := calledJobContexts(ids[i], job, spec, call.with)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", call.uses, err)
		}
		joined := joinReusableContexts(call.prefixes, names)
		if strings.TrimSpace(job.Uses) == "" {
			contexts = append(contexts, joined...)
			continue
		}
		if call.level+1 > maxReusableLevels {
			return nil, nil, fmt.Errorf("%s: job %q calls %s at level %d, deeper than the %d levels GitHub runs",
				call.uses, ids[i], job.Uses, call.level+1, maxReusableLevels)
		}
		calls = append(calls, reusableCall{prefixes: joined, uses: job.Uses, job: ids[i], level: call.level + 1, with: job.With})
	}
	return contexts, calls, nil
}

// calledJobContexts resolves a job's name expression before computing its check contexts.
func calledJobContexts(id string, job workflowJob, spec *workflowSpec, with map[string]any) ([]string, error) {
	if strings.Contains(job.Name, expressionOpen) && job.Strategy.Matrix.ShortTag() == "!!null" {
		if resolved, ok := resolveCalledJobName(job.Name, spec, with); ok {
			job.Name = resolved
		}
	}
	return jobCheckContexts(id, job)
}

// joinReusableContexts names every called job context under every calling one.
func joinReusableContexts(callers, names []string) []string {
	joined := make([]string, 0, len(callers)*len(names))
	for i := 0; i < len(callers) && i < maxMatrixLegs; i++ {
		for j := 0; j < len(names) && j < maxMatrixLegs; j++ {
			joined = append(joined, callers[i]+reusableContextSeparator+names[j])
		}
	}
	return joined
}
