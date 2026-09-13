package wishes

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestRequestJSONRejectsAliasesNullsAndIrrelevantFields(t *testing.T) {
	for _, raw := range []string{
		`{"Action":"init"}`, `{"action":"init","Action":"init"}`,
		`{"action":"init","policy":null}`, `{"action":"init","wish_id":""}`,
		`{"action":"init","policy":{"max_wishes":1,"max_polls":1,"max_voters":1,"allow_vote_changes":true,"Allow_Withdrawal":true}}`,
		`{"action":"set-status","expected_revision":null,"wish_id":"w1","status":"accepted"}`,
		`{"action":"add-wish","expected_revision":1,"wish":{"id":"w1","kind":"skill","target":"repo","title":"Title","Description":"Text"}}`,
		`{"action":"open-poll","expected_revision":1,"poll":{"id":"p1","wish_id":"w1","question":"Question","choices":[{"id":"yes","Label":"Yes"},{"id":"no","label":"No"}]}}`,
		`{"action":"vote","expected_revision":1,"poll_id":"p1","actor_id":"local:a","choice_ids":null}`,
	} {
		if _, err := DecodeRequest([]byte(raw)); err == nil {
			t.Fatalf("ambiguous request accepted: %s", raw)
		}
	}
	if _, err := DecodeRequest([]byte(`{"action":"init"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestLedgerJSONRejectsAliasesAndMissingPolicyFields(t *testing.T) {
	path, _ := initLedger(t)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(original, &raw); err != nil {
		t.Fatal(err)
	}
	delete(raw, "current_ballots")
	missing, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, corrupt := range []string{
		strings.Replace(string(original), `"schema_version"`, `"Schema_Version"`, 1),
		strings.Replace(string(original), `"allow_vote_changes"`, `"Allow_Vote_Changes"`, 1),
		strings.Replace(string(original), `"wishes": []`, `"wishes": null`, 1),
		string(missing),
	} {
		if err := os.WriteFile(path, []byte(corrupt), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Read(t.Context(), path); err == nil {
			t.Fatalf("corrupt ledger accepted: %s", corrupt)
		}
	}
}
