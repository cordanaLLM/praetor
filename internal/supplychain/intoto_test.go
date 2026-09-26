package supplychain

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
)

// fieldNames lists an object's field names, sorted and comma-joined.
func fieldNames(object map[string]json.RawMessage) string {
	return strings.Join(slices.Sorted(maps.Keys(object)), ",")
}

// strictStatement returns a well-formed in-toto v1 statement whose subject list is subjects.
func strictStatement(subjects string) string {
	return `{"_type":"https://in-toto.io/Statement/v1","subject":` + subjects +
		`,"predicateType":"https://slsa.dev/provenance/v1","predicate":{"buildDefinition":{"buildType":"b"}}}`
}

// oneSubject is a subject list holding one well-formed subject.
const oneSubject = `[{"name":"a.tar.gz","digest":{"sha256":"` + digestA + `"}}]`

// Positive: a generated statement, a statement without the optional predicate, and a subject
// using every ResourceDescriptor field all pass.
func TestCheckInTotoStatement_Positive_AcceptsSpecFields(t *testing.T) {
	path, _ := writeArtifact(t, "artifact.tar.gz", []byte("archive bytes\n"))
	stmt, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: path, BuilderID: "b"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	generated, err := json.MarshalIndent(stmt, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	everyField := `[{"name":"a","uri":"https://example.test/a","digest":{"sha256":"` + digestA + `"},` +
		`"content":"YQ==","downloadLocation":"https://example.test/a","mediaType":"application/gzip","annotations":{"k":"v"}}]`
	cases := map[string]string{
		"generated statement":        string(generated),
		"predicate omitted":          `{"_type":"https://in-toto.io/Statement/v1","subject":` + oneSubject + `,"predicateType":"https://slsa.dev/provenance/v1"}`,
		"every descriptor field":     strictStatement(everyField),
		"unknown field in predicate": strings.Replace(strictStatement(oneSubject), `"buildType":"b"`, `"buildType":"b","extra":true`, 1),
	}
	for name, data := range cases {
		if err := CheckInTotoStatement([]byte(data)); err != nil {
			t.Errorf("%s: refused: %v", name, err)
		}
	}
}

// Negative: the praetorEmission extension a strict verifier rejected, an unknown subject
// field, a missing or wrong _type, a missing predicateType or digest, and input that is not
// one JSON object are all refused, naming what is wrong.
func TestCheckInTotoStatement_Negative_RefusesWhatStrictVerifiersReject(t *testing.T) {
	good := strictStatement(oneSubject)
	cases := map[string]struct{ data, want string }{
		"praetorEmission extension": {strings.Replace(good, `{"_type"`, `{"praetorEmission":{"signed":false},"_type"`, 1), "praetorEmission"},
		"unknown subject field":     {strictStatement(`[{"name":"a","sha256":"x","digest":{"sha256":"` + digestA + `"}}]`), "subject 1 holds sha256"},
		"subject without digest":    {strictStatement(`[{"name":"a"}]`), "subject 1 lacks required digest"},
		"subject not an object":     {strictStatement(`[null]`), "subject 1 is not a JSON object"},
		"subject not an array":      {strictStatement(`{"name":"a"}`), "not an array"},
		"missing _type":             {strings.Replace(good, `"_type":"https://in-toto.io/Statement/v1",`, "", 1), "lacks required _type"},
		"statement v0.1 _type":      {strings.Replace(good, "Statement/v1", "Statement/v0.1", 1), "_type must be"},
		"missing predicateType":     {strings.Replace(good, `,"predicateType":"https://slsa.dev/provenance/v1"`, "", 1), "lacks required predicateType"},
		"JSON array":                {"[" + good + "]", "not one JSON object"},
		"null":                      {"null", "statement is not a JSON object"},
		"trailing document":         {good + good, "not one JSON object"},
		"empty input":               {"", "not one JSON object"},
	}
	for name, tc := range cases {
		err := CheckInTotoStatement([]byte(tc.data))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}
}

// Boundary: exactly maxProvenanceSubjects subjects pass; one more, none, and an empty object
// are refused.
func TestCheckInTotoStatement_Boundary_SubjectCountAndEmptyObject(t *testing.T) {
	subjects := func(n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = fmt.Sprintf(`{"name":"f%d","digest":{"sha256":"%s"}}`, i, digestA)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	if err := CheckInTotoStatement([]byte(strictStatement(subjects(maxProvenanceSubjects)))); err != nil {
		t.Errorf("%d subjects refused: %v", maxProvenanceSubjects, err)
	}
	cases := map[string]struct{ data, want string }{
		"one over the limit": {strictStatement(subjects(maxProvenanceSubjects + 1)), fmt.Sprintf("%d subjects", maxProvenanceSubjects+1)},
		"no subject":         {strictStatement("[]"), "0 subjects"},
		"empty object":       {"{}", "lacks required _type, predicateType, subject"},
	}
	for name, tc := range cases {
		err := CheckInTotoStatement([]byte(tc.data))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}
}
