package router

import "sort"

// MaxTaskLabels bounds the label union of one routing configuration.
const MaxTaskLabels = MaxRoutingTiers * MaxRoutingTags

// ValidTaskLabel reports whether name has the shape every routing name must have. Other
// packages that key configuration by target_tasks labels validate through it, so the
// vocabulary keeps one definition.
func ValidTaskLabel(name string) bool { return routingName(name) }

// DeclaredTaskLabels returns the sorted, deduplicated union of every tier's target_tasks.
// A nil configuration declares nothing.
func DeclaredTaskLabels(cfg *RoutingConfig) []string {
	if cfg == nil {
		return []string{}
	}
	return taskLabelUnion(cfg.Tiers)
}

// DefaultTaskLabels returns the labels of the built-in tiers, which govern a repository
// that ships no routing.yaml.
func DefaultTaskLabels() []string {
	return taskLabelUnion(defaultRoutingTiers())
}

func taskLabelUnion(tiers map[string]Tier) []string {
	names := make([]string, 0, len(tiers))
	for name := range tiers {
		names = append(names, name)
	}
	sort.Strings(names)
	seen := make(map[string]bool)
	labels := make([]string, 0, MaxRoutingTags)
	for i := 0; i < len(names) && i < MaxRoutingTiers; i++ {
		tasks := tiers[names[i]].TargetTasks
		for j := 0; j < len(tasks) && j < MaxRoutingTags; j++ {
			if !seen[tasks[j]] {
				seen[tasks[j]] = true
				labels = append(labels, tasks[j])
			}
		}
	}
	sort.Strings(labels)
	return labels
}
