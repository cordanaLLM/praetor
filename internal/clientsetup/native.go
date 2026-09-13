package clientsetup

import (
	"bytes"
	"fmt"
	"strconv"
)

func nativePlan(registry Registry, client Client) ([][]string, []byte) {
	commands := make([][]string, 0, len(registry.Servers))
	var content bytes.Buffer
	for _, server := range registry.Servers {
		prefix := []string{"agy", "mcp", "add", "--type", "stdio", server.Name, "--", server.Command}
		if client == Codex {
			prefix = []string{"codex", "mcp", "add", server.Name, "--", server.Command}
			fmt.Fprintf(&content, "[mcp_servers.%s]\ncommand = %s\nargs = [", server.Name, tomlString(server.Command))
			for i, arg := range server.Args {
				if i > 0 {
					content.WriteString(", ")
				}
				content.WriteString(tomlString(arg))
			}
			content.WriteString("]\n\n")
		}
		commands = append(commands, append(prefix, server.Args...))
	}
	return commands, content.Bytes()
}

// Registry validation forbids control characters; JSON string escaping is also
// valid for these TOML basic strings, including quotes and literal backslashes.
func tomlString(value string) string { return strconv.Quote(value) }
