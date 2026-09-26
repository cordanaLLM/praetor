package util

// scratchDirNames are the directories kept beside a checkout's own source rather than in it:
// agent session state and linked worktrees (.claude), Praetor's cache (.standards), and the
// private ledgers (.workingdir, .workingdir2). A .claude/worktrees tree holds whole copies of
// the checkout, so a walker that counts repository content and enters it measures the same
// source many times over and trips its entry bound on an ordinary working checkout.
var scratchDirNames = map[string]struct{}{
	".claude":      {},
	".standards":   {},
	".workingdir":  {},
	".workingdir2": {},
}

// IsScratchDir reports whether a directory base name is one of the scratch directories a
// repository walker skips. It is the one list the HISS scanner, adopt's verification planner
// and dedupe's non-Git walker share (HISS-19), so the three cannot drift apart again. Matching
// is exact; a caller that folds case lowers the name first.
func IsScratchDir(name string) bool {
	_, ok := scratchDirNames[name]
	return ok
}
