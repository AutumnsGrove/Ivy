package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// Draft version states. `saving` is the live one: the outbox op that files the
// version is not terminal yet. `failed` means that op failed for good, so the
// version never reached the server; as the head it is still the operator's only
// copy, so it is listed and kept until a newer save, a send or a discard.
const (
	DraftSaving    = "saving"
	DraftSaved     = "saved"
	DraftFailed    = "failed"
	DraftSent      = "sent"
	DraftDiscarded = "discarded"
)

// Draft list bounds (STANDARDS.md 4a): a page, never an unbounded read.
const (
	DefaultDraftListLimit = 50
	MaxDraftListLimit     = 200
)

// ErrDraftConflict reports a save that began from a version another tab has
// already replaced. The current head is returned with it so the losing tab can
// show the newer content.
var ErrDraftConflict = errors.New("draft changed elsewhere")

// Draft is one saved compose version. The head is the highest live version of a
// draft_id; the body is immutable so a live op always files the bytes it was
// committed with (docs/handoffs/2026-10-06-4d-drafts-design.md). The built MIME
// is not on this struct: it is large, so it is read by DraftBodyForOp only by the
// callers that need it, never by a list.
type Draft struct {
	ID           string
	DraftID      string
	AccountID    string
	Version      int
	MessageID    string
	ContentKey   string
	Supersedes   string
	Subject      string
	To           []string
	DestFolderID string
	Compose      []byte
	State        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// SaveDraftInput is one autosave. A retried request reuses ID and returns the
// stored version; BaseVersion is the version the caller last saw, 0 for a new
// draft.
type SaveDraftInput struct {
	ID           string
	DraftID      string
	AccountID    string
	DestFolderID string
	MessageID    string
	Subject      string
	To           []string
	Compose      []byte
	Body         []byte
	BaseVersion  int
	OpID         string
	Now          time.Time
}

// DraftBody is the material a draft op files: the Message-ID the idempotency
// search uses and the built MIME bytes.
type DraftBody struct {
	AccountID string
	MessageID string
	Body      []byte
}

const draftSelect = `SELECT id, draft_id, account_id, version, message_id, content_key,
	supersedes, subject, to_addrs, dest_folder_id, compose_json, state,
	created_at, updated_at FROM drafts`

func scanDraft(s scanner) (Draft, error) {
	var (
		d       Draft
		toAddrs string
		created string
		updated sql.NullString
	)
	if err := s.Scan(
		&d.ID, &d.DraftID, &d.AccountID, &d.Version, &d.MessageID, &d.ContentKey,
		&d.Supersedes, &d.Subject, &toAddrs, &d.DestFolderID, &d.Compose,
		&d.State, &created, &updated,
	); err != nil {
		return Draft{}, err
	}
	if err := json.Unmarshal([]byte(toAddrs), &d.To); err != nil {
		return Draft{}, fmt.Errorf("draft %s recipients: %w", d.ID, err)
	}
	var err error
	if d.CreatedAt, err = parseTime(created); err != nil {
		return Draft{}, fmt.Errorf("draft %s created_at: %w", d.ID, err)
	}
	if updated.Valid {
		if d.UpdatedAt, err = parseTime(updated.String); err != nil {
			return Draft{}, fmt.Errorf("draft %s updated_at: %w", d.ID, err)
		}
	}
	return d, nil
}

// DraftServerRow is one mirrored message in an account's Drafts folder: a draft
// created by another client, with no local compose row.
type DraftServerRow struct {
	ID         string
	AccountID  string
	ContentKey string
	MessageID  string
	Subject    string
	To         []Address
	Date       time.Time
}

// DraftsInFolder returns an account's mirrored Drafts-folder messages, newest
// first, for the drafts list's server-side entries. A disabled message is not
// listed.
func (d *DBs) DraftsInFolder(ctx context.Context, accountID string, limit int) ([]DraftServerRow, error) {
	if limit <= 0 {
		limit = DefaultDraftListLimit
	}
	limit = min(limit, MaxDraftListLimit)
	rows, err := d.Mirror.Read.QueryContext(ctx, `
		SELECT m.id, m.account_id, m.content_key, COALESCE(m.message_id_hdr, ''),
		       COALESCE(m.subject, ''), COALESCE(m.to_json, ''), COALESCE(m.date, '')
		FROM messages m JOIN folders f ON f.id = m.folder_id
		WHERE f.account_id = ? AND f.role = ? AND f.gone_at IS NULL AND m.disabled_at IS NULL
		ORDER BY COALESCE(m.date, '') DESC, m.id DESC LIMIT ?`,
		accountID, RoleDrafts, limit)
	if err != nil {
		return nil, fmt.Errorf("drafts in folder for %s: %w", accountID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []DraftServerRow
	for rows.Next() {
		var (
			r      DraftServerRow
			toJSON string
			date   string
		)
		if err := rows.Scan(&r.ID, &r.AccountID, &r.ContentKey, &r.MessageID, &r.Subject, &toJSON, &date); err != nil {
			return nil, fmt.Errorf("drafts in folder for %s: %w", accountID, err)
		}
		if err := decodeJSON(toJSON, &r.To); err != nil {
			r.To = nil
		}
		if date != "" {
			if t, err := parseTime(date); err == nil {
				r.Date = t
			}
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("drafts in folder for %s: %w", accountID, err)
	}
	return out, nil
}

// DraftTerminalRetention is how long a sent or discarded version row survives,
// so a recent history or undo can still resolve it. The queue's own pruning is
// the only deletion, and only of terminal rows.
const DraftTerminalRetention = 7 * 24 * time.Hour

// PruneDrafts removes terminal version rows older than the retention. It never
// touches a live (saving or saved) row, and a pending remove op does not need
// the row: it carries the Message-ID itself.
func (d *DBs) PruneDrafts(ctx context.Context, now time.Time) (int, error) {
	cutoff := formatTime(now.Add(-DraftTerminalRetention))
	tx, err := d.State.Write.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("prune drafts: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM drafts WHERE state IN (?, ?) AND updated_at < ?`,
		DraftSent, DraftDiscarded, cutoff).Scan(&n); err != nil {
		return 0, fmt.Errorf("prune drafts: %w", err)
	}
	hashes, err := deleteDraftRowsTx(ctx, tx,
		`SELECT body_hash FROM drafts WHERE state IN (?, ?) AND updated_at < ? AND body_hash != ''`,
		`DELETE FROM drafts WHERE state IN (?, ?) AND updated_at < ?`,
		DraftSent, DraftDiscarded, cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune drafts: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("prune drafts: %w", err)
	}
	d.releaseDraftBodies(ctx, hashes)
	return n, nil
}

// SaveDraft commits a new version and the outbox op that files it in one
// transaction, so a version is never durable without the op that will reach the
// server. A stale BaseVersion is ErrDraftConflict carrying the current head.
func (d *DBs) SaveDraft(ctx context.Context, in SaveDraftInput) (Draft, error) {
	draft, pruned, err := d.saveDraft(ctx, in)
	// The files of the rows this save dropped go once the save no longer holds the
	// lock that stops a sweep deleting a body whose row is not inserted yet.
	d.releaseDraftBodies(ctx, pruned)
	return draft, err
}

// saveDraft is SaveDraft's body. It also returns the body hashes of the rows it
// pruned so the caller can release their files.
func (d *DBs) saveDraft(ctx context.Context, in SaveDraftInput) (Draft, []string, error) {
	switch {
	case in.ID == "", in.DraftID == "", in.AccountID == "", in.MessageID == "", in.DestFolderID == "":
		return Draft{}, nil, errors.New("save draft: id, draft id, account, message id and destination are required")
	case in.OpID == "":
		return Draft{}, nil, errors.New("save draft: an op id is required")
	case in.Now.IsZero():
		return Draft{}, nil, errors.New("save draft: no timestamp")
	}
	toAddrs, err := json.Marshal(in.To)
	if err != nil {
		return Draft{}, nil, fmt.Errorf("save draft: recipients: %w", err)
	}

	// Held from before the body is written until the row is committed, so a sweep
	// can never see a body file whose row is about to be inserted as an orphan.
	d.draftMu.RLock()
	defer d.draftMu.RUnlock()

	// A retried request is answered before the body is written, so a retry does not
	// leave a second copy on disk. The transaction below checks again for a racer.
	switch prior, err := d.DraftVersion(ctx, in.ID); {
	case err == nil:
		if prior.AccountID != in.AccountID {
			return Draft{}, nil, fmt.Errorf("save draft: id %s belongs to another account", in.ID)
		}
		return prior, nil, nil
	case !errors.Is(err, ErrNotFound):
		return Draft{}, nil, err
	}
	bodyHash, _, err := d.DraftBodies.Put(ctx, bytes.NewReader(in.Body))
	if err != nil {
		return Draft{}, nil, fmt.Errorf("save draft %s: body: %w", in.ID, err)
	}

	tx, err := d.State.Write.BeginTx(ctx, nil)
	if err != nil {
		return Draft{}, nil, fmt.Errorf("save draft: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// A retried request is the same save and must not bump the version again.
	existing, err := scanDraft(tx.QueryRowContext(ctx, draftSelect+` WHERE id = ?`, in.ID))
	switch {
	case err == nil:
		if existing.AccountID != in.AccountID {
			return Draft{}, nil, fmt.Errorf("save draft: id %s belongs to another account", in.ID)
		}
		return existing, nil, tx.Commit()
	case !errors.Is(err, sql.ErrNoRows):
		return Draft{}, nil, fmt.Errorf("save draft: %w", err)
	}

	// The current head, whatever its state, decides the version and what the new
	// save supersedes. A sent or discarded head can be resumed: the next save is
	// a new version that also clears the old server copy.
	head, headErr := draftHeadTx(ctx, tx, in.AccountID, in.DraftID)
	var supersedes string
	version := 1
	switch {
	case headErr == nil:
		if head.Version != in.BaseVersion {
			return head, nil, ErrDraftConflict
		}
		version = head.Version + 1
		supersedes = head.MessageID
	case errors.Is(headErr, ErrNotFound):
		if in.BaseVersion != 0 {
			return Draft{}, nil, ErrDraftConflict
		}
	default:
		return Draft{}, nil, headErr
	}

	draft := Draft{
		ID: in.ID, DraftID: in.DraftID, AccountID: in.AccountID, Version: version,
		MessageID: in.MessageID, ContentKey: ContentKey(in.MessageID, nil),
		Supersedes: supersedes, Subject: in.Subject, To: in.To,
		DestFolderID: in.DestFolderID, Compose: in.Compose,
		State: DraftSaving, CreatedAt: in.Now, UpdatedAt: in.Now,
	}
	// The body column is NOT NULL, so a row whose body is on disk stores it empty.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO drafts (
			id, draft_id, account_id, version, message_id, content_key, supersedes,
			subject, to_addrs, dest_folder_id, compose_json, body, body_hash, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		draft.ID, draft.DraftID, draft.AccountID, draft.Version, draft.MessageID,
		draft.ContentKey, draft.Supersedes, draft.Subject, string(toAddrs),
		draft.DestFolderID, draft.Compose, []byte{}, bodyHash, draft.State,
		formatTime(draft.CreatedAt), formatTime(draft.UpdatedAt)); err != nil {
		return Draft{}, nil, fmt.Errorf("save draft %s: %w", in.ID, err)
	}

	supersedesList := []string{}
	if supersedes != "" {
		supersedesList = []string{supersedes}
	}
	if _, _, err := enqueueOutboxTx(ctx, tx, OutboxOp{
		ID: in.OpID, AccountID: in.AccountID, Kind: OutboxDraft,
		ContentKey: draft.ContentKey, SourceFolderID: in.DestFolderID,
		Expect: OutboxExpect{
			DestFolderID:   in.DestFolderID,
			FlagsAdd:       []string{`\Draft`},
			DraftID:        in.DraftID,
			DraftVersionID: in.ID,
			Supersedes:     supersedesList,
		},
		CreatedAt: in.Now,
	}); err != nil {
		// Includes ErrOutboxFull: the whole save rolls back, so nothing is
		// durable without the op that will file it. The body file is left for the
		// orphan sweep.
		return Draft{}, nil, err
	}

	// A superseded version whose op is terminal is no longer needed; a saving one
	// still has a live op that needs its bytes.
	pruned, err := deleteDraftRowsTx(ctx, tx,
		`SELECT body_hash FROM drafts
			WHERE account_id = ? AND draft_id = ? AND version < ? AND state != ? AND body_hash != ''`,
		`DELETE FROM drafts WHERE account_id = ? AND draft_id = ? AND version < ? AND state != ?`,
		in.AccountID, in.DraftID, version, DraftSaving)
	if err != nil {
		return Draft{}, nil, fmt.Errorf("save draft %s: prune: %w", in.ID, err)
	}

	if err := tx.Commit(); err != nil {
		return Draft{}, nil, fmt.Errorf("save draft %s: %w", in.ID, err)
	}
	return draft, pruned, nil
}

// deleteDraftRowsTx runs a literal SELECT of the body hashes and the matching
// literal DELETE with the same arguments, and returns the hashes so the caller
// can release the files after the commit. Whole statements are passed in, never
// assembled, so no SQL is built from strings.
func deleteDraftRowsTx(ctx context.Context, tx *sql.Tx, selectHashes, deleteRows string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, selectHashes, args...)
	if err != nil {
		return nil, err
	}
	var hashes []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			_ = rows.Close()
			return nil, err
		}
		hashes = append(hashes, h)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, deleteRows, args...); err != nil {
		return nil, err
	}
	return hashes, nil
}

// releaseDraftBodies removes body files no row references any more. It never
// waits: while a save is in flight a file may be about to gain a row, so it is
// left for the orphan sweep. The reference count is taken under the lock.
func (d *DBs) releaseDraftBodies(ctx context.Context, hashes []string) {
	if len(hashes) == 0 || !d.draftMu.TryLock() {
		return
	}
	defer d.draftMu.Unlock()
	for _, hash := range hashes {
		var refs int
		if err := d.State.Read.QueryRowContext(ctx, `SELECT count(*) FROM drafts WHERE body_hash = ?`, hash).Scan(&refs); err != nil || refs > 0 {
			continue
		}
		// A failed removal is only a leftover file; the orphan sweep retries it.
		_ = d.DraftBodies.Remove(hash)
	}
}

func draftHeadTx(ctx context.Context, tx *sql.Tx, accountID, draftID string) (Draft, error) {
	d, err := scanDraft(tx.QueryRowContext(ctx, draftSelect+`
		WHERE account_id = ? AND draft_id = ? ORDER BY version DESC LIMIT 1`, accountID, draftID))
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, fmt.Errorf("draft head %s/%s: %w", accountID, draftID, err)
	}
	return d, nil
}

// DraftHead returns the newest version of a draft whatever its state, or
// ErrNotFound.
func (d *DBs) DraftHead(ctx context.Context, accountID, draftID string) (Draft, error) {
	draft, err := scanDraft(d.State.Read.QueryRowContext(ctx, draftSelect+`
		WHERE account_id = ? AND draft_id = ? ORDER BY version DESC LIMIT 1`, accountID, draftID))
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, fmt.Errorf("draft head %s/%s: %w", accountID, draftID, err)
	}
	return draft, nil
}

// DraftVersion returns one version row by its id, or ErrNotFound.
func (d *DBs) DraftVersion(ctx context.Context, id string) (Draft, error) {
	draft, err := scanDraft(d.State.Read.QueryRowContext(ctx, draftSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, fmt.Errorf("draft %s: %w", id, err)
	}
	return draft, nil
}

// DraftBodyForOp returns the bytes a draft op should file for one version. A
// missing row is ErrNotFound; the op then fails rather than appending nothing.
func (d *DBs) DraftBodyForOp(ctx context.Context, versionID string) (DraftBody, error) {
	var (
		b    DraftBody
		hash string
	)
	err := d.State.Read.QueryRowContext(ctx,
		`SELECT account_id, message_id, body, body_hash FROM drafts WHERE id = ?`, versionID).
		Scan(&b.AccountID, &b.MessageID, &b.Body, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return DraftBody{}, ErrNotFound
	}
	if err != nil {
		return DraftBody{}, fmt.Errorf("draft body %s: %w", versionID, err)
	}
	if hash == "" {
		// A row saved before bodies moved to disk keeps its bytes inline.
		return b, nil
	}
	f, err := d.DraftBodies.Open(hash)
	if errors.Is(err, os.ErrNotExist) {
		// The file is gone (a restore, a manual clean): the op fails rather than
		// appending nothing.
		return DraftBody{}, ErrNotFound
	}
	if err != nil {
		return DraftBody{}, fmt.Errorf("draft body %s: %w", versionID, err)
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, MaxStoredDraftBytes+1))
	if err != nil {
		return DraftBody{}, fmt.Errorf("draft body %s: %w", versionID, err)
	}
	if len(body) > MaxStoredDraftBytes {
		return DraftBody{}, fmt.Errorf("draft body %s: over %d bytes", versionID, MaxStoredDraftBytes)
	}
	b.Body = body
	return b, nil
}

// MaxStoredDraftBytes bounds a draft body read back from disk: the 25 MiB
// attachment cap inflates by a third in base64, plus the text and headers.
const MaxStoredDraftBytes = 64 << 20

// SweepOrphanDraftBodies removes body files no draft row references: a file left
// by a crash between the write and the insert, by a refused save, or an
// interrupted write. It stands down while a save is in flight (see
// SweepOrphanUploads for why the lock makes an age threshold unnecessary).
func (d *DBs) SweepOrphanDraftBodies(ctx context.Context) (int, error) {
	return sweepOrphanBlobs(ctx, &d.draftMu, d.DraftBodies, d.State.Read,
		`SELECT DISTINCT body_hash FROM drafts WHERE body_hash != ''`)
}

// MarkDraftSaved records that the outbox op filed the version. Only a saving
// version can enter it.
func (d *DBs) MarkDraftSaved(ctx context.Context, versionID string, now time.Time) error {
	res, err := d.State.Write.ExecContext(ctx,
		`UPDATE drafts SET state = ?, updated_at = ? WHERE id = ? AND state = ?`,
		DraftSaved, formatTime(now), versionID, DraftSaving)
	if err != nil {
		return fmt.Errorf("mark draft saved %s: %w", versionID, err)
	}
	return rowsAffectedOrNotFound(res, "mark draft saved")
}

// MarkDraftFailed records that the outbox op for a version failed permanently, so
// the list can say it is not on the server. Only a saving version can fail.
func (d *DBs) MarkDraftFailed(ctx context.Context, versionID string, now time.Time) error {
	res, err := d.State.Write.ExecContext(ctx,
		`UPDATE drafts SET state = ?, updated_at = ? WHERE id = ? AND state = ?`,
		DraftFailed, formatTime(now), versionID, DraftSaving)
	if err != nil {
		return fmt.Errorf("mark draft failed %s: %w", versionID, err)
	}
	return rowsAffectedOrNotFound(res, "mark draft failed")
}

// MarkDraftSent records that the version left Drafts because the message was
// sent. It matches on the version's Message-ID so a newer edit is never touched.
func (d *DBs) MarkDraftSent(ctx context.Context, accountID, messageID string, now time.Time) error {
	res, err := d.State.Write.ExecContext(ctx, `
		UPDATE drafts SET state = ?, updated_at = ? WHERE account_id = ? AND message_id = ?
			AND state IN (?, ?, ?)`,
		DraftSent, formatTime(now), accountID, messageID, DraftSaving, DraftSaved, DraftFailed)
	if err != nil {
		return fmt.Errorf("mark draft sent %s: %w", messageID, err)
	}
	return rowsAffectedOrNotFound(res, "mark draft sent")
}

// DiscardDraft removes a draft the operator deleted: the head is marked
// discarded and an expunge-only draft op removes its server copy. It is
// ErrNotFound when there is no live draft.
func (d *DBs) DiscardDraft(ctx context.Context, accountID, draftID, opID string, now time.Time) (Draft, error) {
	tx, err := d.State.Write.BeginTx(ctx, nil)
	if err != nil {
		return Draft{}, fmt.Errorf("discard draft: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	head, err := scanDraft(tx.QueryRowContext(ctx, draftSelect+`
		WHERE account_id = ? AND draft_id = ? AND state IN (?, ?, ?)
		ORDER BY version DESC LIMIT 1`, accountID, draftID, DraftSaving, DraftSaved, DraftFailed))
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, fmt.Errorf("discard draft: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE drafts SET state = ?, updated_at = ? WHERE id = ?`,
		DraftDiscarded, formatTime(now), head.ID); err != nil {
		return Draft{}, fmt.Errorf("discard draft %s: %w", head.ID, err)
	}
	if _, _, err := enqueueOutboxTx(ctx, tx, OutboxOp{
		ID: opID, AccountID: accountID, Kind: OutboxDraft,
		ContentKey: head.ContentKey, SourceFolderID: head.DestFolderID,
		Expect: OutboxExpect{
			DestFolderID: head.DestFolderID,
			DraftID:      head.DraftID,
			Supersedes:   []string{head.MessageID},
			Remove:       true,
		},
		CreatedAt: now,
	}); err != nil {
		return Draft{}, err
	}
	if err := tx.Commit(); err != nil {
		return Draft{}, fmt.Errorf("discard draft %s: %w", head.ID, err)
	}
	head.State, head.UpdatedAt = DraftDiscarded, now
	return head, nil
}

// LiveDraftHeads returns one row per draft: the newest version whose state is
// live, newest first. A draft whose newest version was sent or discarded is not
// listed.
func (d *DBs) LiveDraftHeads(ctx context.Context, accountID string, limit int) ([]Draft, error) {
	if limit <= 0 {
		limit = DefaultDraftListLimit
	}
	limit = min(limit, MaxDraftListLimit)
	rows, err := d.State.Read.QueryContext(ctx, draftSelect+`
		WHERE account_id = ? AND state IN (?, ?, ?)
			AND version = (SELECT MAX(d2.version) FROM drafts d2
				WHERE d2.account_id = drafts.account_id AND d2.draft_id = drafts.draft_id)
		ORDER BY updated_at DESC, id DESC LIMIT ?`,
		accountID, DraftSaving, DraftSaved, DraftFailed, limit)
	if err != nil {
		return nil, fmt.Errorf("draft heads for %s: %w", accountID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []Draft
	for rows.Next() {
		draft, err := scanDraft(rows)
		if err != nil {
			return nil, fmt.Errorf("scan draft: %w", err)
		}
		out = append(out, draft)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("draft heads for %s: %w", accountID, err)
	}
	return out, nil
}

// rowsAffectedOrNotFound maps a zero-row UPDATE to ErrNotFound, the same
// contract the send and outbox state machines use.
func rowsAffectedOrNotFound(res sql.Result, what string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
