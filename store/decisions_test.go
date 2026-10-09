package store

import (
	"context"
	"testing"
	"time"
)

func decisionFor(q, hash, model, choice string) Decision {
	return Decision{
		AccountID: "acct", ContentKey: "ck1", QuestionID: q, InstructionHash: hash, Model: model,
		Choice: choice, Probabilities: map[string]float64{"none": 0.2, "likely": 0.8}, Confidence: 0.9,
		CostUSD: 0.00004, DecidedAt: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC),
	}
}

func TestDecisionsRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	in := decisionFor("needs_me", "h1", "jev-latest", "likely")
	if err := dbs.PutDecisions(ctx, []Decision{in}); err != nil {
		t.Fatal(err)
	}
	got, err := dbs.DecisionsFor(ctx, "acct", "ck1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d decisions, want 1", len(got))
	}
	d := got[0]
	if d.QuestionID != "needs_me" || d.Choice != "likely" || d.Confidence != 0.9 || d.Probabilities["likely"] != 0.8 ||
		d.InstructionHash != "h1" || d.Model != "jev-latest" || d.CostUSD != 0.00004 || !d.DecidedAt.Equal(in.DecidedAt) {
		t.Fatalf("round trip changed the decision: %+v", d)
	}
}

func TestDecisionsAreKeyedByQuestionHashAndModel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	all := []Decision{
		decisionFor("needs_me", "h1", "jev-latest", "none"),
		decisionFor("needs_me", "h2", "jev-latest", "likely"), // edited question: a new row, the old one kept
		decisionFor("needs_me", "h1", "jev-1.13", "none"),     // pinned model: a new row
		decisionFor("urgency", "h1", "jev-latest", "none"),
	}
	if err := dbs.PutDecisions(ctx, all); err != nil {
		t.Fatal(err)
	}
	got, _ := dbs.DecisionsFor(ctx, "acct", "ck1")
	if len(got) != 4 {
		t.Fatalf("got %d rows, want 4 distinct keys", len(got))
	}
	// Writing the same key again replaces, never duplicates (a retry after a crash).
	again := decisionFor("needs_me", "h1", "jev-latest", "likely")
	if err := dbs.PutDecisions(ctx, []Decision{again}); err != nil {
		t.Fatal(err)
	}
	got, _ = dbs.DecisionsFor(ctx, "acct", "ck1")
	if len(got) != 4 {
		t.Fatalf("a rewrite duplicated: %d rows", len(got))
	}
}

func TestDecisionsAreScopedByAccountAndContentKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	other := decisionFor("needs_me", "h1", "m", "none")
	other.AccountID = "acct2"
	third := decisionFor("needs_me", "h1", "m", "none")
	third.ContentKey = "ck2"
	if err := dbs.PutDecisions(ctx, []Decision{decisionFor("needs_me", "h1", "m", "none"), other, third}); err != nil {
		t.Fatal(err)
	}
	if got, _ := dbs.DecisionsFor(ctx, "acct", "ck1"); len(got) != 1 {
		t.Fatalf("leaked across accounts or keys: %d", len(got))
	}
}

func TestPutDecisionsIsAllOrNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	bad := decisionFor("urgency", "h1", "m", "none")
	bad.Probabilities = map[string]float64{"x": nan()}
	if err := dbs.PutDecisions(ctx, []Decision{decisionFor("needs_me", "h1", "m", "none"), bad}); err == nil {
		t.Fatal("a non-finite probability was stored")
	}
	if got, _ := dbs.DecisionsFor(ctx, "acct", "ck1"); len(got) != 0 {
		t.Fatalf("a failed batch left %d rows behind", len(got))
	}
}

func TestMissesRoundTripAndKeyLikeDecisions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	ms := []DecisionMiss{
		{AccountID: "acct", ContentKey: "ck1", QuestionID: "needs_me", InstructionHash: "h1", Model: "m", Reason: MissNothingToRead, At: at},
		{AccountID: "acct", ContentKey: "ck1", QuestionID: "urgency", InstructionHash: "h1", Model: "m", Reason: MissRejected, At: at},
	}
	if err := dbs.PutDecisionMisses(ctx, ms); err != nil {
		t.Fatal(err)
	}
	if err := dbs.PutDecisionMisses(ctx, ms[:1]); err != nil { // idempotent
		t.Fatal(err)
	}
	got, err := dbs.DecisionMissesFor(ctx, "acct", "ck1")
	if err != nil || len(got) != 2 {
		t.Fatalf("misses = %+v, %v", got, err)
	}
}

func TestPutDecisionsRefusesAnEmptyKey(t *testing.T) {
	t.Parallel()
	dbs := openTemp(t)
	d := decisionFor("", "h1", "m", "none")
	if err := dbs.PutDecisions(context.Background(), []Decision{d}); err == nil {
		t.Fatal("a decision without a question id was stored")
	}
}

func nan() float64 { var z float64; return z / z }
