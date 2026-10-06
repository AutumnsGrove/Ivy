package compose

import (
	"bytes"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
)

// goldmark renders the operator's markdown to the HTML part of an outgoing
// multipart/alternative. The default configuration is safe: raw HTML stays off
// (never html.WithUnsafe), so a typed <script> is text, not markup.
var goldmarkMD = goldmark.New()

// outgoingPolicy is the allow-list for the HTML part of an outgoing message. It
// is deliberately separate from render/'s inbound reader policy: outgoing HTML
// is for recipients' mail clients and must never be routed through the reader's
// sanitiser (CHUNK4-BRIEF trap). Raw HTML is already escaped by goldmark; this
// policy is the second line that also narrows link schemes to http, https and
// mailto.
var outgoingPolicy = newOutgoingPolicy()

func newOutgoingPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements(
		"p", "br", "hr",
		"strong", "em", "b", "i", "u", "s", "del", "mark",
		"ul", "ol", "li",
		"blockquote", "pre", "code",
		"h1", "h2", "h3", "h4", "h5", "h6",
		"a", "img",
		"table", "thead", "tbody", "tr", "th", "td",
	)
	p.AllowAttrs("href", "title").OnElements("a")
	p.AllowAttrs("src", "alt", "title").OnElements("img")
	p.RequireParseableURLs(true)
	p.AllowRelativeURLs(false)
	p.AllowURLSchemes("http", "https", "mailto")
	return p
}

// renderMarkdown converts the operator's text to the outgoing HTML part. Raw
// HTML is escaped, and a link whose scheme is not http, https or mailto loses
// its destination.
func renderMarkdown(text string) ([]byte, error) {
	var buf bytes.Buffer
	if err := goldmarkMD.Convert([]byte(text), &buf); err != nil {
		return nil, err
	}
	return outgoingPolicy.SanitizeBytes(buf.Bytes()), nil
}
