// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package util

import (
	"fmt"
	"text/template/parse"
)

// templateBranch exposes the common branch fields without traversing recursively.
func templateBranch(node parse.Node) *parse.BranchNode {
	switch node := node.(type) {
	case *parse.IfNode:
		return &node.BranchNode
	case *parse.RangeNode:
		return &node.BranchNode
	case *parse.WithNode:
		return &node.BranchNode
	default:
		return nil
	}
}

// pushTemplateList pushes children in reverse order, preserving execution order when popped.
func pushTemplateList(stack []parse.Node, list *parse.ListNode) []parse.Node {
	if list == nil {
		return stack
	}
	for i := len(list.Nodes) - 1; i >= 0 && i < maxActionYAMLNodes; i-- {
		stack = append(stack, list.Nodes[i])
	}
	return stack
}

// inspectActionTemplate counts every branch and refuses unsupported Go constructs by name.
func inspectActionTemplate(root *parse.ListNode) (map[parse.Node]int, error) {
	branches := make(map[parse.Node]int)
	stack := pushTemplateList(nil, root)
	for visited := 0; len(stack) > 0 && visited < maxActionYAMLNodes; visited++ {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if branch := templateBranch(node); branch != nil {
			if len(branches) >= maxActionTemplateBranches {
				return nil, fmt.Errorf("unsupported uses template: more than %d branch nodes", maxActionTemplateBranches)
			}
			branches[node] = len(branches)
			stack = pushTemplateList(stack, branch.ElseList)
			stack = pushTemplateList(stack, branch.List)
			continue
		}
		if err := supportedTemplateLeaf(node); err != nil {
			return nil, err
		}
	}
	if len(stack) != 0 {
		return nil, fmt.Errorf("unsupported uses template: exceeds %d nodes", maxActionYAMLNodes)
	}
	return branches, nil
}

// supportedTemplateLeaf accepts text and printing actions, refusing calls and loop controls.
func supportedTemplateLeaf(node parse.Node) error {
	switch node := node.(type) {
	case *parse.TextNode:
		return nil
	case *parse.ActionNode:
		if len(node.Pipe.Decl) == 0 {
			return nil
		}
		return fmt.Errorf("unsupported uses template directive: variable declaration or assignment")
	case *parse.TemplateNode:
		return fmt.Errorf("unsupported uses template directive: template %q", node.Name)
	default:
		return fmt.Errorf("unsupported uses template directive: %s", node.String())
	}
}

// templatePiece holds one unchanged parser text node or one printing directive.
type templatePiece struct {
	text     string
	original int
	action   *parse.ActionNode
}

// pieces emits one body or else per branch, taking range/with once or skipping once.
func (tmpl *actionTemplate) pieces(choice int) ([]templatePiece, error) {
	var pieces []templatePiece
	stack := pushTemplateList(nil, tmpl.root)
	for visited := 0; len(stack) > 0 && visited < maxActionYAMLNodes; visited++ {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if branch := templateBranch(node); branch != nil {
			list := branch.ElseList
			if choice&(1<<tmpl.branches[node]) != 0 {
				list = branch.List
			}
			stack = pushTemplateList(stack, list)
			continue
		}
		switch node := node.(type) {
		case *parse.TextNode:
			pieces = append(pieces, templatePiece{text: string(node.Text), original: int(node.Pos)})
		case *parse.ActionNode:
			pieces = append(pieces, templatePiece{action: node})
		}
	}
	if len(stack) != 0 {
		return nil, fmt.Errorf("unsupported uses template: exceeds %d nodes", maxActionYAMLNodes)
	}
	return pieces, nil
}
