package needs

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

// FrameworkDemandRequest captures an actionable, deduplicated demand for a framework capability.
type FrameworkDemandRequest struct {
	RequestID             string        `json:"request_id" yaml:"request_id"`
	Title                 string        `json:"title" yaml:"title"`
	Capability            CapabilityKey `json:"capability" yaml:"capability"`
	TargetOrg             string        `json:"target_org" yaml:"target_org"`
	TargetBuilderKit      string        `json:"target_builder_kit" yaml:"target_builder_kit"`
	ConsumingRepos        []string      `json:"consuming_repos" yaml:"consuming_repos"`
	ConsumerCount         int           `json:"consumer_count" yaml:"consumer_count"`
	ReplacedPackages      []string      `json:"replaced_packages" yaml:"replaced_packages"`
	DeduplicationRatio    float64       `json:"deduplication_ratio" yaml:"deduplication_ratio"`
	MaintenanceROI        string        `json:"maintenance_roi" yaml:"maintenance_roi"`
	SpecificationMarkdown string        `json:"specification_markdown" yaml:"specification_markdown"`
}

// SynthesizeDemands aggregates and deduplicates gaps across the fleet into prioritized
// requests, each routed to the first builder kit of the target serving its capability.
func SynthesizeDemands(report *FleetDemandReport, targets Targets) []FrameworkDemandRequest {
	if report == nil || len(report.Gaps) == 0 {
		return make([]FrameworkDemandRequest, 0)
	}

	resolved := targets.resolved()
	requests := make([]FrameworkDemandRequest, 0, len(report.Gaps))
	for _, gap := range report.Gaps {
		req := createDemandRequest(gap, resolved)
		requests = append(requests, req)
	}

	sort.Slice(requests, func(i, j int) bool {
		if requests[i].ConsumerCount == requests[j].ConsumerCount {
			return requests[i].RequestID < requests[j].RequestID
		}
		return requests[i].ConsumerCount > requests[j].ConsumerCount
	})

	return requests
}

func createDemandRequest(gap GapDetail, targets Targets) FrameworkDemandRequest {
	capStr := string(gap.Capability)
	reqID := "REQ-CAP-" + strings.ToUpper(cleanDepKey(capStr))
	language := capabilityLanguage(capStr)
	targetKit := targets[language].RoutingKit()
	ratio := float64(gap.ConsumerCount)
	roi := calculateROI(gap.ConsumerCount)

	title := fmt.Sprintf("[FRAMEWORK-DEMAND] Universal %s Adapter (%d consuming repos)", capStr, gap.ConsumerCount)
	specMD := renderRequestMarkdown(reqID, title, gap, routingLine(targetKit, language), roi)

	return FrameworkDemandRequest{
		RequestID:             reqID,
		Title:                 title,
		Capability:            gap.Capability,
		TargetOrg:             kitOwner(targetKit),
		TargetBuilderKit:      targetKit,
		ConsumingRepos:        gap.Consumers,
		ConsumerCount:         gap.ConsumerCount,
		ReplacedPackages:      gap.PackagesUsed,
		DeduplicationRatio:    ratio,
		MaintenanceROI:        roi,
		SpecificationMarkdown: specMD,
	}
}

// capabilityLanguage names the framework language whose target builds a capability: UI
// capabilities belong to typescript, python and AI to python, rust to rust, native, media
// and GPU to native, and every other capability to go.
func capabilityLanguage(capStr string) string {
	switch {
	case strings.HasPrefix(capStr, "ui."):
		return "typescript"
	case strings.HasPrefix(capStr, "python.") || strings.HasPrefix(capStr, "ai."):
		return "python"
	case strings.HasPrefix(capStr, "rust."):
		return "rust"
	case strings.HasPrefix(capStr, "native.") || strings.HasPrefix(capStr, "media.") || strings.HasPrefix(capStr, "gpu."):
		return "native"
	default:
		return "go"
	}
}

// kitOwner is the owner part of an <owner>/<name> builder kit, or "" for an unrouted request.
func kitOwner(kit string) string {
	owner, _, _ := strings.Cut(kit, "/")
	return owner
}

// routingLine renders the request's routing target: the builder kit, or why there is none.
func routingLine(kit, language string) string {
	if kit == "" {
		return fmt.Sprintf("unrouted (framework.targets.%s.builder_kits not set)", language)
	}
	return "`" + kit + "`"
}

func calculateROI(count int) string {
	if count >= 3 {
		return fmt.Sprintf("High (%d:1 deduplication leverage, eliminating %d duplicate dependencies)", count, count-1)
	}
	if count == 2 {
		return "Medium (2:1 deduplication across fleet)"
	}
	return "Standard (Single repo gap)"
}

// renderRequestMarkdown renders one request; routing is its rendered routingLine.
func renderRequestMarkdown(reqID, title string, gap GapDetail, routing, roi string) string {
	var sb strings.Builder
	writef(&sb, "# %s\n\n", title)
	writef(&sb, "- **Request ID**: `%s`\n", reqID)
	writef(&sb, "- **Target Builder Kit**: %s\n", routing)
	writef(&sb, "- **Maintenance ROI**: %s\n\n", roi)
	sb.WriteString("## Consuming Repositories\n\n")
	for _, repo := range gap.Consumers {
		writef(&sb, "- `%s`\n", repo)
	}
	sb.WriteString("\n## Replaced Third-Party Packages\n\n")
	for _, pkg := range gap.PackagesUsed {
		writef(&sb, "- `%s`\n", pkg)
	}
	sb.WriteString("\n## Acceptance Criteria\n\n")
	sb.WriteString("1. Zero-dependency implementation adhering to HISS-01..16 invariants.\n")
	// The request is built in the builder kit's repository, not the consumer's, so it states
	// the HISS ceiling rather than any one consumer's resolved policy.
	writef(&sb, "2. NASA JPL Rule 4 compliance: all functions bounded to <= %d LOC.\n", config.HISSComplexityCeiling().MaxFuncLOC)
	sb.WriteString("3. 3D unit tests covering positive, negative, and boundary conditions.\n")
	return sb.String()
}

// EmitDemandRequests writes individual RFC files and consolidated manifest to outputDir.
//
// The emitted artifacts enumerate every repository of the operator's fleet, its remote
// identity and its full third-party dependency inventory, so the directory and the files
// are created owner-only instead of world-readable.
//
// HISS-02: ctx is checked before every write, so a cancelled or expired caller deadline
// stops the emission instead of blocking on a hung mount for the whole request set.
func EmitDemandRequests(ctx context.Context, requests []FrameworkDemandRequest, outputDir string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if strings.TrimSpace(outputDir) == "" {
		return fmt.Errorf("needs: demand request output directory must not be empty")
	}
	if err := util.MkdirSecure(outputDir, util.SecureDirPerm); err != nil {
		return fmt.Errorf("failed to create output dir %s: %w", outputDir, err)
	}

	manifestPath := filepath.Join(outputDir, "FRAMEWORK_DEMAND.yaml")
	yamlData, err := yaml.Marshal(requests)
	if err != nil {
		return fmt.Errorf("failed to serialize demands manifest: %w", err)
	}
	if err := util.WriteFileSecure(manifestPath, yamlData, util.SecureFilePerm); err != nil {
		return fmt.Errorf("failed to write %s: %w", manifestPath, err)
	}

	for _, req := range requests {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("demand request emission aborted: %w", ctxErr)
		}
		filePath, pErr := util.ConfinePath(outputDir, req.RequestID+".md")
		if pErr != nil {
			return fmt.Errorf("invalid request id %q: %w", req.RequestID, pErr)
		}
		if err := util.WriteFileSecure(filePath, []byte(req.SpecificationMarkdown), util.SecureFilePerm); err != nil {
			return fmt.Errorf("failed to write request file %s: %w", filePath, err)
		}
	}

	return nil
}
