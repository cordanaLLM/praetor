package util

import "testing"

// TestLinguistGeneratedAttrValue_Positive_SetAndTrue checks the set and true values mark generated code.
func TestLinguistGeneratedAttrValue_Positive_SetAndTrue(t *testing.T) {
	for _, val := range []string{"set", "true"} {
		if !LinguistGeneratedAttrValue(val) {
			t.Fatalf("value %q should mark generated", val)
		}
	}
}

// TestLinguistGeneratedAttrValue_Negative_DenyListValues checks every other value leaves a file hand-written.
func TestLinguistGeneratedAttrValue_Negative_DenyListValues(t *testing.T) {
	for _, val := range []string{"unset", "false", "unspecified", "maybe"} {
		if LinguistGeneratedAttrValue(val) {
			t.Fatalf("value %q must not mark generated", val)
		}
	}
}

// TestParseCheckAttrOutput_Boundary_Empty checks an empty check-attr answer reads as no records.
func TestParseCheckAttrOutput_Boundary_Empty(t *testing.T) {
	got, err := ParseCheckAttrOutput(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

// TestParseCheckAttrRecords_Positive_KeepsGitOrder checks records keep git output order and the grouped view
// holds every attribute of a path.
func TestParseCheckAttrRecords_Positive_KeepsGitOrder(t *testing.T) {
	got, err := ParseCheckAttrRecords([]byte("b.py\x00linguist-generated\x00set\x00a.py\x00linguist-generated\x00unspecified\x00b.py\x00eol\x00lf\x00"))
	if err != nil {
		t.Fatal(err)
	}
	want := []CheckAttrRecord{
		{Path: "b.py", Attr: "linguist-generated", Value: "set"},
		{Path: "a.py", Attr: "linguist-generated", Value: "unspecified"},
		{Path: "b.py", Attr: "eol", Value: "lf"},
	}
	if len(got) != len(want) {
		t.Fatalf("records = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("record %d = %v, want %v", i, got[i], want[i])
		}
	}
	grouped, err := ParseCheckAttrOutput([]byte("b.py\x00linguist-generated\x00set\x00b.py\x00eol\x00lf\x00"))
	if err != nil || grouped["b.py"]["linguist-generated"] != "set" || grouped["b.py"]["eol"] != "lf" {
		t.Fatalf("grouped = %v (%v)", grouped, err)
	}
}

// TestParseCheckAttrRecords_Negative_Truncated checks both readers refuse an answer cut inside a record.
func TestParseCheckAttrRecords_Negative_Truncated(t *testing.T) {
	for _, malformed := range []string{"a.py\x00linguist-generated\x00", "a.py\x00linguist-generated\x00set"} {
		if _, err := ParseCheckAttrRecords([]byte(malformed)); err == nil {
			t.Fatalf("malformed answer %q accepted", malformed)
		}
		if _, err := ParseCheckAttrOutput([]byte(malformed)); err == nil {
			t.Fatalf("malformed answer %q accepted by the grouped reader", malformed)
		}
	}
}
