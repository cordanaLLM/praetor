package p

import "plugin"

// LoadPlugin executes code chosen at runtime.
func LoadPlugin(path string) error {
	_, err := plugin.Open(path)
	return err
}
