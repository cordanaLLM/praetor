package planning

import "github.com/cordanaLLM/praetor/internal/strictjson"

// draftJSON bounds one planning draft to MaxJSONBytes and 32 nesting levels and words each
// refusal of the shared strictjson reader for the planning compiler.
var draftJSON = strictjson.Options{
	MaxBytes: MaxJSONBytes,
	MaxDepth: 32,
	Messages: strictjson.Messages{
		Size:      "planning JSON requires 1..%d UTF-8 bytes",
		Syntax:    "decode planning JSON token: %w",
		Surrogate: "planning JSON holds an unpaired UTF-16 surrogate escape",
		Depth:     "planning JSON nesting exceeds %d",
		Tokens:    "planning JSON token bound exceeded",
		Duplicate: "invalid or duplicate planning JSON key",
		Trailing:  "expected exactly one planning JSON document",
		Decode:    "decode planning draft: %w",
	},
}

// decodeDraft validates raw strictly, then checks the complete field shape, which names the
// path of a missing or mistyped field, before the typed decode would report it less precisely.
func decodeDraft(raw []byte) (Draft, error) {
	var draft Draft
	if err := strictjson.Validate(raw, draftJSON); err != nil {
		return draft, err
	}
	if err := validateDraftJSONShape(raw); err != nil {
		return draft, err
	}
	if err := strictjson.Decode(raw, &draft, draftJSON); err != nil {
		return draft, err
	}
	normalizeEmptyDependencies(&draft)
	return draft, nil
}

func normalizeEmptyDependencies(draft *Draft) {
	for index := range draft.Milestones {
		if len(draft.Milestones[index].DependsOn) == 0 {
			draft.Milestones[index].DependsOn = []string{}
		}
	}
	for index := range draft.Steps {
		if len(draft.Steps[index].DependsOn) == 0 {
			draft.Steps[index].DependsOn = []string{}
		}
	}
}
