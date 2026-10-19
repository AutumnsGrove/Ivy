package insight

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

// Written as runes so no invisible character sits in the source where a reviewer
// could not see it.
const (
	rightToLeftOverride = string(rune(0x202e))
	zeroWidthSpace      = string(rune(0x200b))
)

func TestCleanKeepsPlainTextAsItIs(t *testing.T) {
	got, err := Clean("Sam and Priya settle on Thursday for the venue walk-through.")
	if err != nil || got != "Sam and Priya settle on Thursday for the venue walk-through." {
		t.Fatalf("got %q, %v", got, err)
	}
}

// Model output is data, never markup: whatever the model sends, the reader gets text.
func TestCleanReducesMarkupAndControlsToPlainText(t *testing.T) {
	cases := map[string]string{
		"tags":        "<b>Agreed</b> on <script>alert(1)</script>Friday",
		"entities":    "Agreed &lt;img src=x onerror=alert(1)&gt; on Friday",
		"controls":    "Agreed\x00 on\x1b[31m Friday" + rightToLeftOverride,
		"whitespace":  "  Agreed \n\n on\t\tFriday  ",
		"zero width":  "Agr" + zeroWidthSpace + "eed on Friday",
		"unclosed":    "Agreed on <img src=x onerror=alert(1) Friday",
		"markdownish": "**Agreed** on `Friday`",
	}
	for name, in := range cases {
		got, err := Clean(in)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if strings.ContainsAny(got, "<>") || strings.ContainsFunc(got, func(r rune) bool { return r < ' ' && r != '\n' }) {
			t.Errorf("%s: %q still has markup or controls", name, got)
		}
		if strings.Contains(got, rightToLeftOverride) || strings.Contains(got, zeroWidthSpace) {
			t.Errorf("%s: %q keeps an invisible character", name, got)
		}
		if !strings.Contains(got, "Friday") {
			t.Errorf("%s: %q lost the text", name, got)
		}
	}
}

func TestCleanKeepsParagraphsButNotBlankRuns(t *testing.T) {
	got, err := Clean("First point.\n\n\n\nSecond point.")
	if err != nil {
		t.Fatal(err)
	}
	if got != "First point.\n\nSecond point." {
		t.Fatalf("got %q, want the two paragraphs separated by one blank line", got)
	}
}

func TestCleanCutsAtAWordBoundaryAndSaysSo(t *testing.T) {
	long := strings.Repeat("quarterly planning ", 200)
	got, err := Clean(long)
	if err != nil {
		t.Fatal(err)
	}
	if n := utf8.RuneCountInString(got); n > MaxSummaryRunes {
		t.Fatalf("%d runes, want at most %d", n, MaxSummaryRunes)
	}
	cut, marked := strings.CutSuffix(got, "…")
	if !marked || strings.HasSuffix(cut, " ") {
		t.Fatalf("got a cut that does not end cleanly: %q", got[len(got)-20:])
	}
	if !strings.HasSuffix(cut, "planning") && !strings.HasSuffix(cut, "quarterly") {
		t.Fatalf("cut a word in half: %q", got[len(got)-20:])
	}
}

func TestCleanRefusesWhatIsNotASummary(t *testing.T) {
	for name, in := range map[string]string{
		"empty":      "",
		"blank":      " \n\t ",
		"only tags":  "<p></p><br>",
		"huge reply": strings.Repeat("a ", MaxReplyBytes),
		"bad utf8":   "\xff\xfe\xfd",
	} {
		if _, err := Clean(in); !errors.Is(err, ErrInvalidSummary) {
			t.Errorf("%s: err = %v, want ErrInvalidSummary", name, err)
		}
	}
}
