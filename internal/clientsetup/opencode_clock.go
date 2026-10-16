package clientsetup

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var ErrLaneHiddenHookPath = errors.New("hook path is hidden from lane sandboxes")

// RenderOpenCodeClockPlugin renders the OpenCode V1 message-transform plugin.
// OpenCode 1.18.7's plugin SDK runs experimental.chat.messages.transform before
// model requests and passes the live message array; Bun 1.4.3 spawn accepts argv
// arrays and Response stdin (https://opencode.ai/docs/plugins/,
// https://bun.sh/docs/runtime/child-process, checked 2026-10-16).
func RenderOpenCodeClockPlugin(executable string) ([]byte, error) {
	if executable == "" {
		return nil, errors.New("hook executable is empty")
	}
	if hiddenLanePath(executable) {
		return nil, fmt.Errorf("%w: %s", ErrLaneHiddenHookPath, executable)
	}
	encoded, err := json.Marshal(executable)
	if err != nil {
		return nil, fmt.Errorf("encode hook executable: %w", err)
	}
	return []byte(strings.ReplaceAll(opencodeClockTemplate, "{{EXECUTABLE}}", string(encoded))), nil
}

func hiddenLanePath(value string) bool {
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".workingdir" {
			return true
		}
	}
	return false
}

const opencodeClockTemplate = `// OpenCode 1.18.7 plugin API: https://opencode.ai/docs/plugins/ and
// https://github.com/anomalyco/opencode/blob/dev/packages/plugin/src/index.ts
// experimental.chat.messages.transform; SDK source checked 2026-10-16.

const executable = {{EXECUTABLE}};
const clockLine = /^clock: .* \(from the system clock; use this, never estimate\)$/m;

export const PraetorClock = async () => ({
  "experimental.chat.messages.transform": async (_input, output) => {
    const messages = output.messages;
    const latest = messages.at(-1);
    if (!latest) return;
    const event = latest.info.role === "user" ? "prompt" : "post-tool";
    const process = Bun.spawn({
      cmd: [executable, "hook", "opencode", event],
      stdin: new Response("{}"),
      stdout: "pipe",
      stderr: "pipe",
    });
    const [stdout, stderr, exitCode] = await Promise.all([
      process.stdout.text(), process.stderr.text(), process.exited,
    ]);
    if (exitCode !== 0) throw new Error("praetor clock hook failed (" + exitCode + "): " + stderr);
    const response = JSON.parse(stdout);
    if (typeof response.additionalContext !== "string" || response.additionalContext === "") {
      throw new Error("praetor clock hook returned no additionalContext");
    }
    // Only the newest message is changed; prior messages remain untouched.
    const textPart = latest.parts.findLast((part) => part.type === "text");
    if (textPart) {
      textPart.text = textPart.text.replace(clockLine, "").trimEnd() + "\n" + response.additionalContext;
    } else {
      latest.parts.push({
        id: crypto.randomUUID(),
        sessionID: latest.info.sessionID,
        messageID: latest.info.id,
        type: "text",
        text: response.additionalContext,
      });
    }
  },
});
`
