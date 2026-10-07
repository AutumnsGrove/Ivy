package compose

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// sanitizeOutgoingHTML narrows the operator's outgoing HTML to the same
// allow-list goldmark's output already passes through. It is the authoritative
// sanitiser for the HTML part; the client's paste walker is only defence for the
// editing surface.
func sanitizeOutgoingHTML(s string) []byte {
	return outgoingPolicy.SanitizeBytes([]byte(s))
}

// htmlToText derives the text/plain alternative from the sanitised HTML part.
// Readable text only: block ends become a line break, list items keep a marker,
// entities decode, and the contents of script, style and head are dropped. It is
// deliberately not render/'s inbound plainText: compose stays a pure builder and
// must not share the reader's path (CHUNK4-BRIEF, section 3).
func htmlToText(s string) string {
	z := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	// Lists are tracked as a stack so an <li> knows whether to write a bullet or
	// the next ordered number; a malformed list falls back to a bullet.
	var ordered []bool
	var counters []int
	var hrefs []string
	var linkStarts []int
	skip := 0
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return collapseText(b.String())
		case html.TextToken:
			if skip == 0 {
				b.Write(z.Text())
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			tag := string(name)
			switch tag {
			case "script", "style", "head", "title", "template":
				if tt == html.StartTagToken {
					skip++
				}
			case "a":
				if tt == html.StartTagToken {
					href := ""
					for hasAttr {
						var k, v []byte
						k, v, hasAttr = z.TagAttr()
						if string(k) == "href" {
							href = string(v)
						}
					}
					hrefs = append(hrefs, href)
					linkStarts = append(linkStarts, b.Len())
				}
			case "br":
				b.WriteByte('\n')
			case "ul":
				ordered = append(ordered, false)
				counters = append(counters, 0)
			case "ol":
				ordered = append(ordered, true)
				counters = append(counters, 0)
			case "li":
				b.WriteByte('\n')
				if n := len(ordered); n > 0 && ordered[n-1] {
					counters[n-1]++
					b.WriteString(strconv.Itoa(counters[n-1]))
					b.WriteString(". ")
				} else {
					b.WriteString("- ")
				}
			case "p", "div", "blockquote", "pre", "tr", "h1", "h2", "h3", "h4", "h5", "h6":
				b.WriteByte('\n')
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "script", "style", "head", "title", "template":
				if skip > 0 {
					skip--
				}
			case "a":
				if n := len(hrefs); n > 0 {
					// A text-only reader cannot follow a link, so show where it goes
					// unless the link text already is the address.
					href, start := hrefs[n-1], linkStarts[n-1]
					hrefs, linkStarts = hrefs[:n-1], linkStarts[:n-1]
					if !strings.Contains(b.String()[start:], strings.TrimPrefix(href, "mailto:")) && href != "" {
						b.WriteString(" (" + href + ")")
					}
				}
			case "p", "div", "blockquote", "pre", "tr", "li", "h1", "h2", "h3", "h4", "h5", "h6":
				b.WriteByte('\n')
			case "ul", "ol":
				if n := len(ordered); n > 0 {
					ordered = ordered[:n-1]
					counters = counters[:n-1]
				}
			}
		case html.CommentToken, html.DoctypeToken:
			// not visible text
		}
	}
}

// collapseText trims each line, drops runs of blank lines to one and removes the
// leading and trailing blanks, so the text/plain part is tidy without losing the
// paragraph and list separation the block tags added.
func collapseText(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" && (len(out) == 0 || out[len(out)-1] == "") {
			continue
		}
		out = append(out, line)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}
