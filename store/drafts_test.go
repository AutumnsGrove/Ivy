package store

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

// draftNow keeps every save timestamp deterministic so version order and
// retention are asserted without sleeping.
var draftNow = time.Date(2026, 10, 6, 16, 0, 0, 0, time.UTC)

func newDraftSave(id, draftID, msgID string, base int) SaveDraftInput {
	return SaveDraftInput{
		ID: id, DraftID: draftID, AccountID: "acct-1",
		DestFolderID: "drafts-folder", MessageID: msgID,
		Subject: "Hello", To: []string{"you@example.test"},
		Compose: []byte(`{"subject":"Hello"}`), Body: []byte("raw mime"),
		BaseVersion: base, OpID: "op-" + id, Now: draftNow,
	}
}

// A new save is version 1, `saving`, and one draft op carrying the built bytes
// and the \Draft flag.
func TestSaveDraftCreatesVersionOneAndOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	d, err := dbs.SaveDraft(ctx, newDraftSave("v1", "d1", "<m1@example.test>", 0))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if d.Version != 1 || d.State != DraftSaving || d.Supersedes != "" {
		t.Fatalf("draft = %+v, want version 1 saving with no supersedes", d)
	}
	if d.ContentKey != ContentKey("<m1@example.test>", nil) {
		t.Errorf("content key = %q, want the new Message-ID's key", d.ContentKey)
	}
	op, err := dbs.GetOutbox(ctx, "op-v1")
	if err != nil {
		t.Fatalf("get op: %v", err)
	}
	if op.Kind != OutboxDraft || op.Expect.DraftID != "d1" || op.Expect.DraftVersionID != "v1" {
		t.Fatalf("op = %+v, want a draft op for v1", op)
	}
	if op.Expect.DestFolderID != "drafts-folder" || len(op.Expect.Supersedes) != 0 {
		t.Errorf("op expect = %+v, want the Drafts folder and no supersedes", op.Expect)
	}
	// Flags are canonicalised to lower case by the outbox (IMAP flags are
	// case-insensitive), so the stored value is \draft.
	if len(op.Expect.FlagsAdd) != 1 || op.Expect.FlagsAdd[0] != `\draft` {
		t.Errorf("flags = %v, want \\draft only", op.Expect.FlagsAdd)
	}
}

// A retried request (same version-row id) returns the stored version and does
// not create a second one or a second op.
func TestSaveDraftRepeatIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	first, err := dbs.SaveDraft(ctx, newDraftSave("v1", "d1", "<m1@example.test>", 0))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	again, err := dbs.SaveDraft(ctx, newDraftSave("v1", "d1", "<m2@example.test>", 0))
	if err != nil {
		t.Fatalf("repeat: %v", err)
	}
	if again.ID != first.ID || again.MessageID != first.MessageID {
		t.Fatalf("repeat = %+v, want the stored v1", again)
	}
	rows, err := dbs.draftsForTest(ctx)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("version rows = %d, want 1", len(rows))
	}
}

// A replace is a new version that names the previous version's Message-ID to
// expunge. The previous terminal row is then pruned.
func TestSaveDraftReplaceBumpsVersionAndSupersedes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if _, err := dbs.SaveDraft(ctx, newDraftSave("v1", "d1", "<m1@example.test>", 0)); err != nil {
		t.Fatalf("save v1: %v", err)
	}
	if err := dbs.MarkDraftSaved(ctx, "v1", draftNow); err != nil {
		t.Fatalf("mark saved: %v", err)
	}
	d, err := dbs.SaveDraft(ctx, newDraftSave("v2", "d1", "<m2@example.test>", 1))
	if err != nil {
		t.Fatalf("save v2: %v", err)
	}
	if d.Version != 2 || d.Supersedes != "<m1@example.test>" {
		t.Fatalf("v2 = %+v, want version 2 superseding m1", d)
	}
	op, err := dbs.GetOutbox(ctx, "op-v2")
	if err != nil {
		t.Fatalf("get op v2: %v", err)
	}
	if len(op.Expect.Supersedes) != 1 || op.Expect.Supersedes[0] != "<m1@example.test>" {
		t.Fatalf("op v2 supersedes = %v, want [m1]", op.Expect.Supersedes)
	}
	// The terminal v1 row is pruned; only the new head remains.
	rows, err := dbs.draftsForTest(ctx)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "v2" {
		t.Fatalf("rows = %+v, want only v2", rows)
	}
}

// A save whose base version is not the current head loses: it is refused and
// returned the newer content, so the losing tab can be told.
func TestSaveDraftStaleBaseConflicts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if _, err := dbs.SaveDraft(ctx, newDraftSave("v1", "d1", "<m1@example.test>", 0)); err != nil {
		t.Fatalf("save v1: %v", err)
	}
	if err := dbs.MarkDraftSaved(ctx, "v1", draftNow); err != nil {
		t.Fatalf("mark saved: %v", err)
	}
	if _, err := dbs.SaveDraft(ctx, newDraftSave("v2", "d1", "<m2@example.test>", 1)); err != nil {
		t.Fatalf("save v2: %v", err)
	}
	if err := dbs.MarkDraftSaved(ctx, "v2", draftNow); err != nil {
		t.Fatalf("mark v2 saved: %v", err)
	}

	head, err := dbs.SaveDraft(ctx, newDraftSave("v3", "d1", "<m3@example.test>", 1))
	if !errors.Is(err, ErrDraftConflict) {
		t.Fatalf("stale save = %v, want ErrDraftConflict", err)
	}
	if head.ID != "v2" || head.Version != 2 {
		t.Fatalf("conflict head = %+v, want v2", head)
	}
	rows, err := dbs.draftsForTest(ctx)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "v2" {
		t.Fatalf("rows = %+v, want no v3 written", rows)
	}
}

// draftsForTest returns every version row, terminal or not, for the assertions
// about pruning and idempotency.
func (d *DBs) draftsForTest(ctx context.Context) ([]Draft, error) {
	rows, err := d.State.Read.QueryContext(ctx, draftSelect+` WHERE account_id = 'acct-1' ORDER BY draft_id, version`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Draft
	for rows.Next() {
		draft, err := scanDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, draft)
	}
	return out, rows.Err()
}

func TestSaveDraftKeepsSupersededWithLiveOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if _, err := dbs.SaveDraft(ctx, newDraftSave("v1", "d1", "<m1@example.test>", 0)); err != nil {
		t.Fatalf("save v1: %v", err)
	}
	if _, err := dbs.SaveDraft(ctx, newDraftSave("v2", "d1", "<m2@example.test>", 1)); err != nil {
		t.Fatalf("save v2: %v", err)
	}
	rows, err := dbs.draftsForTest(ctx)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want both live versions kept", len(rows))
	}
}

// The body the op files is a specific immutable version, so two racing saves
// can never make one op file the other's bytes.
func TestDraftBodyForOpIsVersioned(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if _, err := dbs.SaveDraft(ctx, newDraftSave("v1", "d1", "<m1@example.test>", 0)); err != nil {
		t.Fatalf("save v1: %v", err)
	}
	if _, err := dbs.SaveDraft(ctx, newDraftSave("v2", "d1", "<m2@example.test>", 1)); err != nil {
		t.Fatalf("save v2: %v", err)
	}
	body, err := dbs.DraftBodyForOp(ctx, "v1")
	if err != nil {
		t.Fatalf("body v1: %v", err)
	}
	if body.MessageID != "<m1@example.test>" || string(body.Body) != "raw mime" {
		t.Fatalf("body v1 = %+v", body)
	}
	if _, err := dbs.DraftBodyForOp(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing body = %v, want ErrNotFound", err)
	}
}

func TestMarkDraftSavedOnlyFromSaving(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if _, err := dbs.SaveDraft(ctx, newDraftSave("v1", "d1", "<m1@example.test>", 0)); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := dbs.MarkDraftSaved(ctx, "v1", draftNow); err != nil {
		t.Fatalf("mark saved: %v", err)
	}
	if err := dbs.MarkDraftSaved(ctx, "v1", draftNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second mark = %v, want ErrNotFound", err)
	}
}

// Listing returns one row per draft (the newest live one), newest first, and
// hides a draft that was sent or discarded.
func TestListDraftHeads(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if _, err := dbs.SaveDraft(ctx, newDraftSave("a1", "da", "<a1@example.test>", 0)); err != nil {
		t.Fatalf("save a1: %v", err)
	}
	if err := dbs.MarkDraftSaved(ctx, "a1", draftNow); err != nil {
		t.Fatalf("mark a1: %v", err)
	}
	a2 := newDraftSave("a2", "da", "<a2@example.test>", 1)
	a2.Now = draftNow.Add(time.Minute)
	if _, err := dbs.SaveDraft(ctx, a2); err != nil {
		t.Fatalf("save a2: %v", err)
	}
	if err := dbs.MarkDraftSaved(ctx, "a2", draftNow.Add(time.Minute)); err != nil {
		t.Fatalf("mark a2: %v", err)
	}

	b1 := newDraftSave("b1", "db", "<b1@example.test>", 0)
	b1.Now = draftNow.Add(2 * time.Minute)
	if _, err := dbs.SaveDraft(ctx, b1); err != nil {
		t.Fatalf("save b1: %v", err)
	}
	c1 := newDraftSave("c1", "dc", "<c1@example.test>", 0)
	c1.Now = draftNow.Add(3 * time.Minute)
	if _, err := dbs.SaveDraft(ctx, c1); err != nil {
		t.Fatalf("save c1: %v", err)
	}
	if err := dbs.MarkDraftSent(ctx, "acct-1", "<c1@example.test>", draftNow.Add(3*time.Minute)); err != nil {
		t.Fatalf("mark c1 sent: %v", err)
	}

	heads, err := dbs.LiveDraftHeads(ctx, "acct-1", 50)
	if err != nil {
		t.Fatalf("heads: %v", err)
	}
	got := make([]string, len(heads))
	for i, h := range heads {
		got[i] = h.ID
	}
	want := []string{"b1", "a2"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("heads = %v, want %v", got, want)
	}
}

func TestDiscardDraftMarksAndEnqueuesRemoval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if _, err := dbs.SaveDraft(ctx, newDraftSave("v1", "d1", "<m1@example.test>", 0)); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := dbs.MarkDraftSaved(ctx, "v1", draftNow); err != nil {
		t.Fatalf("mark saved: %v", err)
	}
	d, err := dbs.DiscardDraft(ctx, "acct-1", "d1", "op-discard", draftNow)
	if err != nil {
		t.Fatalf("discard: %v", err)
	}
	if d.State != DraftDiscarded {
		t.Fatalf("discard state = %q, want %q", d.State, DraftDiscarded)
	}
	op, err := dbs.GetOutbox(ctx, "op-discard")
	if err != nil {
		t.Fatalf("get op: %v", err)
	}
	if op.Kind != OutboxDraft || !op.Expect.Remove {
		t.Fatalf("op = %+v, want a remove draft op", op)
	}
	if len(op.Expect.Supersedes) != 1 || op.Expect.Supersedes[0] != "<m1@example.test>" {
		t.Fatalf("supersedes = %v, want [m1]", op.Expect.Supersedes)
	}
}

func TestSaveDraftRefusesBeyondTheOutboxCap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	for i := range MaxQueuedOps {
		in := newDraftSave("v"+strconv.Itoa(i), "d"+strconv.Itoa(i), "<m"+strconv.Itoa(i)+"@example.test>", 0)
		in.OpID = "op-" + strconv.Itoa(i)
		if _, err := dbs.SaveDraft(ctx, in); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	in := newDraftSave("over", "over", "<over@example.test>", 0)
	in.OpID = "op-over"
	if _, err := dbs.SaveDraft(ctx, in); !errors.Is(err, ErrOutboxFull) {
		t.Fatalf("save over the cap = %v, want ErrOutboxFull", err)
	}
	// The refused save left no version row.
	if _, err := dbs.DraftBodyForOp(ctx, "over"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("refused save left a row: %v", err)
	}
}

// A draft created in another client has no local row but still appears: the
// list reads the mirrored Drafts folder for the server-side entries.
func TestDraftsInFolderListsServerDrafts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	if err := dbs.UpsertFolder(ctx, Folder{
		ID: "drafts-folder", AccountID: "acct-1", Name: "Drafts", Role: RoleDrafts,
	}); err != nil {
		t.Fatalf("folder: %v", err)
	}
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := dbs.UpsertMessage(ctx, Message{
		ID: "srv-1", AccountID: "acct-1", FolderID: "drafts-folder", UID: 1,
		ContentKey: "ck:srv-1", MessageID: "<srv-1@example.test>",
		Subject: "A draft from Apple Mail", To: []Address{{Address: "you@example.test"}},
		Date: base,
	}); err != nil {
		t.Fatalf("message: %v", err)
	}
	rows, err := dbs.DraftsInFolder(ctx, "acct-1", 50)
	if err != nil {
		t.Fatalf("drafts in folder: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "srv-1" || rows[0].Subject != "A draft from Apple Mail" {
		t.Fatalf("rows = %+v, want the one server draft", rows)
	}
	if len(rows[0].To) != 1 || rows[0].To[0].Address != "you@example.test" {
		t.Fatalf("to = %+v, want the stored recipient", rows[0].To)
	}
}

// A terminal abandoned draft is pruned after the retention; a live draft is not.
func TestPruneDraftsRemovesOnlyOldTerminalRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if _, err := dbs.SaveDraft(ctx, newDraftSave("v1", "d1", "<m1@example.test>", 0)); err != nil {
		t.Fatalf("save v1: %v", err)
	}
	if err := dbs.MarkDraftSaved(ctx, "v1", draftNow); err != nil {
		t.Fatalf("mark saved: %v", err)
	}
	if _, err := dbs.DiscardDraft(ctx, "acct-1", "d1", "op-discard", draftNow); err != nil {
		t.Fatalf("discard: %v", err)
	}
	if _, err := dbs.SaveDraft(ctx, newDraftSave("v2", "d2", "<m2@example.test>", 0)); err != nil {
		t.Fatalf("save v2: %v", err)
	}

	n, err := dbs.PruneDrafts(ctx, draftNow.Add(DraftTerminalRetention+time.Hour))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("pruned %d rows, want the one terminal row", n)
	}
	rows, err := dbs.draftsForTest(ctx)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "v2" {
		t.Fatalf("rows = %+v, want only the live v2", rows)
	}
}

// A version whose op failed for good is `failed`: still the head (it holds the
// only copy of what the operator typed, so it is listed and never pruned by age),
// able to be discarded or sent, and dropped once a newer save supersedes it.
func TestFailedDraftVersionStaysTheHeadUntilSuperseded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if _, err := dbs.SaveDraft(ctx, newDraftSave("v1", "d1", "<m1@example.test>", 0)); err != nil {
		t.Fatalf("save v1: %v", err)
	}
	if err := dbs.MarkDraftFailed(ctx, "v1", draftNow); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	if err := dbs.MarkDraftFailed(ctx, "v1", draftNow); !errors.Is(err, ErrNotFound) {
		t.Errorf("a second MarkDraftFailed = %v, want ErrNotFound (only a saving version can fail)", err)
	}
	if err := dbs.MarkDraftSaved(ctx, "v1", draftNow); !errors.Is(err, ErrNotFound) {
		t.Errorf("MarkDraftSaved on a failed version = %v, want ErrNotFound", err)
	}

	heads, err := dbs.LiveDraftHeads(ctx, "acct-1", 10)
	if err != nil {
		t.Fatalf("heads: %v", err)
	}
	if len(heads) != 1 || heads[0].State != DraftFailed {
		t.Fatalf("heads = %+v, want the failed version listed", heads)
	}
	// A failed head is the operator's only copy: a retention pass leaves it.
	if n, err := dbs.PruneDrafts(ctx, draftNow.Add(30*24*time.Hour)); err != nil || n != 0 {
		t.Fatalf("PruneDrafts = %d, %v, want the failed head kept", n, err)
	}

	// A newer save supersedes it and the failed row is pruned with its bytes.
	if _, err := dbs.SaveDraft(ctx, newDraftSave("v2", "d1", "<m2@example.test>", 1)); err != nil {
		t.Fatalf("save v2: %v", err)
	}
	if _, err := dbs.DraftVersion(ctx, "v1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("v1 after v2 = %v, want it pruned", err)
	}
}

// A failed head can still be discarded (the list offers it) and a send that came
// from it still settles it.
func TestFailedDraftCanBeDiscardedAndSent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if _, err := dbs.SaveDraft(ctx, newDraftSave("v1", "d1", "<m1@example.test>", 0)); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := dbs.MarkDraftFailed(ctx, "v1", draftNow); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	if err := dbs.MarkDraftSent(ctx, "acct-1", "<m1@example.test>", draftNow); err != nil {
		t.Fatalf("mark sent: %v", err)
	}

	if _, err := dbs.SaveDraft(ctx, newDraftSave("v2", "d2", "<m2@example.test>", 0)); err != nil {
		t.Fatalf("save d2: %v", err)
	}
	if err := dbs.MarkDraftFailed(ctx, "v2", draftNow); err != nil {
		t.Fatalf("mark d2 failed: %v", err)
	}
	if _, err := dbs.DiscardDraft(ctx, "acct-1", "d2", "op-discard", draftNow); err != nil {
		t.Fatalf("discard a failed draft: %v", err)
	}
}

// A draft's built MIME (attachments included, tens of MiB) lives on disk under its
// hash, not in the backed-up state database, so an autosave never grows state.db by
// the size of a photo.
func TestSaveDraftKeepsTheBodyOutOfTheDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	body := bytes.Repeat([]byte("x"), 1<<20)
	in := newDraftSave("v1", "d1", "<m1@example.test>", 0)
	in.Body = body
	if _, err := dbs.SaveDraft(ctx, in); err != nil {
		t.Fatalf("save: %v", err)
	}

	var inline int
	var hash string
	if err := dbs.State.Read.QueryRowContext(ctx,
		`SELECT length(body), body_hash FROM drafts WHERE id = 'v1'`).Scan(&inline, &hash); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if inline != 0 || hash == "" {
		t.Fatalf("row holds %d inline bytes and hash %q, want none inline and a body hash", inline, hash)
	}
	if !dbs.DraftBodies.Has(hash) {
		t.Fatal("the body is not in the draft body store")
	}
	got, err := dbs.DraftBodyForOp(ctx, "v1")
	if err != nil {
		t.Fatalf("body for op: %v", err)
	}
	if !bytes.Equal(got.Body, body) {
		t.Errorf("body for op = %d bytes, want the %d saved", len(got.Body), len(body))
	}
}

// A superseded version and an aged terminal one release their body files with
// their rows.
func TestDraftBodiesAreReleasedWithTheirRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	hashOf := func(id string) string {
		t.Helper()
		var h string
		if err := dbs.State.Read.QueryRowContext(ctx, `SELECT body_hash FROM drafts WHERE id = ?`, id).Scan(&h); err != nil {
			t.Fatalf("hash of %s: %v", id, err)
		}
		return h
	}
	one := newDraftSave("v1", "d1", "<m1@example.test>", 0)
	one.Body = []byte("first body")
	if _, err := dbs.SaveDraft(ctx, one); err != nil {
		t.Fatalf("save v1: %v", err)
	}
	h1 := hashOf("v1")
	if err := dbs.MarkDraftSaved(ctx, "v1", draftNow); err != nil {
		t.Fatalf("mark saved: %v", err)
	}
	two := newDraftSave("v2", "d1", "<m2@example.test>", 1)
	two.Body = []byte("second body")
	if _, err := dbs.SaveDraft(ctx, two); err != nil {
		t.Fatalf("save v2: %v", err)
	}
	if dbs.DraftBodies.Has(h1) {
		t.Error("the superseded version's body survived its row")
	}
	h2 := hashOf("v2")
	if !dbs.DraftBodies.Has(h2) {
		t.Fatal("the head's body is missing")
	}

	if err := dbs.MarkDraftSent(ctx, "acct-1", "<m2@example.test>", draftNow); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	if n, err := dbs.PruneDrafts(ctx, draftNow.Add(DraftTerminalRetention+time.Hour)); err != nil || n != 1 {
		t.Fatalf("PruneDrafts = %d, %v, want the sent row", n, err)
	}
	if dbs.DraftBodies.Has(h2) {
		t.Error("a pruned row's body survived")
	}
}

// Rows saved before bodies moved to disk keep their inline bytes and still read.
func TestDraftBodyForOpReadsALegacyInlineRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if _, err := dbs.State.Write.ExecContext(ctx, `
		INSERT INTO drafts (id, draft_id, account_id, version, message_id, content_key, body, state, created_at, updated_at)
		VALUES ('old', 'dold', 'acct-1', 1, '<old@example.test>', 'ck', ?, 'saved', ?, ?)`,
		[]byte("legacy raw mime"), formatTime(draftNow), formatTime(draftNow)); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	got, err := dbs.DraftBodyForOp(ctx, "old")
	if err != nil {
		t.Fatalf("body for op: %v", err)
	}
	if string(got.Body) != "legacy raw mime" {
		t.Errorf("body = %q, want the inline bytes", got.Body)
	}
}

// A body file with no row (a crash between the write and the insert, or a refused
// save) is collected; a referenced one is kept.
func TestSweepOrphanDraftBodies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if _, err := dbs.SaveDraft(ctx, newDraftSave("v1", "d1", "<m1@example.test>", 0)); err != nil {
		t.Fatalf("save: %v", err)
	}
	var kept string
	if err := dbs.State.Read.QueryRowContext(ctx, `SELECT body_hash FROM drafts WHERE id = 'v1'`).Scan(&kept); err != nil {
		t.Fatalf("hash: %v", err)
	}
	orphan, _, err := dbs.DraftBodies.Put(ctx, bytes.NewReader([]byte("never recorded")))
	if err != nil {
		t.Fatalf("put orphan: %v", err)
	}
	n, err := dbs.SweepOrphanDraftBodies(ctx)
	if err != nil || n != 1 {
		t.Fatalf("sweep = %d, %v, want the orphan only", n, err)
	}
	if dbs.DraftBodies.Has(orphan) || !dbs.DraftBodies.Has(kept) {
		t.Errorf("orphan present = %v, referenced present = %v", dbs.DraftBodies.Has(orphan), dbs.DraftBodies.Has(kept))
	}
}
