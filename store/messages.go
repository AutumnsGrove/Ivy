package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Address is one mail header address, stored as JSON so a rebuild keeps the
// display name as it arrived.
type Address struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
}

// AuthResults is the SPF/DKIM/DMARC trust signal parsed from a message's
// Authentication-Results headers (ARCHITECTURE.md 5). AuthservID names the
// trusted server that attested the verdicts, so the operator can see where the
// signal came from (or that a trusted header carried none).
type AuthResults struct {
	AuthservID string   `json:"authserv_id,omitempty"`
	SPF        string   `json:"spf,omitempty"`
	DKIM       string   `json:"dkim,omitempty"`
	DMARC      string   `json:"dmarc,omitempty"`
	Raw        []string `json:"raw,omitempty"`
}

// Message is one mirrored message row. BodyHTML is the server-sanitized HTML;
// RawBlob is the original RFC 822 bytes, stored so everything derived can be
// rebuilt.
type Message struct {
	ID        string
	AccountID string
	FolderID  string
	UID       uint32
	// UIDValidity is the folder's UIDVALIDITY when this row was stored. It is
	// part of the message identity, so a folder rebuild that reuses UID numbers
	// cannot collide with the disabled rows of the old validity.
	UIDValidity    uint32
	ContentKey     string
	MessageID      string
	InReplyTo      string
	References     string
	Subject        string
	From           Address
	To             []Address
	CC             []Address
	ReplyTo        []Address
	DeliveredTo    []Address
	Date           time.Time
	Size           int64
	Flags          []string
	InternalDate   time.Time
	HasAttachments bool
	// RawBlob holds the original bytes of a message small enough to keep in the
	// database. A larger one lives on disk instead: RawPath is its file, relative
	// to the data directory, and RawBlob is empty. Neither is set for a message
	// that was not downloaded (BodyStatus BodyTooLarge).
	RawBlob        []byte
	RawPath        string
	BodyStatus     string
	BodyText       string
	BodyHTML       string
	ThreadID       string
	Snippet        string
	AuthResults    AuthResults
	ParseErrors    []string
	DisabledAt     time.Time
	DisabledReason string
	// DisabledBlob is the content hash of this message's bytes in the blob store,
	// set when it is hidden and cleared when it reappears (ARCHITECTURE.md 9).
	DisabledBlob string
	Seen         bool
	// DerivedVersion is the pipeline version that last wrote this row's derived
	// data (body text, sanitised HTML, attachment rows). Only SetMessageDerived
	// changes it; UpsertMessage leaves it alone.
	DerivedVersion int
}

// Body statuses: whether the mirror holds a parsed body for the message.
const (
	// BodyOK means the body was downloaded and parsed (the default).
	BodyOK = "ok"
	// BodyTooLarge means the message exceeds the download limit, so only its
	// envelope is mirrored (STANDARDS.md 4a); the UI offers "open in webmail".
	BodyTooLarge = "too_large"
	// BodyUnparsed means the body is on disk or in raw_blob but could not be
	// read into text; the reasons are in ParseErrors.
	BodyUnparsed = "unparsed"
)

// UpsertMessage writes a message keyed by (folder, uid), so re-syncing a folder
// updates rows in place rather than duplicating them. A reappearing message
// has its disabled fields cleared, re-enabling it with its derived data intact
// (ARCHITECTURE.md 4).
//
// The derived columns (thread_id, body_text, snippet, has_attachments,
// parse_errors, body_status, body_html_sanitized, derived_version) are written
// on the first insert only; later layers own them (SetMessageThread,
// SetMessageDerived), and a row built from the envelope alone must not blank
// them. The raw bytes (raw_blob, raw_path) are the source everything is derived
// from, so a row that carries none keeps the ones already stored: nothing is
// ever erased.
func (d *DBs) UpsertMessage(ctx context.Context, m Message) error {
	from, err := marshalAddress(m.From)
	if err != nil {
		return err
	}
	to, err := marshalAddresses(m.To)
	if err != nil {
		return err
	}
	cc, err := marshalAddresses(m.CC)
	if err != nil {
		return err
	}
	replyTo, err := marshalAddresses(m.ReplyTo)
	if err != nil {
		return err
	}
	deliveredTo, err := marshalAddresses(m.DeliveredTo)
	if err != nil {
		return err
	}
	flags, err := marshalFlags(m.Flags)
	if err != nil {
		return err
	}
	authResults, err := marshalAuthResults(m.AuthResults)
	if err != nil {
		return err
	}
	parseErrors, err := marshalStrings(m.ParseErrors)
	if err != nil {
		return err
	}
	bodyStatus := m.BodyStatus
	if bodyStatus == "" {
		bodyStatus = BodyOK
	}

	_, err = d.Mirror.Write.ExecContext(ctx, `
		INSERT INTO messages (
			id, account_id, folder_id, uid, uidvalidity, content_key, message_id_hdr, in_reply_to,
			refs, subject, from_json, to_json, cc_json, reply_to_json, delivered_to_json,
			date, size, flags_json, internaldate, has_attachments, raw_blob, body_text,
			body_html_sanitized, thread_id, snippet, auth_results, parse_errors,
			disabled_at, disabled_reason, disabled_blob, seen, raw_path, body_status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(folder_id, uidvalidity, uid) DO UPDATE SET
			account_id=excluded.account_id, content_key=excluded.content_key,
			message_id_hdr=excluded.message_id_hdr, in_reply_to=excluded.in_reply_to,
			refs=excluded.refs, subject=excluded.subject, from_json=excluded.from_json,
			to_json=excluded.to_json, cc_json=excluded.cc_json,
			reply_to_json=excluded.reply_to_json, delivered_to_json=excluded.delivered_to_json,
			date=excluded.date, size=excluded.size, flags_json=excluded.flags_json,
			internaldate=excluded.internaldate,
			raw_blob=CASE WHEN length(excluded.raw_blob) > 0 THEN excluded.raw_blob ELSE messages.raw_blob END,
			raw_path=COALESCE(excluded.raw_path, messages.raw_path),
			auth_results=excluded.auth_results, disabled_at=excluded.disabled_at,
			disabled_reason=excluded.disabled_reason, disabled_blob=excluded.disabled_blob,
			seen=excluded.seen`,
		m.ID, m.AccountID, m.FolderID, m.UID, m.UIDValidity, m.ContentKey, m.MessageID, m.InReplyTo,
		m.References, m.Subject, from, to, cc, replyTo, deliveredTo,
		nullableTime(m.Date), m.Size, flags, nullableTime(m.InternalDate),
		m.HasAttachments, m.RawBlob, m.BodyText, m.BodyHTML, m.ThreadID, m.Snippet,
		authResults, parseErrors, nullableTime(m.DisabledAt), m.DisabledReason,
		m.DisabledBlob, slices.Contains(m.Flags, `\Seen`), nullableString(m.RawPath), bodyStatus,
	)
	if err != nil {
		return fmt.Errorf("upsert message %s: %w", m.ID, err)
	}
	return nil
}

// SetMessageThread records the conversation a message belongs to (chunk 2e).
// It is its own statement, not part of UpsertMessage, so a later sync of the
// same message cannot blank it.
func (d *DBs) SetMessageThread(ctx context.Context, id, threadID string) error {
	return d.setMessageColumn(ctx, `UPDATE messages SET thread_id = ? WHERE id = ?`, id, threadID)
}

// setMessageColumn runs a fixed single-column UPDATE statement, which takes the
// new value first and the message id second.
func (d *DBs) setMessageColumn(ctx context.Context, update, id, value string) error {
	res, err := d.Mirror.Write.ExecContext(ctx, update, value, id)
	if err != nil {
		return fmt.Errorf("update derived column of message %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update derived column of message %s: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetMessage returns one visible message or ErrNotFound. Disabled messages are
// hidden everywhere until they are restored.
func (d *DBs) GetMessage(ctx context.Context, id string) (Message, error) {
	row := d.Mirror.Read.QueryRowContext(ctx, messageSelect+` WHERE id = ? AND disabled_at IS NULL`, id)
	m, err := scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, fmt.Errorf("get message %s: %w", id, err)
	}
	return m, nil
}

// GetMessageByUID returns one visible message by its folder's unique key, the
// identity sync upserts on (ARCHITECTURE.md 3). Disabled messages are hidden.
func (d *DBs) GetMessageByUID(ctx context.Context, folderID string, uid uint32) (Message, error) {
	row := d.Mirror.Read.QueryRowContext(ctx,
		messageSelect+` WHERE folder_id = ? AND uid = ? AND disabled_at IS NULL`, folderID, uid)
	m, err := scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, fmt.Errorf("get message %s/%d: %w", folderID, uid, err)
	}
	return m, nil
}

// MessageUIDs returns the UIDs already mirrored in a folder, so a resumed
// fetch skips bodies it already holds instead of re-downloading them. Disabled
// rows are excluded: if the server has the message again, a fresh fetch must
// upsert it and clear the disabled state.
func (d *DBs) MessageUIDs(ctx context.Context, folderID string) ([]uint32, error) {
	rows, err := d.Mirror.Read.QueryContext(ctx,
		`SELECT uid FROM messages WHERE folder_id = ? AND disabled_at IS NULL`, folderID)
	if err != nil {
		return nil, fmt.Errorf("message uids for %s: %w", folderID, err)
	}
	defer func() { _ = rows.Close() }()

	var uids []uint32
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("message uids for %s: %w", folderID, err)
		}
		u, err := uidFromDB(uid)
		if err != nil {
			return nil, fmt.Errorf("message uids for %s: %w", folderID, err)
		}
		uids = append(uids, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("message uids for %s: %w", folderID, err)
	}
	return uids, nil
}

// SyncMessageRef is the small projection of a live message that reconciliation
// needs: it holds no body, so a run over a large folder stays bounded in memory.
type SyncMessageRef struct {
	ID          string
	UID         uint32
	UIDValidity uint32
	MessageID   string
	Flags       []string
}

// SyncMessageRefs returns every live (not disabled) row in a folder, without
// reading a raw blob or a spool file.
func (d *DBs) SyncMessageRefs(ctx context.Context, folderID string) ([]SyncMessageRef, error) {
	rows, err := d.Mirror.Read.QueryContext(ctx,
		`SELECT id, uid, uidvalidity, COALESCE(message_id_hdr, ''), COALESCE(flags_json, '') FROM messages
		 WHERE folder_id = ? AND disabled_at IS NULL`, folderID)
	if err != nil {
		return nil, fmt.Errorf("sync refs for %s: %w", folderID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []SyncMessageRef
	for rows.Next() {
		var (
			r          SyncMessageRef
			uid, valid int64
			flagsJSON  string
		)
		if err := rows.Scan(&r.ID, &uid, &valid, &r.MessageID, &flagsJSON); err != nil {
			return nil, fmt.Errorf("sync refs for %s: %w", folderID, err)
		}
		u, err := uidFromDB(uid)
		if err != nil {
			return nil, fmt.Errorf("sync refs for %s: %w", folderID, err)
		}
		r.UID = u
		r.UIDValidity = uint32(valid) //nolint:gosec // G115: written from a uint32
		if err := decodeJSON(flagsJSON, &r.Flags); err != nil {
			return nil, fmt.Errorf("sync refs for %s: flags: %w", folderID, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sync refs for %s: %w", folderID, err)
	}
	return out, nil
}

// DisableMessage hides a message the server no longer holds in its folder. The
// row, its raw bytes and its spool file stay: nothing is ever erased. The first
// disable wins, so a later pass cannot rewrite the reason that explained it;
// blobHash is the content hash of the message's backup copy, recorded so the
// backup can reconcile it (an empty hash is filled later, never cleared).
func (d *DBs) DisableMessage(ctx context.Context, id, reason string, at time.Time, blobHash string) error {
	res, err := d.Mirror.Write.ExecContext(ctx, `
		UPDATE messages SET
			disabled_reason = CASE WHEN disabled_at IS NULL THEN ? ELSE disabled_reason END,
			disabled_at = COALESCE(disabled_at, ?),
			disabled_blob = CASE WHEN COALESCE(disabled_blob, '') = '' THEN NULLIF(?, '') ELSE disabled_blob END
		WHERE id = ?`, reason, nullableTime(at), blobHash, id)
	if err != nil {
		return fmt.Errorf("disable message %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("disable message %s: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetDisabledBlob records the blob store hash for a hidden row, used by the
// backup's reconcile to heal a row whose bytes were not stored when it was
// disabled. It does not check that the row is hidden: the caller already knows.
func (d *DBs) SetDisabledBlob(ctx context.Context, id, blobHash string) error {
	res, err := d.Mirror.Write.ExecContext(ctx,
		`UPDATE messages SET disabled_blob = ? WHERE id = ?`, blobHash, id)
	if err != nil {
		return fmt.Errorf("set disabled blob %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set disabled blob %s: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// EachDisabledRaw yields every hidden row's raw bytes in turn, one at a time, so
// the backup can copy each into the blob store without an account's disabled
// mail ever being held in memory together. Inline blobs come from the row; a
// larger message's file is opened for the callback. A row with no bytes
// (BodyTooLarge, never downloaded) is skipped, and a cancelled context stops at
// the next row.
func (d *DBs) EachDisabledRaw(ctx context.Context, fn func(id, blobHash string, raw io.Reader) error) error {
	rows, err := d.Mirror.Read.QueryContext(ctx, `
		SELECT id, COALESCE(disabled_blob, ''), raw_blob, COALESCE(raw_path, '')
		FROM messages
		WHERE disabled_at IS NOT NULL AND (length(raw_blob) > 0 OR raw_path IS NOT NULL)`)
	if err != nil {
		return fmt.Errorf("each disabled raw: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var (
			id, blobHash string
			blob         []byte
			rawPath      string
		)
		if err := rows.Scan(&id, &blobHash, &blob, &rawPath); err != nil {
			return fmt.Errorf("each disabled raw: %w", err)
		}
		if err := d.withRaw(rawPath, blob, func(raw io.Reader) error {
			return fn(id, blobHash, raw)
		}); err != nil {
			return err
		}
	}
	return rows.Err()
}

// withRaw presents a row's bytes, whether they live in the row or in a spool
// file. A spool file that cannot be opened is handed to fn as a reader that
// fails on first use, so one unreadable message does not stop the caller's walk
// over the others (the caller decides whether to skip or abort).
func (d *DBs) withRaw(rawPath string, blob []byte, fn func(io.Reader) error) error {
	if rawPath != "" {
		f, err := os.Open(filepath.Join(d.Dir, filepath.FromSlash(rawPath)))
		if err != nil {
			return fn(errorReader{err: fmt.Errorf("open spool %s: %w", rawPath, err)})
		}
		defer func() { _ = f.Close() }()
		return fn(f)
	}
	return fn(bytes.NewReader(blob))
}

// errorReader fails every read with err, standing in for a spool file that
// could not be opened.
type errorReader struct{ err error }

func (e errorReader) Read([]byte) (int, error) { return 0, e.err }

// MessageRawReader opens one message's original bytes regardless of hidden
// state, so the disable path can copy them into the blob store before the row
// loses its live status. A message never downloaded has none (ErrNoRaw); an
// unknown id is ErrNotFound.
func (d *DBs) MessageRawReader(ctx context.Context, id string) (io.ReadCloser, error) {
	var (
		blob    []byte
		rawPath string
	)
	err := d.Mirror.Read.QueryRowContext(ctx,
		`SELECT raw_blob, COALESCE(raw_path, '') FROM messages WHERE id = ?`, id).Scan(&blob, &rawPath)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read raw %s: %w", id, err)
	}
	if rawPath != "" {
		f, err := os.Open(filepath.Join(d.Dir, filepath.FromSlash(rawPath)))
		if err != nil {
			return nil, fmt.Errorf("open spool %s: %w", rawPath, err)
		}
		return f, nil
	}
	if len(blob) == 0 {
		return nil, ErrNoRaw
	}
	return io.NopCloser(bytes.NewReader(blob)), nil
}

// Why a message is disabled. A message that vanished from a folder but still
// exists elsewhere on the server moved; one that is nowhere was removed. Sync
// cannot tell which until it has read the whole account, so it disables with
// DisabledPending and settles every pending row at the end of a pass that
// completed; a pass that dies in between leaves them pending for the next one.
const (
	DisabledPending = "pending_classification"
	DisabledMoved   = "moved"
	DisabledRemoved = "server_removed"
)

// SettlePendingDisabled gives every pending disabled row of an account its real
// reason: moved when the same Message-ID is live in some other row, removed
// otherwise. It runs inside the database, so the account's Message-IDs are never
// held in memory.
func (d *DBs) SettlePendingDisabled(ctx context.Context, accountID string) error {
	if _, err := d.Mirror.Write.ExecContext(ctx, `
		UPDATE messages SET disabled_reason = ?
		WHERE account_id = ? AND disabled_at IS NOT NULL AND disabled_reason = ?
		  AND message_id_hdr IN (
			SELECT message_id_hdr FROM messages
			WHERE account_id = ? AND disabled_at IS NULL AND message_id_hdr <> '')`,
		DisabledMoved, accountID, DisabledPending, accountID); err != nil {
		return fmt.Errorf("settle moved messages of %s: %w", accountID, err)
	}
	if _, err := d.Mirror.Write.ExecContext(ctx, `
		UPDATE messages SET disabled_reason = ?
		WHERE account_id = ? AND disabled_at IS NOT NULL AND disabled_reason = ?`,
		DisabledRemoved, accountID, DisabledPending); err != nil {
		return fmt.Errorf("settle removed messages of %s: %w", accountID, err)
	}
	return nil
}

// SetMessageFlags updates the sync-owned flag columns of a live message. It is
// deliberately narrow: a reconciliation pass that only refreshes flags must not
// touch the content key, the raw bytes or any derived data.
func (d *DBs) SetMessageFlags(ctx context.Context, id string, flags []string) error {
	flagsJSON, err := marshalFlags(flags)
	if err != nil {
		return err
	}
	res, err := d.Mirror.Write.ExecContext(ctx,
		`UPDATE messages SET flags_json = ?, seen = ? WHERE id = ?`,
		flagsJSON, slices.Contains(flags, `\Seen`), id)
	if err != nil {
		return fmt.Errorf("set message %s flags: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set message %s flags: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// messageSelect coalesces nullable columns so scanning never needs sql.Null*.
const messageSelect = `
	SELECT id, account_id, folder_id, uid, uidvalidity, content_key,
	       COALESCE(message_id_hdr, ''), COALESCE(in_reply_to, ''), COALESCE(refs, ''),
	       COALESCE(subject, ''), COALESCE(from_json, ''), COALESCE(to_json, ''),
	       COALESCE(cc_json, ''), COALESCE(reply_to_json, ''), COALESCE(delivered_to_json, ''),
	       COALESCE(date, ''), size, COALESCE(flags_json, ''),
	       COALESCE(internaldate, ''), has_attachments, raw_blob,
	       COALESCE(body_text, ''), COALESCE(body_html_sanitized, ''),
	       COALESCE(thread_id, ''), COALESCE(snippet, ''),
	       COALESCE(auth_results, ''), COALESCE(parse_errors, ''),
	       COALESCE(disabled_at, ''), COALESCE(disabled_reason, ''), COALESCE(disabled_blob, ''), seen,
	       COALESCE(raw_path, ''), body_status, derived_version
	FROM messages`

func scanMessage(s scanner) (Message, error) {
	var (
		m                                Message
		uid, size                        int64
		fromJSON, toJSON, ccJSON         string
		replyToJSON, deliveredToJSON     string
		flagsJSON, date                  string
		internalDate                     string
		authResultsJSON, parseErrorsJSON string
		disabledAt                       string
	)
	err := s.Scan(
		&m.ID, &m.AccountID, &m.FolderID, &uid, &m.UIDValidity, &m.ContentKey, &m.MessageID,
		&m.InReplyTo, &m.References, &m.Subject, &fromJSON, &toJSON, &ccJSON,
		&replyToJSON, &deliveredToJSON,
		&date, &size, &flagsJSON, &internalDate, &m.HasAttachments, &m.RawBlob,
		&m.BodyText, &m.BodyHTML, &m.ThreadID, &m.Snippet,
		&authResultsJSON, &parseErrorsJSON,
		&disabledAt, &m.DisabledReason, &m.DisabledBlob, &m.Seen, &m.RawPath, &m.BodyStatus,
		&m.DerivedVersion,
	)
	if err != nil {
		return Message{}, err
	}
	if m.UID, err = uidFromDB(uid); err != nil {
		return Message{}, err
	}
	m.Size = size

	if err := decodeJSON(fromJSON, &m.From); err != nil {
		return Message{}, fmt.Errorf("from: %w", err)
	}
	if err := decodeJSON(toJSON, &m.To); err != nil {
		return Message{}, fmt.Errorf("to: %w", err)
	}
	if err := decodeJSON(ccJSON, &m.CC); err != nil {
		return Message{}, fmt.Errorf("cc: %w", err)
	}
	if err := decodeJSON(replyToJSON, &m.ReplyTo); err != nil {
		return Message{}, fmt.Errorf("reply-to: %w", err)
	}
	if err := decodeJSON(deliveredToJSON, &m.DeliveredTo); err != nil {
		return Message{}, fmt.Errorf("delivered-to: %w", err)
	}
	if err := decodeJSON(flagsJSON, &m.Flags); err != nil {
		return Message{}, fmt.Errorf("flags: %w", err)
	}
	if err := decodeJSON(authResultsJSON, &m.AuthResults); err != nil {
		return Message{}, fmt.Errorf("auth results: %w", err)
	}
	if err := decodeJSON(parseErrorsJSON, &m.ParseErrors); err != nil {
		return Message{}, fmt.Errorf("parse errors: %w", err)
	}
	for _, tc := range []struct {
		raw  string
		dest *time.Time
		name string
	}{{date, &m.Date, "date"}, {internalDate, &m.InternalDate, "internaldate"}, {disabledAt, &m.DisabledAt, "disabled_at"}} {
		if tc.raw == "" {
			continue
		}
		t, err := parseTime(tc.raw)
		if err != nil {
			return Message{}, fmt.Errorf("%s: %w", tc.name, err)
		}
		*tc.dest = t
	}
	return m, nil
}

// uidFromDB narrows a stored UID. IMAP UIDs are 32-bit, so a value outside that
// range means the row is corrupt; wrapping it would point sync at the wrong
// message.
func uidFromDB(v int64) (uint32, error) {
	if v < 0 || v > math.MaxUint32 {
		return 0, fmt.Errorf("stored uid %d is out of the 32-bit range", v)
	}
	return uint32(v), nil //nolint:gosec // G115: range checked above
}

// nullableString stores NULL for an empty string, so an unset path is not a
// path of "".
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func marshalAddress(a Address) (any, error) {
	if a.Name == "" && a.Address == "" {
		return nil, nil
	}
	b, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("marshal address: %w", err)
	}
	return string(b), nil
}

func marshalAddresses(list []Address) (any, error) {
	if list == nil {
		list = []Address{}
	}
	b, err := json.Marshal(list)
	if err != nil {
		return nil, fmt.Errorf("marshal addresses: %w", err)
	}
	return string(b), nil
}

// marshalFlags stores flags as a sorted set: a server may list the same flags in
// any order, and the order carries no meaning, so it must not change the row.
func marshalFlags(flags []string) (any, error) {
	flags = slices.Compact(slices.Sorted(slices.Values(flags)))
	if flags == nil {
		flags = []string{}
	}
	b, err := json.Marshal(flags)
	if err != nil {
		return nil, fmt.Errorf("marshal flags: %w", err)
	}
	return string(b), nil
}

// marshalAuthResults stores NULL for a message with no verdicts, so a scan
// leaves the zero value rather than decoding an empty object.
func marshalAuthResults(a AuthResults) (any, error) {
	if a.AuthservID == "" && a.SPF == "" && a.DKIM == "" && a.DMARC == "" && len(a.Raw) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("marshal auth results: %w", err)
	}
	return string(b), nil
}

// marshalStrings stores NULL for an empty list, keeping the scanned zero value
// nil instead of an empty slice.
func marshalStrings(list []string) (any, error) {
	if len(list) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(list)
	if err != nil {
		return nil, fmt.Errorf("marshal strings: %w", err)
	}
	return string(b), nil
}

func decodeJSON[T any](raw string, dest *T) error {
	if raw == "" {
		return nil
	}
	return json.Unmarshal([]byte(raw), dest)
}

// SpooledPaths returns every raw_path the mirror owns, disabled messages
// included: a message that vanished from the server keeps its file along with its
// row (nothing is ever erased), so a sweep for orphans must not touch it. Only
// messages above the in-memory size have a path, so the set stays small.
func (d *DBs) SpooledPaths(ctx context.Context) (map[string]bool, error) {
	rows, err := d.Mirror.Read.QueryContext(ctx, `SELECT raw_path FROM messages WHERE raw_path IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("spooled paths: %w", err)
	}
	defer func() { _ = rows.Close() }()

	paths := make(map[string]bool)
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("spooled paths: %w", err)
		}
		paths[p] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("spooled paths: %w", err)
	}
	return paths, nil
}
