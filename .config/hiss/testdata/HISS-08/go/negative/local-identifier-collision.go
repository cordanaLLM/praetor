package p

// registry has an Open field, and the parameter below is named plugin. With
// analyze-types the forbidigo rules resolve real package symbols, so this local
// plugin.Open must not be reported.
type registry struct{ Open func(string) error }

// UseLocal calls the caller's own Open function.
func UseLocal(plugin registry, path string) error {
	return plugin.Open(path)
}
