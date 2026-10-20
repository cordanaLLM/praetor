package sandbox

import (
	"errors"
	"fmt"
	"strings"
)

func environmentVariables(spec Spec, environ []string) ([]Variable, error) {
	if len(spec.Environment)+len(spec.PassEnvironment) > 256 {
		return nil, errors.New("sandbox allows at most 256 environment variables")
	}
	variables := append([]Variable{}, spec.Environment...)
	for i := 0; i < len(spec.PassEnvironment); i++ {
		name := spec.PassEnvironment[i]
		if !validEnvironmentName(name) {
			return nil, fmt.Errorf("invalid sandbox environment name %q", name)
		}
		if value, ok := environmentValue(environ, name); ok {
			variables = append(variables, Variable{Name: name, Value: value})
		}
	}
	seen := make(map[string]bool, len(variables))
	for i := 0; i < len(variables); i++ {
		v := variables[i]
		if !validEnvironmentName(v.Name) || strings.ContainsRune(v.Value, 0) || seen[v.Name] {
			return nil, fmt.Errorf("invalid or duplicate sandbox environment variable %q", v.Name)
		}
		seen[v.Name] = true
	}
	return variables, nil
}

func validEnvironmentName(name string) bool {
	return name != "" && !strings.ContainsAny(name, "=\x00")
}

func environmentValue(environ []string, name string) (string, bool) {
	for i := 0; i < len(environ); i++ {
		key, value, ok := strings.Cut(environ[i], "=")
		if ok && key == name {
			return value, true
		}
	}
	return "", false
}
