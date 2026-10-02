package devstack_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/AutumnsGrove/Ivy/internal/devstack"
	"github.com/AutumnsGrove/Ivy/internal/mailworld"
)

func TestParseScenario(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "scenario.yaml")
	body := `
steps:
  - state: mirror-healthy
  - deliver:
      account: ivy@grove.test
      mailbox: Empty
      from: other@example.com
      subject: Hello
      text: Body
  - advance_clock: 2h
  - fault:
      kind: smtp-reject
      code: 552
      message: too big
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := devstack.ParseScenario(path)
	if err != nil {
		t.Fatalf("ParseScenario: %v", err)
	}
	if len(s.Steps) != 4 {
		t.Fatalf("got %d steps, want 4", len(s.Steps))
	}
	if s.Steps[0].State != "mirror-healthy" {
		t.Errorf("step 0 = %+v", s.Steps[0])
	}
	if s.Steps[1].Deliver == nil || s.Steps[1].Deliver.Subject != "Hello" {
		t.Errorf("step 1 = %+v", s.Steps[1])
	}
	if s.Steps[2].AdvanceClock != "2h" {
		t.Errorf("step 2 = %+v", s.Steps[2])
	}
	if s.Steps[3].Fault == nil || s.Steps[3].Fault.Code != 552 {
		t.Errorf("step 3 = %+v", s.Steps[3])
	}
}

func TestRunScenarioAppliesSteps(t *testing.T) {
	t.Parallel()
	w, c := startControl(t)
	addr := "ivy@grove.test"

	scenario := devstack.Scenario{Steps: []devstack.Step{
		{State: "mirror-healthy"},
		{Deliver: &devstack.DeliverStep{
			Account: addr, Mailbox: "Archive", From: "friend@example.com",
			Subject: "scripted", Text: "from a scenario",
		}},
		{Flag: &devstack.FlagStep{Account: addr, Mailbox: "Archive", UID: 1, Flags: []string{"seen"}}},
		{Move: &devstack.MoveStep{Account: addr, Mailbox: "Archive", UID: 1, Dest: "Trash"}},
		{AdvanceClock: "2h"},
	}}

	before := w.Clock().Now()
	if err := devstack.RunScenario(c, scenario, ""); err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if got := w.Clock().Now(); !got.Equal(before.Add(2 * time.Hour)) {
		t.Fatalf("clock = %s, want %s", got, before.Add(2*time.Hour))
	}
	if n := mailboxCounts(t, w, addr, "Archive"); n != 0 {
		t.Fatalf("Archive has %d messages, want 0", n)
	}
	if n := mailboxCounts(t, w, addr, "Trash"); n != 1 {
		t.Fatalf("Trash has %d messages, want 1", n)
	}
	flags := fetchFlags(t, w, addr, "Trash", 1)
	if !hasFlag(flags, imap.FlagSeen) {
		t.Fatalf("flags = %v, want Seen", flags)
	}
}

func TestRunScenarioReportsBadStep(t *testing.T) {
	t.Parallel()
	_, c := startControl(t)
	scenario := devstack.Scenario{Steps: []devstack.Step{
		{AdvanceClock: "not-a-duration"},
	}}
	if err := devstack.RunScenario(c, scenario, ""); err == nil {
		t.Fatal("expected an error for a bad duration")
	}
}

func TestRunScenarioDeliversFromFile(t *testing.T) {
	t.Parallel()
	w, c := startControl(t)
	addr := "ivy@grove.test"

	dir := t.TempDir()
	msg := filepath.Join(dir, "msg.eml")
	raw := mailworld.Msg().From("a@example.com").To(addr).Subject("file").Text("x").Build()
	if err := os.WriteFile(msg, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	scenario := devstack.Scenario{Steps: []devstack.Step{
		{Deliver: &devstack.DeliverStep{Account: addr, Mailbox: "Archive", File: "msg.eml"}},
	}}
	if err := devstack.RunScenario(c, scenario, dir); err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if n := mailboxCounts(t, w, addr, "Archive"); n != 1 {
		t.Fatalf("Archive has %d messages, want 1", n)
	}
}

// A misspelt field would otherwise parse to a half-empty step that does
// something other than what the author wrote.
func TestParseScenarioRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "scenario.yaml")
	body := "steps:\n  - deliver:\n      account: ivy@grove.test\n      subjct: typo\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := devstack.ParseScenario(path); err == nil {
		t.Fatal("ParseScenario accepted an unknown field")
	}
}
