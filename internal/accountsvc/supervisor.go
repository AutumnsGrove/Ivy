// Package accountsvc runs and connects mailbox accounts: the supervisor owns
// each account's sync and outbox workers (so an account can be added or have its
// password replaced while Ivy runs), and the connector is the safe order of
// steps for an account typed into the app.
package accountsvc

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/AutumnsGrove/Ivy/events"
	"github.com/AutumnsGrove/Ivy/send"
	"github.com/AutumnsGrove/Ivy/smtp"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// Supervisor owns one sync worker and one outbox worker per account, all under
// one context. Starting an account that is already running replaces its workers,
// so two workers never hold connections to one mailbox.
type Supervisor struct {
	ctx       context.Context
	dbs       *store.DBs
	hub       *events.Hub
	submitter *smtp.Submitter

	mu      sync.Mutex
	running map[string]*handle
	wg      sync.WaitGroup
}

type handle struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// NewSupervisor starts nothing; Start does. Every worker ends when ctx does.
func NewSupervisor(ctx context.Context, dbs *store.DBs, hub *events.Hub) *Supervisor {
	return &Supervisor{
		ctx: ctx, dbs: dbs, hub: hub, submitter: smtp.New(),
		running: make(map[string]*handle),
	}
}

// Start runs the account's workers, first stopping any it already has. After
// the supervisor's context has ended it starts nothing.
func (s *Supervisor) Start(acct ivysync.Account) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return
	}
	if old := s.running[acct.ID]; old != nil {
		old.cancel()
		<-old.done
	}
	ctx, cancel := context.WithCancel(s.ctx)
	h := &handle{cancel: cancel, done: make(chan struct{})}
	s.running[acct.ID] = h

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(h.done)
		defer cancel()
		var inner sync.WaitGroup
		inner.Add(3)
		go func() { defer inner.Done(); s.runSync(ctx, acct) }()
		go func() { defer inner.Done(); s.runOutbox(ctx, acct) }()
		go func() { defer inner.Done(); s.runSend(ctx, acct) }()
		inner.Wait()
	}()
}

// Wait returns once every worker has stopped. Cancel the context first.
func (s *Supervisor) Wait() {
	// Held through the wait so a Start racing shutdown cannot wg.Add while the
	// count is draining; it blocks, then sees the ended context and returns.
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wg.Wait()
}

func (s *Supervisor) runSync(ctx context.Context, acct ivysync.Account) {
	worker := ivysync.NewWorker(ivysync.NewFetcher(s.dbs), acct,
		ivysync.WithWorkerSyncFunc(func(res ivysync.Result, err error) {
			s.hub.Publish(events.Event{Type: events.SyncState, AccountID: acct.ID})
			if err == nil && res.Stored > 0 {
				s.hub.Publish(events.Event{Type: events.MessageChanged, AccountID: acct.ID})
			}
			// A sweep that hid enough mail to look like a mistake is a Mirror
			// health alert, not a reason to stop syncing (ARCHITECTURE.md 4).
			for _, md := range res.MassDisabled {
				s.hub.Publish(events.Event{
					Type: events.HealthAlert, AccountID: acct.ID,
					Folder: md.Folder, Code: "mass_disable",
				})
			}
		}))
	if err := worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.WarnContext(ctx, "sync worker stopped", "account", acct.ID, "error", err)
	}
}

// runOutbox is the one writer to IMAP; it owns its own connection and publishes
// a hint whenever an op changes, so a screen can drop its optimistic overlay once
// the server has it.
func (s *Supervisor) runOutbox(ctx context.Context, acct ivysync.Account) {
	worker := ivysync.NewOutboxWorker(ivysync.NewFetcher(s.dbs), acct,
		ivysync.WithOutboxNotify(func(op store.OutboxOp) {
			s.hub.Publish(events.Event{Type: events.OutboxState, AccountID: acct.ID})
			if op.State == store.OutboxDone {
				s.hub.Publish(events.Event{Type: events.MessageChanged, AccountID: acct.ID})
			}
		}))
	if err := worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.WarnContext(ctx, "outbox worker stopped", "account", acct.ID, "error", err)
	}
}

// runSend drains the account's send queue and files the Sent copies. It is the
// only SMTP user; a state change is a `send.state` hint for the compose screen.
func (s *Supervisor) runSend(ctx context.Context, acct ivysync.Account) {
	worker := send.NewWorker(s.dbs, send.Account{
		ID: acct.ID, Address: acct.Address,
		Host: acct.SMTPHost, Port: acct.SMTPPort,
		Username: acct.Username, Password: acct.Password, Insecure: acct.Insecure,
	}, s.submitter, send.WithStateFunc(func(store.SendMessage) {
		s.hub.Publish(events.Event{Type: events.SendState, AccountID: acct.ID})
	}))
	if err := worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.WarnContext(ctx, "send worker stopped", "account", acct.ID, "error", err)
	}
}
