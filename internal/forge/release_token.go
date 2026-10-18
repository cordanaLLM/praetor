package forge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
)

// releaseTokensTimeout bounds one MeasureReleaseTokens call (HISS-02).
const releaseTokensTimeout = 30 * time.Second

// The findings of a release-please step, one per way its token can fail to start the release pull request's checks.
const (
	problemNoToken       = "sets no token, so release-please uses the workflow token, which starts no checks on its release pull request"
	problemWorkflowToken = "takes the workflow token (github.token or secrets.GITHUB_TOKEN), which starts no checks on its release pull request"
	problemFallback      = "falls back with || to another token; a missing app token must fail the run"
	problemNotStepToken  = "takes its token from no steps.<id>.outputs.token of an actions/create-github-app-token step"
	problemNoEarlierStep = "names a token step that no earlier step of the job is"
	problemNotAppToken   = "names a token step that is not actions/create-github-app-token"
	problemSkippable     = "its token step has if: or continue-on-error, so a skipped or forgiven step leaves the token empty"
	problemKeyNotSecret  = "its token step takes private-key from no single ${{ secrets.NAME }}, so a missing key would not stop the run"
	problemNoAppID       = "its token step sets neither client-id nor app-id"
)

// ReleaseTokenFinding describes one violation of the release-token gate.
type ReleaseTokenFinding struct {
	// Workflow is the workflow's path under .github/workflows.
	Workflow string
	// JobID is the job's key under jobs:.
	JobID string
	// Step is the step's label: its name if set, else its 1-based index ("step N").
	Step string
	// Line is the document line where known, or 0.
	Line int
	// Problem describes what is wrong with the token or token step.
	Problem string
}

// Where locates the finding in the workflow document.
func (f ReleaseTokenFinding) Where() string {
	if f.Line > 0 {
		return fmt.Sprintf("%s:%d: job %s, step %s", f.Workflow, f.Line, f.JobID, f.Step)
	}
	return fmt.Sprintf("%s: job %s, step %s", f.Workflow, f.JobID, f.Step)
}

// String returns the formatted finding.
func (f ReleaseTokenFinding) String() string {
	return fmt.Sprintf("%s: %s", f.Where(), f.Problem)
}

// MeasureReleaseTokens checks every workflow under repoPath's .github/workflows:
// any release-please step must take its token from an earlier create-github-app-token step.
func MeasureReleaseTokens(ctx context.Context, repoPath string) ([]ReleaseTokenFinding, int, error) {
	if ctx == nil {
		return nil, 0, errors.New("release token measurement requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, releaseTokensTimeout)
	defer cancel()

	files, err := readWorkflowFiles(ctx, repoPath)
	if err != nil {
		return nil, 0, fmt.Errorf("read workflows: %w", err)
	}

	var findings []ReleaseTokenFinding
	totalSteps := 0
	for i := 0; i < len(files) && i < ghworkflow.MaxFiles; i++ {
		fileFindings, count, err := measureWorkflowReleaseTokens(files[i])
		if err != nil {
			return nil, 0, err
		}
		totalSteps += count
		findings = append(findings, fileFindings...)
	}
	return findings, totalSteps, nil
}

// measureWorkflowReleaseTokens inspects one workflow file for release-please steps.
func measureWorkflowReleaseTokens(file workflowFile) ([]ReleaseTokenFinding, int, error) {
	spec, err := ghworkflow.Parse(file.Data)
	if err != nil {
		return nil, 0, fmt.Errorf("workflow %s: %w", file.Name, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(file.Data, &doc); err != nil {
		return nil, 0, fmt.Errorf("workflow %s: parse yaml node: %w", file.Name, err)
	}

	path := ghworkflow.Dir + "/" + file.Name
	jobIDs := ghworkflow.SortedJobIDs(spec.Jobs)
	var findings []ReleaseTokenFinding
	totalSteps := 0

	for i := 0; i < len(jobIDs) && i < ghworkflow.MaxJobsPerFile; i++ {
		jobID := jobIDs[i]
		job := spec.Jobs[jobID]
		jobFindings, count := inspectJobReleaseTokens(path, jobID, job, &doc)
		totalSteps += count
		findings = append(findings, jobFindings...)
	}
	return findings, totalSteps, nil
}

// inspectJobReleaseTokens checks steps of a single job for release-please actions.
func inspectJobReleaseTokens(path, jobID string, job ghworkflow.Job, doc *yaml.Node) ([]ReleaseTokenFinding, int) {
	var findings []ReleaseTokenFinding
	count := 0

	for j := 0; j < len(job.Steps) && j < ghworkflow.MaxStepsPerJob; j++ {
		step := job.Steps[j]
		if !isReleasePleaseStep(step.Uses) {
			continue
		}
		count++
		line := stepLineNumber(doc, jobID, j)
		stepFindings := evaluateReleasePleaseStep(path, jobID, j, step, job.Steps, line)
		findings = append(findings, stepFindings...)
	}
	return findings, count
}

// evaluateReleasePleaseStep checks one release-please step against the release-token rules.
func evaluateReleasePleaseStep(path, jobID string, idx int, step ghworkflow.Step, steps []ghworkflow.Step, line int) []ReleaseTokenFinding {
	stepLabel := formatStepLabel(step, idx)
	newFinding := func(problem string) ReleaseTokenFinding {
		return ReleaseTokenFinding{
			Workflow: path,
			JobID:    jobID,
			Step:     stepLabel,
			Line:     line,
			Problem:  problem,
		}
	}

	stepID, problem := validateTokenExpression(step.With)
	if problem != "" {
		return []ReleaseTokenFinding{newFinding(problem)}
	}

	appStep, problem := findAppTokenStep(steps, idx, stepID)
	if problem != "" {
		return []ReleaseTokenFinding{newFinding(problem)}
	}

	var findings []ReleaseTokenFinding
	for _, prob := range validateAppTokenStep(appStep) {
		findings = append(findings, newFinding(prob))
	}
	return findings
}

// formatStepLabel formats a step name or 1-based index.
func formatStepLabel(step ghworkflow.Step, idx int) string {
	if strings.TrimSpace(step.Name) != "" {
		return fmt.Sprintf("%q", step.Name)
	}
	return fmt.Sprintf("%d", idx+1)
}

// isReleasePleaseStep reports whether uses invokes release-please-action.
func isReleasePleaseStep(uses string) bool {
	u := strings.TrimSpace(uses)
	return strings.HasPrefix(u, "googleapis/release-please-action@") ||
		strings.HasPrefix(u, "google-github-actions/release-please-action@")
}

// isAppTokenStep reports whether uses invokes actions/create-github-app-token.
func isAppTokenStep(uses string) bool {
	return strings.HasPrefix(strings.TrimSpace(uses), "actions/create-github-app-token@")
}

// expressionBody is the inside of value when value is one closed ${{ }} expression (ghworkflow.UnwrapExpression).
func expressionBody(value any) (string, bool) {
	text := strings.TrimSpace(fmt.Sprint(value))
	if !strings.HasPrefix(text, "${{") {
		return "", false
	}
	body, closed := ghworkflow.UnwrapExpression(text)
	return body, closed && !strings.Contains(body, "${{") && !strings.Contains(body, "}}")
}

// validateTokenExpression reads with.token: the id of the step whose token output it names, or a problem.
func validateTokenExpression(with map[string]any) (string, string) {
	token, problem := tokenText(with)
	if problem != "" {
		return "", problem
	}
	body, ok := expressionBody(token)
	if !ok {
		return "", problemNotStepToken
	}
	return stepOutputID(body)
}

// tokenText is with.token as written, or the problem a missing, fallback or workflow token has.
func tokenText(with map[string]any) (string, string) {
	raw, ok := with["token"]
	token := strings.TrimSpace(fmt.Sprint(raw))
	switch {
	case !ok || raw == nil || token == "":
		return "", problemNoToken
	case strings.Contains(token, "||"):
		return "", problemFallback
	case strings.Contains(token, "github.token") || strings.Contains(token, "GITHUB_TOKEN"):
		return "", problemWorkflowToken
	}
	return token, ""
}

// stepOutputID is the step id of an expression body steps.<id>.outputs.token, or problemNotStepToken.
func stepOutputID(body string) (string, string) {
	stepID, isStep := strings.CutPrefix(body, "steps.")
	stepID, isOutput := strings.CutSuffix(stepID, ".outputs.token")
	if !isStep || !isOutput || stepID == "" || strings.ContainsAny(stepID, " \t\r\n.|&()[]'\"") {
		return "", problemNotStepToken
	}
	return stepID, ""
}

// findAppTokenStep looks up stepID in earlier steps of the same job.
func findAppTokenStep(steps []ghworkflow.Step, currentIdx int, stepID string) (*ghworkflow.Step, string) {
	for k := 0; k < currentIdx && k < len(steps); k++ {
		if steps[k].ID == stepID {
			if !isAppTokenStep(steps[k].Uses) {
				return nil, fmt.Sprintf("%s (%s runs %s)", problemNotAppToken, stepID, strings.TrimSpace(steps[k].Uses))
			}
			return &steps[k], ""
		}
	}
	return nil, fmt.Sprintf("%s (%s)", problemNoEarlierStep, stepID)
}

// validateAppTokenStep verifies an actions/create-github-app-token step.
func validateAppTokenStep(step *ghworkflow.Step) []string {
	var problems []string
	if strings.TrimSpace(step.If) != "" || strings.TrimSpace(step.ContinueOnError) != "" {
		problems = append(problems, problemSkippable)
	}
	if !isValidSecretsExpression(step.With) {
		problems = append(problems, problemKeyNotSecret)
	}
	if !hasAppOrClientID(step.With) {
		problems = append(problems, problemNoAppID)
	}
	return problems
}

// isValidSecretsExpression checks if with.private-key is a single ${{ secrets.<NAME> }} expression.
func isValidSecretsExpression(with map[string]any) bool {
	body, ok := expressionBody(with["private-key"])
	name, isSecret := strings.CutPrefix(body, "secrets.")
	return ok && isSecret && name != "" && !strings.ContainsAny(name, " \t\r\n.|&()[]'\"")
}

// hasAppOrClientID reports whether client-id or app-id is set to a non-empty value.
func hasAppOrClientID(with map[string]any) bool {
	if with == nil {
		return false
	}
	if cid, ok := with["client-id"]; ok && cid != nil && strings.TrimSpace(fmt.Sprint(cid)) != "" {
		return true
	}
	if aid, ok := with["app-id"]; ok && aid != nil && strings.TrimSpace(fmt.Sprint(aid)) != "" {
		return true
	}
	return false
}

// stepJobNode returns the mapping node for jobID under doc.
func stepJobNode(doc *yaml.Node, jobID string) *yaml.Node {
	if doc == nil || doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	jobs := util.YAMLMappingValue(doc.Content[0], "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return nil
	}
	jobNode := util.YAMLMappingValue(jobs, jobID)
	if jobNode == nil || jobNode.Kind != yaml.MappingNode {
		return nil
	}
	return jobNode
}

// stepLineNumber finds the 1-based line number for a step in the workflow YAML document.
func stepLineNumber(doc *yaml.Node, jobID string, stepIndex int) int {
	jobNode := stepJobNode(doc, jobID)
	if jobNode == nil {
		return 0
	}
	steps := util.YAMLMappingValue(jobNode, "steps")
	if steps == nil || steps.Kind != yaml.SequenceNode || stepIndex < 0 || stepIndex >= len(steps.Content) {
		return 0
	}
	stepNode := steps.Content[stepIndex]
	if stepNode == nil {
		return 0
	}
	if usesNode := util.YAMLMappingValue(stepNode, "uses"); usesNode != nil && usesNode.Line > 0 {
		return usesNode.Line
	}
	return stepNode.Line
}
