package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

// Address is one mail header address, stored as JSON so a rebuild keeps the
// display name as it arrived.
type Address struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
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
	Date           time.Time
	Size           int64
	Flags          []string
	InternalDate   time.Time
	HasAttachments bool
	RawBlob        []byte
	BodyText       string
	BodyHTML       string
	ThreadID       string
	Snippet        string
	DisabledAt     time.Time
	DisabledReason string
	Seen           bool
}

// UpsertMessage writes a message keyed by (folder, uid), so re-syncing a folder
// updates rows in place rather than duplicating them. A reappearing message
// has its disabled fields cleared, re-enabling it with its derived data intact
// (ARCHITECTURE.md 4).
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
	flags, err := marshalFlags(m.Flags)
	if err != nil {
		return err
	}

	_, err = d.Mirror.ExecContext(ctx, `
		INSERT INTO messages (
			id, account_id, folder_id, uid, content_key, message_id_hdr, in_reply_to,
			refs, subject, from_json, to_json, cc_json, date, size, flags_json,
			internaldate, has_attachments, raw_blob, body_text, body_html_sanitized,
			thread_id, snippet, disabled_at, disabled_reason, seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(folder_id, uid) DO UPDATE SET
			account_id=excluded.account_id, content_key=excluded.content_key,
			message_id_hdr=excluded.message_id_hdr, in_reply_to=excluded.in_reply_to,
			refs=excluded.refs, subject=excluded.subject, from_json=excluded.from_json,
			to_json=excluded.to_json, cc_json=excluded.cc_json, date=excluded.date,
			size=excluded.size, flags_json=excluded.flags_json,
			internaldate=excluded.internaldate, has_attachments=excluded.has_attachments,
			raw_blob=excluded.raw_blob, body_text=excluded.body_text,
			body_html_sanitized=excluded.body_html_sanitized, thread_id=excluded.thread_id,
			snippet=excluded.snippet, disabled_at=excluded.disabled_at,
			disabled_reason=excluded.disabled_reason, seen=excluded.seen`,
		m.ID, m.AccountID, m.FolderID, m.UID, m.ContentKey, m.MessageID, m.InReplyTo,
		m.References, m.Subject, from, to, cc, nullableTime(m.Date), m.Size, flags,
		nullableTime(m.InternalDate), m.HasAttachments, m.RawBlob, m.BodyText,
		m.BodyHTML, m.ThreadID, m.Snippet, nullableTime(m.DisabledAt), m.DisabledReason,
		slices.Contains(m.Flags, `\Seen`),
	)
	if err != nil {
		return fmt.Errorf("upsert message %s: %w", m.ID, err)
	}
	return nil
}

// GetMessage returns one visible message or ErrNotFound. Disabled messages are
// hidden everywhere until they are restored.
func (d *DBs) GetMessage(ctx context.Context, id string) (Message, error) {
	row := d.Mirror.QueryRowContext(ctx, messageSelect+` WHERE id = ? AND disabled_at IS NULL`, id)
	m, err := scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, fmt.Errorf("get message %s: %w", id, err)
	}
	return m, nil
}

// messageSelect coalesces nullable columns so scanning never needs sql.Null*.
const messageSelect = `
	SELECT id, account_id, folder_id, uid, content_key,
	       COALESCE(message_id_hdr, ''), COALESCE(in_reply_to, ''), COALESCE(refs, ''),
	       COALESCE(subject, ''), COALESCE(from_json, ''), COALESCE(to_json, ''),
	       COALESCE(cc_json, ''), COALESCE(date, ''), size, COALESCE(flags_json, ''),
	       COALESCE(internaldate, ''), has_attachments, raw_blob,
	       COALESCE(body_text, ''), COALESCE(body_html_sanitized, ''),
	       COALESCE(thread_id, ''), COALESCE(snippet, ''),
	       COALESCE(disabled_at, ''), COALESCE(disabled_reason, ''), seen
	FROM messages`

func scanMessage(s scanner) (Message, error) {
	var (
		m                        Message
		uid, size                int64
		fromJSON, toJSON, ccJSON string
		flagsJSON, date          string
		internalDate             string
		disabledAt               string
	)
	err := s.Scan(
		&m.ID, &m.AccountID, &m.FolderID, &uid, &m.ContentKey, &m.MessageID,
		&m.InReplyTo, &m.References, &m.Subject, &fromJSON, &toJSON, &ccJSON,
		&date, &size, &flagsJSON, &internalDate, &m.HasAttachments, &m.RawBlob,
		&m.BodyText, &m.BodyHTML, &m.ThreadID, &m.Snippet, &disabledAt,
		&m.DisabledReason, &m.Seen,
	)
	if err != nil {
		return Message{}, err
	}
	m.UID = uint32(uid)
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
	if err := decodeJSON(flagsJSON, &m.Flags); err != nil {
		return Message{}, fmt.Errorf("flags: %w", err)
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

func decodeJSON[T any](raw string, dest *T) error {
	if raw == "" {
		return nil
	}
	return json.Unmarshal([]byte(raw), dest)
}
