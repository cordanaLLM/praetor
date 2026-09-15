package p

// The suppression is specific and explained but nothing is suppressed on this line, so it
// is a stale claim that a warning once existed here.
func StaleSuppression() int {
	//nolint:errcheck // the remove failure is not actionable here
	return 1
}
