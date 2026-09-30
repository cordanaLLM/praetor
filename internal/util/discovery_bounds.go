package util

// DefaultDiscoveryEntries is the entry bound a repository discovery walk applies when its caller
// raises none, and DiscoveryEntriesCeiling is the most a caller may raise it to. Adoption's
// verification planner and the editor language scan share both (HISS-19), so one
// `--verification-max-entries` value reaches every walk on the adopt path and the two walks
// cannot drift to different defaults or ceilings again (issue #535).
//
// The verification walk counts every entry of every directory it enters, including generated
// output it does not skip by name, such as a built documentation site. The earlier default of
// 4096 stopped this repository's own checkout once its docs were built (about 4100 entries).
// Large public source trees walk 10000 to 16000 entries, so 65536 keeps every measured tree at
// least four times under the default while the bound stays a scalar the flag can still raise.
const (
	DefaultDiscoveryEntries = 65536
	DiscoveryEntriesCeiling = 200000
)
