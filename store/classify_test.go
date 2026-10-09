package store

import (
	"context"
	"testing"
	"time"
)

var classifyNow = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

type classifySeed struct {
	id, folder, key string
	arrived         time.Time
	disabled        bool
}

func seedClassify(t *testing.T, dbs *DBs, account string, rows ...classifySeed) {
	t.Helper()
	ctx := context.Background()
	seedAccount(t, dbs, account)
	roles := map[string]string{"inbox": RoleInbox, "junk": RoleJunk, "archive": RoleArchive, "sent": RoleSent, "trash": RoleTrash}
	made := map[string]bool{}
	for i, r := range rows {
		if !made[r.folder] {
			if err := dbs.UpsertFolder(ctx, Folder{ID: account + "-" + r.folder, AccountID: account, Name: r.folder, Role: roles[r.folder]}); err != nil {
				t.Fatal(err)
			}
			made[r.folder] = true
		}
		m := Message{
			ID: r.id, AccountID: account, FolderID: account + "-" + r.folder, UID: uint32(i + 1),
			ContentKey: r.key, Subject: "s " + r.id, BodyText: "body", Date: r.arrived, InternalDate: r.arrived,
		}
		if err := dbs.UpsertMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
		if r.disabled {
			if _, err := dbs.Mirror.Write.ExecContext(ctx, `UPDATE messages SET disabled_at = ? WHERE id = ?`, formatTime(classifyNow), r.id); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func keysOf(cs []Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Message.ContentKey
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestUnclassifiedIsNewestFirstWithinInboxAndJunkOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	h := func(n int) time.Time { return classifyNow.Add(-time.Duration(n) * time.Hour) }
	seedClassify(t, dbs, "a",
		classifySeed{id: "m1", folder: "inbox", key: "k1", arrived: h(3)},
		classifySeed{id: "m2", folder: "inbox", key: "k2", arrived: h(1)},
		classifySeed{id: "m3", folder: "junk", key: "k3", arrived: h(2)},
		classifySeed{id: "m4", folder: "archive", key: "k4", arrived: h(1)},
		classifySeed{id: "m5", folder: "sent", key: "k5", arrived: h(1)},
		classifySeed{id: "m6", folder: "trash", key: "k6", arrived: h(1)},
	)
	got, err := dbs.UnclassifiedMessages(ctx, "a", h(10), 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"k2", "k3", "k1"}; !eq(keysOf(got), want) {
		t.Fatalf("got %v, want %v (Inbox and Junk only, newest first)", keysOf(got), want)
	}
}

func TestUnclassifiedHonoursTheWatermarkAndTheLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	h := func(n int) time.Time { return classifyNow.Add(-time.Duration(n) * time.Hour) }
	seedClassify(t, dbs, "a",
		classifySeed{id: "old", folder: "inbox", key: "old", arrived: h(50)},
		classifySeed{id: "m1", folder: "inbox", key: "k1", arrived: h(3)},
		classifySeed{id: "m2", folder: "inbox", key: "k2", arrived: h(2)},
		classifySeed{id: "m3", folder: "inbox", key: "k3", arrived: h(1)},
	)
	got, _ := dbs.UnclassifiedMessages(ctx, "a", h(24), 2)
	if want := []string{"k3", "k2"}; !eq(keysOf(got), want) {
		t.Fatalf("got %v, want %v (history before the watermark is never eligible)", keysOf(got), want)
	}
	// A message exactly at the watermark arrived "afterwards or at" it.
	got, _ = dbs.UnclassifiedMessages(ctx, "a", h(3), 10)
	if len(got) != 3 {
		t.Fatalf("at the watermark: %v", keysOf(got))
	}
}

func TestUnclassifiedSkipsHiddenMailAndOtherAccounts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedClassify(t, dbs, "a",
		classifySeed{id: "m1", folder: "inbox", key: "k1", arrived: classifyNow},
		classifySeed{id: "m2", folder: "inbox", key: "k2", arrived: classifyNow, disabled: true},
	)
	seedClassify(t, dbs, "b", classifySeed{id: "n1", folder: "inbox", key: "kb", arrived: classifyNow})
	got, _ := dbs.UnclassifiedMessages(ctx, "a", classifyNow.Add(-time.Hour), 10)
	if want := []string{"k1"}; !eq(keysOf(got), want) {
		t.Fatalf("got %v, want %v (nothing hidden, nothing from another account)", keysOf(got), want)
	}
}

func TestAMessageInTwoFoldersIsOneContentKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedClassify(t, dbs, "a",
		classifySeed{id: "m1", folder: "inbox", key: "same", arrived: classifyNow},
		classifySeed{id: "m2", folder: "junk", key: "same", arrived: classifyNow.Add(-time.Minute)},
	)
	got, _ := dbs.UnclassifiedMessages(ctx, "a", classifyNow.Add(-time.Hour), 10)
	if len(got) != 1 {
		t.Fatalf("one content key came back %d times; it must be asked about once (N8)", len(got))
	}
	if got[0].Role != RoleInbox || got[0].Message.ID != "m1" {
		t.Fatalf("picked %s in %s; the Inbox copy is preferred", got[0].Message.ID, got[0].Role)
	}
}

func TestMarkClassifiedRemovesFromTheQueueAndSurvivesAMove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedClassify(t, dbs, "a",
		classifySeed{id: "m1", folder: "inbox", key: "k1", arrived: classifyNow},
		classifySeed{id: "m2", folder: "inbox", key: "k2", arrived: classifyNow.Add(-time.Minute)},
	)
	if err := dbs.MarkClassified(ctx, "a", []string{"k1"}, classifyNow); err != nil {
		t.Fatal(err)
	}
	if err := dbs.MarkClassified(ctx, "a", []string{"k1"}, classifyNow); err != nil { // idempotent
		t.Fatal(err)
	}
	got, _ := dbs.UnclassifiedMessages(ctx, "a", classifyNow.Add(-time.Hour), 10)
	if want := []string{"k2"}; !eq(keysOf(got), want) {
		t.Fatalf("got %v, want %v", keysOf(got), want)
	}
	// Filing the message in Junk changes its folder, not its content key, so it is
	// not new mail and is not asked about again.
	if err := dbs.UpsertFolder(ctx, Folder{ID: "a-junk", AccountID: "a", Name: "junk", Role: RoleJunk}); err != nil {
		t.Fatal(err)
	}
	if _, err := dbs.Mirror.Write.ExecContext(ctx, `UPDATE messages SET folder_id = 'a-junk', uid = 99 WHERE id = 'm1'`); err != nil {
		t.Fatal(err)
	}
	got, _ = dbs.UnclassifiedMessages(ctx, "a", classifyNow.Add(-time.Hour), 10)
	if want := []string{"k2"}; !eq(keysOf(got), want) {
		t.Fatalf("after a move got %v, want %v", keysOf(got), want)
	}
}

func TestCountUnclassifiedMatchesTheQueue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	h := func(n int) time.Time { return classifyNow.Add(-time.Duration(n) * time.Hour) }
	seedClassify(t, dbs, "a",
		classifySeed{id: "m1", folder: "inbox", key: "k1", arrived: h(1)},
		classifySeed{id: "m2", folder: "inbox", key: "k2", arrived: h(2)},
		classifySeed{id: "m3", folder: "junk", key: "k3", arrived: h(30)},
	)
	_ = dbs.MarkClassified(ctx, "a", []string{"k2"}, classifyNow)
	n, err := dbs.CountUnclassified(ctx, "a", h(24))
	if err != nil || n != 1 {
		t.Fatalf("count = %d, %v; want 1 (k1 only: k2 is done, k3 is before the watermark)", n, err)
	}
	n, _ = dbs.CountUnclassified(ctx, "a", h(100))
	if n != 2 {
		t.Fatalf("count with a wider window = %d, want 2", n)
	}
}

func TestMeanClassifiableBytes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedClassify(t, dbs, "a", classifySeed{id: "m1", folder: "inbox", key: "k1", arrived: classifyNow})
	n, err := dbs.MeanBodyBytes(ctx, "a", classifyNow.Add(-time.Hour))
	if err != nil || n <= 0 {
		t.Fatalf("mean body bytes = %d, %v", n, err)
	}
	// No mail in the window is zero, not an error: the estimate then has nothing to price.
	if n, err := dbs.MeanBodyBytes(ctx, "a", classifyNow.Add(time.Hour)); err != nil || n != 0 {
		t.Fatalf("empty window = %d, %v", n, err)
	}
}
