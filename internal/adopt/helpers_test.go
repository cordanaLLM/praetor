package adopt

import (
	"encoding/json"
	"reflect"

	"gopkg.in/yaml.v3"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

// buildRulesetJSON renders the ruleset adoption writes for policy and contexts, as text. The
// branch-ruleset step renders through forge.RenderRulesetForRepository, which derives contexts
// from the workflows on disk; tests that fix the contexts themselves render through this.
func buildRulesetJSON(policy config.BranchProtectionPolicy, contexts []string) (string, error) {
	data, err := forge.RenderRepositoryRuleset(policy, contexts)
	return string(data), err
}

// Thin wrappers keep the encoding imports out of the main test file's namespace.

func yamlUnmarshal(data []byte, out any) error {
	return yaml.Unmarshal(data, out)
}

func jsonUnmarshal(data []byte, out any) error {
	return json.Unmarshal(data, out)
}

func deepEqual(a, b any) bool {
	return reflect.DeepEqual(a, b)
}
