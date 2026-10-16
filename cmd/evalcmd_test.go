package cmd

import (
	"bytes"
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/store"
)

// evalFixture is a mailbox where the worker has considered three messages: a real
// request stage two kept, a newsletter stage one let go, and one with no stage-one
// answer at all (the safety checks withheld it).
func evalFixture(t *testing.T) (*store.DBs, string) {
	t.Helper()
	ctx := context.Background()
	dbs, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	if err := dbs.UpsertAccount(ctx, store.Account{ID: "a", Address: "a@example.test"}); err != nil {
		t.Fatal(err)
	}
	if err := dbs.UpsertFolder(ctx, store.Folder{ID: "f", AccountID: "a", Name: "INBOX", Role: store.RoleInbox}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	for i, m := range []struct{ key, subject, body string }{
		{"aaaaaaaaaaaa-venue", "Venue", "Could you please confirm the venue by Friday?"},
		{"bbbbbbbbbbbb-news", "Weekly digest", "Ten links you might like"},
		{"cccccccccccc-held", "Odd one", "Something that was withheld"},
	} {
		when := at.Add(-time.Duration(i) * time.Hour)
		if err := dbs.UpsertMessage(ctx, store.Message{
			ID: "m" + m.key[:1], AccountID: "a", FolderID: "f", UID: uint32(i + 1), ContentKey: m.key,
			Subject: m.subject, BodyText: m.body, Date: when, InternalDate: when,
			From: store.Address{Name: "Sender " + m.key[:1], Address: m.key[:1] + "@example.com"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := dbs.MarkClassified(ctx, "a", []string{"aaaaaaaaaaaa-venue", "bbbbbbbbbbbb-news", "cccccccccccc-held"}, at); err != nil {
		t.Fatal(err)
	}

	reg, err := newJevRegistry(ctx, dbs, nil)
	if err != nil {
		t.Fatal(err)
	}
	q, ok := reg.Get("needs_me")
	if !ok {
		t.Fatal("the shipped needs_me question is missing")
	}
	model, _ := llm.LookupModel(llm.JevModelID)
	put := func(key, choice string, probs map[string]float64) {
		t.Helper()
		if err := dbs.PutDecisions(ctx, []store.Decision{{
			AccountID: "a", ContentKey: key, QuestionID: "needs_me", InstructionHash: q.Hash(), Model: model.Slug,
			Choice: choice, Probabilities: probs, Confidence: 1, DecidedAt: at,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	put("aaaaaaaaaaaa-venue", "likely", map[string]float64{"likely": 0.9, "maybe": 0.05, "none": 0.05})
	put("bbbbbbbbbbbb-news", "none", map[string]float64{"likely": 0.02, "maybe": 0.03, "none": 0.95})
	if err := dbs.PutNeedsMe(ctx, store.NeedsMeRow{
		AccountID: "a", ContentKey: "aaaaaaaaaaaa-venue", Verdict: "needs", Reason: "Ada wants the venue confirmed.", Model: "chat",
	}); err != nil {
		t.Fatal(err)
	}
	return dbs, filepath.Join(t.TempDir(), "eval")
}

// label fills the sheet the way she would, by message subject.
func label(t *testing.T, dir string, bySubject map[string]string) {
	t.Helper()
	path := filepath.Join(dir, "needs-me-labels.csv")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(f).ReadAll()
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range records[1:] {
		if cell, ok := bySubject[rec[3]]; ok {
			rec[5] = cell
		}
	}
	var out bytes.Buffer
	if err := csv.NewWriter(&out).WriteAll(records); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTheSheetListsClassifiedMailBlindAndTheReportScoresHerLabels(t *testing.T) {
	ctx := context.Background()
	dbs, dir := evalFixture(t)

	var told bytes.Buffer
	if err := runEvalSheet(ctx, dbs, dir, "", 100, &told); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "needs-me-labels.csv"))
	if err != nil {
		t.Fatal(err)
	}
	sheet := string(raw)
	for _, want := range []string{"Venue", "Weekly digest", "Odd one", "Sender a", "Could you please confirm"} {
		if !strings.Contains(sheet, want) {
			t.Errorf("the sheet is missing %q:\n%s", want, sheet)
		}
	}
	for _, leak := range []string{"likely", "0.9", "Ada wants the venue confirmed", "needs,"} {
		if strings.Contains(sheet, leak) {
			t.Errorf("the sheet shows %q, so she would not be labelling blind:\n%s", leak, sheet)
		}
	}
	if info, err := os.Stat(filepath.Join(dir, "needs-me-labels.csv")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the sheet names real mail and must be private: %v %v", info, err)
	}

	label(t, dir, map[string]string{"Venue": "y", "Weekly digest": "n", "Odd one": "y"})
	var report bytes.Buffer
	if err := runEvalReport(ctx, dbs, dir, "", &report); err != nil {
		t.Fatal(err)
	}
	md := report.String()
	for _, want := range []string{
		"Labelled 2 (1 needed her)", // the withheld one has no stage-one answer, so it cannot be scored
		"Precision 100%, recall 100%",
		"1 labelled message has no stage-one answer",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("the report is missing %q:\n%s", want, md)
		}
	}

	// Rebuilding the sheet keeps what she typed.
	if err := runEvalSheet(ctx, dbs, dir, "", 100, &told); err != nil {
		t.Fatal(err)
	}
	kept, _ := os.ReadFile(filepath.Join(dir, "needs-me-labels.csv"))
	if strings.Count(string(kept), ",y\n") != 2 || strings.Count(string(kept), ",n\n") != 1 {
		t.Errorf("regenerating lost her labels:\n%s", kept)
	}
}

func TestTheReportRefusesWhatItCannotTrust(t *testing.T) {
	ctx := context.Background()
	dbs, dir := evalFixture(t)

	if err := runEvalReport(ctx, dbs, dir, "", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "ivy eval sheet") {
		t.Fatalf("with no sheet: err = %v, want a pointer to `ivy eval sheet`", err)
	}
	if err := runEvalSheet(ctx, dbs, dir, "", 100, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	label(t, dir, map[string]string{"Venue": "maybe"})
	err := runEvalReport(ctx, dbs, dir, "", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), `"maybe" is not y or n`) {
		t.Fatalf("a bad label: err = %v, want it refused and named", err)
	}
	if err := runEvalReport(ctx, dbs, dir, "nobody", &bytes.Buffer{}); err == nil {
		t.Fatal("an account that does not exist was accepted")
	}
}

func TestTwoAccountsNeedTheOneToBeNamed(t *testing.T) {
	ctx := context.Background()
	dbs, dir := evalFixture(t)
	if err := dbs.UpsertAccount(ctx, store.Account{ID: "b", Address: "b@example.test"}); err != nil {
		t.Fatal(err)
	}
	err := runEvalSheet(ctx, dbs, dir, "", 100, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--account") {
		t.Fatalf("err = %v, want a request to name the account", err)
	}
	if err := runEvalSheet(ctx, dbs, dir, "a", 100, &bytes.Buffer{}); err != nil {
		t.Fatalf("naming it did not help: %v", err)
	}
}
