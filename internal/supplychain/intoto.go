package supplychain

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// inTotoStatementType is the _type of every in-toto v1 Statement.
const inTotoStatementType = "https://in-toto.io/Statement/v1"

// inTotoStatementFields maps every top-level field of an in-toto v1 Statement to whether the
// spec requires it (https://github.com/in-toto/attestation/blob/main/spec/v1/statement.md).
var inTotoStatementFields = map[string]bool{"_type": true, "subject": true, "predicateType": true, "predicate": false}

// resourceDescriptorFields maps every field of an in-toto v1 ResourceDescriptor, the shape
// of each subject, to whether a subject requires it: the Statement spec requires digest.
var resourceDescriptorFields = map[string]bool{
	"name": false, "uri": false, "digest": true, "content": false,
	"downloadLocation": false, "mediaType": false, "annotations": false,
}

// CheckInTotoStatement refuses encoded JSON that a strict in-toto v1 decoder rejects or
// cannot match an artifact against: anything but one JSON object, a missing or wrong _type,
// a missing subject or predicateType, a top-level field the Statement does not define, no
// subject, more than the subject limit, or a subject without digest or with a field
// ResourceDescriptor does not define. sigstore-go, which cosign verify-blob-attestation uses,
// decodes the DSSE payload with protojson and fails on any undeclared field. The predicate is
// not inspected: the Statement types it as an arbitrary object. The provenance command runs
// this on every statement before writing it, so a field added to SLSAStatement fails
// generation instead of the release workflow's verify step.
func CheckInTotoStatement(data []byte) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return fmt.Errorf("slsa: statement is not one JSON object: %w", err)
	}
	if err := checkFieldSet("statement", top, inTotoStatementFields); err != nil {
		return err
	}
	var statementType string
	if err := json.Unmarshal(top["_type"], &statementType); err != nil || statementType != inTotoStatementType {
		return fmt.Errorf("slsa: statement _type must be %q, got %s", inTotoStatementType, top["_type"])
	}
	return checkSubjectFields(top["subject"])
}

// checkSubjectFields refuses a subject list that is not 1 to maxProvenanceSubjects
// ResourceDescriptor objects, each with a digest and no field the descriptor does not define.
func checkSubjectFields(raw json.RawMessage) error {
	var subjects []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &subjects); err != nil {
		return fmt.Errorf("slsa: statement subject is not an array of objects: %w", err)
	}
	if len(subjects) == 0 || len(subjects) > maxProvenanceSubjects {
		return fmt.Errorf("slsa: statement names %d subjects, want 1 to %d", len(subjects), maxProvenanceSubjects)
	}
	for i := 0; i < len(subjects) && i < maxProvenanceSubjects; i++ {
		if err := checkFieldSet(fmt.Sprintf("subject %d", i+1), subjects[i], resourceDescriptorFields); err != nil {
			return err
		}
	}
	return nil
}

// checkFieldSet refuses an object that lacks a required field or holds a field the schema
// does not define, naming every offending field in sorted order.
func checkFieldSet(where string, object map[string]json.RawMessage, schema map[string]bool) error {
	if object == nil {
		return fmt.Errorf("slsa: %s is not a JSON object", where)
	}
	var unknown, missing []string
	for field := range object {
		if _, defined := schema[field]; !defined {
			unknown = append(unknown, field)
		}
	}
	for field, required := range schema {
		if _, present := object[field]; required && !present {
			missing = append(missing, field)
		}
	}
	sort.Strings(unknown)
	sort.Strings(missing)
	switch {
	case len(unknown) > 0:
		return fmt.Errorf("slsa: %s holds %s, which in-toto v1 does not define; strict verifiers reject it", where, strings.Join(unknown, ", "))
	case len(missing) > 0:
		return fmt.Errorf("slsa: %s lacks required %s", where, strings.Join(missing, ", "))
	}
	return nil
}
