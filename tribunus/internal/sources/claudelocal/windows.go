package claudelocal

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/tribunus/catalog"
)

// windowKinds maps a rateLimitType prefix to its window length. A type with
// a suffix (seven_day_opus) is a per-model sub-limit: only usage of models
// whose id names that family counts toward it. five_hour is the type seen in
// the logs on kcromm; seven_day and its suffixed forms follow the same
// naming and are not yet observed there.
var windowKinds = []struct {
	prefix string
	length time.Duration
}{
	{"five_hour", 5 * time.Hour},
	{"seven_day", weeklyWindow},
}

// alwaysReported are the windows every fetch reports, observed or not.
var alwaysReported = []string{"five_hour", "seven_day"}

// maxCeilingWindows bounds how many past windows with a rejection calibrate a ceiling.
const maxCeilingWindows = 64

type windowSpec struct {
	limitType string
	length    time.Duration
	family    string
	known     bool
}

func specFor(limitType string) windowSpec {
	spec := windowSpec{limitType: limitType}
	for _, kind := range windowKinds {
		if limitType == kind.prefix || strings.HasPrefix(limitType, kind.prefix+"_") {
			spec.length, spec.known = kind.length, true
			spec.family = strings.TrimPrefix(strings.TrimPrefix(limitType, kind.prefix), "_")
			return spec
		}
	}
	return spec
}

// records returns one record per reported window, in a stable order.
func (s *logScan) records() []catalog.Record {
	sort.Slice(s.usage, func(i, j int) bool { return s.usage[i].at.Before(s.usage[j].at) })
	types := append([]string(nil), alwaysReported...)
	known := map[string]bool{"five_hour": true, "seven_day": true}
	for i := 0; i < len(s.rejections) && i < maxRejections; i++ {
		if limitType := s.rejections[i].limitType; !known[limitType] {
			known[limitType] = true
			types = append(types, limitType)
		}
	}
	sort.Strings(types[len(alwaysReported):])
	records := make([]catalog.Record, 0, len(types))
	for _, limitType := range types {
		records = append(records, s.record(specFor(limitType)))
	}
	return records
}

func (s *logScan) record(spec windowSpec) catalog.Record {
	record := baseRecord(spec.limitType, s.now)
	window := &catalog.UsageWindow{}
	record.UsageWindow = window
	if !spec.known {
		record.Absent["usage_window"] = "window length of rateLimitType " + spec.limitType + " is not known to this source"
		return record
	}
	minutes := int(spec.length / time.Minute)
	window.WindowMinutes = &minutes
	hits := s.hitsFor(spec.limitType)
	if len(hits) == 0 {
		record.Absent["used_percent"] = "no rejected " + spec.limitType + " request in the logs of the last 7 days, so the ceiling is unknown"
		return record
	}
	reset := currentReset(hits[len(hits)-1].resetsAt, spec.length, s.now)
	window.ResetsAt = &reset
	ceiling, reason := s.ceiling(spec, hits)
	if reason != "" {
		record.Absent["used_percent"] = reason
		return record
	}
	used, ok := s.sum(spec.family, reset.Add(-spec.length), s.now)
	if !ok {
		record.Absent["used_percent"] = "usage of a model without rates falls in the current window"
		return record
	}
	usedPercent := used / ceiling * 100
	window.UsedPercent = &usedPercent
	s.addBurn(record, window, spec.family, ceiling)
	return record
}

// addBurn sets the sliding-window burn and the agents active in it.
func (s *logScan) addBurn(record catalog.Record, window *catalog.UsageWindow, family string, ceiling float64) {
	from := s.now.Add(-burnWindow)
	burn, ok := s.sum(family, from, s.now)
	if !ok {
		record.Absent["burn_percent_per_hour"] = "usage of a model without rates falls in the burn window"
		return
	}
	perHour := burn / ceiling * 100 * float64(time.Hour/burnWindow)
	agents := s.activeAgents(family, from, s.now)
	window.BurnPercentPerHour, window.ActiveAgents = &perHour, &agents
}

func baseRecord(limitType string, now time.Time) catalog.Record {
	return catalog.Record{
		ModelID:    "claude-code/" + limitType,
		Provider:   "anthropic",
		AccessPath: catalog.AccessSubscriptionCLI,
		Provenance: catalog.Provenance{Source: SourceName, FetchedAt: now, Kind: catalog.KindMeasured},
		Absent: map[string]string{
			"context_window":  "not reported by Claude Code session logs",
			"price_in_per_m":  "subscription plan, not metered pricing",
			"price_out_per_m": "subscription plan, not metered pricing",
			"limits":          "the logs report rejected requests, not rpm/tpm caps",
		},
	}
}

// hitsFor returns the rejections of one window type, oldest first.
func (s *logScan) hitsFor(limitType string) []rejection {
	var hits []rejection
	for i := 0; i < len(s.rejections) && i < maxRejections; i++ {
		if s.rejections[i].limitType == limitType {
			hits = append(hits, s.rejections[i])
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].at.Before(hits[j].at) })
	return hits
}

// currentReset advances the last reported reset in whole window steps until
// it lies after now.
func currentReset(last time.Time, length time.Duration, now time.Time) time.Time {
	if last.After(now) {
		return last
	}
	steps := now.Sub(last)/length + 1
	return last.Add(steps * length)
}

// ceiling is the smallest cost-equivalent a window held at its first
// rejection, over the windows whose usage the scanned logs hold completely.
func (s *logScan) ceiling(spec windowSpec, hits []rejection) (float64, string) {
	first, resets := firstRejections(hits)
	best := 0.0
	for i := 0; i < len(resets) && i < maxCeilingWindows; i++ {
		held := s.heldAtRejection(spec, resets[i], first[resets[i]])
		if held > 0 && (best == 0 || held < best) {
			best = held
		}
	}
	if best == 0 {
		return 0, fmt.Sprintf("no rejected %s request with fully logged, priced usage in the last 7 days, so the ceiling is unknown", spec.limitType)
	}
	return best, ""
}

// firstRejections maps each reported reset to its window's earliest
// rejection and lists the resets newest first.
func firstRejections(hits []rejection) (map[time.Time]time.Time, []time.Time) {
	first := make(map[time.Time]time.Time)
	var resets []time.Time
	for _, hit := range hits {
		at, ok := first[hit.resetsAt]
		if !ok {
			resets = append(resets, hit.resetsAt)
		}
		if !ok || hit.at.Before(at) {
			first[hit.resetsAt] = hit.at
		}
	}
	sort.Slice(resets, func(i, j int) bool { return resets[i].After(resets[j]) })
	return first, resets
}

// heldAtRejection is the usage a window held when its first request was
// rejected; 0 when the window started before the scanned logs or holds
// unpriced usage.
func (s *logScan) heldAtRejection(spec windowSpec, reset, rejectedAt time.Time) float64 {
	start := reset.Add(-spec.length)
	if start.Before(s.now.Add(-weeklyWindow)) || rejectedAt.Before(start) {
		return 0
	}
	held, ok := s.sum(spec.family, start, rejectedAt)
	if !ok {
		return 0
	}
	return held
}

// sum adds the cost of the family's usage in [from, to]; ok is false when
// unpriced usage falls in the range. s.usage is sorted by time.
func (s *logScan) sum(family string, from, to time.Time) (total float64, ok bool) {
	i := sort.Search(len(s.usage), func(k int) bool { return !s.usage[k].at.Before(from) })
	for ; i < len(s.usage) && !s.usage[i].at.After(to); i++ {
		event := s.usage[i]
		if !inFamily(event.model, family) {
			continue
		}
		if event.unpriced {
			return 0, false
		}
		total += event.cost
	}
	return total, true
}

func (s *logScan) activeAgents(family string, from, to time.Time) int {
	agents := make(map[string]bool)
	i := sort.Search(len(s.usage), func(k int) bool { return !s.usage[k].at.Before(from) })
	for ; i < len(s.usage) && !s.usage[i].at.After(to); i++ {
		if inFamily(s.usage[i].model, family) {
			agents[s.usage[i].agent] = true
		}
	}
	return len(agents)
}

// inFamily reports whether a model id names the family (claude-opus-5-5 is
// in opus); the empty family holds every model.
func inFamily(model, family string) bool {
	return family == "" || strings.Contains(model, "-"+family+"-") || strings.HasSuffix(model, "-"+family)
}
