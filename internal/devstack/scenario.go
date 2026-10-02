package devstack

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
)

// Scenario is a scripted sequence of dev-stack commands, used by E2E and by
// `ivy-dev scenario run` (STANDARDS.md section 3). Each step is a thin mapping
// to the same control protocol the CLI's individual commands use.
type Scenario struct {
	Steps []Step `yaml:"steps"`
}

// Step runs the first non-empty field; a step with none is an error.
type Step struct {
	Deliver      *DeliverStep `yaml:"deliver,omitempty"`
	Flag         *FlagStep    `yaml:"flag,omitempty"`
	Move         *MoveStep    `yaml:"move,omitempty"`
	Expunge      *ExpungeStep `yaml:"expunge,omitempty"`
	Fault        *FaultSpec   `yaml:"fault,omitempty"`
	State        string       `yaml:"state,omitempty"`
	AdvanceClock string       `yaml:"advance_clock,omitempty"`
}

// DeliverStep appends a message, from fields or a raw .eml file.
type DeliverStep struct {
	Account string `yaml:"account"`
	Mailbox string `yaml:"mailbox,omitempty"`
	From    string `yaml:"from,omitempty"`
	Subject string `yaml:"subject,omitempty"`
	Text    string `yaml:"text,omitempty"`
	File    string `yaml:"file,omitempty"`
}

// FlagStep adds flags to a message.
type FlagStep struct {
	Account string   `yaml:"account"`
	Mailbox string   `yaml:"mailbox,omitempty"`
	UID     uint32   `yaml:"uid"`
	Flags   []string `yaml:"flags"`
}

// MoveStep moves a message.
type MoveStep struct {
	Account string `yaml:"account"`
	Mailbox string `yaml:"mailbox,omitempty"`
	UID     uint32 `yaml:"uid"`
	Dest    string `yaml:"dest"`
}

// ExpungeStep removes a message.
type ExpungeStep struct {
	Account string `yaml:"account"`
	Mailbox string `yaml:"mailbox,omitempty"`
	UID     uint32 `yaml:"uid"`
}

// ParseScenario reads a YAML scenario from path.
func ParseScenario(path string) (Scenario, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: the operator names the scenario file
	if err != nil {
		return Scenario{}, fmt.Errorf("devstack: read scenario %s: %w", path, err)
	}
	var s Scenario
	if err := yaml.UnmarshalWithOptions(data, &s, yaml.Strict()); err != nil {
		return Scenario{}, fmt.Errorf("devstack: parse scenario %s: %w", path, err)
	}
	return s, nil
}

// RunScenario applies every step through the control client. baseDir resolves
// relative raw-message paths so a scenario travels with its fixtures.
func RunScenario(c *Client, s Scenario, baseDir string) error {
	for i, step := range s.Steps {
		if err := runStep(c, step, baseDir); err != nil {
			return fmt.Errorf("devstack: scenario step %d: %w", i+1, err)
		}
	}
	return nil
}

func runStep(c *Client, step Step, baseDir string) error {
	switch {
	case step.State != "":
		return c.State(step.State)
	case step.Fault != nil:
		return c.Fault(*step.Fault)
	case step.AdvanceClock != "":
		d, err := time.ParseDuration(step.AdvanceClock)
		if err != nil {
			return err
		}
		return c.AdvanceClock(d)
	case step.Deliver != nil:
		raw, err := scenarioMessage(step.Deliver, baseDir)
		if err != nil {
			return err
		}
		_, err = c.Deliver(step.Deliver.Account, mailboxOr(step.Deliver.Mailbox), raw)
		return err
	case step.Flag != nil:
		return c.Flag(step.Flag.Account, mailboxOr(step.Flag.Mailbox), step.Flag.UID, step.Flag.Flags...)
	case step.Move != nil:
		return c.Move(step.Move.Account, mailboxOr(step.Move.Mailbox), step.Move.UID, step.Move.Dest)
	case step.Expunge != nil:
		return c.Expunge(step.Expunge.Account, mailboxOr(step.Expunge.Mailbox), step.Expunge.UID)
	default:
		return errors.New("empty step")
	}
}

func scenarioMessage(d *DeliverStep, baseDir string) ([]byte, error) {
	if d.File != "" {
		path := d.File
		if !filepath.IsAbs(path) {
			path = filepath.Join(baseDir, path)
		}
		raw, err := os.ReadFile(path) //nolint:gosec // G304: a scenario file names its own fixtures
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		return raw, nil
	}
	from := d.From
	if from == "" {
		from = "other@example.com"
	}
	subject := d.Subject
	if subject == "" {
		subject = "dev message"
	}
	text := d.Text
	if text == "" {
		text = "A message delivered by ivy-dev.\n"
	}
	return mailworld.Msg().From(from).To(d.Account).Subject(subject).Text(text).Build(), nil
}

func mailboxOr(mailbox string) string {
	if mailbox == "" {
		return "INBOX"
	}
	return mailbox
}
