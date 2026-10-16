// Package eval measures how well the needs-me cascade agrees with the operator's own
// judgement (gate G3, docs/handoffs/2026-10-16-G3-needs-me-evaluation.md). It is pure:
// the caller reads the store and passes rows in, so everything here is a function of
// its inputs and nothing reaches a provider or a disk.
package eval

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// idLen is how much of a content key names a row in the sheet: short enough to read,
// long enough that two messages never share one.
const idLen = 12

// ID is the short name a message has in the labelling sheet.
func ID(contentKey string) string {
	if len(contentKey) <= idLen {
		return contentKey
	}
	return contentKey[:idLen]
}

// Item is one message as the operator sees it while labelling. It holds nothing Ivy
// decided, so the labels are given blind.
type Item struct {
	Key, Date, From, Subject, Preview string
}

var sheetHeader = []string{"id", "date", "from", "subject", "preview", "needs_me"}

// Labels is the sheet's last column as typed, by row id. A blank cell means not
// labelled yet.
type Labels map[string]string

// defuse stops a spreadsheet running a cell as a formula. The text comes from mail,
// which anyone can send, and the sheet is meant to be opened in a spreadsheet.
func defuse(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// WriteSheet writes the labelling sheet. Labels already typed are kept against their
// rows, so regenerating after more mail is classified loses no work.
func WriteSheet(w io.Writer, items []Item, labels Labels) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(sheetHeader); err != nil {
		return err
	}
	for _, it := range items {
		id := ID(it.Key)
		rec := []string{id, defuse(it.Date), defuse(it.From), defuse(it.Subject), defuse(it.Preview), labels[id]}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// ReadSheet reads the labels back. A file that is not our sheet is refused whole
// rather than trusted row by row.
func ReadSheet(r io.Reader) (Labels, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = len(sheetHeader)
	head, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, errors.New("the labels file is empty; make one with `ivy eval sheet`")
	}
	if err != nil {
		return nil, fmt.Errorf("read the labels file: %w", err)
	}
	for i, want := range sheetHeader {
		if head[i] != want {
			return nil, fmt.Errorf("this is not a labels sheet: column %d is %q, want %q", i+1, head[i], want)
		}
	}
	out := Labels{}
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read the labels file: %w", err)
		}
		out[rec[0]] = strings.TrimSpace(rec[5])
	}
}

// Parse turns the typed cells into labels: true means it needed her. A blank cell is
// left out (not labelled yet); anything that is not a clear yes or no is an error
// naming the row, because a guessed label would quietly skew every number.
func (l Labels) Parse() (map[string]bool, error) {
	out := make(map[string]bool, len(l))
	ids := make([]string, 0, len(l))
	for id := range l {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		switch strings.ToLower(strings.TrimSpace(l[id])) {
		case "":
		case "y", "yes", "1", "true":
			out[id] = true
		case "n", "no", "0", "false":
			out[id] = false
		default:
			return nil, fmt.Errorf("row %s: %q is not y or n", id, l[id])
		}
	}
	return out, nil
}

// Stage1 is the stored answer to the needs_me question.
type Stage1 struct {
	Choice     string
	Probs      map[string]float64
	Confidence float64
}

// fires is the registry's own rule (jev.Evaluate): not the quiet option, and both the
// choice's probability and the answer's confidence reach the bar.
func (s Stage1) fires(quiet string, bar float64) bool {
	return s.Choice != "" && s.Choice != quiet && s.Probs[s.Choice] >= bar && s.Confidence >= bar
}

// Row is one classified message with everything the cascade said about it.
type Row struct {
	Key, Subject, From string
	Stage1             Stage1
	// Verdict is stage two's: "needs", another verdict, or empty when stage one did
	// not pass the message on (or stage two has not finished).
	Verdict string
	Reason  string
}

// Config is the cascade's shipped setting that the report reads against.
type Config struct {
	// Quiet is stage one's "nothing here" option.
	Quiet string
	// Threshold is the bar stage one fires at.
	Threshold float64
}

// Counts is a confusion matrix.
type Counts struct{ TP, FP, FN, TN int }

// Precision is the share of flagged mail that needed her. ok is false when nothing
// was flagged, so it never reads as 0 or 100.
func (c Counts) Precision() (float64, bool) {
	if c.TP+c.FP == 0 {
		return 0, false
	}
	return float64(c.TP) / float64(c.TP+c.FP), true
}

// Recall is the share of mail that needed her that was flagged. ok is false when
// none of it did.
func (c Counts) Recall() (float64, bool) {
	if c.TP+c.FN == 0 {
		return 0, false
	}
	return float64(c.TP) / float64(c.TP+c.FN), true
}

// SweepPoint is stage one at another bar.
type SweepPoint struct {
	Threshold float64
	Flagged   int
	Counts    Counts
}

// sweep is the bars tried. Literals, so a report can be compared across runs.
var sweep = []float64{0.30, 0.35, 0.40, 0.45, 0.50, 0.55, 0.60, 0.65, 0.70, 0.75, 0.80, 0.85, 0.90, 0.95}

// StageTwo is what the second look did with what the first look flagged.
type StageTwo struct {
	// Kept: flagged by stage one, kept by stage two, and she labelled it y.
	Kept int
	// Dropped: she labelled it y, stage one flagged it, and stage two did not keep it.
	Dropped int
	// NeverReached: she labelled it y and stage one did not flag it.
	NeverReached int
}

// Disagreement is a message the cascade got wrong as she sees it.
type Disagreement struct {
	// Kind is "missed" (she labelled y, it is not in Needs attention) or "wrong" (it is
	// there and she labelled n).
	Kind string
	Row  Row
}

// Report is the whole evaluation.
type Report struct {
	Config                          Config
	Labelled, Positives, Unlabelled int
	Stage1                          Counts
	Sweep                           []SweepPoint
	Cascade                         Counts
	Stage2                          StageTwo
	Disagreements                   []Disagreement
}

func tally(c *Counts, predicted, actual bool) {
	switch {
	case predicted && actual:
		c.TP++
	case predicted:
		c.FP++
	case actual:
		c.FN++
	default:
		c.TN++
	}
}

// Score compares the cascade with her labels. Rows keyed by ID(row.Key) that she has
// not labelled are counted as Unlabelled and left out of every figure.
func Score(rows []Row, labels map[string]bool, cfg Config) Report {
	r := Report{Config: cfg, Sweep: make([]SweepPoint, len(sweep))}
	for i, t := range sweep {
		r.Sweep[i].Threshold = t
	}
	for _, row := range rows {
		actual, labelled := labels[ID(row.Key)]
		if !labelled {
			r.Unlabelled++
			continue
		}
		r.Labelled++
		if actual {
			r.Positives++
		}
		flagged := row.Stage1.fires(cfg.Quiet, cfg.Threshold)
		surfaced := row.Verdict == "needs"
		tally(&r.Stage1, flagged, actual)
		tally(&r.Cascade, surfaced, actual)
		for i := range r.Sweep {
			at := row.Stage1.fires(cfg.Quiet, r.Sweep[i].Threshold)
			tally(&r.Sweep[i].Counts, at, actual)
		}
		if actual {
			switch {
			case !flagged:
				r.Stage2.NeverReached++
			case surfaced:
				r.Stage2.Kept++
			default:
				r.Stage2.Dropped++
			}
		}
		switch {
		case actual && !surfaced:
			r.Disagreements = append(r.Disagreements, Disagreement{Kind: "missed", Row: row})
		case !actual && surfaced:
			r.Disagreements = append(r.Disagreements, Disagreement{Kind: "wrong", Row: row})
		}
	}
	for i := range r.Sweep {
		c := r.Sweep[i].Counts
		r.Sweep[i].Flagged = c.TP + c.FP
	}
	// Missed first: a mail that needed her and was not surfaced is the costly kind.
	sort.SliceStable(r.Disagreements, func(i, j int) bool {
		a, b := r.Disagreements[i], r.Disagreements[j]
		if a.Kind != b.Kind {
			return a.Kind == "missed"
		}
		return a.Row.Key < b.Row.Key
	})
	return r
}

func rate(v float64, ok bool) string {
	if !ok {
		return "n/a"
	}
	return fmt.Sprintf("%.0f%%", v*100)
}

func (c Counts) line() string {
	p, pok := c.Precision()
	rc, rok := c.Recall()
	return fmt.Sprintf("flagged right %d, flagged wrong %d, missed %d, rightly left alone %d. Precision %s, recall %s.",
		c.TP, c.FP, c.FN, c.TN, rate(p, pok), rate(rc, rok))
}

func odds(p map[string]float64) string {
	names := make([]string, 0, len(p))
	for name := range p {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if p[names[i]] != p[names[j]] {
			return p[names[i]] > p[names[j]]
		}
		return names[i] < names[j]
	})
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = fmt.Sprintf("%s %.2f", name, p[name])
	}
	return strings.Join(parts, ", ")
}

// Markdown renders the report for the terminal.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Needs-me evaluation\n\n")
	fmt.Fprintf(&b, "Labelled %d (%d needed her), not labelled yet %d.\n\n", r.Labelled, r.Positives, r.Unlabelled)
	fmt.Fprintf(&b, "With this few messages every figure is rough: one message moves a percentage a lot. "+
		"Read the disagreements below before the percentages, and treat the bar as your choice, not a measurement.\n\n")

	fmt.Fprintf(&b, "## Stage one at %.2f\n\n%s\n\n", r.Config.Threshold, r.Stage1.line())
	fmt.Fprintf(&b, "Stage one over-includes on purpose, so recall matters more than precision here.\n\n")

	fmt.Fprintf(&b, "## Stage one at other bars\n\n| bar | flagged | precision | recall |\n|---|---|---|---|\n")
	for _, p := range r.Sweep {
		pr, pok := p.Counts.Precision()
		rc, rok := p.Counts.Recall()
		fmt.Fprintf(&b, "| %.2f | %d | %s | %s |\n", p.Threshold, p.Flagged, rate(pr, pok), rate(rc, rok))
	}

	fmt.Fprintf(&b, "\n## What reaches Needs attention (both stages)\n\n%s\n\n", r.Cascade.line())

	fmt.Fprintf(&b, "## Stage two alone\n\nOf the %d that needed her: kept %d, dropped %d, never reached stage two %d.\n\n",
		r.Positives, r.Stage2.Kept, r.Stage2.Dropped, r.Stage2.NeverReached)

	fmt.Fprintf(&b, "## Where it disagreed with you (%d)\n\n", len(r.Disagreements))
	if len(r.Disagreements) == 0 {
		fmt.Fprintf(&b, "Nowhere.\n")
	}
	for _, d := range r.Disagreements {
		what := "needed you but was not surfaced"
		if d.Kind == "wrong" {
			what = "was surfaced but did not need you"
		}
		fmt.Fprintf(&b, "- **%s** from %s %s.\n  Stage one: %s (%s, confidence %.2f).", d.Row.Subject, d.Row.From, what,
			d.Row.Stage1.Choice, odds(d.Row.Stage1.Probs), d.Row.Stage1.Confidence)
		if d.Row.Reason != "" {
			fmt.Fprintf(&b, " Stage two said: %s", d.Row.Reason)
		}
		b.WriteString("\n")
	}
	return b.String()
}
