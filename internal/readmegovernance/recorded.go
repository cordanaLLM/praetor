package readmegovernance

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// RecordedState reads back the state content's managed block records: the debt baseline
// paragraph and whether the documentation contract paragraph is present. With the contract present the state
// names owner/name, the repository the block's documentation badge must link to. It reports
// false with no error when content carries no managed block. A state is returned only when
// content is exactly Reconcile's output for it, so a hand-edited block, a block another
// renderer version wrote or a badge linking another repository fails with ErrStale instead of
// yielding a guessed state. A caller that re-renders the block for another repository
// changes the identity and calls Reconcile, the one renderer.
func RecordedState(content, owner, name string) (State, bool, error) {
	normalized, _, err := util.NormalizeLineEndingsStrict(content)
	if err != nil {
		return State{}, false, fmt.Errorf("README line endings are inconsistent: %w", err)
	}
	first, last, err := util.FindMarkedBlock(normalized, Start, End)
	if err != nil {
		return State{}, false, fmt.Errorf("README governance markers: %w", err)
	}
	if first < 0 {
		return State{}, false, nil
	}
	state, err := blockState(strings.Split(normalized, "\n")[first+1:last], owner, name)
	if err != nil {
		return State{}, false, err
	}
	if err := Verify(content, state); err != nil {
		return State{}, false, err
	}
	return state, true, nil
}

// blockState parses the facts renderBlock writes into the lines between the markers.
// RecordedState then proves the parse by rendering it again through Verify.
func blockState(lines []string, owner, name string) (State, error) {
	var state State
	if slices.Contains(lines, documentationLine) {
		state.DocumentationEnabled = true
		state.RepositoryOwner, state.RepositoryName = owner, name
	}
	for index, line := range lines {
		switch {
		case line == debtPendingLine:
			return state, nil
		case line == debtRecordedLine && index+1 < len(lines):
			return baselineState(state, strings.TrimSuffix(lines[index+1], debtRecordedSuffix))
		}
	}
	return State{}, fmt.Errorf("%w: no debt baseline row", ErrStale)
}

// baselineState reads the recorded count line baselineParagraph writes back into state.
func baselineState(state State, description string) (State, error) {
	count, _, _ := strings.Cut(description, " ")
	n, err := strconv.Atoi(count)
	if err != nil || n < 0 {
		return State{}, fmt.Errorf("%w: unreadable debt baseline row", ErrStale)
	}
	state.BaselineKnown, state.LegacyDebtCount = true, n
	return state, nil
}

// WithoutBlockBody returns content with the lines between its managed block markers removed
// and the markers kept in place. Two READMEs compare equal after it exactly when they differ
// only inside the region Praetor re-renders. Content without a managed block comes back
// unchanged; unbalanced or duplicated markers are an error.
func WithoutBlockBody(content string) (string, error) {
	first, last, err := util.FindMarkedBlock(content, Start, End)
	if err != nil {
		return "", fmt.Errorf("README governance markers: %w", err)
	}
	if first < 0 {
		return content, nil
	}
	lines := strings.Split(content, "\n")
	return strings.Join(append(lines[:first+1:first+1], lines[last:]...), "\n"), nil
}
