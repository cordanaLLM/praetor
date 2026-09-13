// Package planning compiles bounded planning drafts into deterministic proposal artifacts.
// It validates structure only; source assertions and instructions remain inert, unverified data.
package planning

const (
	SchemaVersion         = 1
	MaxJSONBytes          = 512 << 10
	MaxArtifactBytes      = 1 << 20
	MaxTotalArtifactBytes = 4 << 20
	MaxSources            = 64
	MaxRequirements       = 256
	MaxMilestones         = 64
	MaxSteps              = 512
	MaxDependencies       = 32
	MaxCitations          = 16
	MaxActions            = 16
	MaxExpectedOutputs    = 16
	MaxAcceptanceCases    = 8
	MaxIDBytes            = 128
	MaxTextBytes          = 4096
)

const (
	StatusStructurallyValid = "structurally_valid"
	ProvenanceUnverified    = "caller_asserted_unverified"
)

// Draft is the complete caller-authored planning graph. ID is its stable logical
// identity; Digest on Result changes when any normalized graph content changes.
type Draft struct {
	SchemaVersion int           `json:"schema_version"`
	ID            string        `json:"id"`
	Project       Project       `json:"project"`
	Sources       []Source      `json:"sources"`
	Requirements  []Requirement `json:"requirements"`
	Milestones    []Milestone   `json:"milestones"`
	Steps         []Step        `json:"steps"`
}

type Project struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Repository string `json:"repository"`
	Revision   string `json:"revision"`
}

// Source records provenance asserted by the caller. This package has no source
// adapter and therefore requires Verified to remain false.
type Source struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Locator    string `json:"locator"`
	Revision   string `json:"revision"`
	SHA256     string `json:"sha256"`
	Provenance string `json:"provenance"`
	Verified   bool   `json:"verified"`
}

type Citation struct {
	SourceID     string `json:"source_id"`
	SourceSHA256 string `json:"source_sha256"`
	Quote        string `json:"quote"`
}

type Requirement struct {
	ID          string     `json:"id"`
	Detail      string     `json:"detail"`
	Disposition string     `json:"disposition"`
	SourceRefs  []Citation `json:"source_refs"`
}

type Acceptance struct {
	Positive []string `json:"positive"`
	Negative []string `json:"negative"`
	Boundary []string `json:"boundary"`
}

type Milestone struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	Outcome    string     `json:"outcome"`
	DependsOn  []string   `json:"depends_on"`
	Acceptance Acceptance `json:"acceptance"`
}

type ExpectedOutput struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

type ProjectLink struct {
	TODOID      string `json:"todo_id"`
	RoadmapID   string `json:"roadmap_id"`
	MilestoneID string `json:"milestone_id"`
}

// Step actions are inert text. Kind classifies planned work; Compile never
// interprets actions as shell, model, or tool commands.
type Step struct {
	ID              string           `json:"id"`
	Title           string           `json:"title"`
	Detail          string           `json:"detail"`
	Kind            string           `json:"kind"`
	Status          string           `json:"status"`
	RequirementIDs  []string         `json:"requirement_ids"`
	MilestoneID     string           `json:"milestone_id"`
	DependsOn       []string         `json:"depends_on"`
	Actions         []string         `json:"actions"`
	ExpectedOutputs []ExpectedOutput `json:"expected_outputs"`
	Acceptance      Acceptance       `json:"acceptance"`
	Link            ProjectLink      `json:"link"`
}

// Result is a structurally valid draft proposal. Files are derived from Draft
// and contain plan.json plus the TODO, roadmap, and milestone projections.
type Result struct {
	Status           string            `json:"status"`
	ReviewRequired   bool              `json:"review_required"`
	ProvenanceStatus string            `json:"provenance_status"`
	Digest           string            `json:"digest"`
	MilestoneOrder   []string          `json:"milestone_order"`
	StepOrder        []string          `json:"step_order"`
	Unsupported      []string          `json:"unsupported"`
	Draft            Draft             `json:"draft"`
	Files            map[string][]byte `json:"-"`
}
