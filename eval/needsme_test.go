package eval

import (
	"bytes"
	"strings"
	"testing"
)

func TestTheSheetCarriesNoAnswerFromIvyAndKeepsLabelsOnRegeneration(t *testing.T) {
	items := []Item{
		{Key: "aaaaaaaaaaaa1111", Date: "2026-10-15", From: "Ada <ada@example.com>", Subject: "Lunch?", Preview: "Are you free"},
		{Key: "bbbbbbbbbbbb2222", Date: "2026-10-14", From: "Shop", Subject: "Your receipt", Preview: "Thanks"},
	}
	var first bytes.Buffer
	if err := WriteSheet(&first, items, nil); err != nil {
		t.Fatal(err)
	}
	head := strings.SplitN(first.String(), "\n", 2)[0]
	if head != "id,date,from,subject,preview,needs_me" {
		t.Fatalf("header = %q; the sheet must show the message and nothing Ivy decided", head)
	}

	// She labels the first row, then more mail is classified and the sheet is rebuilt.
	typed := strings.Replace(first.String(), "Are you free,", "Are you free,y", 1)
	labels, err := ReadSheet(strings.NewReader(typed))
	if err != nil {
		t.Fatal(err)
	}
	more := append(items, Item{Key: "cccccccccccc3333", Date: "2026-10-16", From: "Bo", Subject: "Hi", Preview: "x"})
	var second bytes.Buffer
	if err := WriteSheet(&second, more, labels); err != nil {
		t.Fatal(err)
	}
	again, err := ReadSheet(&second)
	if err != nil {
		t.Fatal(err)
	}
	if again[ID("aaaaaaaaaaaa1111")] != "y" || len(again) != 3 || again[ID("cccccccccccc3333")] != "" {
		t.Fatalf("labels after regenerating = %v, want her y kept, two blanks, three rows", again)
	}
}

func TestTheSheetSurvivesSubjectsThatWouldBreakACSV(t *testing.T) {
	odd := Item{Key: "dddddddddddd4444", Date: "d", From: `"Quote, Comma" <q@example.com>`, Subject: "line one\nline two, \"quoted\"", Preview: "a,b\r\nc"}
	var buf bytes.Buffer
	if err := WriteSheet(&buf, []Item{odd}, nil); err != nil {
		t.Fatal(err)
	}
	labels, err := ReadSheet(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 1 {
		t.Fatalf("a hostile subject changed the row count to %d", len(labels))
	}
}

// Email is hostile input and she opens this file in a spreadsheet, which runs a cell
// that starts with = + - or @ as a formula. A sender controls the subject.
func TestNoCellFromTheMailCanRunAsASpreadsheetFormula(t *testing.T) {
	var buf bytes.Buffer
	item := Item{Key: "eeeeeeeeeeee5555", Date: "d", From: "+evil", Subject: `=HYPERLINK("http://x.test","click")`, Preview: "@SUM(1+1)"}
	if err := WriteSheet(&buf, []Item{item}, nil); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(buf.String(), "\n")[1:] {
		for _, bad := range []string{",=", ",+", ",-", ",@", `,"=`, `,"+`, `,"-`, `,"@`} {
			if strings.Contains(line, bad) {
				t.Errorf("a cell starts with a formula character (%q) in %q", bad, line)
			}
		}
	}
}

func TestOnlyAValidLabelCountsAndBlankIsNotLabelledYet(t *testing.T) {
	got, err := Labels{"a": "Y", "b": " no ", "c": "", "d": "needs", "e": "0"}.Parse()
	if err == nil {
		t.Fatalf("parsed %v; %q is not a label", got, "needs")
	}
	if !strings.Contains(err.Error(), "d") {
		t.Errorf("error %q must name the row", err)
	}
	got, err = Labels{"a": "Y", "b": " no ", "c": "", "e": "0", "f": "yes", "g": "1"}.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || !got["a"] || got["b"] || got["e"] || !got["f"] || !got["g"] {
		t.Errorf("parsed = %v", got)
	}
	if _, labelled := got["c"]; labelled {
		t.Error("a blank cell was counted as labelled")
	}
}

func TestAWrongHeaderIsRefusedBeforeAnyLabelIsTrusted(t *testing.T) {
	if _, err := ReadSheet(strings.NewReader("id,subject,label\nabc,x,y\n")); err == nil {
		t.Fatal("a sheet that is not ours was accepted")
	}
	if _, err := ReadSheet(strings.NewReader("")); err == nil {
		t.Fatal("an empty file was accepted")
	}
}

var shipped = Config{Quiet: "none", Threshold: 0.65}

// row builds one classified message: its stage-one answer, and stage two's verdict
// (empty when stage one did not pass it on).
func row(key, choice string, p map[string]float64, conf float64, verdict string) Row {
	return Row{Key: key, Subject: "s-" + key, From: "f-" + key, Stage1: Stage1{Choice: choice, Probs: p, Confidence: conf}, Verdict: verdict}
}

func TestTheNumbersAtTheShippedThreshold(t *testing.T) {
	// likely 0.66 passes at 0.65 and likely 0.5 does not; a high probability with low confidence does not either.
	rows := []Row{
		row("tp-kept", "likely", map[string]float64{"likely": 0.9}, 1, "needs"),
		row("tp-dropped-by-stage-two", "likely", map[string]float64{"likely": 0.8}, 1, "dismiss"),
		row("fp", "likely", map[string]float64{"likely": 0.7}, 1, "needs"),
		row("fn-below-bar", "likely", map[string]float64{"likely": 0.5}, 1, ""),
		row("fn-quiet", "none", map[string]float64{"none": 0.9, "likely": 0.1}, 1, ""),
		row("tn", "none", map[string]float64{"none": 1}, 1, ""),
		row("tn-low-confidence", "likely", map[string]float64{"likely": 0.9}, 0.4, ""),
	}
	// The sheet names a row by ID(key), so that is what her labels are keyed by.
	labels := map[string]bool{}
	for key, needed := range map[string]bool{
		"tp-kept": true, "tp-dropped-by-stage-two": true, "fp": false,
		"fn-below-bar": true, "fn-quiet": true, "tn": false, "tn-low-confidence": false,
	} {
		labels[ID(key)] = needed
	}
	r := Score(rows, labels, shipped)

	if r.Labelled != 7 || r.Positives != 4 || r.Unlabelled != 0 {
		t.Errorf("coverage = %+v", r)
	}
	if want := (Counts{TP: 2, FP: 1, FN: 2, TN: 2}); r.Stage1 != want {
		t.Errorf("stage one = %+v, want %+v", r.Stage1, want)
	}
	// What she sees in Needs attention: tp-kept and fp.
	if want := (Counts{TP: 1, FP: 1, FN: 3, TN: 2}); r.Cascade != want {
		t.Errorf("cascade = %+v, want %+v", r.Cascade, want)
	}
	if r.Stage2.Kept != 1 || r.Stage2.Dropped != 1 || r.Stage2.NeverReached != 2 {
		t.Errorf("stage two = %+v, want kept 1 (tp), dropped 1 (labelled y but dismissed), never reached 2", r.Stage2)
	}
	if p, ok := r.Stage1.Precision(); !ok || p != 2.0/3.0 {
		t.Errorf("precision = %v %v", p, ok)
	}
	if rec, ok := r.Stage1.Recall(); !ok || rec != 0.5 {
		t.Errorf("recall = %v %v", rec, ok)
	}
	var kinds []string
	for _, d := range r.Disagreements {
		kinds = append(kinds, d.Kind+":"+d.Row.Key)
	}
	want := []string{"missed:fn-below-bar", "missed:fn-quiet", "missed:tp-dropped-by-stage-two", "wrong:fp"}
	if strings.Join(kinds, " ") != strings.Join(want, " ") {
		t.Errorf("disagreements = %v, want %v (what the cascade got wrong, missed first)", kinds, want)
	}
}

func TestARateWithNothingUnderItIsNotApplicableAndNeverZeroOrOneHundred(t *testing.T) {
	rows := []Row{row("a", "none", map[string]float64{"none": 1}, 1, ""), row("b", "none", map[string]float64{"none": 1}, 1, "")}
	r := Score(rows, map[string]bool{"a": false, "b": false}, shipped)
	if _, ok := r.Stage1.Precision(); ok {
		t.Error("precision is defined with nothing flagged")
	}
	if _, ok := r.Stage1.Recall(); ok {
		t.Error("recall is defined with nothing that needed her")
	}
	md := r.Markdown()
	if !strings.Contains(md, "n/a") {
		t.Errorf("the report must say n/a:\n%s", md)
	}
}

func TestUnlabelledMailIsLeftOutOfEveryNumber(t *testing.T) {
	rows := []Row{
		row("a", "likely", map[string]float64{"likely": 0.9}, 1, "needs"),
		row("b", "likely", map[string]float64{"likely": 0.9}, 1, "needs"),
	}
	r := Score(rows, map[string]bool{"a": true}, shipped)
	if r.Labelled != 1 || r.Unlabelled != 1 || r.Stage1.TP != 1 || r.Stage1.FP != 0 {
		t.Errorf("report = %+v; the unlabelled message leaked in", r)
	}
}

func TestTheSweepReadsStoredProbabilitiesWithTheFiringRule(t *testing.T) {
	rows := []Row{
		row("hi", "likely", map[string]float64{"likely": 0.95}, 1, ""),
		row("mid", "likely", map[string]float64{"likely": 0.7}, 1, ""),
		row("low", "likely", map[string]float64{"likely": 0.4}, 1, ""),
		row("quiet", "none", map[string]float64{"none": 0.99}, 1, ""),
	}
	labels := map[string]bool{"hi": true, "mid": false, "low": true, "quiet": false}
	r := Score(rows, labels, shipped)

	byT := map[float64]SweepPoint{}
	for _, p := range r.Sweep {
		byT[p.Threshold] = p
	}
	if byT[0.30].Flagged != 3 || byT[0.70].Flagged != 2 || byT[0.95].Flagged != 1 {
		t.Errorf("flagged at 0.30/0.70/0.95 = %d/%d/%d, want 3/2/1", byT[0.30].Flagged, byT[0.70].Flagged, byT[0.95].Flagged)
	}
	for i := 1; i < len(r.Sweep); i++ {
		if r.Sweep[i].Flagged > r.Sweep[i-1].Flagged {
			t.Fatalf("raising the bar flagged more mail: %+v then %+v", r.Sweep[i-1], r.Sweep[i])
		}
	}
	if rec, _ := byT[0.30].Counts.Recall(); rec != 1 {
		t.Errorf("recall at 0.30 = %v, want 1", rec)
	}
}

func TestTheReportNamesEveryDisagreementWithItsReasonAndOddsAndCallsItRough(t *testing.T) {
	bad := row("fp", "likely", map[string]float64{"likely": 0.7, "none": 0.3}, 1, "needs")
	bad.Reason = "Ada asks Autumn to confirm lunch"
	bad.Subject = "Lunch | Friday?"
	r := Score([]Row{bad}, map[string]bool{"fp": false}, shipped)
	md := r.Markdown()
	for _, want := range []string{"Lunch | Friday?", "f-fp", "Ada asks Autumn to confirm lunch", "likely 0.70", "rough"} {
		if !strings.Contains(md, want) {
			t.Errorf("the report is missing %q:\n%s", want, md)
		}
	}
}
