package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestStopWaitingIsKeptPerMessageAndPerAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	now := classifyNow

	if refs, err := dbs.StoppedWaitingRefs(ctx, "a"); err != nil || len(refs) != 0 {
		t.Fatalf("before any = %v, %v", refs, err)
	}
	if err := dbs.StopWaiting(ctx, "a", "ck:1", now); err != nil {
		t.Fatal(err)
	}
	// Doing it twice is one row, not an error.
	if err := dbs.StopWaiting(ctx, "a", "ck:1", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := dbs.StopWaiting(ctx, "b", "ck:2", now); err != nil {
		t.Fatal(err)
	}

	a, err := dbs.StoppedWaitingRefs(ctx, "a")
	if err != nil || len(a) != 1 || a[0] != (ContentRef{AccountID: "a", ContentKey: "ck:1"}) {
		t.Fatalf("account a = %v, %v", a, err)
	}
	all, err := dbs.StoppedWaitingRefs(ctx, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("all accounts = %v, %v", all, err)
	}
	for _, bad := range [][2]string{{"", "ck"}, {"a", ""}} {
		if err := dbs.StopWaiting(ctx, bad[0], bad[1], now); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// The list the queries read is bounded: past the limit the oldest decisions are forgotten,
// which at worst shows a stale wait again, never loses mail.
func TestStopWaitingForgetsTheOldestPastTheLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	for i := 0; i < MaxStoppedWaits+3; i++ {
		if err := dbs.StopWaiting(ctx, "a", fmt.Sprintf("ck:%05d", i), classifyNow.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	refs, err := dbs.StoppedWaitingRefs(ctx, "a")
	if err != nil || len(refs) != MaxStoppedWaits {
		t.Fatalf("kept %d, %v; want %d", len(refs), err, MaxStoppedWaits)
	}
	have := map[string]bool{}
	for _, r := range refs {
		have[r.ContentKey] = true
	}
	if have["ck:00000"] || have["ck:00002"] || !have["ck:00003"] {
		t.Errorf("the oldest three should be the ones forgotten")
	}
}
