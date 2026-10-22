package adopt

import (
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
)

// The [FAIL] lines every gate that reads the manifest's exceptions list prints about its own
// entries, worded once so the HISS-10 and HISS-11 gates cannot drift apart (HISS-19). excused
// names what an entry of the gate's rule excuses: "gap" for HISS-11, "lane" for HISS-10.

// staleExceptionEntries is the [FAIL] line for entries of rule that excuse nothing, and why.
func staleExceptionEntries(gate, rule, excused string, entries []config.Exception, why string) string {
	targets := make([]string, 0, len(entries))
	for index := 0; index < len(entries) && index < config.MaxExceptions; index++ {
		targets = append(targets, entries[index].Target())
	}
	return fmt.Sprintf("[FAIL] %s: the exceptions entries with rule %s for %s excuse no %s, because %s; remove them.",
		gate, rule, strings.Join(targets, ", "), excused, why)
}

// expiredExceptionEntry is the [FAIL] line for an expired entry: what it excused, named by
// failing, fails as if the entry were not declared.
func expiredExceptionEntry(gate, failing, remedy string, entry config.Exception) string {
	return fmt.Sprintf("[FAIL] %s: the exceptions entry (rule %s, %s) expired on %s, so %s as if it were not declared; "+
		"%s, or renew the entry with a new reason and expiry.", gate, entry.Rule, entry.Target(), entry.Expires, failing, remedy)
}
