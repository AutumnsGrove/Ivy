// Package waiting lists the threads where the owner wrote last and has heard nothing
// back (5e.2). Which threads those are is plain data; the one thing a model adds is
// whether the owner's last message was waiting for an answer at all, so a "thanks!"
// does not sit on the list for a week. It runs when the list is opened, never in the
// background, and it only reads and keeps text: nothing here sends, moves or deletes.
package waiting

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/AutumnsGrove/Ivy/jev"
	"github.com/AutumnsGrove/Ivy/store"
)

const (
	// Feature is the switch Waiting on ships dark behind.
	Feature = "waiting_on"
	// Question is the Jev question that says a sent message expects an answer.
	Question = "expects_reply"
	// SettingDays is how many days of silence make a sent message a wait.
	SettingDays = "waiting_on.days"

	// DefaultDays is the operator's choice (5e decision 4); MaxDays keeps a typo from
	// meaning "never".
	DefaultDays = 3
	MaxDays     = 60

	// MaxCandidates bounds how many sent messages one open considers.
	MaxCandidates = 200
	// MaxAskPerList bounds the paid calls one open may make, longest wait first. The rest
	// are asked on later opens, so a large backlog is never one surprise.
	MaxAskPerList = 12
	// MaxListed bounds the list itself.
	MaxListed = 100
)

// Item is one thread the owner is waiting on, shown by the sent message that began the wait.
type Item struct {
	AccountID  string
	MessageID  string
	ContentKey string
	ThreadID   string
	Subject    string
	To         []store.Address
	SentAt     time.Time
	// Days is whole days since the message was sent.
	Days int
}

// Engine is what the service needs of jev.Engine, declared here at the point of use.
type Engine interface {
	Decide(ctx context.Context, m jev.Message) (jev.Outcome, error)
	Odds(ctx context.Context, accountID, contentKey string) (jev.Odds, error)
}

// Gate is what the service needs of llm.Gate.
type Gate interface {
	CanRun(ctx context.Context, accountID, feature string) bool
}

// Store is the mirror, the operator's "stop waiting" list and the settings. Writing to
// that list is the gateway's job, on the operator's click.
type Store interface {
	ListAccounts(ctx context.Context) ([]store.Account, error)
	WaitingOnCandidates(ctx context.Context, accountID string, sentBefore time.Time, limit int) ([]store.WaitingCandidate, error)
	StoppedWaitingRefs(ctx context.Context, accountID string) ([]store.ContentRef, error)
	GetMessage(ctx context.Context, id string) (store.Message, error)
	GetSetting(ctx context.Context, accountID, key string) (string, bool, error)
}

// Service builds the Waiting on list.
type Service struct {
	engine Engine
	gate   Gate
	st     Store
	now    func() time.Time

	// mu serialises opens so two screens opening at once make one set of paid calls.
	mu sync.Mutex
}

// NewService builds the service. A nil gate or engine is safe: the list is empty.
func NewService(engine Engine, gate Gate, st Store) *Service {
	return &Service{engine: engine, gate: gate, st: st, now: time.Now}
}

// List is the threads an account, or every account when accountID is empty, is waiting
// on, longest wait first. The error is for the caller to log: a cap, a refusal or a
// provider failure leaves the list as far as it is known, never an error screen.
func (s *Service) List(ctx context.Context, accountID string) ([]Item, error) {
	if s.engine == nil || s.gate == nil {
		return nil, nil
	}
	accounts := []string{accountID}
	if accountID == "" {
		all, err := s.st.ListAccounts(ctx)
		if err != nil {
			return nil, fmt.Errorf("waiting on: %w", err)
		}
		accounts = accounts[:0]
		for _, a := range all {
			accounts = append(accounts, a.ID)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []Item
	for _, id := range accounts {
		items, err := s.account(ctx, id)
		if err != nil {
			return out, err
		}
		out = append(out, items...)
	}
	sortLongestFirst(out)
	if len(out) > MaxListed {
		out = out[:MaxListed]
	}
	return out, nil
}

func (s *Service) account(ctx context.Context, accountID string) ([]Item, error) {
	if !s.gate.CanRun(ctx, accountID, Feature) {
		return nil, nil
	}
	now := s.now()
	cutoff := now.Add(-time.Duration(s.days(ctx, accountID)) * 24 * time.Hour)
	cands, err := s.st.WaitingOnCandidates(ctx, accountID, cutoff, MaxCandidates)
	if err != nil {
		return nil, fmt.Errorf("waiting on: %w", err)
	}
	stopped, err := s.st.StoppedWaitingRefs(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("waiting on: %w", err)
	}
	skip := make(map[store.ContentRef]bool, len(stopped))
	for _, r := range stopped {
		skip[r] = true
	}

	var out []Item
	asks := 0
	asking := true
	for _, c := range cands {
		if skip[store.ContentRef{AccountID: accountID, ContentKey: c.ContentKey}] {
			continue
		}
		verdict, known, err := s.known(ctx, accountID, c.ContentKey)
		if err != nil {
			return out, err
		}
		if !known && asking && asks < MaxAskPerList {
			asks++
			var askErr error
			if verdict, askErr = s.ask(ctx, accountID, c); askErr != nil {
				// Stop paying for this open; what is already known still lists.
				asking = false
				if !jev.IsOffRefusal(askErr) {
					slog.WarnContext(ctx, "waiting on: could not ask whether a sent message expects an answer", "account", accountID, "error", askErr)
				}
			}
		}
		if !verdict {
			continue
		}
		m, err := s.st.GetMessage(ctx, c.MessageID)
		if errors.Is(err, store.ErrNotFound) {
			continue // hidden since the candidates were read
		}
		if err != nil {
			return out, fmt.Errorf("waiting on: %w", err)
		}
		out = append(out, Item{
			AccountID: accountID, MessageID: m.ID, ContentKey: c.ContentKey, ThreadID: c.ThreadID,
			Subject: m.Subject, To: m.To, SentAt: c.Date, Days: int(now.Sub(c.Date) / (24 * time.Hour)),
		})
	}
	return out, nil
}

// known reads the kept answer: whether the message expects a reply under the question
// as it stands, and whether there is an answer (or a settled failure) to read at all.
func (s *Service) known(ctx context.Context, accountID, contentKey string) (expects, settled bool, err error) {
	odds, err := s.engine.Odds(ctx, accountID, contentKey)
	if err != nil {
		return false, false, fmt.Errorf("waiting on: %w", err)
	}
	for _, v := range odds.Answers {
		if v.QuestionID == Question {
			return v.Acts(), true, nil
		}
	}
	for _, u := range odds.Unanswered {
		if u.QuestionID == Question {
			return false, true, nil
		}
	}
	return false, false, nil
}

// ask puts the question to Jev about one sent message and reads the answer it kept.
func (s *Service) ask(ctx context.Context, accountID string, c store.WaitingCandidate) (bool, error) {
	m, err := s.st.GetMessage(ctx, c.MessageID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if _, err := s.engine.Decide(ctx, jev.FromStored(m, store.RoleSent, nil)); err != nil {
		return false, err
	}
	expects, _, err := s.known(ctx, accountID, c.ContentKey)
	return expects, err
}

// days is the account's silence line, falling back to the global setting and then the
// default; a value that is not a whole number from 1 to MaxDays is ignored.
func (s *Service) days(ctx context.Context, accountID string) int {
	for _, scope := range []string{accountID, ""} {
		v, ok, err := s.st.GetSetting(ctx, scope, SettingDays)
		if err != nil || !ok {
			continue
		}
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= MaxDays {
			return n
		}
	}
	return DefaultDays
}

func sortLongestFirst(items []Item) {
	sort.SliceStable(items, func(i, j int) bool { return items[i].SentAt.Before(items[j].SentAt) })
}
