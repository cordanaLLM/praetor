package agenthook

import (
	"strings"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// commandDirectories reads leading assignments/wrappers, then just the actual command word.
func commandDirectories(words []string, base string) []commandTarget {
	var targets []commandTarget
	wrapperSeen := false
	for i := 0; i < len(words); i++ {
		name, value, assignment := forge.ShellAssignment(words[i])
		if assignment {
			if name == "GIT_DIR" || name == "GIT_WORK_TREE" {
				targets = append(targets, commandTarget{targetDirectory(value, base), name == "GIT_DIR"})
			}
			continue
		}
		switch strings.TrimLeft(unquoteTarget(words[i]), "(") {
		case "", "{":
			continue // A subshell or group opener ahead of the command word.
		case "env":
			wrapperSeen = true
			var dirs []commandTarget
			i, base, dirs = envDirectories(words, i+1, base)
			targets = append(targets, dirs...)
			i-- // Continue at the command word after env's options.
		case "git":
			return append(targets, gitDirectories(words[i+1:], base)...)
		case "cd", "pushd":
			return append(targets, changeDirectoryTarget(words[i+1:], base)...)
		default:
			return targets
		}
	}
	if wrapperSeen {
		return targets
	}
	return nil // Assignment-only shell statements have no command target.
}

func changeDirectoryTarget(args []string, base string) []commandTarget {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return nil
	}
	return []commandTarget{{dir: targetDirectory(args[0], base)}}
}

func gitDirectories(words []string, base string) []commandTarget {
	var targets, pending []commandTarget
	for i := 0; i < len(words); i++ {
		option, value, next := directoryOption(words, i)
		if !strings.HasPrefix(option, "-") {
			break // Arguments after the git subcommand are not global directory options.
		}
		i = next
		switch option {
		case "-C":
			if unquoteTarget(value) != "" {
				base = targetDirectory(value, base)
			}
			targets = append(targets, commandTarget{dir: base})
		case "--git-dir", "--work-tree":
			pending = append(pending, commandTarget{value, option == "--git-dir"})
		}
	}
	for i := 0; i < len(pending); i++ {
		pending[i].dir = targetDirectory(pending[i].dir, base)
		targets = append(targets, pending[i])
	}
	return targets
}

func envDirectories(words []string, start int, base string) (int, string, []commandTarget) {
	var targets []commandTarget
	i := start
	for ; i < len(words); i++ {
		option, value, next := directoryOption(words, i)
		if option == "--" {
			return i + 1, base, targets
		}
		if !strings.HasPrefix(option, "-") {
			break
		}
		i = next
		if option == "-C" || option == "--chdir" {
			base = targetDirectory(value, base)
			targets = append(targets, commandTarget{dir: base})
		}
	}
	return i, base, targets
}

// directoryOption consumes option values so a -C inside a config or unset value stays data.
func directoryOption(words []string, i int) (option, value string, next int) {
	if strings.HasPrefix(words[i], "-C") && len(words[i]) > 2 {
		return "-C", words[i][2:], i
	}
	option, value, inline := strings.Cut(words[i], "=")
	if inline {
		return option, value, i
	}
	switch option {
	case "-C", "--git-dir", "--work-tree", "--chdir", "-c", "--config-env", "--namespace", "--attr-source", "-u", "--unset", "-a", "--argv0", "-S", "--split-string":
		if i+1 < len(words) {
			return option, words[i+1], i + 1
		}
	}
	return option, "", i
}
