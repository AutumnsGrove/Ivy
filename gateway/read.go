package gateway

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AutumnsGrove/Ivy/api"
	mailmime "github.com/AutumnsGrove/Ivy/mime"
	"github.com/AutumnsGrove/Ivy/store"
)

// handleAccounts lists the mirrored accounts with their unread count and sync
// state, the data the rail and the account picker render.
func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	views, err := s.accountViews(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, views)
}

// handleInbox serves a page of the combined or per-account inbox, newest first.
func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := s.dbs.ListInbox(r.Context(), store.InboxQuery{
		AccountID: q.Get("account_id"),
		Cursor:    q.Get("cursor"),
	})
	if errors.Is(err, store.ErrBadCursor) {
		writeError(w, http.StatusBadRequest, "bad_request", "That page link is not valid")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	out := api.Inbox{
		Items:          make([]api.MailSummary, 0, len(page.Items)),
		UnreadCount:    page.UnreadCount,
		NeedCount:      page.NeedCount,
		ReadingWaiting: 0,
	}
	now := s.now()
	for _, m := range page.Items {
		out.Items = append(out.Items, summaryView(m, now))
	}
	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}
	writeJSON(w, http.StatusOK, out)
}

// handleMessage serves one message with its stored sanitised body.
func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request) {
	m, err := s.dbs.GetMessage(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "message")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	view, err := s.messageView(r, m)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleMessageSummary serves the header alone, so a body that failed to load
// still renders under its real subject line.
func (s *Server) handleMessageSummary(w http.ResponseWriter, r *http.Request) {
	m, err := s.dbs.GetMessage(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "message")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	needs, err := s.dbs.MessageNeeds(r.Context(), m.AccountID, m.ContentKey)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, summaryView(store.MessageSummary{
		ID: m.ID, AccountID: m.AccountID, From: m.From, Subject: m.Subject,
		Snippet: m.Snippet, Date: m.Date, Unread: !m.Seen, Needs: needs,
	}, s.now()))
}

// handleMirrorHealth summarizes per-account sync and index state. Search and
// meaning search report honestly that they are not built yet; storage is the
// mirror file's size.
func (s *Server) handleMirrorHealth(w http.ResponseWriter, r *http.Request) {
	views, err := s.accountViews(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	size, err := s.dbs.MirrorBytes(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, api.HealthOverview{
		Accounts:      views,
		SearchIndex:   "Not built yet",
		MeaningSearch: "Not built yet",
		Storage:       humanSize(size),
	})
}

func (s *Server) messageView(r *http.Request, m store.Message) (api.MailMessage, error) {
	needs, err := s.dbs.MessageNeeds(r.Context(), m.AccountID, m.ContentKey)
	if err != nil {
		return api.MailMessage{}, err
	}
	acct, err := s.dbs.GetAccount(r.Context(), m.AccountID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return api.MailMessage{}, err
	}
	v := api.MailMessage{
		Id:          m.ID,
		AccountId:   m.AccountID,
		From:        displayName(m.From),
		Initials:    initials(m.From.Name, m.From.Address),
		Time:        humanTime(s.now(), m.Date),
		Subject:     m.Subject,
		Preview:     m.Snippet,
		Unread:      !m.Seen,
		Needs:       needs,
		ToShort:     accountShort(acct.Address),
		ToFull:      acct.Address,
		Paragraphs:  paragraphs(m.BodyText),
		Attachments: s.attachments(r, m),
	}
	if m.BodyHTML != "" {
		htmlBody := m.BodyHTML
		v.Html = &htmlBody
	}
	return v, nil
}

func summaryView(m store.MessageSummary, now time.Time) api.MailSummary {
	return api.MailSummary{
		Id:        m.ID,
		AccountId: m.AccountID,
		From:      displayName(m.From),
		Initials:  initials(m.From.Name, m.From.Address),
		Time:      humanTime(now, m.Date),
		Subject:   m.Subject,
		Preview:   m.Snippet,
		Unread:    m.Unread,
		Needs:     m.Needs,
	}
}

func (s *Server) accountViews(r *http.Request) ([]api.Account, error) {
	accounts, err := s.dbs.ListAccounts(r.Context())
	if err != nil {
		return nil, err
	}
	stats, err := s.dbs.AccountStats(r.Context())
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := make([]api.Account, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, accountView(a, stats[a.ID], now))
	}
	return out, nil
}

func accountView(a store.Account, st store.AccountStat, now time.Time) api.Account {
	state, note, progress := syncState(st, now)
	v := api.Account{
		Id:       a.ID,
		Address:  a.Address,
		Short:    accountShort(a.Address),
		Initial:  initials(a.DisplayName, a.Address),
		Slot:     api.AccountSlot(slotOf(a.SortOrder)),
		Unread:   st.Unread,
		Smart:    a.LLMEnabled,
		Sync:     state,
		SyncNote: note,
	}
	if progress > 0 {
		p := float32(progress)
		v.Progress = &p
	}
	return v
}

func syncState(st store.AccountStat, now time.Time) (api.SyncState, string, float64) {
	switch {
	case st.Folders == 0:
		return api.SyncStateOk, "Waiting for the first sync", 0
	case st.SyncedFolders < st.Folders:
		return api.SyncStateSyncing, "Reading your mailbox, newest first",
			float64(st.SyncedFolders) / float64(st.Folders)
	case !st.LastSync.IsZero():
		return api.SyncStateOk, "Up to date · synced " + since(now, st.LastSync), 0
	default:
		return api.SyncStateOk, "Up to date", 0
	}
}

func since(now, t time.Time) string {
	switch d := now.Sub(t); {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + " min ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + " h ago"
	default:
		return "on " + t.In(now.Location()).Format("Jan 2")
	}
}

func displayName(a store.Address) string {
	if a.Name != "" {
		return a.Name
	}
	return a.Address
}

// attachments lists a message's file and inline parts. It reads the raw message
// only when the mirror recorded one, and streams it, so a spooled message is
// never held in memory.
func (s *Server) attachments(r *http.Request, m store.Message) []api.Attachment {
	if !m.HasAttachments {
		return []api.Attachment{}
	}
	rc, ok := s.rawReader(m)
	if !ok {
		return []api.Attachment{}
	}
	defer func() { _ = rc.Close() }()

	parts, err := mailmime.ListParts(rc)
	if err != nil {
		slog.WarnContext(r.Context(), "gateway: list message parts", "message", m.ID, "error", err)
		return []api.Attachment{}
	}
	out := make([]api.Attachment, 0, len(parts))
	tone := 0
	for _, p := range parts {
		if !p.Attachment && p.CID == "" {
			continue
		}
		name := p.Filename
		if name == "" {
			name = "attachment"
		}
		a := api.Attachment{Id: p.Path, Name: name, Size: humanSize(p.Size), Kind: api.File}
		if strings.HasPrefix(p.ContentType, "image/") {
			a.Kind = api.Image
			t := []api.AttachmentTone{api.A, api.B}[tone%2]
			a.Tone = &t
			tone++
		}
		out = append(out, a)
	}
	return out
}

// readSeekCloser is what serving one part needs: read it once to find the part,
// rewind, then read it again to stream it.
type readSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

// rawReader opens a message's original bytes: the row's blob for a small
// message, or the spooled file for a large one. Nothing whole is read into
// memory here.
func (s *Server) rawReader(m store.Message) (readSeekCloser, bool) {
	switch {
	case m.RawPath != "":
		f, err := os.Open(filepath.Join(s.dbs.Dir, filepath.FromSlash(m.RawPath)))
		if err != nil {
			return nil, false
		}
		return f, true
	case len(m.RawBlob) > 0:
		return nopSeekCloser{bytes.NewReader(m.RawBlob)}, true
	default:
		return nil, false
	}
}

type nopSeekCloser struct{ *bytes.Reader }

func (nopSeekCloser) Close() error { return nil }
