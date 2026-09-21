package agenthook

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func agentTrafficEvent(event Event) bool {
	return event == EventPreDispatch || event == EventDispatchReceipt || event == EventDispatchAbort ||
		event == EventPreHandback || event == EventHandbackReceipt || event == EventHandbackAbort ||
		event == EventPostReturn
}

func decodeNativeAgentTraffic(client string, event Event, object map[string]json.RawMessage) (Canonical, error) {
	canonical := Canonical{Event: event}
	if err := fillAgentCommon(&canonical, object); err != nil {
		return Canonical{}, err
	}
	switch event {
	case EventPreDispatch:
		return decodeNativeBrief(client, canonical, object)
	case EventDispatchReceipt:
		return decodeNativeReceipt(client, canonical, object)
	case EventDispatchAbort:
		return decodeNativeDispatchAbort(client, canonical, object)
	case EventPreHandback:
		return decodeClaudeHandback(client, canonical, object)
	case EventHandbackReceipt, EventHandbackAbort:
		return decodeClaudeHandback(client, canonical, object)
	case EventPostReturn:
		return decodeNativeReturn(client, canonical, object)
	default:
		return Canonical{}, fmt.Errorf("%w: %s has no agent text shape for %s", ErrUnsupported, client, event)
	}
}

func decodeNativeDispatchAbort(client string, canonical Canonical, object map[string]json.RawMessage) (Canonical, error) {
	if client != "claude" {
		return Canonical{}, fmt.Errorf("%w: %s has no correlatable dispatch abort", ErrUnsupported, client)
	}
	tool, _, err := agentToolInput(client, object)
	if err != nil {
		return Canonical{}, err
	}
	canonical.Tool = tool
	canonical.ToolUseID, err = requiredString(object, "tool_use_id")
	return canonical, err
}

func decodeClaudeHandback(client string, canonical Canonical, object map[string]json.RawMessage) (Canonical, error) {
	if client != "claude" {
		return Canonical{}, fmt.Errorf("%w: %s has no pre-delivery handback", ErrUnsupported, client)
	}
	tool, input, err := namedToolInput(object, "SubagentHandback", "claude subagent handback")
	if err != nil {
		return Canonical{}, err
	}
	canonical.Tool = tool
	if canonical.AgentID, err = requiredString(object, "agent_id"); err != nil {
		return Canonical{}, err
	}
	if canonical.ToolUseID, err = requiredString(object, "tool_use_id"); err != nil {
		return Canonical{}, err
	}
	canonical.Return, err = requiredString(input, "message")
	return canonical, err
}

func fillAgentCommon(canonical *Canonical, object map[string]json.RawMessage) error {
	var err error
	canonical.ConversationID, err = requiredString(object, "session_id")
	if err != nil {
		return err
	}
	workspace, err := optionalString(object, "cwd")
	if err != nil {
		return err
	}
	if workspace != "" {
		canonical.Workspaces = []string{workspace}
	}
	return nil
}

func decodeNativeBrief(client string, canonical Canonical, object map[string]json.RawMessage) (Canonical, error) {
	tool, input, err := agentToolInput(client, object)
	if err != nil {
		return Canonical{}, err
	}
	canonical.Tool = tool
	key := "prompt"
	if client == "codex" {
		key = "message"
	}
	brief, err := requiredString(input, key)
	if err != nil {
		return Canonical{}, fmt.Errorf("tool_input.%w", err)
	}
	canonical.Briefs = []string{brief}
	if client == "claude" {
		background, present, boolErr := optionalBool(input, "run_in_background")
		if boolErr != nil {
			return Canonical{}, fmt.Errorf("tool_input.%w", boolErr)
		}
		if present && !background {
			return Canonical{}, errors.New("tool_input.run_in_background must not be false: foreground return precedes correlation")
		}
	}
	if client != "gemini" {
		canonical.ToolUseID, err = requiredString(object, "tool_use_id")
	}
	return canonical, err
}

func decodeNativeReceipt(client string, canonical Canonical, object map[string]json.RawMessage) (Canonical, error) {
	if client != "claude" {
		return Canonical{}, fmt.Errorf("%w: %s has no correlatable dispatch receipt", ErrUnsupported, client)
	}
	tool, _, err := agentToolInput(client, object)
	if err != nil {
		return Canonical{}, err
	}
	canonical.Tool, canonical.ToolUseID = tool, ""
	if canonical.ToolUseID, err = requiredString(object, "tool_use_id"); err != nil {
		return Canonical{}, err
	}
	response, err := requiredObject(object, "tool_response")
	if err != nil {
		return Canonical{}, err
	}
	status, statusErr := requiredString(response, "status")
	if statusErr != nil || status != "async_launched" {
		return Canonical{}, errors.New("tool_response.status must be async_launched")
	}
	canonical.AgentID, err = requiredString(response, "agentId")
	return canonical, err
}

func decodeNativeReturn(client string, canonical Canonical, object map[string]json.RawMessage) (Canonical, error) {
	if client != "claude" && client != "codex" {
		return Canonical{}, fmt.Errorf("%w: %s has no return payload", ErrUnsupported, client)
	}
	var err error
	if canonical.AgentID, err = requiredString(object, "agent_id"); err != nil {
		return Canonical{}, err
	}
	if client == "claude" {
		canonical.Return, err = optionalString(object, "last_assistant_message")
	} else {
		canonical.Return, err = requiredString(object, "last_assistant_message")
	}
	if err != nil {
		return Canonical{}, err
	}
	return canonical, nil
}

func agentToolInput(client string, object map[string]json.RawMessage) (string, map[string]json.RawMessage, error) {
	expected := map[string]string{"claude": "Agent", "codex": "spawn_agent", "gemini": "invoke_agent"}[client]
	if expected == "" {
		return "", nil, fmt.Errorf("%w: %s has no subagent dispatch tool", ErrUnsupported, client)
	}
	return namedToolInput(object, expected, client+" subagent dispatch")
}

func namedToolInput(object map[string]json.RawMessage, expected, label string) (string, map[string]json.RawMessage, error) {
	tool, err := requiredString(object, "tool_name")
	if err != nil {
		return "", nil, err
	}
	if tool != expected {
		return "", nil, fmt.Errorf("tool_name %q is not a %s", boundReason(tool), label)
	}
	input, err := requiredObject(object, "tool_input")
	return tool, input, err
}

func requiredObject(object map[string]json.RawMessage, key string) (map[string]json.RawMessage, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(object[key], &value); err != nil || value == nil {
		return nil, fmt.Errorf("%s must be an object", key)
	}
	return value, nil
}

func requiredString(object map[string]json.RawMessage, key string) (string, error) {
	value, err := optionalString(object, key)
	if err != nil || strings.TrimFunc(value, isPythonSpace) == "" {
		return "", fmt.Errorf("%s must be nonempty text", key)
	}
	return value, nil
}

func optionalBool(object map[string]json.RawMessage, key string) (bool, bool, error) {
	raw, present := object[key]
	if !present || string(raw) == "null" {
		return false, false, nil
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, true, fmt.Errorf("%s must be boolean", key)
	}
	return value, true, nil
}
