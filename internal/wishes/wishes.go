// Package wishes stores a private local wish and poll ledger with CAS updates.
package wishes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/notebook"
)

const (
	SchemaVersion = 1
	MaxBytes      = 1 << 20
	DefaultWishes = 256
	DefaultPolls  = 128
	DefaultVoters = 1024
)

type Policy struct {
	MaxWishes        int  `json:"max_wishes"`
	MaxPolls         int  `json:"max_polls"`
	MaxVoters        int  `json:"max_voters"`
	AllowVoteChanges bool `json:"allow_vote_changes"`
	AllowWithdrawal  bool `json:"allow_withdrawal"`
}
type WishDraft struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Target      string `json:"target"`
	Title       string `json:"title"`
	Description string `json:"description"`
}
type Choice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type PollDraft struct {
	ID       string   `json:"id"`
	WishID   string   `json:"wish_id"`
	Question string   `json:"question"`
	Choices  []Choice `json:"choices"`
}
type Wish struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Target      string `json:"target"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
}
type Poll struct {
	ID       string   `json:"id"`
	WishID   string   `json:"wish_id"`
	Question string   `json:"question"`
	Choices  []Choice `json:"choices"`
	Closed   bool     `json:"closed"`
	Tallies  []Tally  `json:"tallies"`
}
type Tally struct {
	ChoiceID string `json:"choice_id"`
	Votes    int    `json:"votes"`
}
type Ballot struct {
	PollID   string `json:"poll_id"`
	ChoiceID string `json:"choice_id"`
	ActorID  string `json:"actor_id"`
}
type Ledger struct {
	SchemaVersion  int               `json:"schema_version"`
	Revision       uint64            `json:"revision"`
	Policy         Policy            `json:"policy"`
	Wishes         []Wish            `json:"wishes"`
	Polls          []Poll            `json:"polls"`
	CurrentBallots map[string]Ballot `json:"current_ballots"`
}
type Request struct {
	Action           string     `json:"action"`
	ExpectedRevision *uint64    `json:"expected_revision,omitempty"`
	Wish             *WishDraft `json:"wish,omitempty"`
	Poll             *PollDraft `json:"poll,omitempty"`
	WishID           string     `json:"wish_id,omitempty"`
	PollID           string     `json:"poll_id,omitempty"`
	ActorID          string     `json:"actor_id,omitempty"`
	ChoiceIDs        []string   `json:"choice_ids,omitempty"`
	Status           string     `json:"status,omitempty"`
	Policy           *Policy    `json:"policy,omitempty"`
}

func DecodeRequest(data []byte) (Request, error) {
	if err := checkRequestJSON(data); err != nil {
		return Request{}, fmt.Errorf("request contract: %w", err)
	}
	var request Request
	if err := notebook.Decode(data, &request); err != nil {
		return request, fmt.Errorf("decode wish request: %w", err)
	}
	if err := validateRequest(request); err != nil {
		return Request{}, err
	}
	return request, nil
}

func Read(ctx context.Context, path string) (*Ledger, error) {
	if ctx == nil {
		return nil, errors.New("wish read requires context")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	data, exists, err := contextopt.ObserveSnapshot(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("observe wish ledger: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("wish ledger missing: %s", path)
	}
	return decodeLedger(data)
}

func Apply(ctx context.Context, path string, request Request) (*Ledger, error) {
	if ctx == nil {
		return nil, errors.New("wish apply requires context")
	}
	if err := validateRequest(request); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	before, exists, err := contextopt.ObserveSnapshot(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("observe wish ledger: %w", err)
	}
	if request.Action == "init" {
		if exists {
			return nil, errors.New("wish ledger already exists")
		}
		return initializeStore(ctx, path, request.Policy)
	}
	if !exists {
		return nil, errors.New("wish ledger missing; initialize explicitly")
	}
	return updateStore(ctx, path, before, request)
}

func updateStore(ctx context.Context, path string, before []byte, request Request) (*Ledger, error) {
	ledger, err := decodeLedger(before)
	if err != nil {
		return nil, err
	}
	if *request.ExpectedRevision != ledger.Revision {
		return nil, fmt.Errorf("stale wish ledger revision: expected %d, current %d", *request.ExpectedRevision, ledger.Revision)
	}
	if ledger.Revision == ^uint64(0) {
		return nil, errors.New("wish ledger revision exhausted")
	}
	if err := mutate(ledger, request); err != nil {
		return nil, err
	}
	ledger.Revision++
	for i := range ledger.Polls {
		ledger.Polls[i].Tallies = nil
	}
	if err := validateLedger(ledger); err != nil {
		return nil, err
	}
	data, err := encodeLedger(ledger)
	if err != nil {
		return nil, err
	}
	if err := contextopt.ReplaceSnapshot(ctx, path, data, contextopt.ReplaceOptions{Expected: before, Exists: true, Mode: 0o600}); err != nil {
		return nil, err
	}
	return ledger, nil
}

func initializeStore(ctx context.Context, path string, policy *Policy) (*Ledger, error) {
	ledger := newLedger(policy)
	data, err := encodeLedger(ledger)
	if err != nil {
		return nil, err
	}
	if err := contextopt.EnsureDirectory(ctx, filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := contextopt.ReplaceSnapshot(ctx, path, data, contextopt.ReplaceOptions{Mode: 0o600}); err != nil {
		return nil, err
	}
	return ledger, nil
}
func newLedger(policy *Policy) *Ledger {
	p := Policy{MaxWishes: DefaultWishes, MaxPolls: DefaultPolls, MaxVoters: DefaultVoters, AllowVoteChanges: true, AllowWithdrawal: true}
	if policy != nil {
		p = *policy
	}
	return &Ledger{SchemaVersion: SchemaVersion, Revision: 1, Policy: p, Wishes: []Wish{}, Polls: []Poll{}, CurrentBallots: map[string]Ballot{}}
}

func encodeLedger(ledger *Ledger) ([]byte, error) {
	if err := validateLedger(ledger); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(data)+1 > MaxBytes {
		return nil, errors.New("wish ledger exceeds 1 MiB")
	}
	return append(data, '\n'), nil
}

func decodeLedger(data []byte) (*Ledger, error) {
	if err := checkLedgerJSON(data); err != nil {
		return nil, fmt.Errorf("ledger contract: %w", err)
	}
	var ledger Ledger
	if err := notebook.Decode(data, &ledger); err != nil {
		return nil, fmt.Errorf("decode wish ledger: %w", err)
	}
	if err := validateLedger(&ledger); err != nil {
		return nil, err
	}
	return &ledger, nil
}

func validateRequest(r Request) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := checkRequestJSON(data); err != nil {
		return err
	}
	if r.Policy != nil {
		return validatePolicy(*r.Policy)
	}
	if r.Action == "vote" && len(r.ChoiceIDs) != 1 {
		return errors.New("vote requires exactly one choice")
	}
	return nil
}

func validatePolicy(p Policy) error {
	if p.MaxWishes < 1 || p.MaxWishes > DefaultWishes || p.MaxPolls < 1 || p.MaxPolls > DefaultPolls || p.MaxVoters < 1 || p.MaxVoters > DefaultVoters {
		return errors.New("wish policy limits are out of bounds")
	}
	return nil
}
func text(s string) bool {
	return s != "" && len(s) <= 8192 && utf8.ValidString(s) && strings.TrimSpace(s) != "" && strings.IndexFunc(s, func(r rune) bool { return (r < 0x20 && r != '\t' && r != '\n' && r != '\r') || r == 0x7f }) < 0
}
func identifier(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !identifierRune(r) {
			return false
		}
	}
	return true
}
func identifierRune(r rune) bool {
	return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r)
}
func localActor(s string) bool {
	return strings.HasPrefix(s, "local:") && identifier(strings.TrimPrefix(s, "local:"))
}
func validKind(s string) bool {
	return map[string]bool{"repository": true, "skill": true, "template": true, "library": true, "framework": true, "extension": true}[s]
}
func validStatus(s string) bool {
	return map[string]bool{"proposed": true, "accepted": true, "planned": true, "implemented": true, "declined": true}[s]
}

func validateLedger(l *Ledger) error {
	if l.SchemaVersion != SchemaVersion || l.Revision == 0 || validatePolicy(l.Policy) != nil || l.CurrentBallots == nil {
		return errors.New("invalid wish ledger schema, revision or policy")
	}
	if len(l.Wishes) > l.Policy.MaxWishes || len(l.Polls) > l.Policy.MaxPolls || len(l.CurrentBallots) > l.Policy.MaxVoters {
		return errors.New("wish ledger exceeds configured limits")
	}
	wishes, err := validateWishes(l.Wishes)
	if err != nil {
		return err
	}
	polls, err := validatePolls(l.Polls, wishes)
	if err != nil {
		return err
	}
	if err := validateBallots(l.CurrentBallots, polls); err != nil {
		return err
	}
	return refreshTallies(l)
}

func validateWishes(items []Wish) (map[string]bool, error) {
	seen := make(map[string]bool, len(items))
	for _, w := range items {
		if !validWish(w) || seen[w.ID] {
			return nil, errors.New("invalid or duplicate wish")
		}
		seen[w.ID] = true
	}
	return seen, nil
}

func validWish(w Wish) bool {
	return identifier(w.ID) && validKind(w.Kind) && text(w.Target) && text(w.Title) && text(w.Description) && validStatus(w.Status)
}

func validatePolls(items []Poll, wishes map[string]bool) (map[string]map[string]bool, error) {
	seen := make(map[string]map[string]bool, len(items))
	for _, p := range items {
		if !identifier(p.ID) || !wishes[p.WishID] || !text(p.Question) || seen[p.ID] != nil {
			return nil, errors.New("invalid or duplicate poll")
		}
		choices, err := validateChoices(p.Choices)
		if err != nil {
			return nil, err
		}
		seen[p.ID] = choices
	}
	return seen, nil
}

func validateChoices(choices []Choice) (map[string]bool, error) {
	if len(choices) < 2 || len(choices) > 10 {
		return nil, errors.New("poll requires 2..10 choices")
	}
	seen := make(map[string]bool, len(choices))
	for _, c := range choices {
		if !identifier(c.ID) || !text(c.Label) || seen[c.ID] {
			return nil, errors.New("invalid or duplicate poll choice")
		}
		seen[c.ID] = true
	}
	return seen, nil
}

func validateBallots(ballots map[string]Ballot, polls map[string]map[string]bool) error {
	for key, b := range ballots {
		if !localActor(b.ActorID) || key != b.PollID+":"+b.ActorID || !polls[b.PollID][b.ChoiceID] {
			return errors.New("invalid current ballot or choice")
		}
	}
	return nil
}

func refreshTallies(l *Ledger) error {
	for i := range l.Polls {
		if err := refreshPollTally(&l.Polls[i], l.CurrentBallots); err != nil {
			return err
		}
	}
	return nil
}

func refreshPollTally(poll *Poll, ballots map[string]Ballot) error {
	counts := make(map[string]int, len(poll.Choices))
	for _, b := range ballots {
		if b.PollID == poll.ID {
			counts[b.ChoiceID]++
		}
	}
	expected := make([]Tally, len(poll.Choices))
	for i, c := range poll.Choices {
		expected[i] = Tally{ChoiceID: c.ID, Votes: counts[c.ID]}
	}
	if poll.Tallies != nil {
		if len(poll.Tallies) != len(expected) {
			return errors.New("invalid poll tallies")
		}
		for i := range expected {
			if poll.Tallies[i] != expected[i] {
				return errors.New("poll tallies do not match ballots")
			}
		}
	}
	poll.Tallies = expected
	return nil
}

func mutate(l *Ledger, r Request) error {
	switch r.Action {
	case "add-wish":
		return addWish(l, r.Wish)
	case "set-status":
		return setWishStatus(l, r.WishID, r.Status)
	case "open-poll":
		return openPoll(l, r.Poll)
	case "vote":
		return castVote(l, r.PollID, r.ActorID, r.ChoiceIDs[0])
	case "withdraw":
		return withdrawVote(l, r.PollID, r.ActorID)
	case "close-poll":
		return closePoll(l, r.PollID)
	}
	return nil
}
func addWish(l *Ledger, d *WishDraft) error {
	if d == nil || !identifier(d.ID) || !validKind(d.Kind) || !text(d.Target) || !text(d.Title) || !text(d.Description) || len(l.Wishes) >= l.Policy.MaxWishes {
		return errors.New("invalid or excessive wish")
	}
	for _, w := range l.Wishes {
		if w.ID == d.ID {
			return errors.New("duplicate wish id")
		}
	}
	l.Wishes = append(l.Wishes, Wish{d.ID, d.Kind, d.Target, d.Title, d.Description, "proposed"})
	return nil
}
func setWishStatus(l *Ledger, id, status string) error {
	if !identifier(id) || !validStatus(status) {
		return errors.New("invalid wish status request")
	}
	for i := range l.Wishes {
		if l.Wishes[i].ID == id {
			l.Wishes[i].Status = status
			return nil
		}
	}
	return errors.New("wish not found")
}
func openPoll(l *Ledger, d *PollDraft) error {
	if d == nil || len(l.Polls) >= l.Policy.MaxPolls {
		return errors.New("invalid or excessive poll")
	}
	p := Poll{ID: d.ID, WishID: d.WishID, Question: d.Question, Choices: append([]Choice(nil), d.Choices...)}
	l.Polls = append(l.Polls, p)
	return validateLedger(l)
}

func findOpenPoll(l *Ledger, id string) (*Poll, error) {
	for i := range l.Polls {
		if l.Polls[i].ID == id && !l.Polls[i].Closed {
			return &l.Polls[i], nil
		}
	}
	return nil, errors.New("poll unavailable or closed")
}

func containsChoice(poll *Poll, id string) bool {
	for _, c := range poll.Choices {
		if c.ID == id {
			return true
		}
	}
	return false
}

func castVote(l *Ledger, pollID, actor, choice string) error {
	if !identifier(pollID) || !localActor(actor) || !identifier(choice) {
		return errors.New("vote requires local actor and one choice")
	}
	poll, err := findOpenPoll(l, pollID)
	if err != nil {
		return err
	}
	if !containsChoice(poll, choice) {
		return errors.New("choice not found")
	}
	key := pollID + ":" + actor
	if err := checkVotePolicy(l, key); err != nil {
		return err
	}
	l.CurrentBallots[key] = Ballot{pollID, choice, actor}
	return nil
}

func checkVotePolicy(l *Ledger, key string) error {
	_, exists := l.CurrentBallots[key]
	if exists && !l.Policy.AllowVoteChanges {
		return errors.New("vote changes are disabled")
	}
	if !exists && len(l.CurrentBallots) >= l.Policy.MaxVoters {
		return errors.New("voter limit exceeded")
	}
	return nil
}

func withdrawVote(l *Ledger, pollID, actor string) error {
	if !l.Policy.AllowWithdrawal || !identifier(pollID) || !localActor(actor) {
		return errors.New("withdrawal is disabled or invalid")
	}
	if _, err := findOpenPoll(l, pollID); err != nil {
		return err
	}
	key := pollID + ":" + actor
	if _, ok := l.CurrentBallots[key]; !ok {
		return errors.New("current ballot not found")
	}
	delete(l.CurrentBallots, key)
	return nil
}
func closePoll(l *Ledger, id string) error {
	if !identifier(id) {
		return errors.New("invalid poll id")
	}
	for i := range l.Polls {
		if l.Polls[i].ID == id {
			l.Polls[i].Closed = true
			return nil
		}
	}
	return errors.New("poll not found")
}
