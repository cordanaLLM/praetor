package planning

import (
	"context"
	"fmt"
	"sort"
)

type graphNode struct {
	id           string
	dependencies []string
	rank         int
}

func validateIDs(kind, owner string, values []string, maximum int) error {
	if len(values) > maximum {
		return fmt.Errorf("%s %s exceeds %d references", kind, owner, maximum)
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if !validID(value) || value == owner || seen[value] {
			return fmt.Errorf("%s %s contains invalid, self, or duplicate reference", kind, owner)
		}
		seen[value] = true
	}
	return nil
}

func validateReferences(kind string, nodes []graphNode, known map[string]bool) error {
	for _, node := range nodes {
		for _, dependency := range node.dependencies {
			if !known[dependency] {
				return fmt.Errorf("%s %s references unknown id %s", kind, node.id, dependency)
			}
		}
	}
	return nil
}

func graphOrder(ctx context.Context, kind string, nodes []graphNode, maximum int) ([]string, error) {
	if len(nodes) == 0 || len(nodes) > maximum {
		return nil, fmt.Errorf("%s graph requires 1..%d nodes", kind, maximum)
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].rank < nodes[j].rank || nodes[i].rank == nodes[j].rank && nodes[i].id < nodes[j].id
	})
	emitted := make(map[string]bool, len(nodes))
	order := make([]string, 0, len(nodes))
	for position := 0; position < len(nodes); position++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		chosen := ""
		for _, node := range nodes {
			if !emitted[node.id] && dependenciesEmitted(node.dependencies, emitted) {
				chosen = node.id
				break
			}
		}
		if chosen == "" {
			return nil, fmt.Errorf("%s dependency cycle", kind)
		}
		emitted[chosen] = true
		order = append(order, chosen)
	}
	return order, nil
}

func dependenciesEmitted(dependencies []string, emitted map[string]bool) bool {
	for _, dependency := range dependencies {
		if !emitted[dependency] {
			return false
		}
	}
	return true
}
