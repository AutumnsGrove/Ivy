package compose

import (
	"strings"
	"unicode/utf8"
)

// maxWireLine is below RFC 5322's 998-byte line limit with room to spare.
const maxWireLine = 900

// wireText makes a body safe to emit as 7bit: every line break is CRLF and no
// line is longer than maxWireLine. enmime sends an all-ASCII part as it was
// given, with no re-encoding, so without this an operator's lone LF or one long
// phone-typed paragraph reached the wire as it was. A long line is broken at a
// space; when hardCut is set and a line has none, at a rune boundary (plain
// text only: a hard cut could split an HTML tag).
func wireText(s string, hardCut bool) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	var out []string
	for _, line := range lines {
		for len(line) > maxWireLine {
			cut, skip := strings.LastIndexByte(line[:maxWireLine], ' '), 1
			if cut <= 0 {
				if !hardCut {
					// No space within reach: keep the line rather than corrupt it.
					break
				}
				cut, skip = maxWireLine, 0
				for cut > 0 && !utf8.RuneStart(line[cut]) {
					cut--
				}
			}
			out = append(out, line[:cut])
			line = line[cut+skip:]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\r\n")
}
