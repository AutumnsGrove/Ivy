package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
// Snoozed and Reading mail is hidden from every folder view; the explicit
// `snoozed` view and a `tag` filter are the local views over state.db.
func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	accountID := q.Get("account_id")
	if q.Get("folder") == "snoozed" {
		s.handleSnoozedInbox(w, r, accountID)
		return
	}
	if tagID := q.Get("tag"); tagID != "" {
		s.handleTaggedInbox(w, r, accountID, tagID)
		return
	}

	hidden, reading, err := s.hiddenKeys(r.Context(), accountID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page, err := s.dbs.ListInbox(r.Context(), store.InboxQuery{
		AccountID: accountID,
		Role:      q.Get("folder"),
		Cursor:    q.Get("cursor"),
		Hide:      hidden,
	})
	if errors.Is(err, store.ErrBadCursor) {
		writeError(w, http.StatusBadRequest, "bad_request", "That page link is not valid")
		return
	}
	if errors.Is(err, store.ErrBadRole) {
		writeError(w, http.StatusBadRequest, "bad_request", "That folder is not available")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	out, err := s.inboxResponse(r, page, reading)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// inboxResponse projects a page and attaches the first tag name to each row.
func (s *Server) inboxResponse(r *http.Request, page store.InboxPage, reading int) (api.Inbox, error) {
	out := api.Inbox{
		Items:          make([]api.MailSummary, 0, len(page.Items)),
		UnreadCount:    page.UnreadCount,
		NeedCount:      page.NeedCount,
		ReadingWaiting: reading,
	}
	keysByAccount := map[string][]string{}
	for _, m := range page.Items {
		keysByAccount[m.AccountID] = append(keysByAccount[m.AccountID], m.ContentKey)
	}
	names := map[string]map[string]string{}
	for accountID, keys := range keysByAccount {
		byKey, err := s.tagNames(r.Context(), accountID, keys)
		if err != nil {
			return api.Inbox{}, err
		}
		names[accountID] = byKey
	}
	for _, m := range page.Items {
		view := summaryView(m)
		if name, ok := names[m.AccountID][m.ContentKey]; ok {
			view.Tag = &name
		}
		out.Items = append(out.Items, view)
	}
	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}
	return out, nil
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
	names, err := s.tagNames(r.Context(), m.AccountID, []string{m.ContentKey})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	view := summaryView(store.MessageSummary{
		ID: m.ID, AccountID: m.AccountID, From: m.From, Subject: m.Subject,
		Snippet: m.Snippet, Date: m.Date, Unread: !m.Seen, Needs: needs,
	})
	if name, ok := names[m.ContentKey]; ok {
		view.Tag = &name
	}
	writeJSON(w, http.StatusOK, view)
}

// EmbedBacklog is the embed worker's queue, per account: the documents it has yet
// to embed. The worker implements it, so the page states what the worker will do.
type EmbedBacklog interface {
	Backlog(ctx context.Context) (map[string]int, error)
}

// WithEmbedBacklog gives Mirror health the embed queue. Without it the page
// reports meaning search as off.
func (s *Server) WithEmbedBacklog(b EmbedBacklog) *Server {
	s.embedBacklog = b
	return s
}

// handleMirrorHealth summarizes per-account sync, the search index, the embed
// queue, storage and the process's memory. The queue is a side figure: if it
// cannot be counted the page still answers, because this is the page the
// operator opens when something is wrong.
func (s *Server) handleMirrorHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	views, err := s.accountViews(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	size, err := s.dbs.MirrorBytes(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	docs, err := s.dbs.SearchDocCount(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	queue, meaning := s.embedQueue(ctx, views)
	writeJSON(w, http.StatusOK, api.HealthOverview{
		Accounts:       views,
		SearchIndex:    indexLabel(docs),
		MeaningSearch:  meaning,
		EmbeddingQueue: queue,
		Storage:        humanSize(size),
		Memory:         humanSize(processMemory()),
	})
}

// embedQueue sums the backlog of the accounts with smart features on and words
// it. An account that is off is left out: it will never be embedded, so counting
// it would show a queue that cannot drain.
func (s *Server) embedQueue(ctx context.Context, views []api.Account) (int, string) {
	if s.embedBacklog == nil {
		return 0, "Off"
	}
	smart := make([]string, 0, len(views))
	for _, v := range views {
		if v.Smart {
			smart = append(smart, v.Id)
		}
	}
	if len(smart) == 0 {
		return 0, "Off"
	}
	counts, err := s.embedBacklog.Backlog(ctx)
	if err != nil {
		slog.WarnContext(ctx, "gateway: cannot count the embed queue", "error", err)
		return 0, "Unavailable"
	}
	queue := 0
	for _, id := range smart {
		queue += counts[id]
	}
	if queue == 0 {
		return 0, "Up to date"
	}
	return queue, groupDigits(queue) + " waiting"
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
	tags, err := s.dbs.TagsForMessages(r.Context(), m.AccountID, []string{m.ContentKey})
	if err != nil {
		return api.MailMessage{}, err
	}
	parties, err := s.partyBuilder(r.Context())
	if err != nil {
		return api.MailMessage{}, err
	}
	v := api.MailMessage{
		Id:          m.ID,
		AccountId:   m.AccountID,
		Sender:      parties.one(m.From),
		To:          parties.list(m.To),
		Cc:          parties.list(m.CC),
		Auth:        authView(m.AuthResults),
		From:        displayName(m.From),
		Initials:    initials(m.From.Name, m.From.Address),
		Date:        m.Date.UTC(),
		Subject:     m.Subject,
		Preview:     m.Snippet,
		Unread:      !m.Seen,
		Flagged:     boolPtr(m.Flagged),
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
	if mine := tags[m.ContentKey]; len(mine) > 0 {
		first := mine[0].Name
		v.Tag = &first
		ids := make([]string, len(mine))
		for i, t := range mine {
			ids[i] = t.ID
		}
		v.TagIds = &ids
	}
	return v, nil
}

// summaryView projects a list row. The date is the instant in UTC; the browser
// knows the viewer's zone and locale, the server does not (N15, round 32b).
func summaryView(m store.MessageSummary) api.MailSummary {
	return api.MailSummary{
		Id:        m.ID,
		AccountId: m.AccountID,
		From:      displayName(m.From),
		Initials:  initials(m.From.Name, m.From.Address),
		Date:      m.Date.UTC(),
		Subject:   m.Subject,
		Preview:   m.Snippet,
		Unread:    m.Unread,
		Flagged:   boolPtr(m.Flagged),
		Needs:     m.Needs,
	}
}

func boolPtr(b bool) *bool { return &b }

func (s *Server) accountViews(r *http.Request) ([]api.Account, error) {
	accounts, err := s.dbs.ListAccounts(r.Context())
	if err != nil {
		return nil, err
	}
	stats, err := s.dbs.AccountStats(r.Context())
	if err != nil {
		return nil, err
	}
	hidden, err := s.dbs.DisabledStats(r.Context())
	if err != nil {
		return nil, err
	}
	smart, err := s.smartAccounts(r.Context())
	if err != nil {
		return nil, err
	}
	out := make([]api.Account, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, accountView(a, stats[a.ID], hidden[a.ID], smartFor(a, smart)))
	}
	return out, nil
}

// smartAccounts is the smart-features choice of every configured account:
// ivy.yaml first (it wins a clash, as at startup), then the app-connected ones
// in state.db. The mirror row is not asked, because sync never sets it.
func (s *Server) smartAccounts(ctx context.Context) (map[string]bool, error) {
	configs, err := s.dbs.AccountConfigs(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(configs)+len(s.configuredSmart))
	for _, c := range configs {
		out[c.ID] = c.LLMEnabled
	}
	for id, on := range s.configuredSmart {
		out[id] = on
	}
	return out, nil
}

// smartFor falls back to the mirror row only for an account no config names,
// which is a seeded dev or test account.
func smartFor(a store.Account, smart map[string]bool) bool {
	if on, ok := smart[a.ID]; ok {
		return on
	}
	return a.LLMEnabled
}

func accountView(a store.Account, st store.AccountStat, hidden store.DisabledStat, smart bool) api.Account {
	state, note, progress := syncState(st)
	v := api.Account{
		Id:       a.ID,
		Address:  a.Address,
		Short:    accountShort(a.Address),
		Initial:  initials(a.DisplayName, a.Address),
		Name:     a.DisplayName,
		Icon:     a.Icon,
		Photo:    a.HasPhoto,
		Slot:     api.AccountSlot(slotOf(a.SortOrder)),
		Unread:   st.Unread,
		Smart:    smart,
		Sync:     state,
		SyncNote: note,
	}
	if progress > 0 {
		p := float32(progress)
		v.Progress = &p
	}
	if !st.LastSync.IsZero() {
		synced := st.LastSync.UTC()
		v.SyncedAt = &synced
	}
	if hidden.Hidden > 0 {
		v.Hidden = &api.HiddenMail{
			Total: hidden.Hidden, Moved: hidden.Moved, Removed: hidden.Removed, Pending: hidden.Pending,
		}
	}
	return v
}

// syncState is the account's sync state and a short phrase for it. The phrase
// carries no time: "synced 4 min ago" depends on the viewer's clock, zone and
// language, so the browser composes it from syncedAt (N17, round 32b).
func syncState(st store.AccountStat) (api.SyncState, string, float64) {
	switch {
	case st.Folders == 0:
		return api.SyncStateOk, "Waiting for the first sync", 0
	case st.SyncedFolders < st.Folders:
		return api.SyncStateSyncing, "Reading your mailbox, newest first",
			float64(st.SyncedFolders) / float64(st.Folders)
	default:
		return api.SyncStateOk, "Up to date", 0
	}
}

func displayName(a store.Address) string {
	if name := store.CleanName(a.Name); name != "" {
		return name
	}
	return a.Address
}

// attachments lists a message's file and inline parts. It reads the stored
// rows written by sync; only a message mirrored before the attachment table
// was populated falls back to walking the raw bytes.
func (s *Server) attachments(r *http.Request, m store.Message) []api.Attachment {
	if !m.HasAttachments {
		return []api.Attachment{}
	}
	rows, err := s.dbs.ListAttachments(r.Context(), m.ID)
	if err != nil {
		slog.WarnContext(r.Context(), "gateway: list attachments", "message", m.ID, "error", err)
		return []api.Attachment{}
	}
	if len(rows) == 0 {
		return s.attachmentsFromRaw(r, m)
	}
	out := make([]api.Attachment, 0, len(rows))
	tone := 0
	for _, a := range rows {
		name := a.Filename
		if name == "" {
			name = "attachment"
		}
		item := api.Attachment{Id: a.StoragePath, Name: name, Size: humanSize(a.Size), Kind: api.File}
		if strings.HasPrefix(a.MIMEType, "image/") {
			item.Kind = api.Image
			t := []api.AttachmentTone{api.A, api.B}[tone%2]
			item.Tone = &t
			tone++
		}
		out = append(out, item)
	}
	return out
}

// attachmentsFromRaw derives the list by walking the raw message. It is the
// compatibility path for a message synced before the attachment table existed;
// once it is re-fetched the rows make it unnecessary.
func (s *Server) attachmentsFromRaw(r *http.Request, m store.Message) []api.Attachment {
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
