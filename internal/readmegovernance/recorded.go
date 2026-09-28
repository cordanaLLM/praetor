package readmegovernance

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// RecordedState reads back the state content's managed block records: the debt baseline
// paragraph, whether the documentation contract paragraph is present, and the forge host the
// HISS badge's AGENTS.md link names (LinkedHost). With the contract or the link present the
// state names owner/name, the repository the block's badges must link to. It reports false
// with no error when content carries no managed block. A state is returned only when content
// is exactly Reconcile's output for it, so a hand-edited block, a block another renderer
// version wrote or a badge linking another repository fails with ErrStale instead of
// yielding a guessed state. A caller that re-renders the block for another repository
// changes the identity and calls Reconcile, the one renderer.
func RecordedState(content, owner, name string) (State, bool, error) {
	lines, managed, err := blockBody(content)
	if err != nil || !managed {
		return State{}, false, err
	}
	state, err := blockState(lines, owner, name)
	if err != nil {
		return State{}, false, err
	}
	if err := Verify(content, state); err != nil {
		return State{}, false, err
	}
	return state, true, nil
}

// blockBody returns the lines between content's managed block markers, line endings
// normalized to LF, and false with no error when content carries no managed block.
func blockBody(content string) ([]string, bool, error) {
	normalized, _, err := util.NormalizeLineEndingsStrict(content)
	if err != nil {
		return nil, false, fmt.Errorf("README line endings are inconsistent: %w", err)
	}
	first, last, err := util.FindMarkedBlock(normalized, Start, End)
	if err != nil {
		return nil, false, fmt.Errorf("README governance markers: %w", err)
	}
	if first < 0 {
		return nil, false, nil
	}
	return strings.Split(normalized, "\n")[first+1 : last], true, nil
}

// blockState parses the facts renderBlock writes into the lines between the markers.
// RecordedState then proves the parse by rendering it again through Verify.
func blockState(lines []string, owner, name string) (State, error) {
	var state State
	if slices.Contains(lines, documentationLine) {
		state.DocumentationEnabled = true
		state.RepositoryOwner, state.RepositoryName = owner, name
	}
	if host := linkedHost(lines, owner, name); host != "" {
		state.RepositoryOwner, state.RepositoryName, state.RepositoryHost = owner, name, host
	}
	for index, line := range lines {
		switch {
		case line == debtPendingLine:
			return state, nil
		case line == debtRecordedLine && index+1 < len(lines):
			return baselineState(state, strings.TrimSuffix(lines[index+1], debtRecordedSuffix))
		}
	}
	return State{}, fmt.Errorf("%w: no debt baseline paragraph", ErrStale)
}

// baselineState reads the recorded count line baselineParagraph writes back into state.
func baselineState(state State, description string) (State, error) {
	count, _, _ := strings.Cut(description, " ")
	n, err := strconv.Atoi(count)
	if err != nil || n < 0 {
		return State{}, fmt.Errorf("%w: unreadable debt baseline paragraph", ErrStale)
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
