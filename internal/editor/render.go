package editor

import (
	"html"
	"strings"
	"unicode"
)

// planCommands returns the resolved repository commands under the loop bound every renderer
// shares. Renderers call it instead of reading Plan.Commands directly so one bound, and one
// answer to "which commands may this repository be told to run", covers every editor.
func planCommands(plan Plan) []Command {
	if len(plan.Commands) > maxLoopBound {
		return plan.Commands[:maxLoopBound]
	}
	return plan.Commands
}

// commandShellLine renders a command for the editor formats that take one shell string.
func commandShellLine(command Command) string {
	return strings.TrimSpace(command.Program + " " + strings.Join(command.Args, " "))
}

// commandArgs returns a never-nil argument list, so a command with no arguments encodes as
// [] rather than as JSON null.
func commandArgs(command Command) []string {
	if command.Args == nil {
		return []string{}
	}
	return command.Args
}

// xmlAttr escapes a value for an XML attribute in the JetBrains projection. Command labels
// and programs reach this from repository configuration, so they are escaped rather than
// trusted to be attribute-safe. html.EscapeString covers the five XML-special characters with
// entity or numeric references that XML accepts unchanged.
func xmlAttr(value string) string {
	return html.EscapeString(value)
}

// luaEscaper escapes the characters a Lua double-quoted string literal cannot carry raw. It is a
// fixed replacement table, so the whole value is escaped without a per-byte loop or a length cap
// that would silently truncate a long command.
var luaEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)

// luaString renders a Lua double-quoted string literal for the Neovim projection.
func luaString(value string) string {
	return `"` + luaEscaper.Replace(value) + `"`
}

// neovimCommandName derives a :CamelCase user-command name from a command label. Neovim accepts
// only ASCII letters and digits after a leading capital, so every other rune separates words. A
// label that carries none, or starts with a digit, falls back to a positional name, so every
// resolved command is still reachable and two commands never collide on an empty name.
func neovimCommandName(label string, index int) string {
	var name strings.Builder
	upper := true
	for _, char := range label {
		if char < unicode.MaxASCII && (unicode.IsLetter(char) || unicode.IsDigit(char)) {
			name.WriteRune(mapCase(char, upper))
			upper = false
		} else {
			upper = true
		}
		if name.Len() >= maxCommandNameRunes {
			break
		}
	}
	if name.Len() == 0 || unicode.IsDigit(rune(name.String()[0])) {
		return "StandardsCommand" + string(rune('A'+index%26))
	}
	return name.String()
}

func mapCase(char rune, upper bool) rune {
	if upper {
		return unicode.ToUpper(char)
	}
	return char
}

// maxCommandNameRunes bounds the derived user-command name (HISS-02).
const maxCommandNameRunes = 64
