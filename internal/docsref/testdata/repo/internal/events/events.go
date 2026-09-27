// Package events holds the fixture's hook clients and events, reached from runHook only
// through a qualified call and a method call.
package events

// PreTool is the one event the fixture hook serves.
const PreTool = "pre-tool"

// templatePath is a file the fixture engine writes into an adopted repository, so a guide
// that names it describes the engine although the fixture does not carry it.
const templatePath = "docs/adr/0000-template.md"

// Registry answers event lookups through a method, the call shape the closure resolves by
// method name.
type Registry struct{}

// Known reports whether client and event name a served pair.
func Known(client, event string) bool {
	return client == "claude" && event != "" && templatePath != ""
}

// Lookup reports whether event is served.
func (Registry) Lookup(event string) bool {
	return event == PreTool
}
