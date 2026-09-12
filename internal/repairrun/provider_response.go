package repairrun

import (
	"encoding/json"
	"errors"
	"strings"
)

func providerDecodeResponse(data []byte, outputLimit int) (*Proposal, error) {
	if err := providerValidateJSON(data); err != nil {
		return nil, err
	}
	response, err := providerObject(data)
	if err != nil {
		return nil, err
	}
	if providerString(response["status"]) != "completed" || !providerNull(response["error"]) || !providerNull(response["incomplete_details"]) {
		return nil, errors.New("repair provider response is not complete and successful")
	}
	id, model := providerString(response["id"]), providerString(response["model"])
	if !providerSafeText(id, 1024) || !providerSafeText(model, 256) {
		return nil, errors.New("repair provider response identity is invalid")
	}
	text, err := providerOutput(response["output"])
	if err != nil {
		return nil, err
	}
	proposal, err := providerDecodeProposal([]byte(text))
	if err != nil {
		return nil, err
	}
	proposal.Usage, err = providerUsage(response["usage"], outputLimit)
	if err != nil {
		return nil, err
	}
	proposal.ResponseID, proposal.ActualModel = id, model
	return proposal, nil
}

func providerOutput(raw json.RawMessage) (string, error) {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil || len(items) == 0 || len(items) > 16 {
		return "", errors.New("repair provider output items are invalid")
	}
	text := ""
	for i := 0; i < len(items) && i < 16; i++ {
		fragment, err := providerOutputItem(items[i])
		if err != nil {
			return "", err
		}
		if fragment == "" {
			continue
		}
		if text != "" {
			return "", errors.New("repair provider returned multiple messages")
		}
		text = fragment
	}
	if text == "" {
		return "", errors.New("repair provider returned no proposal message")
	}
	return text, nil
}

func providerOutputItem(raw json.RawMessage) (string, error) {
	item, err := providerObject(raw)
	if err != nil {
		return "", err
	}
	switch providerString(item["type"]) {
	case "reasoning":
		return "", nil
	case "message":
		return providerMessage(item)
	default:
		return "", errors.New("repair provider returned a tool call or unsupported output item")
	}
}

func providerMessage(item map[string]json.RawMessage) (string, error) {
	if providerString(item["role"]) != "assistant" || providerString(item["status"]) != "completed" {
		return "", errors.New("repair provider message is not complete")
	}
	var content []json.RawMessage
	if json.Unmarshal(item["content"], &content) != nil || len(content) != 1 {
		return "", errors.New("repair provider message content is invalid")
	}
	part, err := providerObject(content[0])
	if err != nil {
		return "", err
	}
	if providerString(part["type"]) != "output_text" {
		return "", errors.New("repair provider refused or returned unsupported content")
	}
	text := providerString(part["text"])
	if len(text) == 0 || len(text) > providerResponseLimit {
		return "", errors.New("repair provider message text is invalid")
	}
	return text, nil
}

func providerDecodeProposal(data []byte) (*Proposal, error) {
	if err := providerValidateJSON(data); err != nil {
		return nil, err
	}
	object, err := providerObject(data)
	if err != nil || !providerExactFields(object, "summary", "edits") {
		return nil, errors.New("repair proposal fields must be exact and complete")
	}
	summary := providerString(object["summary"])
	if len(summary) > 4096 || strings.TrimSpace(summary) == "" {
		return nil, errors.New("repair proposal summary is invalid")
	}
	edits, err := providerEdits(object["edits"])
	if err != nil {
		return nil, err
	}
	return &Proposal{Summary: summary, Edits: edits}, nil
}

func providerEdits(raw json.RawMessage) ([]Edit, error) {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil || items == nil || len(items) > 8 {
		return nil, errors.New("repair proposal edits must be an explicit bounded array")
	}
	edits := make([]Edit, 0, len(items))
	for i := 0; i < len(items) && i < 8; i++ {
		edit, err := providerEdit(items[i])
		if err != nil {
			return nil, err
		}
		edits = append(edits, edit)
	}
	return edits, nil
}

func providerEdit(data []byte) (Edit, error) {
	object, err := providerObject(data)
	if err != nil || !providerExactFields(object, "path", "original_sha256", "content") {
		return Edit{}, errors.New("repair edit fields must be exact and complete")
	}
	var edit Edit
	if json.Unmarshal(data, &edit) != nil || !providerSafeText(edit.Path, 4096) || !providerDigest(edit.OriginalSHA256) {
		return Edit{}, errors.New("repair edit field types or identity are invalid")
	}
	if providerNull(object["content"]) || len(edit.Content) > providerPromptLimit {
		return Edit{}, errors.New("repair edit content must be a bounded string")
	}
	return edit, nil
}

func providerUsage(raw json.RawMessage, outputLimit int) (Usage, error) {
	object, err := providerObject(raw)
	if err != nil {
		return Usage{}, err
	}
	input, validInput := providerCount(object["input_tokens"])
	output, validOutput := providerCount(object["output_tokens"])
	if !validInput || !validOutput || output > int64(outputLimit) {
		return Usage{}, errors.New("repair provider token usage is invalid or exceeds requested output bound")
	}
	if rawTotal, ok := object["total_tokens"]; ok {
		total, valid := providerCount(rawTotal)
		if !valid || total != input+output {
			return Usage{}, errors.New("repair provider total usage is inconsistent")
		}
	}
	return Usage{InputTokens: input, OutputTokens: output}, nil
}

func providerCount(raw json.RawMessage) (int64, bool) {
	var count int64
	valid := len(raw) > 0 && !providerNull(raw) && json.Unmarshal(raw, &count) == nil
	return count, valid && count >= 0 && count <= 1<<40
}

func providerObject(raw []byte) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, errors.New("repair provider JSON object is invalid")
	}
	return object, nil
}

func providerString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

func providerNull(raw json.RawMessage) bool { return strings.TrimSpace(string(raw)) == "null" }

func providerExactFields(object map[string]json.RawMessage, keys ...string) bool {
	if len(object) != len(keys) {
		return false
	}
	for i := 0; i < len(keys) && i < 16; i++ {
		if _, ok := object[keys[i]]; !ok {
			return false
		}
	}
	return true
}
