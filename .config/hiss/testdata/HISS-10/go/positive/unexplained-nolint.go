package p

import "os"

// The suppression names a linter but gives no reason, so the warning it hides is invisible.
func UnexplainedSuppression(path string) {
	//nolint:errcheck
	os.Remove(path)
}
