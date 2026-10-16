package mime

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// SpamStatus is the score a mail provider's SpamAssassin gave a message, read from
// its X-Spam-Status header. It is a hint shown beside the message and never an
// all-clear: a clean score does not make a message safe.
type SpamStatus struct {
	// Present is false when there is no score to show, whether the header is absent,
	// unreadable, or not believed.
	Present bool
	// Flagged is the provider's own verdict ("Yes").
	Flagged bool
	// Score is the total, negative for mail that looks wanted. Required is the
	// provider's threshold for calling it spam.
	Score, Required float64
}

// spamHeadRunes bounds how much of the header is read. It can list hundreds of rule
// names after the part that matters, which is its first few dozen bytes.
const spamHeadRunes = 256

// maxSpamScore is the largest magnitude believed. Real totals are single or double
// digits, so anything past this is not a score.
const maxSpamScore = 1000

var spamStatusRe = regexp.MustCompile(`^\s*(?i:(yes|no))\s*,\s*(?:score|hits)=(-?\d{1,6}(?:\.\d{1,4})?)\s+required=(\d{1,6}(?:\.\d{1,4})?)`)

// ParseSpamStatus reads the topmost X-Spam-Status value, which is the one the
// receiving server added; a sender's forged header sits below it. Anything that is not
// clearly the SpamAssassin form reads as no score.
//
// Whether to believe the header at all is the caller's decision: Parse only keeps it
// when a trusted server also vouched for the message.
func ParseSpamStatus(values []string) SpamStatus {
	if len(values) == 0 {
		return SpamStatus{}
	}
	head := values[0]
	if len(head) > spamHeadRunes {
		head = head[:spamHeadRunes]
	}
	m := spamStatusRe.FindStringSubmatch(head)
	if m == nil {
		return SpamStatus{}
	}
	score, err1 := strconv.ParseFloat(m[2], 64)
	required, err2 := strconv.ParseFloat(m[3], 64)
	if err1 != nil || err2 != nil || math.Abs(score) > maxSpamScore || required > maxSpamScore {
		return SpamStatus{}
	}
	return SpamStatus{Present: true, Flagged: strings.EqualFold(m[1], "yes"), Score: score, Required: required}
}
