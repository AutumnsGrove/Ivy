package gateway

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// initials renders a sender badge: two letters from the display name, or one
// from the address when there is no name.
func initials(name, email string) string {
	fields := strings.Fields(name)
	if len(fields) > 0 {
		var b strings.Builder
		b.WriteString(upperFirst(fields[0]))
		if len(fields) > 1 {
			b.WriteString(upperFirst(fields[len(fields)-1]))
		}
		return b.String()
	}
	return upperFirst(email)
}

func upperFirst(s string) string {
	r, _ := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return ""
	}
	return strings.ToUpper(string(r))
}

// accountShort is the compact form of an address the list shows beside the
// account dot: the local part and its "@" ("me@").
func accountShort(address string) string {
	at := strings.IndexByte(address, '@')
	if at < 0 {
		return address
	}
	return address[:at+1]
}

// humanSize renders an attachment size for display; the labels are the usual
// 1024-based ones even though they read KB/MB.
func humanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.0f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
	}
}

var blankLine = regexp.MustCompile(`\n[ \t\r]*\n+`)

// paragraphs turns a plain-text body into display paragraphs: a blank line
// starts a new one and a soft wrap inside a paragraph becomes a space, so the
// reader renders the sender's words, not their terminal wrapping.
func paragraphs(text string) []string {
	var out []string
	for _, chunk := range blankLine.Split(text, -1) {
		if joined := strings.Join(strings.Fields(chunk), " "); joined != "" {
			out = append(out, joined)
		}
	}
	return out
}

// slotOf maps the stored sort order onto the five account colours the UI has.
func slotOf(sortOrder int) int {
	if sortOrder < 0 {
		return 1
	}
	if slot := sortOrder + 1; slot <= 5 {
		return slot
	}
	return 5
}
