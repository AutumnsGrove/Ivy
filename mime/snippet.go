package mime

import "strings"

// snippetRunes caps the one-line preview shown in the inbox list. Runes, not
// bytes, so a multibyte character is never split.
const snippetRunes = 200

// Snippet reduces a message body to one whitespace-collapsed preview line.
func Snippet(text string) string {
	joined := strings.Join(strings.Fields(text), " ")
	runes := []rune(joined)
	if len(runes) > snippetRunes {
		runes = runes[:snippetRunes]
	}
	return string(runes)
}
