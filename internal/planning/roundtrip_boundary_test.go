package planning

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCanonicalPlanRemainsWithinInputByteLimit(t *testing.T) {
	raw, err := json.Marshal(stepCountDraft(t, MaxSteps))
	if err != nil {
		t.Fatal(err)
	}
	first, err := Compile(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	canonical := first.Files["plan.json"]
	if len(canonical) > MaxJSONBytes {
		t.Fatalf("input %d bytes expanded to %d canonical bytes, exceeding compiler input limit %d", len(raw), len(canonical), MaxJSONBytes)
	}
	second, err := Compile(t.Context(), canonical)
	if err != nil {
		t.Fatalf("compiler rejected its own bounded canonical output: %v", err)
	}
	if first.Digest != second.Digest || !reflect.DeepEqual(first.Files, second.Files) {
		t.Fatal("boundary round trip changed the plan")
	}
}

func TestCanonicalEscapingCannotProduceUncompilableOutput(t *testing.T) {
	draft := stepCountDraft(t, 64)
	for i := range draft.Steps {
		draft.Steps[i].Detail = strings.Repeat("<", MaxTextBytes)
	}
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(draft); err != nil {
		t.Fatal(err)
	}
	if raw.Len() > MaxJSONBytes {
		t.Fatal("fixture must fit the input limit before canonical escaping")
	}
	if _, err := Compile(t.Context(), raw.Bytes()); err == nil || !strings.Contains(err.Error(), "canonical planning draft exceeds") {
		t.Fatalf("canonical expansion must fail before producing artifacts: %v", err)
	}
}
