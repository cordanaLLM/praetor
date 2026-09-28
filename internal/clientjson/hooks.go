package clientjson

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	// MaxHooks bounds the handlers one plan registers.
	MaxHooks = 16
	// MaxHookGroups bounds the matcher groups one native event may hold; a longer list is
	// refused rather than scanned in part, since a registration past the cut would be missed.
	MaxHookGroups = 256
)

// Hook is one handler a governed repository registers under a native hook event, in the
// {"hooks": {"<Event>": [{"matcher": ..., "hooks": [handler]}]}} shape Claude Code, Codex and
// Gemini CLI share.
type Hook struct {
	Event   string // the client's event key, such as PreToolUse or BeforeTool
	Matcher string // the tool matcher; empty where the event takes none
	Command string
	Timeout int64 // in the unit of the client's file; zero leaves the field out
	// ExactLiteral reports that the client reads a matcher made only of letters, digits and _
	// as the whole tool name, so NAME and ^NAME$ select the same tool (sameSelection). False
	// keeps the two apart, for a client that tests every matcher as an unanchored regular
	// expression, where NAME also selects every tool whose name contains NAME.
	ExactLiteral bool
	// ServedBy reports whether an existing handler's command line already runs this hook's
	// evaluator. Nil accepts only a handler whose command line is Command.
	ServedBy func(commandLine string) bool
}

// HookPlan is one in-memory merge of hooks into a client's hook file. Content is the file to
// write; it equals the input when Changed is false.
type HookPlan struct {
	Added   []string // the commands this plan registers
	Present []string // the existing command lines that already serve a hook
	Changed bool
	Content []byte
}

type hookHandler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int64  `json:"timeout,omitzero"`
}

type hookGroup struct {
	Matcher string           `json:"matcher,omitempty"`
	Hooks   []jsontext.Value `json:"hooks"`
}

// PlanHooks merges hooks into existing, the bytes of a client's hook file (empty when there is
// none). A hook already served by a handler in a group of its event whose matcher covers the
// hook's (coversMatcher) is reported as present and left alone. A missing file, hooks object,
// event list or matcher group is created; a handler is appended to the first group whose
// matcher selects the same tools as the hook's (sameSelection). Every member the plan does not
// touch keeps its place and its number literals; the document is re-indented and its string
// escapes normalised. A section of the wrong type fails the plan.
func PlanHooks(ctx context.Context, existing []byte, hooks []Hook) (*HookPlan, error) {
	if len(hooks) > MaxHooks {
		return nil, fmt.Errorf("hook plan exceeds %d hooks", MaxHooks)
	}
	root, err := decodeDocument(ctx, existing)
	if err != nil {
		return nil, err
	}
	events, err := objectMember(root, "hooks")
	if err != nil {
		return nil, err
	}
	plan := &HookPlan{Added: []string{}, Present: []string{}, Content: bytes.Clone(existing)}
	for _, hook := range hooks {
		if err := contextErr(ctx); err != nil {
			return nil, err
		}
		if events, err = planHook(plan, events, hook); err != nil {
			return nil, err
		}
	}
	if len(plan.Added) == 0 {
		return plan, nil
	}
	return plan, encodePlan(plan, root, events)
}

// planHook registers hook in events unless a handler already serves it, and records which.
func planHook(plan *HookPlan, events Object, hook Hook) (Object, error) {
	groups, err := eventGroups(events, hook.Event)
	if err != nil {
		return nil, err
	}
	if line, ok := servingHandler(groups, hook); ok {
		plan.Present = append(plan.Present, line)
		return events, nil
	}
	groups, err = appendHandler(groups, hook)
	if err != nil {
		return nil, err
	}
	list, err := json.Marshal(groups)
	if err != nil {
		return nil, errEncode
	}
	plan.Added = append(plan.Added, hook.Command)
	return events.With(hook.Event, list), nil
}

func encodePlan(plan *HookPlan, root, events Object) error {
	section, err := events.Encode()
	if err != nil {
		return err
	}
	content, err := root.With("hooks", section).Encode()
	if err != nil {
		return err
	}
	if len(content) > MaxBytes {
		return fmt.Errorf("hook file candidate exceeds %d bytes", MaxBytes)
	}
	plan.Content, plan.Changed = content, true
	return nil
}

// decodeDocument validates a whole hook file and returns its root object; no bytes are an
// empty document, the state of a file that does not exist yet.
func decodeDocument(ctx context.Context, existing []byte) (Object, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if len(existing) == 0 {
		return Object{}, nil
	}
	if err := Validate(ctx, existing); err != nil {
		return nil, err
	}
	return DecodeObject(existing)
}

// objectMember returns the object held by the member called name, empty when it is absent.
func objectMember(parent Object, name string) (Object, error) {
	raw, ok := parent.Get(name)
	if !ok {
		return Object{}, nil
	}
	object, err := DecodeObject(raw)
	if err != nil {
		return nil, fmt.Errorf("%q must be an object", name)
	}
	return object, nil
}

// eventGroups returns the matcher groups of one event, none when the event is absent.
func eventGroups(events Object, event string) ([]jsontext.Value, error) {
	raw, ok := events.Get(event)
	if !ok {
		return nil, nil
	}
	var groups []jsontext.Value
	if err := json.Unmarshal(raw, &groups); err != nil || groups == nil {
		return nil, fmt.Errorf("hooks.%s must be an array", event)
	}
	if len(groups) > MaxHookGroups {
		return nil, fmt.Errorf("hooks.%s exceeds %d matcher groups", event, MaxHookGroups)
	}
	return groups, nil
}

// servingHandler returns the command line of the first handler that already serves hook, in a
// group of the event whose matcher covers hook's. A handler under a narrower or unrelated
// matcher, such as an evaluator registered only for ^(Edit|Write)$, does not run for the tools
// hook guards and serves nothing. An entry that is not an object is not a handler of any shape
// the clients run, so it serves nothing either.
func servingHandler(groups []jsontext.Value, hook Hook) (string, bool) {
	for _, raw := range groups {
		group, err := DecodeObject(raw)
		if err != nil || !coversMatcher(group, hook) {
			continue
		}
		for _, handler := range groupHandlers(group) {
			line := commandLine(handler)
			if line == "" {
				continue
			}
			if line == hook.Command || (hook.ServedBy != nil && hook.ServedBy(line)) {
				return line, true
			}
		}
	}
	return "", false
}

// matchAllMatchers select every tool of an event: an absent or empty matcher, the "*" wildcard,
// and the regular expression matching any tool name.
var matchAllMatchers = [...]string{"", "*", ".*"}

// coversMatcher reports whether group runs for every tool hook selects: its matcher selects the
// same tools as hook's (sameSelection) or selects every tool. A matcher that is present but not
// a string covers nothing.
func coversMatcher(group Object, hook Hook) bool {
	raw, ok := group.Get("matcher")
	if !ok {
		return true
	}
	var matcher string
	if err := json.Unmarshal(raw, &matcher); err != nil {
		return false
	}
	return sameSelection(matcher, hook.Matcher, hook.ExactLiteral) || slices.Contains(matchAllMatchers[:], matcher)
}

// anchoredLiteral matches ^NAME$ where NAME is a nonempty run of ASCII letters, digits and _,
// the regular expression that selects exactly the tool a client with exact literal matchers
// (Hook.ExactLiteral) selects for the bare NAME.
var anchoredLiteral = regexp.MustCompile(`^\^([A-Za-z0-9_]+)\$$`)

// sameSelection reports whether matchers have and want select the same tools: they are equal,
// or, when exactLiteral holds, one is ^NAME$ of the other with NAME literal (anchoredLiteral).
// Claude Code and Codex read a literal NAME as that exact tool name; Gemini CLI tests every
// matcher as an unanchored regular expression, so there NAME is broader than ^NAME$. A
// metacharacter inside NAME, as in Ba.h, keeps the two apart for every client.
func sameSelection(have, want string, exactLiteral bool) bool {
	switch {
	case have == want:
		return true
	case !exactLiteral:
		return false
	default:
		return anchors(have, want) || anchors(want, have)
	}
}

// anchors reports whether anchored is ^name$ with name literal (anchoredLiteral).
func anchors(anchored, name string) bool {
	match := anchoredLiteral.FindStringSubmatch(anchored)
	return match != nil && match[1] == name
}

// groupHandlers returns the handler objects of one matcher group.
func groupHandlers(group Object) []Object {
	list, ok := group.Get("hooks")
	if !ok {
		return nil
	}
	var entries []jsontext.Value
	if err := json.Unmarshal(list, &entries); err != nil {
		return nil
	}
	handlers := make([]Object, 0, len(entries))
	for _, entry := range entries {
		if handler, err := DecodeObject(entry); err == nil {
			handlers = append(handlers, handler)
		}
	}
	return handlers
}

// commandLine joins a handler's command and its args, as the client runs them.
func commandLine(handler Object) string {
	command := stringMember(handler, "command")
	if command == "" {
		return ""
	}
	parts := []string{command}
	if args, ok := handler.Get("args"); ok {
		var values []string
		if err := json.Unmarshal(args, &values); err == nil {
			parts = append(parts, values...)
		}
	}
	return strings.Join(parts, " ")
}

// appendHandler adds hook's handler to the first group whose matcher selects the same tools as
// hook.Matcher (sameSelection) and whose hooks member is a list, or appends a new group holding
// only that handler.
func appendHandler(groups []jsontext.Value, hook Hook) ([]jsontext.Value, error) {
	handler, err := json.Marshal(hookHandler{Type: "command", Command: hook.Command, Timeout: hook.Timeout})
	if err != nil {
		return nil, errEncode
	}
	for i, raw := range groups {
		group, err := DecodeObject(raw)
		if err != nil || !sameSelection(stringMember(group, "matcher"), hook.Matcher, hook.ExactLiteral) {
			continue
		}
		extended, ok := appendToList(group, handler)
		if !ok {
			continue
		}
		if groups[i], err = extended.Encode(); err != nil {
			return nil, err
		}
		return groups, nil
	}
	fresh, err := json.Marshal(hookGroup{Matcher: hook.Matcher, Hooks: []jsontext.Value{handler}})
	if err != nil {
		return nil, errEncode
	}
	return append(groups, fresh), nil
}

// appendToList returns group with handler appended to its hooks list, or false when group has
// no hooks list to extend.
func appendToList(group Object, handler jsontext.Value) (Object, bool) {
	raw, ok := group.Get("hooks")
	if !ok {
		return nil, false
	}
	var entries []jsontext.Value
	if err := json.Unmarshal(raw, &entries); err != nil || entries == nil {
		return nil, false
	}
	list, err := json.Marshal(append(entries, handler))
	if err != nil {
		return nil, false
	}
	return group.With("hooks", list), true
}

// stringMember returns the string value of the member called name, or "" when it is absent or
// not a string.
func stringMember(object Object, name string) string {
	raw, _ := object.Get(name)
	return StringValue(raw)
}
