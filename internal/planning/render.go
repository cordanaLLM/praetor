package planning

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"strings"
)

const proposalNotice = "Status: draft proposal; structurally valid; review required. Source provenance is caller-asserted and unverified."

func renderArtifacts(ctx context.Context, result *Result) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// plan.json remains a canonical compiler input, so a consumer can validate
	// and re-render it without translating a result envelope.
	planJSON, err := json.Marshal(result.Draft)
	if err != nil {
		return fmt.Errorf("marshal canonical plan: %w", err)
	}
	planJSON = append(planJSON, '\n')
	if len(planJSON) > MaxJSONBytes {
		return fmt.Errorf("canonical planning draft exceeds the %d-byte compiler input limit", MaxJSONBytes)
	}
	files := map[string][]byte{
		"plan.json":     planJSON,
		"TODO.md":       []byte(renderTODO(result)),
		"ROADMAP.md":    []byte(renderRoadmap(result)),
		"MILESTONES.md": []byte(renderMilestones(result)),
	}
	if err := validateArtifactBounds(files); err != nil {
		return err
	}
	result.Files = files
	return ctx.Err()
}

func validateArtifactBounds(files map[string][]byte) error {
	total := 0
	for _, name := range []string{"MILESTONES.md", "ROADMAP.md", "TODO.md", "plan.json"} {
		data := files[name]
		total += len(data)
		if len(data) == 0 || len(data) > MaxArtifactBytes || total > MaxTotalArtifactBytes {
			return fmt.Errorf("generated artifact %s exceeds bounded output limits", name)
		}
	}
	return nil
}

func renderHeader(builder *strings.Builder, result *Result, title string) {
	fmt.Fprintf(builder, "# %s\n\n%s\n\n", markdownText(title), proposalNotice)
	fmt.Fprintf(builder, "- Plan: `%s`\n- Project: `%s`\n- Digest: `%s`\n- Canonical data: [plan.json](plan.json)\n\n",
		result.Draft.ID, result.Draft.Project.ID, result.Digest)
	builder.WriteString("Unsupported in this proposal:\n\n")
	for _, stage := range result.Unsupported {
		fmt.Fprintf(builder, "- %s\n", stage)
	}
	builder.WriteByte('\n')
}

func renderTODO(result *Result) string {
	var builder strings.Builder
	renderHeader(&builder, result, result.Draft.Project.Title+" TODO")
	for _, step := range result.Draft.Steps {
		fmt.Fprintf(&builder, "<a id=\"%s\"></a>\n", anchorID("todo", step.Link.TODOID))
		fmt.Fprintf(&builder, "- [ ] `%s` — **%s** (`%s`)\n", step.Link.TODOID, markdownText(step.Title), step.ID)
		fmt.Fprintf(&builder, "  - Roadmap: [`%s`](ROADMAP.md#%s); milestone: [`%s`](MILESTONES.md#%s)\n",
			step.Link.RoadmapID, anchorID("roadmap", step.Link.RoadmapID), step.MilestoneID, anchorID("milestone", step.MilestoneID))
		fmt.Fprintf(&builder, "  - Requirements: %s; dependencies: %s\n", codeList(step.RequirementIDs), codeList(step.DependsOn))
	}
	return builder.String()
}

func renderRoadmap(result *Result) string {
	var builder strings.Builder
	renderHeader(&builder, result, result.Draft.Project.Title+" Roadmap")
	builder.WriteString("## Requirements\n\n")
	for _, requirement := range result.Draft.Requirements {
		fmt.Fprintf(&builder, "- `%s` (`proposed`): %s\n", requirement.ID, markdownText(requirement.Detail))
		for _, citation := range requirement.SourceRefs {
			fmt.Fprintf(&builder, "  - Caller-asserted unverified source `%s` sha256 `%s`: %s\n",
				citation.SourceID, citation.SourceSHA256, markdownText(citation.Quote))
		}
	}
	builder.WriteByte('\n')
	for _, step := range result.Draft.Steps {
		fmt.Fprintf(&builder, "<a id=\"%s\"></a>\n## %s\n\n", anchorID("roadmap", step.Link.RoadmapID), markdownText(step.Link.RoadmapID))
		fmt.Fprintf(&builder, "Step `%s`; TODO [`%s`](TODO.md#%s); milestone [`%s`](MILESTONES.md#%s).\n\n",
			step.ID, step.Link.TODOID, anchorID("todo", step.Link.TODOID), step.MilestoneID, anchorID("milestone", step.MilestoneID))
		fmt.Fprintf(&builder, "%s\n\nKind: `%s`; status: `proposed`. Actions are inert instructions.\n\n", markdownText(step.Detail), step.Kind)
		writeNumbered(&builder, "Actions", step.Actions)
		writeOutputs(&builder, step.ExpectedOutputs)
		writeAcceptance(&builder, step.Acceptance)
	}
	return builder.String()
}

func renderMilestones(result *Result) string {
	var builder strings.Builder
	renderHeader(&builder, result, result.Draft.Project.Title+" Milestones")
	for _, milestone := range result.Draft.Milestones {
		fmt.Fprintf(&builder, "<a id=\"%s\"></a>\n## %s\n\n**%s** — %s\n\n", anchorID("milestone", milestone.ID), markdownText(milestone.ID),
			markdownText(milestone.Title), markdownText(milestone.Outcome))
		fmt.Fprintf(&builder, "Dependencies: %s\n\n", codeList(milestone.DependsOn))
		writeAcceptance(&builder, milestone.Acceptance)
		builder.WriteString("Linked steps:\n\n")
		for _, step := range result.Draft.Steps {
			if step.MilestoneID == milestone.ID {
				fmt.Fprintf(&builder, "- [`%s`](ROADMAP.md#%s) / TODO [`%s`](TODO.md#%s)\n", step.ID,
					anchorID("roadmap", step.Link.RoadmapID), step.Link.TODOID, anchorID("todo", step.Link.TODOID))
			}
		}
		builder.WriteByte('\n')
	}
	return builder.String()
}

func writeNumbered(builder *strings.Builder, title string, values []string) {
	fmt.Fprintf(builder, "### %s\n\n", title)
	for index, value := range values {
		fmt.Fprintf(builder, "%d. %s\n", index+1, markdownText(value))
	}
	builder.WriteByte('\n')
}

func writeOutputs(builder *strings.Builder, outputs []ExpectedOutput) {
	builder.WriteString("### Expected outputs\n\n")
	for _, output := range outputs {
		fmt.Fprintf(builder, "- `%s`: %s\n", output.ID, markdownText(output.Description))
	}
	builder.WriteByte('\n')
}

func writeAcceptance(builder *strings.Builder, acceptance Acceptance) {
	builder.WriteString("### Acceptance proposal\n\n")
	writeCases(builder, "Positive", acceptance.Positive)
	writeCases(builder, "Negative", acceptance.Negative)
	writeCases(builder, "Boundary", acceptance.Boundary)
}

func writeCases(builder *strings.Builder, label string, values []string) {
	fmt.Fprintf(builder, "- %s:\n", label)
	for _, value := range values {
		fmt.Fprintf(builder, "  - %s\n", markdownText(value))
	}
	builder.WriteByte('\n')
}

func anchorID(prefix, value string) string {
	return prefix + "-" + hex.EncodeToString([]byte(value))
}

func markdownText(value string) string {
	escaped := html.EscapeString(value)
	replacer := strings.NewReplacer("\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_",
		"{", "\\{", "}", "\\}", "[", "\\[", "]", "\\]", "(", "\\(", ")", "\\)",
		"#", "\\#", "+", "\\+", "-", "\\-", "!", "\\!", "|", "\\|", ">", "\\>")
	return replacer.Replace(escaped)
}

func codeList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, "`"+value+"`")
	}
	return strings.Join(quoted, ", ")
}
