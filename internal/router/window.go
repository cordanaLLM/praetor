package router

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/cordanaLLM/praetor/tribunus/catalog"
)

// Subscription window governor (#1142). A subscription lane shares one rolling usage window
// with every session and agent on its account; the governor reads that window from a tribunus
// snapshot and refuses new work on the lane before the window runs out. A reading without a
// fresh source is unknown, and unknown refuses.
const (
	// DefaultWindowWarnPercent and DefaultWindowStopPercent are the warn and stop lines, in
	// percent of the window, when governance.window leaves them out.
	DefaultWindowWarnPercent = 65
	DefaultWindowStopPercent = 80
	// DefaultWindowMaxReadingAgeSeconds is the age past which a reading counts as unknown.
	DefaultWindowMaxReadingAgeSeconds = 600
	// DefaultWindowStopHorizonMinutes is how close the projected stop line may come before stop.
	DefaultWindowStopHorizonMinutes = 30
	// MaxWindowReadingAgeSeconds and MaxWindowStopHorizonMinutes bound the configured values.
	MaxWindowReadingAgeSeconds  = 86400
	MaxWindowStopHorizonMinutes = 1440
	// MaxWindowAccounts bounds governance.window.accounts; every account backs at least one lane.
	MaxWindowAccounts = MaxLanes
	// windowClockSkew tolerates a reading stamped slightly ahead of this host's clock.
	windowClockSkew = time.Minute
	// windowAdvice is the pacing signal a warn decision carries.
	windowAdvice = "finish the current unit, then checkpoint; hold optional work while your own burn is above the pace"
)

// WindowPolicy is governance.window: the governor's thresholds and the accounts whose
// usage window it reads. Zero values take the defaults above.
type WindowPolicy struct {
	WarnPercent          float64 `yaml:"warn_percent,omitempty"`
	StopPercent          float64 `yaml:"stop_percent,omitempty"`
	MaxReadingAgeSeconds int     `yaml:"max_reading_age_seconds,omitempty"`
	StopHorizonMinutes   int     `yaml:"stop_horizon_minutes,omitempty"`
	// PerAgentPercentPerHour is the burn floor per active agent; zero turns the floor off.
	PerAgentPercentPerHour float64                  `yaml:"per_agent_percent_per_hour,omitempty"`
	Accounts               map[string]WindowAccount `yaml:"accounts,omitempty"`
}

// WindowAccount names the snapshot record (a subscription_cli record's model_id) that holds
// the account's usage window.
type WindowAccount struct {
	WindowRecord string `yaml:"window_record"`
}

// WindowLevel is the governor's verdict on one account's window.
type WindowLevel string

const (
	WindowOK      WindowLevel = "ok"
	WindowWarn    WindowLevel = "warn"
	WindowStop    WindowLevel = "stop"
	WindowUnknown WindowLevel = "unknown"
)

// WindowDecision is the admission verdict and pacing signal for one account at one instant.
// Percentages are of the account's window; the pace is the burn each active agent may
// spend so the fleet reaches the stop line no earlier than the reset.
type WindowDecision struct {
	Level                       WindowLevel `json:"level"`
	Account                     string      `json:"account"`
	Record                      string      `json:"record,omitempty"`
	Source                      string      `json:"source,omitempty"`
	AgeSeconds                  *int64      `json:"age_seconds,omitempty"`
	UsedPercent                 *float64    `json:"used_percent,omitempty"`
	ResetsAt                    *time.Time  `json:"resets_at,omitempty"`
	ProjectedBurnPercentPerHour *float64    `json:"projected_burn_percent_per_hour,omitempty"`
	PacePercentPerAgentHour     *float64    `json:"pace_percent_per_agent_hour,omitempty"`
	Reason                      string      `json:"reason"`
	Advice                      string      `json:"advice,omitempty"`
}

// Admits reports whether the decision lets new work onto the lane: ok and warn do,
// stop and unknown refuse.
func (d WindowDecision) Admits() bool {
	return d.Level == WindowOK || d.Level == WindowWarn
}

// WindowGovernor decides from one snapshot of readings at one instant. It keeps no counters:
// a new snapshot makes a new governor.
type WindowGovernor struct {
	policy   WindowPolicy
	readings map[string]catalog.Record
	now      time.Time
}

// resolved returns the policy with its defaults filled in.
func (p WindowPolicy) resolved() WindowPolicy {
	if p.WarnPercent == 0 {
		p.WarnPercent = DefaultWindowWarnPercent
	}
	if p.StopPercent == 0 {
		p.StopPercent = DefaultWindowStopPercent
	}
	if p.MaxReadingAgeSeconds == 0 {
		p.MaxReadingAgeSeconds = DefaultWindowMaxReadingAgeSeconds
	}
	if p.StopHorizonMinutes == 0 {
		p.StopHorizonMinutes = DefaultWindowStopHorizonMinutes
	}
	return p
}

func validateWindowPolicy(policy *WindowPolicy) error {
	if policy == nil {
		return nil
	}
	r := policy.resolved()
	if err := validateWindowLines(r); err != nil {
		return err
	}
	if policy.MaxReadingAgeSeconds < 0 || r.MaxReadingAgeSeconds > MaxWindowReadingAgeSeconds {
		return fmt.Errorf("%w: governance.window max_reading_age_seconds must be between 0 and %d", ErrInvalidRoutingConfig, MaxWindowReadingAgeSeconds)
	}
	if policy.StopHorizonMinutes < 0 || r.StopHorizonMinutes > MaxWindowStopHorizonMinutes {
		return fmt.Errorf("%w: governance.window stop_horizon_minutes must be between 0 and %d", ErrInvalidRoutingConfig, MaxWindowStopHorizonMinutes)
	}
	if !finiteNonnegative(r.PerAgentPercentPerHour) || r.PerAgentPercentPerHour > 100 {
		return fmt.Errorf("%w: governance.window per_agent_percent_per_hour must be between 0 and 100", ErrInvalidRoutingConfig)
	}
	return validateWindowAccounts(r.Accounts)
}

func validateWindowLines(r WindowPolicy) error {
	if !finiteNonnegative(r.WarnPercent) || !finiteNonnegative(r.StopPercent) || r.WarnPercent >= r.StopPercent || r.StopPercent > 100 {
		return fmt.Errorf("%w: governance.window needs 0 < warn_percent < stop_percent <= 100", ErrInvalidRoutingConfig)
	}
	return nil
}

func validateWindowAccounts(accounts map[string]WindowAccount) error {
	if len(accounts) > MaxWindowAccounts {
		return fmt.Errorf("%w: more than %d window accounts", ErrInvalidRoutingConfig, MaxWindowAccounts)
	}
	for name, account := range accounts {
		if !routingName(name) || !routingName(account.WindowRecord) {
			return fmt.Errorf("%w: window account %q needs a name and a window_record", ErrInvalidRoutingConfig, name)
		}
	}
	return nil
}

// checkLaneAccount refuses a lane naming an account governance.window does not declare.
func checkLaneAccount(cfg *RoutingConfig, lane, account string) error {
	if account == "" {
		return nil
	}
	window := cfg.Governance.Window
	if window == nil {
		return fmt.Errorf("%w: lane %s names account %s but governance.window is not declared", ErrInvalidRoutingConfig, lane, account)
	}
	if _, ok := window.Accounts[account]; !ok {
		return fmt.Errorf("%w: lane %s names undeclared window account %s", ErrInvalidRoutingConfig, lane, account)
	}
	return nil
}

// LoadWindowSnapshot reads a tribunus snapshot (tribunusctl sync --out) with the routing
// reader's size and time bounds.
func LoadWindowSnapshot(ctx context.Context, path string) (catalog.Snapshot, error) {
	data, err := readRoutingFile(ctx, path)
	if err != nil {
		return catalog.Snapshot{}, fmt.Errorf("window snapshot %s: %w", path, err)
	}
	snapshot, err := catalog.ParseSnapshot(data)
	if err != nil {
		return catalog.Snapshot{}, fmt.Errorf("window snapshot %s: %w", path, err)
	}
	return snapshot, nil
}

// NewWindowGovernor indexes the snapshot's subscription_cli records that carry a usage
// window; of two records with one model_id the newer reading wins.
func NewWindowGovernor(cfg *RoutingConfig, snapshot catalog.Snapshot, now time.Time) (*WindowGovernor, error) {
	if cfg == nil || cfg.Governance.Window == nil {
		return nil, errors.New("window governor needs governance.window in the routing config")
	}
	if now.IsZero() {
		return nil, errors.New("window governor needs the current time")
	}
	if err := validateWindowPolicy(cfg.Governance.Window); err != nil {
		return nil, err
	}
	if err := snapshot.Validate(); err != nil {
		return nil, fmt.Errorf("window snapshot: %w", err)
	}
	return &WindowGovernor{policy: cfg.Governance.Window.resolved(), readings: indexWindowReadings(snapshot), now: now}, nil
}

// indexWindowReadings keys the subscription_cli records with a usage window by model_id,
// keeping the newer of two readings.
func indexWindowReadings(snapshot catalog.Snapshot) map[string]catalog.Record {
	readings := make(map[string]catalog.Record)
	for i := 0; i < len(snapshot.Records) && i < catalog.MaxSnapshotRecords; i++ {
		record := snapshot.Records[i]
		if record.AccessPath != catalog.AccessSubscriptionCLI || record.UsageWindow == nil {
			continue
		}
		if previous, ok := readings[record.ModelID]; ok && !record.Provenance.FetchedAt.After(previous.Provenance.FetchedAt) {
			continue
		}
		readings[record.ModelID] = record
	}
	return readings
}

// Decide returns the verdict for one account. A nil governor, an undeclared account, a
// missing, stale, future-dated or incomplete reading, or a reading of an ended window is
// unknown. The reason always names the window.
func (g *WindowGovernor) Decide(account string) WindowDecision {
	decision := WindowDecision{Level: WindowUnknown, Account: account}
	if g == nil {
		return decision.explain("no window readings supplied")
	}
	entry, ok := g.policy.Accounts[account]
	if !ok {
		return decision.explain("account not declared in governance.window.accounts")
	}
	decision.Record = entry.WindowRecord
	reading, ok := g.readings[entry.WindowRecord]
	if !ok {
		return decision.explain("no subscription_cli reading with a usage window for this record")
	}
	decision.Source = reading.Provenance.Source
	age := int64(g.now.Sub(reading.Provenance.FetchedAt) / time.Second)
	decision.AgeSeconds = &age
	if gap := g.freshnessGap(reading); gap != "" {
		return decision.explain(gap)
	}
	if gap := valueGap(*reading.UsageWindow); gap != "" {
		return decision.explain(gap)
	}
	return g.classify(decision, *reading.UsageWindow)
}

// explain sets the reason, prefixed with the window it is about and suffixed with its source.
func (d WindowDecision) explain(reason string) WindowDecision {
	window := "window"
	if d.Record != "" {
		window += " " + d.Record
	}
	d.Reason = fmt.Sprintf("%s (account %s) %s: %s", window, d.Account, d.Level, reason)
	if d.ResetsAt != nil {
		d.Reason += ", resets " + d.ResetsAt.UTC().Format(time.RFC3339)
	}
	if d.Source != "" && d.AgeSeconds != nil {
		d.Reason += fmt.Sprintf(", source %s age %ds", d.Source, *d.AgeSeconds)
	}
	return d
}

func (g *WindowGovernor) freshnessGap(reading catalog.Record) string {
	age := g.now.Sub(reading.Provenance.FetchedAt)
	maxAge := time.Duration(g.policy.MaxReadingAgeSeconds) * time.Second
	if age < -windowClockSkew {
		return "reading is dated in the future"
	}
	if age > maxAge {
		return fmt.Sprintf("reading is stale, older than %ds", g.policy.MaxReadingAgeSeconds)
	}
	resets := reading.UsageWindow.ResetsAt
	if resets != nil && !resets.After(g.now) {
		return "the window's reset has passed, so the reading belongs to an ended window"
	}
	return ""
}

func valueGap(window catalog.UsageWindow) string {
	if window.UsedPercent == nil {
		return "reading has no used_percent"
	}
	if !finiteNonnegative(*window.UsedPercent) {
		return "reading has an invalid used_percent"
	}
	if window.BurnPercentPerHour != nil && !finiteNonnegative(*window.BurnPercentPerHour) {
		return "reading has an invalid burn_percent_per_hour"
	}
	if window.ActiveAgents != nil && *window.ActiveAgents < 0 {
		return "reading has a negative active_agents"
	}
	return ""
}

func (g *WindowGovernor) classify(decision WindowDecision, window catalog.UsageWindow) WindowDecision {
	used := *window.UsedPercent
	decision.UsedPercent = &used
	decision.ResetsAt = window.ResetsAt
	burn, hasBurn := g.projectedBurn(window)
	if hasBurn {
		decision.ProjectedBurnPercentPerHour = &burn
	}
	hoursToReset := -1.0
	if window.ResetsAt != nil {
		hoursToReset = window.ResetsAt.Sub(g.now).Hours()
		decision.PacePercentPerAgentHour = g.pace(used, hoursToReset, window.ActiveAgents)
	}
	level, reason := g.level(used, burn, hoursToReset)
	decision.Level = level
	if level == WindowWarn {
		decision.Advice = windowAdvice
	}
	decision = decision.explain(reason)
	if !hasBurn {
		decision.Reason += "; no burn reading, thresholds only"
	}
	return decision
}

// projectedBurn is max(measured burn, active agents x per-agent floor). The floor covers the
// minutes after a reset, when many agents wake together and the measured burn still reads low.
func (g *WindowGovernor) projectedBurn(window catalog.UsageWindow) (float64, bool) {
	burn, known := 0.0, false
	if window.BurnPercentPerHour != nil {
		burn, known = *window.BurnPercentPerHour, true
	}
	if window.ActiveAgents != nil && g.policy.PerAgentPercentPerHour > 0 {
		burn, known = math.Max(burn, float64(*window.ActiveAgents)*g.policy.PerAgentPercentPerHour), true
	}
	return burn, known
}

// level compares the used share with the lines, then the projected time to the stop line
// with the time to the reset. hoursToReset is negative when the reset is unknown.
func (g *WindowGovernor) level(used, burn, hoursToReset float64) (WindowLevel, string) {
	stop, warn := g.policy.StopPercent, g.policy.WarnPercent
	if used >= stop {
		return WindowStop, fmt.Sprintf("used %.1f%% >= stop %.1f%%", used, stop)
	}
	if burn > 0 && hoursToReset > 0 {
		hoursToStop := (stop - used) / burn
		if hoursToStop < hoursToReset {
			projection := fmt.Sprintf("stop line %.1f%% reached in %.0f min at %.1f%%/h, reset in %.0f min",
				stop, hoursToStop*60, burn, hoursToReset*60)
			if hoursToStop*60 <= float64(g.policy.StopHorizonMinutes) {
				return WindowStop, projection
			}
			return WindowWarn, projection
		}
	}
	if used >= warn {
		return WindowWarn, fmt.Sprintf("used %.1f%% >= warn %.1f%%", used, warn)
	}
	return WindowOK, fmt.Sprintf("used %.1f%% below warn %.1f%%", used, warn)
}

// pace is the burn per active agent that reaches the stop line exactly at the reset.
func (g *WindowGovernor) pace(used, hoursToReset float64, activeAgents *int) *float64 {
	agents := 1.0
	if activeAgents != nil && *activeAgents > 1 {
		agents = float64(*activeAgents)
	}
	pace := math.Max(g.policy.StopPercent-used, 0) / hoursToReset / agents
	return &pace
}

// windowFor returns the decision for the lane a candidate executes on; governed is false
// when that lane names no account.
func (a *ModelCapacityArbiter) windowFor(tier string, model ModelDescriptor) (WindowDecision, bool) {
	lane, ok := a.Config.Lanes[laneName(a.Config, tier, model)]
	if !ok || lane.Account == "" {
		return WindowDecision{}, false
	}
	return a.Windows.Decide(lane.Account), true
}

// admitWindow records a candidate the window refuses.
func (p *taskPass) admitWindow(model ModelDescriptor, decision WindowDecision) bool {
	if decision.Admits() {
		return true
	}
	if len(p.skips) < MaxRoutingModels {
		p.skips = append(p.skips, RouteSkip{Model: model.ID, Reason: decision.Reason})
	}
	return false
}
