// Package insight is the quiet help on a long thread: a short summary that appears
// as a chip in the reader, produced by one chat call the first time a worthwhile
// thread is opened. Everything the model says is untrusted text, reduced to plain
// text before it is stored, so what the reader renders is never markup.
package insight

import (
	"errors"
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Bounds on what the model may send back and what is kept of it.
const (
	// MaxReplyBytes bounds the raw reply; a model asked for a few sentences that
	// sends more has not followed the instructions.
	MaxReplyBytes = 8 << 10
	// MaxSummaryRunes bounds the summary that reaches the reading surface.
	MaxSummaryRunes = 700
)

// ErrInvalidSummary means the reply could not become a summary. Nothing is stored,
// so the thread simply shows no chip.
var ErrInvalidSummary = errors.New("insight: the reply is not a usable summary")

var (
	tagPattern       = regexp.MustCompile(`<[^>]*>`)
	paragraphPattern = regexp.MustCompile(`\n[ \t]*\n`)
)

// Clean reduces model text to plain paragraphs: entities decoded then markup
// stripped, control and invisible direction characters removed, whitespace
// collapsed, blank-line runs made one paragraph break, and the whole cut at a word
// boundary if it is long.
func Clean(raw string) (string, error) {
	if len(raw) > MaxReplyBytes || !utf8.ValidString(raw) {
		return "", ErrInvalidSummary
	}
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	s = html.UnescapeString(s)
	s = tagPattern.ReplaceAllString(s, " ")
	s = strings.NewReplacer("<", "", ">", "").Replace(s)

	var paragraphs []string
	for _, block := range paragraphPattern.Split(s, -1) {
		if p := plainLine(block); p != "" {
			paragraphs = append(paragraphs, p)
		}
	}
	out := strings.Join(paragraphs, "\n\n")
	if out == "" {
		return "", ErrInvalidSummary
	}
	return cut(out), nil
}

// plainLine drops controls and invisible characters and folds every run of
// whitespace, newlines included, into one space.
func plainLine(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		case unicode.Is(unicode.Cc, r), unicode.Is(unicode.Cf, r), r == utf8.RuneError:
		default:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// cut shortens s to MaxSummaryRunes at a word boundary and marks the cut.
func cut(s string) string {
	if utf8.RuneCountInString(s) <= MaxSummaryRunes {
		return s
	}
	r := []rune(s)[:MaxSummaryRunes-1]
	for i := len(r) - 1; i > 0; i-- {
		if unicode.IsSpace(r[i]) {
			r = r[:i]
			break
		}
	}
	return strings.TrimRight(string(r), " \n,;:.-") + "…"
}
