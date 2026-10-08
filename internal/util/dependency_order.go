// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

// DependencyOrder orders nodes so each follows every node of nodes it depends on, with Kahn's
// algorithm: iterative (HISS-01) and bounded by the number of nodes (HISS-02). dependencies names
// what one node depends on; a name outside nodes is ignored. nodes must be distinct.
//
// Ties break in the order of nodes: the nodes that depend on nothing come first in that order,
// and the nodes one node releases follow in that order too, so sorted nodes give the same order
// on every run. left lists, in the order of nodes, the nodes the order cannot hold: each is on a
// dependency cycle, or depends on a node that is.
//
// The archetype schema scan (internal/archetypecoverage) orders module packages by their imports
// with it, and the workflow trigger audit (internal/forge) orders a workflow's jobs by their needs.
func DependencyOrder(nodes []string, dependencies func(string) []string) (order, left []string) {
	pending, dependents := dependencyEdges(nodes, dependencies)
	ready := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if pending[node] == 0 {
			ready = append(ready, node)
		}
	}
	order = make([]string, 0, len(nodes))
	for i := 0; i < len(ready) && i < len(nodes); i++ {
		order = append(order, ready[i])
		for _, dependent := range dependents[ready[i]] {
			if pending[dependent]--; pending[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
	}
	for _, node := range nodes {
		if pending[node] > 0 {
			left = append(left, node)
		}
	}
	return order, left
}

// dependencyEdges counts, per node, the dependencies it has among nodes, and lists, per node, the
// nodes that depend on it, in the order of nodes. A dependency named twice counts twice and
// releases its dependent once both are counted down.
func dependencyEdges(nodes []string, dependencies func(string) []string) (map[string]int, map[string][]string) {
	known := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		known[node] = true
	}
	pending := make(map[string]int, len(nodes))
	dependents := make(map[string][]string, len(nodes))
	for _, node := range nodes {
		for _, dependency := range dependencies(node) {
			if known[dependency] {
				pending[node]++
				dependents[dependency] = append(dependents[dependency], node)
			}
		}
	}
	return pending, dependents
}
