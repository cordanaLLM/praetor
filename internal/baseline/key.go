package baseline

import (
	"slices"
	"strconv"
)

// An entry used to be keyed "<file>:<line>:<rule>", so an unchanged function that moved was a
// different entry and the ratchet reported it as a new infraction (#29). An entry is now keyed
// by what a shift leaves alone:
//
//	<file>:<rule>:<anchor>#<ordinal>
//
// The anchor is the place the scanner gives the finding (Infraction.Anchor): the function that
// holds it, or a hash of its own line where no function does. The ordinal tells apart the
// findings of one rule that share an anchor, two unbounded loops in one function say: it is the
// finding's rank among them in line order, starting at 1, so it follows the order of the
// findings and never their line numbers. The line stays in the entry as LineNumber, for a
// reader; no match reads it.
//
// Both forms are read. An entry without an anchor is one recorded in the line-keyed form, and
// it is matched the way it was recorded, on its line, until `praetorctl baseline --record`
// rewrites the file in the anchored form.

// AssignFingerprints sets the fingerprint of every infraction of one scan: the anchored key for
// a finding that carries an anchor, the line key for one that carries none. It must see a whole
// scan, since an ordinal counts the findings before it.
func AssignFingerprints(infractions []Infraction) {
	order := make([]int, len(infractions))
	for i := 0; i < len(order); i++ {
		order[i] = i
	}
	// Line order within one file is what a shift preserves; scan order breaks a tie on one line.
	slices.SortStableFunc(order, func(a, b int) int { return infractions[a].LineNumber - infractions[b].LineNumber })
	seen := make(map[string]int, len(infractions))
	for _, i := range order {
		v := &infractions[i]
		if v.Anchor == "" {
			v.Fingerprint = lineKey(*v)
			continue
		}
		stem := NormalizePath(v.FilePath) + ":" + v.RuleID + ":" + v.Anchor
		seen[stem]++
		v.Fingerprint = stem + "#" + strconv.Itoa(seen[stem])
	}
}

// lineKey is the key of the line-keyed form: "<file>:<line>:<rule>".
func lineKey(v Infraction) string {
	return NormalizePath(v.FilePath) + ":" + strconv.Itoa(v.LineNumber) + ":" + v.RuleID
}

// lineKeyed reports an entry recorded in the line-keyed form: it carries no anchor.
func lineKeyed(v Infraction) bool {
	return v.Anchor == ""
}

// recordedKeys counts the recorded entries by the key each is matched on, the anchored entries
// apart from the line-keyed ones.
type recordedKeys struct {
	anchored map[string]int
	byLine   map[string]int
}

// indexRecorded counts the recorded entries by fingerprint, per form.
func indexRecorded(recorded []Infraction) recordedKeys {
	keys := recordedKeys{anchored: make(map[string]int, len(recorded)), byLine: make(map[string]int)}
	for i := 0; i < len(recorded); i++ {
		if lineKeyed(recorded[i]) {
			keys.byLine[fingerprintOf(recorded[i])]++
			continue
		}
		keys.anchored[fingerprintOf(recorded[i])]++
	}
	return keys
}

// records reports whether the baseline records the finding v: an entry carries v's fingerprint,
// or a line-keyed entry names v's rule at v's file and line.
func (k recordedKeys) records(v Infraction) bool {
	fingerprint := fingerprintOf(v)
	return k.anchored[fingerprint] > 0 || k.byLine[fingerprint] > 0 || k.byLine[lineKey(v)] > 0
}

// take consumes the recorded entry that records v and reports whether there was one left.
func (k recordedKeys) take(v Infraction) bool {
	fingerprint := fingerprintOf(v)
	return take(k.anchored, fingerprint) || take(k.byLine, fingerprint) || take(k.byLine, lineKey(v))
}

// scannedKeys holds the keys the current findings can be matched on: each one's fingerprint,
// and its line key for the recorded entries of the line-keyed form.
type scannedKeys struct {
	fingerprints map[string]struct{}
	lines        map[string]struct{}
}

// indexScanned collects the keys of the current findings.
func indexScanned(current []Infraction) scannedKeys {
	keys := scannedKeys{fingerprints: make(map[string]struct{}, len(current)), lines: make(map[string]struct{}, len(current))}
	for i := 0; i < len(current); i++ {
		keys.fingerprints[fingerprintOf(current[i])] = struct{}{}
		keys.lines[lineKey(current[i])] = struct{}{}
	}
	return keys
}

// carries reports whether some current finding matches the recorded entry e.
func (k scannedKeys) carries(e Infraction) bool {
	fingerprint := fingerprintOf(e)
	if _, ok := k.fingerprints[fingerprint]; ok {
		return true
	}
	_, ok := k.lines[fingerprint]
	return ok && lineKeyed(e)
}
