// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

// Ansible playbooks and role task files.
//
// The scanner reads a YAML file that is a playbook (a list holding a play with hosts: or an
// import_playbook:) or a role's task or handler file (roles/<role>/tasks/ or handlers/, a list
// of tasks). Any other YAML file is not Ansible and stays unscanned. The tasks of a play's
// tasks, pre_tasks, post_tasks and handlers, and the tasks nested in a block, rescue or always
// section, are read with an explicit stack. It decides:
//
//   - HISS-07 ignore_errors: true or failed_when: false on a task or block that does not
//     register its result, which discards the failure, and a shell task that pipes without set
//     -o pipefail, whose status is the last command's, so a failure on the left of the pipe is
//     discarded (ansible-lint ignore-errors and risky-shell-pipe).
//   - HISS-08 a command, shell, raw or script task with no changed_when, creates or removes: it
//     reports a change on every run, whatever the host's state, so the play's outcome is not
//     determined by the state it converges (ansible-lint no-changed-when).
//
// A task file outside a role's tasks/ or handlers/ directory, included by path from a playbook,
// is not recognised from its contents alone and stays unscanned.

const (
	// maxAnsibleNesting bounds how deep blocks nest (HISS-02).
	maxAnsibleNesting = 16
	// maxAnsibleTasks bounds the tasks one file is read for (HISS-02).
	maxAnsibleTasks = 10000
)

// ansibleTaskLists are the play keys that hold tasks.
var ansibleTaskLists = []string{"pre_tasks", "tasks", "post_tasks", "handlers"}

// ansibleBlockLists are the block keys that hold nested tasks.
var ansibleBlockLists = []string{"block", "rescue", "always"}

// ansibleCommandModules are the modules that run a command Ansible cannot see the effect of.
var ansibleCommandModules = map[string]bool{"command": true, "shell": true, "raw": true, "script": true}

// ansibleJinja is a Jinja expression, statement or comment, whose filters use the pipe.
var ansibleJinja = regexp.MustCompile(`\{\{.*?\}\}|\{%.*?%\}|\{#.*?#\}`)

// ansibleQuoted is a quoted string inside a shell command, whose pipes are text.
var ansibleQuoted = regexp.MustCompile(`'[^']*'|"(?:[^"\\]|\\.)*"`)

// ansibleLanguage reads Ansible playbooks and role task files.
type ansibleLanguage struct{}

// handles claims no extension: a YAML file is Ansible only when its contents say so.
func (ansibleLanguage) handles(string) bool { return false }

func (ansibleLanguage) name() string { return "ansible" }

func (ansibleLanguage) candidate(_, ext string) bool { return ext == ".yml" || ext == ".yaml" }

func (ansibleLanguage) claims(src sourceFile) bool {
	_, ok := ansibleTaskRoots(src)
	return ok
}

func (ansibleLanguage) scan(src sourceFile, rep *ScanReport, _ ScanOptions) bool {
	roots, ok := ansibleTaskRoots(src)
	if !ok {
		return false
	}
	file := &ScanReport{}
	for _, task := range ansibleTasks(roots) {
		checkAnsibleTask(task, src.rel, file)
	}
	recordInLineOrder(rep, file)
	return true
}

// ansibleTaskRoots returns the task lists a playbook or a role task file holds, and whether the
// file is Ansible at all.
func ansibleTaskRoots(src sourceFile) ([]*yaml.Node, bool) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src.data, &doc); err != nil || doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, false
	}
	root := doc.Content[0]
	if root.Kind != yaml.SequenceNode || len(root.Content) == 0 {
		return nil, false
	}
	if lists, ok := playbookTaskLists(root); ok {
		return lists, true
	}
	// The walk reports paths with the platform separator; the role layout is matched on slashes
	// so a Windows checkout reads the same files (HISS-21).
	if isRoleTaskPath(filepath.ToSlash(src.rel)) && allMappings(root) {
		return []*yaml.Node{root}, true
	}
	return nil, false
}

// playbookTaskLists returns the task lists of every play when root is a playbook: a list in
// which some entry is a play (hosts:) or a playbook import and every entry is a mapping.
func playbookTaskLists(root *yaml.Node) ([]*yaml.Node, bool) {
	if !allMappings(root) {
		return nil, false
	}
	playbook := false
	var lists []*yaml.Node
	for _, play := range root.Content {
		if util.YAMLMappingValue(play, "hosts") != nil || util.YAMLMappingValue(play, "import_playbook") != nil ||
			util.YAMLMappingValue(play, "ansible.builtin.import_playbook") != nil {
			playbook = true
		}
		for _, key := range ansibleTaskLists {
			if list := util.YAMLMappingValue(play, key); list != nil && list.Kind == yaml.SequenceNode {
				lists = append(lists, list)
			}
		}
	}
	return lists, playbook
}

// isRoleTaskPath reports a file under roles/<role>/tasks/ or roles/<role>/handlers/.
func isRoleTaskPath(rel string) bool {
	segments := strings.Split(rel, "/")
	for i := 0; i+3 < len(segments) && i < maxPathSegments; i++ {
		if segments[i] == "roles" && (segments[i+2] == "tasks" || segments[i+2] == "handlers") {
			return true
		}
	}
	return false
}

// allMappings reports whether every entry of a sequence is a mapping.
func allMappings(seq *yaml.Node) bool {
	for _, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			return false
		}
	}
	return true
}

// ansibleTasks flattens the task lists into every task and block they hold, following blocks
// with an explicit stack.
func ansibleTasks(roots []*yaml.Node) []*yaml.Node {
	stack := make([]ansibleFrame, 0, len(roots))
	for i := len(roots) - 1; i >= 0; i-- {
		stack = append(stack, ansibleFrame{list: roots[i]})
	}
	var tasks []*yaml.Node
	for len(stack) > 0 && len(tasks) < maxAnsibleTasks {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, task := range top.list.Content {
			if task.Kind != yaml.MappingNode || len(tasks) >= maxAnsibleTasks {
				continue
			}
			tasks = append(tasks, task)
			stack = append(stack, nestedTaskLists(task, top.depth)...)
		}
	}
	return tasks
}

// ansibleFrame is one task list waiting to be read and how deep in blocks it sits.
type ansibleFrame struct {
	list  *yaml.Node
	depth int
}

// nestedTaskLists returns the block, rescue and always lists of a task at depth, none once the
// nesting bound is reached.
func nestedTaskLists(task *yaml.Node, depth int) []ansibleFrame {
	if depth >= maxAnsibleNesting {
		return nil
	}
	var frames []ansibleFrame
	for _, key := range ansibleBlockLists {
		if nested := util.YAMLMappingValue(task, key); nested != nil && nested.Kind == yaml.SequenceNode {
			frames = append(frames, ansibleFrame{list: nested, depth: depth + 1})
		}
	}
	return frames
}

// checkAnsibleTask applies the rules to one task or block.
func checkAnsibleTask(task *yaml.Node, rel string, rep *ScanReport) {
	checkAnsibleFailure(task, rel, rep)
	checkAnsibleCommand(task, rel, rep)
}

// checkAnsibleFailure reports a task or block that discards its failure without registering the
// result for a later check.
func checkAnsibleFailure(task *yaml.Node, rel string, rep *ScanReport) {
	if util.YAMLMappingValue(task, "register") != nil {
		return
	}
	if v := util.YAMLMappingValue(task, "ignore_errors"); v != nil && ansibleTruth(v) == "true" {
		recordViolation(rep, "HISS-07", rel, v.Line, "", "ignore_errors: true discards the task's failure; register the result and handle it, or state the failure condition in failed_when")
	}
	if v := util.YAMLMappingValue(task, "failed_when"); v != nil && ansibleTruth(v) == "false" {
		recordViolation(rep, "HISS-07", rel, v.Line, "", "failed_when: false discards the task's failure; state the condition that is a failure")
	}
}

// checkAnsibleCommand reports a command module task whose change is not determined by state,
// and a shell pipe that loses a failure.
func checkAnsibleCommand(task *yaml.Node, rel string, rep *ScanReport) {
	key, args := ansibleModule(task)
	if key == nil {
		return
	}
	module := ansibleModuleName(key.Value)
	if module == "shell" && pipesWithoutPipefail(task, args) {
		recordViolation(rep, "HISS-07", rel, key.Line, "", "shell task pipes without set -o pipefail, so a failure on the left of the pipe is discarded")
	}
	if util.YAMLMappingValue(task, "changed_when") == nil && !createsOrRemoves(task, args) {
		recordViolation(rep, "HISS-08", rel, key.Line, "", module+" task reports a change on every run; set changed_when, creates or removes so the result follows the host's state")
	}
}

// ansibleTruth reads a scalar the way Ansible's boolean conversion does: "true" for true, yes,
// on and 1, "false" for false, no, off and 0, and "" for anything else, a template included.
func ansibleTruth(v *yaml.Node) string {
	if v.Kind != yaml.ScalarNode {
		return ""
	}
	switch strings.ToLower(v.Value) {
	case "true", "yes", "on", "1":
		return "true"
	case "false", "no", "off", "0":
		return "false"
	}
	return ""
}

// ansibleModule returns the key and value of a task's command module, or nil when the task runs
// another module.
func ansibleModule(task *yaml.Node) (*yaml.Node, *yaml.Node) {
	for i := 0; i+1 < len(task.Content); i += 2 {
		if ansibleCommandModules[ansibleModuleName(task.Content[i].Value)] {
			return task.Content[i], task.Content[i+1]
		}
	}
	return nil, nil
}

// ansibleModuleName strips the builtin collection prefixes from a module key.
func ansibleModuleName(key string) string {
	for _, prefix := range []string{"ansible.builtin.", "ansible.legacy."} {
		if strings.HasPrefix(key, prefix) {
			return key[len(prefix):]
		}
	}
	return key
}

// moduleArgument returns a named argument of a command module: from the module's own mapping,
// from the task's args mapping, or "" when it has none.
func moduleArgument(task, args *yaml.Node, name string) string {
	if v := util.YAMLMappingValue(args, name); v != nil {
		return v.Value
	}
	if v := util.YAMLMappingValue(util.YAMLMappingValue(task, "args"), name); v != nil {
		return v.Value
	}
	return ""
}

// createsOrRemoves reports a creates or removes argument, as a mapping key or as a key=value
// word of the free-form command.
func createsOrRemoves(task, args *yaml.Node) bool {
	if moduleArgument(task, args, "creates") != "" || moduleArgument(task, args, "removes") != "" {
		return true
	}
	if args.Kind != yaml.ScalarNode {
		return false
	}
	words := " " + args.Value
	return strings.Contains(words, " creates=") || strings.Contains(words, " removes=")
}

// pipesWithoutPipefail reports a shell command holding a pipe outside Jinja and quotes and no
// pipefail, unless PowerShell runs it. The command is the module's free-form value or, when that
// is a mapping, null or empty, its cmd argument, as Ansible reads it.
func pipesWithoutPipefail(task, args *yaml.Node) bool {
	if strings.Contains(moduleArgument(task, args, "executable"), "pwsh") {
		return false
	}
	command := ""
	if args.Kind == yaml.ScalarNode && args.ShortTag() != "!!null" {
		command = args.Value
	}
	if command == "" {
		command = moduleArgument(task, args, "cmd")
	}
	if strings.Contains(command, "pipefail") {
		return false
	}
	command = ansibleQuoted.ReplaceAllString(ansibleJinja.ReplaceAllString(command, ""), "")
	command = strings.ReplaceAll(command, "||", "")
	return strings.Contains(command, "|")
}
