package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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
// Authentication-Results headers (ARCHITECTURE.md 5).
type AuthResults struct {
	SPF   string   `json:"spf,omitempty"`
	DKIM  string   `json:"dkim,omitempty"`
	DMARC string   `json:"dmarc,omitempty"`
	Raw   []string `json:"raw,omitempty"`
}

// Message is one mirrored message row. BodyHTML is the server-sanitized HTML;
// RawBlob is the original RFC 822 bytes, stored so everything derived can be
// rebuilt.
type Message struct {
	ID             string
	AccountID      string
	FolderID       string
	UID            uint32
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
	Seen           bool
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
// thread_id and body_html_sanitized are written on the first insert only. Later
// layers own them (SetMessageThread, SetMessageBodyHTML), and sync, which knows
// neither, must not blank them when it sees the message again.
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
			id, account_id, folder_id, uid, content_key, message_id_hdr, in_reply_to,
			refs, subject, from_json, to_json, cc_json, reply_to_json, delivered_to_json,
			date, size, flags_json, internaldate, has_attachments, raw_blob, body_text,
			body_html_sanitized, thread_id, snippet, auth_results, parse_errors,
			disabled_at, disabled_reason, seen, raw_path, body_status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(folder_id, uid) DO UPDATE SET
			account_id=excluded.account_id, content_key=excluded.content_key,
			message_id_hdr=excluded.message_id_hdr, in_reply_to=excluded.in_reply_to,
			refs=excluded.refs, subject=excluded.subject, from_json=excluded.from_json,
			to_json=excluded.to_json, cc_json=excluded.cc_json,
			reply_to_json=excluded.reply_to_json, delivered_to_json=excluded.delivered_to_json,
			date=excluded.date, size=excluded.size, flags_json=excluded.flags_json,
			internaldate=excluded.internaldate, has_attachments=excluded.has_attachments,
			raw_blob=excluded.raw_blob, body_text=excluded.body_text,
			snippet=excluded.snippet, auth_results=excluded.auth_results,
			parse_errors=excluded.parse_errors, disabled_at=excluded.disabled_at,
			disabled_reason=excluded.disabled_reason, seen=excluded.seen,
			raw_path=excluded.raw_path, body_status=excluded.body_status`,
		m.ID, m.AccountID, m.FolderID, m.UID, m.ContentKey, m.MessageID, m.InReplyTo,
		m.References, m.Subject, from, to, cc, replyTo, deliveredTo,
		nullableTime(m.Date), m.Size, flags, nullableTime(m.InternalDate),
		m.HasAttachments, m.RawBlob, m.BodyText, m.BodyHTML, m.ThreadID, m.Snippet,
		authResults, parseErrors, nullableTime(m.DisabledAt), m.DisabledReason,
		slices.Contains(m.Flags, `\Seen`), nullableString(m.RawPath), bodyStatus,
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

// SetMessageBodyHTML stores the server-sanitised HTML for a message (chunk 2d),
// for the same reason as SetMessageThread.
func (d *DBs) SetMessageBodyHTML(ctx context.Context, id, html string) error {
	return d.setMessageColumn(ctx, `UPDATE messages SET body_html_sanitized = ? WHERE id = ?`, id, html)
}

// setMessageColumn runs one of the fixed single-column UPDATE statements above,
// which take the new value first and the message id second.
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

// messageSelect coalesces nullable columns so scanning never needs sql.Null*.
const messageSelect = `
	SELECT id, account_id, folder_id, uid, content_key,
	       COALESCE(message_id_hdr, ''), COALESCE(in_reply_to, ''), COALESCE(refs, ''),
	       COALESCE(subject, ''), COALESCE(from_json, ''), COALESCE(to_json, ''),
	       COALESCE(cc_json, ''), COALESCE(reply_to_json, ''), COALESCE(delivered_to_json, ''),
	       COALESCE(date, ''), size, COALESCE(flags_json, ''),
	       COALESCE(internaldate, ''), has_attachments, raw_blob,
	       COALESCE(body_text, ''), COALESCE(body_html_sanitized, ''),
	       COALESCE(thread_id, ''), COALESCE(snippet, ''),
	       COALESCE(auth_results, ''), COALESCE(parse_errors, ''),
	       COALESCE(disabled_at, ''), COALESCE(disabled_reason, ''), seen,
	       COALESCE(raw_path, ''), body_status
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
		&m.ID, &m.AccountID, &m.FolderID, &uid, &m.ContentKey, &m.MessageID,
		&m.InReplyTo, &m.References, &m.Subject, &fromJSON, &toJSON, &ccJSON,
		&replyToJSON, &deliveredToJSON,
		&date, &size, &flagsJSON, &internalDate, &m.HasAttachments, &m.RawBlob,
		&m.BodyText, &m.BodyHTML, &m.ThreadID, &m.Snippet,
		&authResultsJSON, &parseErrorsJSON,
		&disabledAt, &m.DisabledReason, &m.Seen, &m.RawPath, &m.BodyStatus,
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

func marshalFlags(flags []string) (any, error) {
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
	if a.SPF == "" && a.DKIM == "" && a.DMARC == "" && len(a.Raw) == 0 {
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
