package adopt

import (
	"encoding/json"
	"reflect"

	"gopkg.in/yaml.v3"
)

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
