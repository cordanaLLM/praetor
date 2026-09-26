package editor

import (
	"fmt"
	"strings"
)

// Selection is the editor set one repository declares, resolved against the supported set.
type Selection struct {
	// Editors are the canonical ids to generate. Empty when the declaration names none.
	Editors []string `json:"editors"`
	// NotApplicable are the supported canonical ids the declaration leaves out, in
	// supportedEditorIDs order. Their files are neither generated nor verified, so a file the
	// repository deleted is not recreated (#202).
	NotApplicable []string `json:"not_applicable,omitempty"`
}

// SelectEditors resolves the editors list a repository declares in .standards.yaml. nil, the
// key being absent, keeps every supported editor, the behaviour before the key existed; a
// non-nil list, empty included, selects exactly the editors it names. An id that resolves to
// no supported editor fails the whole selection, exactly as it fails --editors.
func SelectEditors(declared []string) (Selection, error) {
	if declared == nil {
		return Selection{Editors: append([]string(nil), supportedEditorIDs...)}, nil
	}
	if len(declared) > maxLoopBound {
		return Selection{}, fmt.Errorf("editors names at most %d ids, got %d", maxLoopBound, len(declared))
	}
	selected, unknown := normalizeEditors(declared)
	if len(unknown) > 0 {
		return Selection{}, unknownEditorsError(unknown)
	}
	chosen := make(map[string]bool, len(selected))
	for _, id := range selected {
		chosen[id] = true
	}
	notApplicable := make([]string, 0, len(supportedEditorIDs))
	for _, id := range supportedEditorIDs {
		if !chosen[id] {
			notApplicable = append(notApplicable, id)
		}
	}
	if selected == nil {
		selected = []string{}
	}
	return Selection{Editors: selected, NotApplicable: notApplicable}, nil
}

// unknownEditorsError is the one rejection every editor selection path returns, so --editors
// and the manifest key name an unknown id and its correction the same way.
func unknownEditorsError(unknown []string) error {
	return fmt.Errorf("unknown editor id(s): %s; supported: %s",
		strings.Join(unknown, ", "), strings.Join(supportedEditorIDs, ", "))
}
