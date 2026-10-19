package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ThreadSummary is the one stored summary of a thread. SourceHash names exactly the
// messages (and the instructions and model) it was written from, so a new message
// makes it stale without anything having to be recomputed to find out.
type ThreadSummary struct {
	AccountID    string
	ThreadID     string
	SourceHash   string
	PromptHash   string
	Model        string
	Text         string
	MessageCount int
	WrittenAt    time.Time
}

// PutThreadSummary stores a summary, replacing the thread's previous one: a thread
// shows one summary, and the cost of the old one is already in the ledger.
func (d *DBs) PutThreadSummary(ctx context.Context, s ThreadSummary) error {
	if s.AccountID == "" || s.ThreadID == "" || s.SourceHash == "" || s.Text == "" || s.Model == "" {
		return errors.New("put thread summary: a summary needs an account, thread, source hash, model and text")
	}
	if _, err := d.Mirror.Write.ExecContext(ctx, `
		INSERT INTO thread_summaries (account_id, thread_id, source_hash, prompt_hash, model, summary, message_count, written_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_id, thread_id) DO UPDATE SET
			source_hash=excluded.source_hash, prompt_hash=excluded.prompt_hash, model=excluded.model,
			summary=excluded.summary, message_count=excluded.message_count, written_at=excluded.written_at`,
		s.AccountID, s.ThreadID, s.SourceHash, s.PromptHash, s.Model, s.Text, s.MessageCount, formatTime(s.WrittenAt)); err != nil {
		return fmt.Errorf("put thread summary: %w", err)
	}
	return nil
}

// GetThreadSummary returns a thread's stored summary, if it has one.
func (d *DBs) GetThreadSummary(ctx context.Context, accountID, threadID string) (ThreadSummary, bool, error) {
	s := ThreadSummary{AccountID: accountID, ThreadID: threadID}
	var at string
	err := d.Mirror.Read.QueryRowContext(ctx, `
		SELECT source_hash, prompt_hash, model, summary, message_count, written_at
		FROM thread_summaries WHERE account_id = ? AND thread_id = ?`, accountID, threadID).
		Scan(&s.SourceHash, &s.PromptHash, &s.Model, &s.Text, &s.MessageCount, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return ThreadSummary{}, false, nil
	}
	if err != nil {
		return ThreadSummary{}, false, fmt.Errorf("get thread summary: %w", err)
	}
	if s.WrittenAt, err = parseTime(at); err != nil {
		return ThreadSummary{}, false, fmt.Errorf("get thread summary: %w", err)
	}
	return s, true, nil
}

// ThreadView is the part of a conversation a model may read.
type ThreadView struct {
	// ThreadID is empty for a message that belongs to no thread.
	ThreadID string
	// Messages are oldest first and at most the limit asked for: the first message
	// and the newest ones, because the start says what it is about and the end says
	// where it stands.
	Messages []Message
	// Total is how many live messages the thread holds, so a caller can say what
	// was left out.
	Total int
}

// liveThread is the messages of a thread the owner still has: not hidden, and not in
// Trash or Junk. Sent counts, because the owner's own replies are part of the story.
const liveThread = `account_id = ? AND thread_id = ? AND disabled_at IS NULL AND folder_id NOT IN (
	SELECT id FROM folders WHERE account_id = ? AND role IN ('trash', 'junk'))`

// ThreadMessagesFor returns the thread of one message of an account, bounded to
// limit messages. A message that is in no thread is its own.
func (d *DBs) ThreadMessagesFor(ctx context.Context, accountID, messageID string, limit int) (ThreadView, error) {
	if limit < 1 {
		return ThreadView{}, errors.New("thread messages: the limit must be at least 1")
	}
	opened, err := d.GetMessage(ctx, messageID)
	if err != nil {
		return ThreadView{}, err
	}
	if opened.AccountID != accountID {
		return ThreadView{}, ErrNotFound
	}
	if opened.ThreadID == "" {
		return ThreadView{Messages: []Message{opened}, Total: 1}, nil
	}

	view := ThreadView{ThreadID: opened.ThreadID}
	args := []any{accountID, opened.ThreadID, accountID}
	if err := d.Mirror.Read.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE `+liveThread, args...).Scan(&view.Total); err != nil {
		return ThreadView{}, fmt.Errorf("count thread %s: %w", opened.ThreadID, err)
	}

	var ids []string
	if view.Total <= limit {
		ids, err = d.threadIDs(ctx, args, "ASC", limit)
	} else {
		var first, newest []string
		if first, err = d.threadIDs(ctx, args, "ASC", 1); err == nil {
			newest, err = d.threadIDs(ctx, args, "DESC", limit-1)
		}
		// The newest come back newest first; the view is oldest first.
		for i := len(newest) - 1; i >= 0; i-- {
			first = append(first, newest[i])
		}
		ids = first
	}
	if err != nil {
		return ThreadView{}, err
	}
	for _, id := range ids {
		m, err := d.GetMessage(ctx, id)
		if errors.Is(err, ErrNotFound) {
			continue // hidden between the two reads
		}
		if err != nil {
			return ThreadView{}, err
		}
		view.Messages = append(view.Messages, m)
	}
	return view, nil
}

func (d *DBs) threadIDs(ctx context.Context, args []any, order string, limit int) ([]string, error) {
	rows, err := d.Mirror.Read.QueryContext(ctx,
		`SELECT id FROM messages WHERE `+liveThread+` ORDER BY COALESCE(date, '') `+order+`, id `+order+` LIMIT ?`,
		append(append([]any{}, args...), limit)...)
	if err != nil {
		return nil, fmt.Errorf("list thread messages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("list thread messages: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
