package jev

import (
	"fmt"
	"strings"
	"testing"
)

const validYAML = `
questions:
  - id: needs_me
    instructions: "Does this need a reply from the owner? Newsletters do not count."
    criteria:
      none: "nothing is asked of the owner"
      maybe: "a person may expect an answer"
      likely: "a person clearly expects an answer"
    quiet_option: none
    threshold: 0.8
    suppresses: [urgency]
  - id: urgency
    instructions: "How urgent is it?"
    criteria:
      low: "no rush"
      high: "soon"
    quiet_option: low
    threshold: 0.75
    scope: [acct-a]
    folders: [inbox, junk]
    enabled: false
`

func mustParse(t *testing.T, src string) []Question {
	t.Helper()
	qs, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return qs
}

func TestParseReadsEveryField(t *testing.T) {
	qs := mustParse(t, validYAML)
	if len(qs) != 2 {
		t.Fatalf("got %d questions, want 2", len(qs))
	}
	n := qs[0]
	if n.ID != "needs_me" || n.QuietOption != "none" || n.Threshold != 0.8 || len(n.Criteria) != 3 {
		t.Fatalf("needs_me parsed wrong: %+v", n)
	}
	if !n.IsEnabled() || len(n.Folders) != 0 || len(n.Scope) != 0 {
		t.Fatalf("defaults wrong (enabled, any folder list empty, any scope): %+v", n)
	}
	u := qs[1]
	if u.IsEnabled() || len(u.Scope) != 1 || len(u.Folders) != 2 {
		t.Fatalf("urgency parsed wrong: %+v", u)
	}
}

func TestParseRejects(t *testing.T) {
	q := func(extra string) string {
		return "questions:\n  - id: q\n    instructions: hi\n    criteria: {no: n, yes: y}\n    quiet_option: no\n    threshold: 0.8\n" + extra
	}
	many := new(strings.Builder)
	many.WriteString("questions:\n")
	for i := 0; i <= MaxQuestions; i++ {
		fmt.Fprintf(many, "  - id: q%d\n    instructions: hi\n    criteria: {no: n, yes: y}\n    quiet_option: no\n    threshold: 0.8\n", i)
	}
	bigCriteria := new(strings.Builder)
	for i := 0; i <= MaxOptions; i++ {
		fmt.Fprintf(bigCriteria, "o%d: d, ", i)
	}
	cases := map[string]string{
		"unknown key":           q("    colour: red\n"),
		"unknown top key":       q("") + "extra: 1\n",
		"empty id":              strings.Replace(q(""), "id: q", `id: ""`, 1),
		"bad id characters":     strings.Replace(q(""), "id: q", "id: Needs Me", 1),
		"no instructions":       strings.Replace(q(""), "instructions: hi", `instructions: ""`, 1),
		"huge instructions":     strings.Replace(q(""), "instructions: hi", "instructions: "+strings.Repeat("a", MaxInstructionBytes+1), 1),
		"one option":            strings.Replace(q(""), "{no: n, yes: y}", "{no: n}", 1),
		"too many options":      strings.Replace(q(""), "{no: n, yes: y}", "{"+bigCriteria.String()+"no: n}", 1),
		"quiet not an option":   strings.Replace(q(""), "quiet_option: no", "quiet_option: maybe", 1),
		"threshold zero":        strings.Replace(q(""), "threshold: 0.8", "threshold: 0", 1),
		"threshold above one":   strings.Replace(q(""), "threshold: 0.8", "threshold: 1.1", 1),
		"suppresses itself":     q("    suppresses: [q]\n"),
		"suppresses unknown":    q("    suppresses: [nope]\n"),
		"unknown folder":        q("    folders: [trash]\n"),
		"duplicate id":          q("") + strings.TrimPrefix(q(""), "questions:\n"),
		"too many questions":    many.String(),
		"not yaml":              "questions: [",
		"empty document":        "",
		"empty scope entry":     q("    scope: ['']\n"),
		"unknown feature":       q("    feature: telepathy\n"),
		"chat feature":          q("    feature: summary\n"),
		"option key with blank": strings.Replace(q(""), "{no: n, yes: y}", `{no: n, "": y}`, 1),
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(src)); err == nil {
				t.Fatalf("Parse accepted %q", name)
			}
		})
	}
}

func TestParseAcceptsAJevFeature(t *testing.T) {
	src := "questions:\n  - id: q\n    instructions: hi\n    criteria: {no: n, yes: y}\n    quiet_option: no\n    threshold: 0.8\n    feature: junk_rescue\n"
	qs := mustParse(t, src)
	if qs[0].Feature != "junk_rescue" {
		t.Fatalf("feature = %q", qs[0].Feature)
	}
}

func TestParseRefusesAHugeFile(t *testing.T) {
	if _, err := Parse([]byte(strings.Repeat("#", MaxFileBytes+1))); err == nil {
		t.Fatal("Parse read a file over the limit")
	}
}

func TestParseAcceptsAnEmptyQuestionList(t *testing.T) {
	// The built-in set ships empty at first; 5c to 5g add theirs.
	qs, err := Parse([]byte("questions: []\n"))
	if err != nil || len(qs) != 0 {
		t.Fatalf("empty list: %v, %v", qs, err)
	}
}

func TestHashCoversWhatChangesTheAnswerAndNothingElse(t *testing.T) {
	base := mustParse(t, validYAML)[0]
	h := base.Hash()
	if len(h) != 32 {
		t.Fatalf("hash %q is not 32 hex chars", h)
	}

	same := base
	same.Threshold = 0.5
	same.Scope = []string{"x"}
	same.Folders = []string{"junk"}
	same.Suppresses = nil
	off := false
	same.Enabled = &off
	same.QuietOption = "maybe"
	if same.Hash() != h {
		t.Fatal("tuning a threshold, scope, folder, switch or suppression changed the hash; that would re-ask every cached message")
	}

	text := base
	text.Instructions += " "
	if text.Hash() == h {
		t.Fatal("editing the instructions did not change the hash")
	}
	crit := base
	crit.Criteria = map[string]string{"none": "a", "maybe": "b", "likely": "changed"}
	if crit.Hash() == h {
		t.Fatal("editing a criterion did not change the hash")
	}
	// Order must not matter: a map has none.
	again := mustParse(t, validYAML)[0]
	if again.Hash() != h {
		t.Fatal("the same question hashed twice differently")
	}
}

func TestHashDoesNotConfuseFieldBoundaries(t *testing.T) {
	a := Question{Instructions: "ab", Criteria: map[string]string{"c": "d"}}
	b := Question{Instructions: "a", Criteria: map[string]string{"bc": "d"}}
	if a.Hash() == b.Hash() {
		t.Fatal("hash joined fields without a length prefix")
	}
}

func TestActiveFiltersByAccountFolderAndSwitch(t *testing.T) {
	r := NewRegistry()
	if err := r.Set(mustParse(t, validYAML), nil); err != nil {
		t.Fatal(err)
	}
	ids := func(qs []Question) string {
		var out []string
		for _, q := range qs {
			out = append(out, q.ID)
		}
		return strings.Join(out, ",")
	}
	// urgency is disabled, so only needs_me is asked, whatever the account.
	if got := ids(r.Active("acct-a", FolderInbox)); got != "needs_me" {
		t.Fatalf("inbox: %q", got)
	}
	// needs_me defaults to the inbox only: junk is not classified except by a question that names it.
	if got := ids(r.Active("acct-a", FolderJunk)); got != "" {
		t.Fatalf("junk: %q", got)
	}
	// An unknown folder asks nothing.
	if got := ids(r.Active("acct-a", "trash")); got != "" {
		t.Fatalf("trash: %q", got)
	}
}

func TestScopeLimitsToNamedAccounts(t *testing.T) {
	qs := mustParse(t, strings.Replace(validYAML, "enabled: false", "enabled: true", 1))
	r := NewRegistry()
	if err := r.Set(qs, nil); err != nil {
		t.Fatal(err)
	}
	if got := len(r.Active("acct-a", FolderInbox)); got != 2 {
		t.Fatalf("acct-a got %d, want 2", got)
	}
	if got := len(r.Active("acct-b", FolderInbox)); got != 1 {
		t.Fatalf("acct-b got %d, want 1 (urgency is scoped to acct-a)", got)
	}
}

func TestUserEditOverridesTheBuiltInByID(t *testing.T) {
	builtin := mustParse(t, validYAML)
	edited := builtin[0]
	edited.Threshold = 0.9
	r := NewRegistry()
	if err := r.Set(builtin, []Question{edited}); err != nil {
		t.Fatal(err)
	}
	got, ok := r.Get("needs_me")
	if !ok || got.Threshold != 0.9 {
		t.Fatalf("override not applied: %+v", got)
	}
	if n := len(r.All()); n != 2 {
		t.Fatalf("override duplicated the question: %d", n)
	}
}

func TestUserQuestionIsAddedBesideBuiltIns(t *testing.T) {
	builtin := mustParse(t, validYAML)
	mine := Question{
		ID: "check:receipt", Instructions: "Is it a receipt? Marketing does not count.",
		Criteria: map[string]string{"no": "not a receipt", "yes": "a receipt"}, QuietOption: "no", Threshold: 0.8,
	}
	r := NewRegistry()
	if err := r.Set(builtin, []Question{mine}); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get("check:receipt"); !ok {
		t.Fatal("the user's question is missing")
	}
}

func TestSetKeepsTheOldRegistryWhenTheNewOneIsInvalid(t *testing.T) {
	r := NewRegistry()
	if err := r.Set(mustParse(t, validYAML), nil); err != nil {
		t.Fatal(err)
	}
	bad := mustParse(t, validYAML)[0]
	bad.Threshold = 7
	if err := r.Set(mustParse(t, validYAML), []Question{bad}); err == nil {
		t.Fatal("Set accepted an invalid override")
	}
	if got, _ := r.Get("needs_me"); got.Threshold != 0.8 {
		t.Fatalf("a failed reload replaced the live registry: %+v", got)
	}
}

func TestSetValidatesTheMergedWholeNotJustEachHalf(t *testing.T) {
	builtin := mustParse(t, validYAML)
	// A user override that makes the built-in's suppression dangle.
	dangling := builtin[0]
	dangling.Suppresses = []string{"gone"}
	if err := NewRegistry().Set(builtin, []Question{dangling}); err == nil {
		t.Fatal("a dangling suppression slipped through the merge")
	}
}

func TestActiveReturnsACopy(t *testing.T) {
	r := NewRegistry()
	if err := r.Set(mustParse(t, validYAML), nil); err != nil {
		t.Fatal(err)
	}
	got := r.Active("acct-a", FolderInbox)
	got[0].Threshold = 0.01
	got[0].Criteria["none"] = "mutated"
	again, _ := r.Get("needs_me")
	if again.Threshold != 0.8 || again.Criteria["none"] == "mutated" {
		t.Fatal("a caller mutated the live registry")
	}
}

func TestBuiltInsLoadAndValidate(t *testing.T) {
	qs, err := Builtin()
	if err != nil {
		t.Fatalf("the shipped questions do not validate: %v", err)
	}
	for _, q := range qs {
		if len(q.ID) == 0 {
			t.Fatal("blank id")
		}
	}
}

func TestToLLMIsAChoiceQuestion(t *testing.T) {
	q := mustParse(t, validYAML)[0].ToLLM()
	if q.Type != "choice" || q.Instructions == "" || len(q.Criteria) != 3 {
		t.Fatalf("wire question wrong: %+v", q)
	}
}
