# Claude agent-text fixtures

`cases.json` replays the documented `Agent` dispatch input. `handback-cases.json`
replays positive, negative, missing, and oversized forms of the documented Claude Code
2.1.271+ `PreToolUse` payload for `SubagentHandback`; the report is
`tool_input.message`, while common subagent tool fields supply `session_id` and
`agent_id`.

Source: [Claude Code hooks reference](https://code.claude.com/docs/en/hooks#subagentstop).
These are contract fixtures, not a claim that a live session loaded the tracked hook.
Capability activation therefore remains `unverified`.
