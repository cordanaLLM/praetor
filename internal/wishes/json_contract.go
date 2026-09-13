package wishes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/notebook"
)

// checkObject prevents encoding/json's case-insensitive field aliases and null
// defaults from silently changing a request. All listed fields are required.
func checkObject(raw []byte, fields string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := notebook.Decode(raw, &object); err != nil {
		return nil, err
	}
	keys := strings.Fields(fields)
	if object == nil || len(object) != len(keys) {
		return nil, fmt.Errorf("expected exactly these fields: %s", fields)
	}
	for _, key := range keys {
		value, ok := object[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("required non-null field %q missing", key)
		}
	}
	return object, nil
}

func checkRequestJSON(raw []byte) error {
	var envelope map[string]json.RawMessage
	if err := notebook.Decode(raw, &envelope); err != nil {
		return err
	}
	var action string
	if err := json.Unmarshal(envelope["action"], &action); err != nil {
		return fmt.Errorf("request requires an action string: %w", err)
	}
	fields := requestFields(action)
	if action == "init" && len(envelope) == 2 {
		fields += " policy"
	}
	object, err := checkObject(raw, fields)
	if err != nil {
		return err
	}
	return checkRequestChildren(object)
}

func requestFields(action string) string {
	fields := map[string]string{
		"init": "action", "add-wish": "action expected_revision wish",
		"set-status": "action expected_revision wish_id status",
		"open-poll":  "action expected_revision poll",
		"vote":       "action expected_revision poll_id actor_id choice_ids",
		"withdraw":   "action expected_revision poll_id actor_id",
		"close-poll": "action expected_revision poll_id",
	}
	return fields[action]
}

func checkRequestChildren(object map[string]json.RawMessage) error {
	for _, child := range []struct{ key, fields string }{
		{"policy", "max_wishes max_polls max_voters allow_vote_changes allow_withdrawal"},
		{"wish", "id kind target title description"},
	} {
		if raw, ok := object[child.key]; ok {
			if _, err := checkObject(raw, child.fields); err != nil {
				return fmt.Errorf("%s: %w", child.key, err)
			}
		}
	}
	if raw, ok := object["poll"]; ok {
		poll, err := checkObject(raw, "id wish_id question choices")
		if err != nil {
			return err
		}
		return checkObjectArray(poll["choices"], "id label")
	}
	return nil
}

func checkObjectArray(raw []byte, fields string) error {
	var items []json.RawMessage
	if err := notebook.Decode(raw, &items); err != nil {
		return err
	}
	if items == nil {
		return fmt.Errorf("array must not be null")
	}
	for _, item := range items {
		_, err := checkObject(item, fields)
		if err != nil {
			return err
		}
	}
	return nil
}

func checkLedgerJSON(raw []byte) error {
	object, err := checkObject(raw, "schema_version revision policy wishes polls current_ballots")
	if err != nil {
		return err
	}
	if _, err := checkObject(object["policy"], "max_wishes max_polls max_voters allow_vote_changes allow_withdrawal"); err != nil {
		return err
	}
	if err := checkObjectArray(object["wishes"], "id kind target title description status"); err != nil {
		return err
	}
	if err := checkPollArray(object["polls"]); err != nil {
		return err
	}
	var ballots map[string]json.RawMessage
	if err := notebook.Decode(object["current_ballots"], &ballots); err != nil {
		return err
	}
	for _, ballot := range ballots {
		if _, err := checkObject(ballot, "poll_id choice_id actor_id"); err != nil {
			return err
		}
	}
	return nil
}

func checkPollJSON(poll map[string]json.RawMessage) error {
	if err := checkObjectArray(poll["choices"], "id label"); err != nil {
		return err
	}
	return checkObjectArray(poll["tallies"], "choice_id votes")
}

func checkPollArray(raw []byte) error {
	var polls []json.RawMessage
	if err := notebook.Decode(raw, &polls); err != nil {
		return err
	}
	if polls == nil {
		return fmt.Errorf("polls must not be null")
	}
	for _, rawPoll := range polls {
		poll, err := checkObject(rawPoll, "id wish_id question choices closed tallies")
		if err != nil {
			return err
		}
		if err := checkPollJSON(poll); err != nil {
			return err
		}
	}
	return nil
}
