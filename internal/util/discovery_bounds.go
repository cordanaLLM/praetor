package util

// DefaultDiscoveryEntries is the entry bound a repository discovery walk applies when its caller
// raises none, and DiscoveryEntriesCeiling is the most a caller may raise it to. Adoption's
// verification planner and the editor language scan share both (HISS-19), so one
// `--verification-max-entries` value reaches every walk on the adopt path and the two walks
// cannot drift to different defaults or ceilings again (issue #535).
const (
	DefaultDiscoveryEntries = 4096
	DiscoveryEntriesCeiling = 200000
)
