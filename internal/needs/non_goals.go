package needs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cordanaLLM/praetor/internal/util"
)

// maxNonGoals bounds the non_goals list of one .needs.yaml (HISS-02).
const maxNonGoals = 128

// maxNonGoalUsesReported bounds the packages one contradicted non-goal names.
const maxNonGoalUsesReported = 8

// ErrNonGoalContradicted reports a non-goal the declaring repository's own code still uses,
// or a framework still provides: the declaration is false.
var ErrNonGoalContradicted = errors.New("needs: declared non-goal is contradicted")

// nonGoalFields is NonGoal without its decoding method, so the strict decode below uses the
// struct tags of every field.
type nonGoalFields NonGoal

// UnmarshalYAML decodes one non_goals entry with the strict decoder (util.DecodeYAMLStrict):
// a key the entry does not declare, such as a misspelled rationale, is refused instead of
// dropped. A custom unmarshaler receives the raw node, where the outer decoder's known-field
// rule no longer applies, so the entry is re-encoded and decoded strictly.
func (g *NonGoal) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("non_goals entry at line %d must be a mapping of capability, rationale and alternative", node.Line)
	}
	raw, err := yaml.Marshal(node)
	if err != nil {
		return fmt.Errorf("re-encode the non_goals entry at line %d: %w", node.Line, err)
	}
	var fields nonGoalFields
	if err := util.DecodeYAMLStrict(raw, &fields); err != nil {
		return fmt.Errorf("non_goals entry at line %d: %w", node.Line, err)
	}
	*g = NonGoal(fields)
	return nil
}

// validateNonGoals checks a row's non_goals list: at most maxNonGoals entries, each naming a
// capability once with a non-empty rationale and alternative, none of them a capability the
// row also declares needed (capabilities.required or capabilities.optional). Every error
// names the entry by its index and capability.
func validateNonGoals(row *RepoNeeds) error {
	if len(row.NonGoals) > maxNonGoals {
		return fmt.Errorf("needs: non_goals declares %d entries; at most %d are allowed", len(row.NonGoals), maxNonGoals)
	}
	seen := make(map[CapabilityKey]int, len(row.NonGoals))
	for i, goal := range row.NonGoals {
		if err := validateNonGoal(i, goal); err != nil {
			return err
		}
		if first, dup := seen[goal.Capability]; dup {
			return fmt.Errorf("needs: %s repeats the capability of non_goals[%d]", nonGoalLabel(i, goal), first)
		}
		seen[goal.Capability] = i
		if list := neededList(row.Capabilities, goal.Capability); list != "" {
			return fmt.Errorf("needs: %s is also declared needed under capabilities.%s; a capability is either needed or a non-goal",
				nonGoalLabel(i, goal), list)
		}
	}
	return nil
}

// validateNonGoal checks the fields of the entry at index i.
func validateNonGoal(i int, goal NonGoal) error {
	switch {
	case strings.TrimSpace(string(goal.Capability)) == "":
		return fmt.Errorf("needs: non_goals[%d] names no capability", i)
	case strings.TrimSpace(goal.Rationale) == "":
		return fmt.Errorf("needs: %s has an empty rationale; say why the capability is a non-goal", nonGoalLabel(i, goal))
	case strings.TrimSpace(goal.Alternative) == "":
		return fmt.Errorf("needs: %s has an empty alternative; name what a repository uses instead", nonGoalLabel(i, goal))
	}
	return nil
}

// nonGoalLabel names the entry at index i in an error: non_goals[i] (capability).
func nonGoalLabel(i int, goal NonGoal) string {
	return fmt.Sprintf("non_goals[%d] (%s)", i, goal.Capability)
}

// neededList names the capabilities list of declared that holds capability, or "".
func neededList(declared CapabilityDeclaration, capability CapabilityKey) string {
	switch {
	case slices.Contains(declared.Required, capability):
		return "required"
	case slices.Contains(declared.Optional, capability):
		return "optional"
	}
	return ""
}

// checkDeclaredNonGoals refuses a row whose declared non-goals are false: a capability its
// own dependencies or selected standard-library imports still use, named with the packages
// that use it, then any entry validateNonGoals refuses. A framework that imports a
// capability it declares a non-goal fails here when `needs scan --check` scans the
// framework's own repository.
func checkDeclaredNonGoals(row *RepoNeeds) error {
	if row == nil || len(row.NonGoals) == 0 {
		return nil
	}
	var contradicted []string
	for i, goal := range row.NonGoals {
		if i >= maxNonGoals {
			break
		}
		if users := nonGoalUsers(row, goal.Capability); len(users) > 0 {
			contradicted = append(contradicted, fmt.Sprintf("%s is used by %s", nonGoalLabel(i, goal), strings.Join(users, ", ")))
		}
	}
	if len(contradicted) > 0 {
		return fmt.Errorf("%w: %s; remove the non-goal or stop using the capability",
			ErrNonGoalContradicted, strings.Join(contradicted, "; "))
	}
	return validateNonGoals(row)
}

// nonGoalUsers lists the packages of row demanding capability, at most
// maxNonGoalUsesReported of them followed by a count of the rest.
func nonGoalUsers(row *RepoNeeds, capability CapabilityKey) []string {
	var users []string
	total := 0
	for _, demands := range [...][]DependencyDemand{row.Dependencies, row.StandardLibraryImports} {
		for _, demand := range demands {
			if demand.Capability != capability {
				continue
			}
			total++
			if len(users) < maxNonGoalUsesReported {
				users = append(users, demand.Package)
			}
		}
	}
	if total > len(users) {
		users = append(users, fmt.Sprintf("%d more", total-len(users)))
	}
	return users
}

// mergeNonGoals appends every non-goal of src whose capability dst does not declare yet.
func mergeNonGoals(dst, src []NonGoal) []NonGoal {
	for _, goal := range src {
		if !slices.ContainsFunc(dst, func(have NonGoal) bool { return have.Capability == goal.Capability }) {
			dst = append(dst, goal)
		}
	}
	return dst
}

// nonGoal returns the non-goal idx declares for capability.
func (idx *FrameworkIndex) nonGoal(capability CapabilityKey) (NonGoal, bool) {
	if idx == nil {
		return NonGoal{}, false
	}
	for _, goal := range idx.NonGoals {
		if goal.Capability == capability {
			return goal, true
		}
	}
	return NonGoal{}, false
}

// markFrameworkNonGoal resolves a gap whose capability idx declares a non-goal, noting the
// rationale and the alternative. A demand an earlier framework resolved as a non-goal that
// idx does not declare is a gap again.
func markFrameworkNonGoal(idx *FrameworkIndex, dep *DependencyDemand) {
	goal, declared := idx.nonGoal(dep.Capability)
	switch {
	case declared:
		dep.Status, dep.FrameworkReplacement = StatusNonGoal, ""
		dep.Notes = fmt.Sprintf("%s declares %s a non-goal: %s Alternative: %s",
			idx.Name, dep.Capability, oneSentence(goal.Rationale), oneLine(goal.Alternative))
	case dep.Status == StatusNonGoal:
		dep.Status = StatusGap
		dep.Notes = undeclaredNote(idx.Name, "package", dep.Capability)
	}
}

// oneLine collapses text onto one line.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// oneSentence collapses text onto one line ending in a full stop.
func oneSentence(text string) string {
	text = oneLine(text)
	if strings.HasSuffix(text, ".") {
		return text
	}
	return text + "."
}

// loadFrameworkNonGoals reads the non-goals the framework checkout at idx.RootPath declares
// in its own .needs.yaml, through the reader every scan uses (loadExistingDeclarations), and
// refuses a non-goal the checkout provides a package for (checkFrameworkNonGoals). A checkout
// without a .needs.yaml declares none.
func loadFrameworkNonGoals(ctx context.Context, idx *FrameworkIndex) error {
	var declared RepoNeeds
	if err := loadExistingDeclarations(ctx, idx.RootPath, &declared); err != nil {
		return fmt.Errorf("read the non-goals of the selected framework: %w", err)
	}
	idx.NonGoals = declared.NonGoals
	return checkFrameworkNonGoals(idx)
}

// checkFrameworkNonGoals refuses a framework index that provides a package for a capability
// it declares a non-goal, naming both: the declaration is false.
func checkFrameworkNonGoals(idx *FrameworkIndex) error {
	for i, goal := range idx.NonGoals {
		if i >= maxNonGoals {
			break
		}
		if packages := idx.Capabilities[goal.Capability]; len(packages) > 0 {
			return fmt.Errorf("%w: framework %s declares %s but provides it in %s", ErrNonGoalContradicted,
				idx.Name, nonGoalLabel(i, goal), strings.Join(packages, ", "))
		}
	}
	return nil
}

// FormatDependencyCounts renders a row's dependency counts for CLI and epic output: "N
// covered, M gaps", with the non-goals between them when the row has any.
func FormatDependencyCounts(readiness ReadinessMetrics) string {
	if readiness.NonGoalDeps == 0 {
		return fmt.Sprintf("%d covered, %d gaps", readiness.CoveredDeps, readiness.GapDeps)
	}
	return fmt.Sprintf("%d covered, %d non-goals, %d gaps", readiness.CoveredDeps, readiness.NonGoalDeps, readiness.GapDeps)
}
