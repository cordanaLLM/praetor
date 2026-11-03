package forge

import (
	"errors"
	"regexp"
	"strings"
)

// ErrRateLimited identifies a paced or rate-limited gh read and retains any stated
// budget metadata. Empty fields mean the diagnostic did not supply that value.
type ErrRateLimited struct {
	Bucket, Remaining, Limit, Reset string
	Cause                           error
}

// Error preserves the diagnostic or returns a stable message when no cause is available.
func (e *ErrRateLimited) Error() string {
	if e.Cause == nil {
		return "GitHub read rate limited"
	}
	return e.Cause.Error()
}

// Unwrap preserves the command failure for errors.Is and errors.As callers.
func (e *ErrRateLimited) Unwrap() error {
	return e.Cause
}

var ghRateBudget = regexp.MustCompile(`(?i)\b([a-z][a-z0-9_-]*)[ \t]+([0-9]+)[ \t]+of[ \t]+([0-9]+)\b`)
var ghRateReset = regexp.MustCompile(`(?i)\breset[ \t]+((?:[01][0-9]|2[0-3]):[0-5][0-9])\b`)

// classifyGHError wraps only EX_TEMPFAIL or a stated GitHub rate limit, preserving
// the original cause and any budget metadata in stderr. Other failures are unchanged.
func classifyGHError(err error, stderr []byte) error {
	if err == nil {
		return nil
	}
	var exit interface{ ExitCode() int }
	paced := errors.As(err, &exit) && exit.ExitCode() == 75
	diagnostic := strings.ToLower(string(stderr))
	if !paced && !strings.Contains(diagnostic, "api rate limit already exceeded") &&
		!strings.Contains(diagnostic, "secondary rate limit") {
		return err
	}
	limited := &ErrRateLimited{Cause: err}
	if budget := ghRateBudget.FindStringSubmatch(string(stderr)); len(budget) == 4 {
		limited.Bucket, limited.Remaining, limited.Limit = strings.ToLower(budget[1]), budget[2], budget[3]
	}
	if reset := ghRateReset.FindStringSubmatch(string(stderr)); len(reset) == 2 {
		limited.Reset = reset[1]
	}
	return limited
}
