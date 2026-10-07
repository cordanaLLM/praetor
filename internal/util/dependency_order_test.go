// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"slices"
	"testing"
)

// dependsOn returns a dependencies function reading graph.
func dependsOn(graph map[string][]string) func(string) []string {
	return func(node string) []string { return graph[node] }
}

// Positive: each node follows every node it depends on, ties break in the order of nodes, and a
// node named twice as a dependency is released once both are counted down. A dependency outside
// nodes is ignored.
func TestDependencyOrder_OrdersEachAfterItsDependencies(t *testing.T) {
	graph := map[string][]string{
		"a": {"c", "c"},
		"b": {"c", "outside"},
		"c": {"d"},
		"e": nil,
	}
	order, left := DependencyOrder([]string{"a", "b", "c", "d", "e"}, dependsOn(graph))
	if want := []string{"d", "e", "c", "a", "b"}; !slices.Equal(order, want) || left != nil {
		t.Fatalf("order %v, left %v; want %v and none left", order, left, want)
	}
}

// Negative: nodes on a cycle, a node depending on itself, and a node depending on a cycle are
// left out of the order and listed in the order of nodes; the other nodes are still ordered.
func TestDependencyOrder_LeavesCyclesOut(t *testing.T) {
	graph := map[string][]string{
		"a": {"b"},
		"b": {"a"},
		"c": {"a"},
		"d": nil,
		"s": {"s"},
	}
	order, left := DependencyOrder([]string{"a", "b", "c", "d", "s"}, dependsOn(graph))
	if !slices.Equal(order, []string{"d"}) || !slices.Equal(left, []string{"a", "b", "c", "s"}) {
		t.Fatalf("order %v, left %v; want [d] and [a b c s] left", order, left)
	}
}

// Boundary: no nodes give an empty order, and a chain named against its dependencies comes out
// reversed, every node released by the one before it.
func TestDependencyOrder_EmptyAndReversedChain(t *testing.T) {
	if order, left := DependencyOrder(nil, dependsOn(nil)); len(order) != 0 || left != nil {
		t.Fatalf("no nodes: order %v, left %v", order, left)
	}
	graph := map[string][]string{"a": {"b"}, "b": {"c"}, "c": {"d"}}
	order, left := DependencyOrder([]string{"a", "b", "c", "d"}, dependsOn(graph))
	if want := []string{"d", "c", "b", "a"}; !slices.Equal(order, want) || left != nil {
		t.Fatalf("reversed chain: order %v, left %v; want %v", order, left, want)
	}
}
