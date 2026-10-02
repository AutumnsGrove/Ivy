// Package render turns parsed mail into something safe to display: it
// sanitizes sender HTML, rewrites inline cid: parts, applies the remote-content
// policy and produces a plain-text fallback. The security pass lives here and
// only here (ARCHITECTURE.md section 5).
package render

import (
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/AutumnsGrove/Ivy/mime"
	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
)

// MaxHTMLBytes is the largest sender HTML string SanitizeHTML will process.
// Above it the body falls back to the plain-text view (STANDARDS.md 4a).
const MaxHTMLBytes = 8 << 20

const tooLargeText = "[This message is too large to display here. Open it in a mail app to read it.]"

// IframeSandbox is the sandbox attribute for the reader's body frame. Scripts
// are forbidden by the sanitizer and the CSP, so allow-scripts is absent (that
// flag is what a same-origin sandbox escape needs); the same-origin flag keeps
// the relative inline-image URLs working.
const IframeSandbox = "allow-same-origin"

// ContentSecurityPolicy is the header for the document that displays a
// rendered body. It is deliberately tiny: no script, no frames, no forms and
// no network beyond inline parts (ARCHITECTURE.md section 5).
func ContentSecurityPolicy(opts Options) string {
	img := "img-src 'self'"
	if opts.AllowRemoteImages {
		img = "img-src 'self' https:"
	}
	return strings.Join([]string{
		"default-src 'none'",
		img,
		"style-src 'unsafe-inline'",
		"font-src 'self'",
		"base-uri 'none'",
		"form-action 'none'",
		"frame-ancestors 'self'",
	}, "; ")
}

// trackerRe matches URLs that are almost always beacons rather than content.
// It is anchored on separators so a real "tracking.co" domain is not caught by
// the substring alone.
var trackerRe = regexp.MustCompile(`(?i)(^|[/_.-])(beacon|tracking?|pixel|spacer|webbug|1x1)([_.-]|$)`)

var spaceRun = regexp.MustCompile(`\s+`)

// Options controls what a body may keep and load. The zero value is the safe
// one: no remote content, tracking pixels stripped.
type Options struct {
	// MessageID names the message, for cid: rewriting. Required for inline
	// images; an empty id leaves cid: sources unresolved.
	MessageID string
	// AllowRemoteImages permits http(s) images for a sender the operator has
	// allow-listed. Zero value blocks them.
	AllowRemoteImages bool
	// KeepTrackingPixels keeps 1x1 and known-beacon images even when remote
	// images are allowed. Zero value strips them.
	KeepTrackingPixels bool
	// InlineURL builds the local URL for a cid: part; nil uses the default
	// /api/v1/messages/<id>/inline/<cid> path.
	InlineURL func(cid string) string
}

// Result is the display-ready form of one message body.
type Result struct {
	HTML string
	Text string
	// RemoteBlocked counts remote resources omitted by policy; the UI uses it
	// to offer "show remote content".
	RemoteBlocked int
	// TrackersStripped counts tracking pixels removed even though remote
	// content was allowed.
	TrackersStripped int
}

// SanitizeHTML runs the full safety pass over one HTML body. It never fails: a
// body it cannot tokenise safely yields an empty string (the caller still has
// the plain-text fallback) rather than raw markup.
func SanitizeHTML(htmlStr string, opts Options) string {
	if len(htmlStr) > MaxHTMLBytes {
		return ""
	}
	return sanitize(htmlStr, opts).HTML
}

// Body renders a parsed message: the sanitized HTML (when there is any) and the
// plain-text fallback.
func Body(p mime.Parsed, opts Options) Result {
	if len(p.HTML) > MaxHTMLBytes {
		return Result{Text: tooLargeText}
	}
	res := Result{}
	if p.HTML != "" {
		res = sanitize(p.HTML, opts)
	}
	switch {
	case strings.TrimSpace(p.Text) != "":
		res.Text = strings.TrimSpace(p.Text)
	case res.HTML != "":
		res.Text = plainText(res.HTML)
	}
	return res
}

// sanitize runs bluemonday over the body and then rewrites cid: sources and
// enforces the remote-content policy. The second pass works on already-clean
// markup, so its only job is URLs, rel attributes and beacon removal.
func sanitize(htmlStr string, opts Options) Result {
	clean := basePolicy.Sanitize(htmlStr)
	res := Result{}
	out, ok := rewriteTokens(clean, opts, &res)
	if !ok {
		// A tokenizer failure means we cannot prove the markup is safe to
		// rewrite; fail closed on the HTML and let the text view carry the
		// message.
		return Result{Text: plainText(clean)}
	}
	res.HTML = out
	if res.Text == "" {
		res.Text = plainText(out)
	}
	return res
}

// rewriteTokens streams the sanitised markup and applies the URL policy: cid:
// parts become local links, remote resources are dropped or counted, tracking
// pixels are stripped and links gain noopener noreferrer.
func rewriteTokens(clean string, opts Options, res *Result) (string, bool) {
	z := html.NewTokenizer(strings.NewReader(clean))
	var b strings.Builder
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if errors.Is(z.Err(), io.EOF) {
				return b.String(), true
			}
			return "", false
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			name := strings.ToLower(tok.Data)
			switch name {
			case "img":
				if !rewriteImage(&tok, opts, res) {
					continue // dropped: a blocked remote image or tracking pixel
				}
			case "a", "area":
				rewriteLink(&tok, opts)
				addLinkRel(&tok)
			}
			b.WriteString(tok.String())
		case html.TextToken, html.EndTagToken, html.CommentToken, html.DoctypeToken:
			b.WriteString(z.Token().String())
		}
	}
}

// rewriteImage applies the cid/remote policy to one <img>. It returns false
// when the whole element should be dropped.
func rewriteImage(tok *html.Token, opts Options, res *Result) bool {
	src, i := attr(tok, "src")
	if i < 0 {
		return true
	}
	switch {
	case isCID(src):
		url := inlineURL(strings.TrimSpace(src[len("cid:"):]), opts)
		if url == "" {
			removeAttr(tok, i)
			return true
		}
		tok.Attr[i].Val = url
		return true
	case hasAuthority(src):
		if !opts.AllowRemoteImages {
			res.RemoteBlocked++
			return false
		}
		if !opts.KeepTrackingPixels && isTracker(tok, src) {
			res.TrackersStripped++
			return false
		}
		return true
	default:
		// A relative or unknown-scheme source was not written by us, so it could
		// make the reader fetch an Ivy URL the sender chose. Drop it.
		if !isOurInline(src, opts) {
			removeAttr(tok, i)
		}
		return true
	}
}

// rewriteLink applies the same URL rules to a link: only real destinations
// (with an authority or a mailto/tel scheme) and our own inline URLs survive.
func rewriteLink(tok *html.Token, opts Options) {
	href, i := attr(tok, "href")
	if i < 0 {
		return
	}
	switch {
	case isCID(href):
		url := inlineURL(strings.TrimSpace(href[len("cid:"):]), opts)
		if url == "" {
			removeAttr(tok, i)
			return
		}
		tok.Attr[i].Val = url
	case hasAuthority(href) || hasScheme(href, "mailto") || hasScheme(href, "tel"):
		// A real destination. addLinkRel makes it safe to open.
	default:
		if !isOurInline(href, opts) {
			removeAttr(tok, i)
		}
	}
}

func isCID(raw string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "cid:")
}

// hasAuthority reports whether a URL carries a host, including protocol-relative
// forms (//host/path) that would otherwise look relative.
func hasAuthority(raw string) bool {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "//") {
		return true
	}
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func hasScheme(raw, scheme string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), scheme+":")
}

// isOurInline reports whether a URL is one this renderer produced, so the
// output can be sanitized again without losing its inline images.
func isOurInline(raw string, opts Options) bool {
	p := inlinePrefix(opts)
	return p != "" && strings.HasPrefix(raw, p)
}

func inlinePrefix(opts Options) string {
	if opts.InlineURL != nil || opts.MessageID == "" {
		return ""
	}
	return "/api/v1/messages/" + opts.MessageID + "/inline/"
}

// inlineURL builds the local URL for a cid part. The default path is served by
// the message handlers (chunk 2f).
func inlineURL(cid string, opts Options) string {
	if cid == "" {
		return ""
	}
	if opts.InlineURL != nil {
		return opts.InlineURL(cid)
	}
	if opts.MessageID == "" {
		return ""
	}
	return "/api/v1/messages/" + opts.MessageID + "/inline/" + pathEscape(cid)
}

// isTracker reports whether a remote image is a beacon: a declared size of
// zero or at most two on either axis, or a tell-tale filename. Missing
// dimensions (-1) say nothing and fall through to the name check.
func isTracker(tok *html.Token, src string) bool {
	w := attrInt(tok, "width")
	h := attrInt(tok, "height")
	if w == 0 || h == 0 {
		return true
	}
	if w > 0 && h > 0 && (w <= 2 || h <= 2) {
		return true
	}
	return trackerRe.MatchString(src)
}

// addLinkRel makes external links safe to open: no window.opener and no
// referrer back to the Ivy message URL.
func addLinkRel(tok *html.Token) {
	if _, i := attr(tok, "href"); i < 0 {
		return
	}
	_, i := attr(tok, "rel")
	if i < 0 {
		tok.Attr = append(tok.Attr, html.Attribute{Key: "rel", Val: "noopener noreferrer"})
		return
	}
	tokens := strings.Fields(tok.Attr[i].Val)
	add := func(want string) {
		for _, t := range tokens {
			if strings.EqualFold(t, want) {
				return
			}
		}
		tokens = append(tokens, want)
	}
	add("noopener")
	add("noreferrer")
	tok.Attr[i].Val = strings.Join(tokens, " ")
}

func attr(tok *html.Token, key string) (string, int) {
	for i := range tok.Attr {
		if strings.EqualFold(tok.Attr[i].Key, key) {
			return tok.Attr[i].Val, i
		}
	}
	return "", -1
}

func removeAttr(tok *html.Token, i int) {
	tok.Attr = append(tok.Attr[:i], tok.Attr[i+1:]...)
}

func attrInt(tok *html.Token, key string) int {
	v, i := attr(tok, key)
	if i < 0 {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return -1
	}
	if n < 0 {
		return -1
	}
	return n
}

// pathEscape escapes a content-id for use as one path segment. cid values are
// sender-controlled, so the result is always confined to a single segment.
func pathEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			const hex = "0123456789ABCDEF"
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
		}
	}
	return b.String()
}

// plainText extracts readable text from HTML. It is used for the plain-text
// view of an HTML-only message and for the fallback when a body is too large to
// sanitize. The input must be treated as untrusted; only text nodes are read.
func plainText(htmlStr string) string {
	z := html.NewTokenizer(strings.NewReader(htmlStr))
	var b strings.Builder
	skip := 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			return strings.TrimSpace(spaceRun.ReplaceAllString(b.String(), " "))
		case html.TextToken:
			if skip == 0 {
				b.Write(z.Text())
				b.WriteByte(' ')
			}
		case html.StartTagToken:
			if name, _ := z.TagName(); isHiddenTag(string(name)) {
				skip++
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if isHiddenTag(string(name)) && skip > 0 {
				skip--
			}
			b.WriteByte(' ')
		case html.SelfClosingTagToken:
			b.WriteByte(' ')
		case html.CommentToken, html.DoctypeToken:
			// not visible text
		}
	}
}

func isHiddenTag(name string) bool {
	switch strings.ToLower(name) {
	case "script", "style", "head", "title", "template":
		return true
	}
	return false
}

// basePolicy is built once: it does not depend on Options (the remote/cid rules
// are applied in the token pass, which must see the original URLs to count
// them). bluemonday policies are safe for concurrent use.
var basePolicy = newBasePolicy()

func newBasePolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	p.AllowElements(
		"a", "abbr", "acronym", "address", "article", "aside", "b", "bdi", "bdo",
		"big", "blockquote", "br", "caption", "center", "cite", "code", "col",
		"colgroup", "dd", "del", "details", "dfn", "div", "dl", "dt", "em",
		"figcaption", "figure", "footer", "h1", "h2", "h3", "h4", "h5", "h6",
		"header", "hr", "i", "img", "ins", "kbd", "li", "main", "mark", "nav",
		"ol", "p", "pre", "q", "s", "samp", "section", "small", "span", "strike",
		"strong", "sub", "summary", "sup", "table", "tbody", "td", "tfoot", "th",
		"thead", "tr", "tt", "u", "ul", "var", "wbr",
	)

	// URLs: cid: is the only inline source Ivy trusts; http(s) is kept so the
	// policy can count and decide on it in rewriteTokens (and so links work).
	// Relative URLs pass the policy but rewriteTokens drops all but its own
	// inline links, so a sender cannot make the reader fetch an Ivy URL.
	p.AllowURLSchemes("cid", "http", "https", "mailto", "tel")
	p.AllowRelativeURLs(true)
	p.RequireParseableURLs(true)

	// No <base>, no <meta>, no <link>, no <style>, no forms, no frames, no
	// objects or media elements: they are simply not in the allow list.
	// Empty <a>/<img> shells survive sanitization (AllowNoAttrs) so the URL
	// pass below can keep the output stable when it removes a source: sanitizing
	// twice yields the same markup.
	p.AllowNoAttrs().OnElements("a", "area", "img")

	p.AllowAttrs("href").OnElements("a", "area")
	p.AllowAttrs("cite").OnElements("blockquote", "q", "del", "ins")
	p.AllowAttrs("name").OnElements("a")
	p.AllowAttrs("rel").OnElements("a", "area")

	p.AllowAttrs("src", "alt").OnElements("img")
	p.AllowAttrs("width", "height").OnElements("img")

	p.AllowAttrs("class", "title", "dir", "lang").Globally()

	p.AllowAttrs(
		"colspan", "rowspan", "align", "valign", "width", "height", "bgcolor",
	).OnElements("td", "th")
	p.AllowAttrs("span", "width").OnElements("col", "colgroup")
	p.AllowAttrs(
		"border", "cellpadding", "cellspacing", "align", "width", "height", "bgcolor",
	).OnElements("table")
	p.AllowAttrs("type", "start").OnElements("ol")
	p.AllowAttrs("value").OnElements("li")

	allowSafeStyles(p)
	return p
}

// allowSafeStyles permits the presentational subset real mail relies on while
// keeping every property that could trigger a network fetch or script
// (background, background-image, list-style-image, border-image, cursor, ...)
// off the list.
func allowSafeStyles(p *bluemonday.Policy) {
	props := []string{
		"color", "background-color",
		"font", "font-family", "font-size", "font-style", "font-weight",
		"line-height", "letter-spacing", "word-spacing",
		"text-align", "text-decoration", "text-indent", "text-transform",
		"white-space", "vertical-align", "direction",
		"padding", "padding-top", "padding-right", "padding-bottom", "padding-left",
		"margin", "margin-top", "margin-right", "margin-bottom", "margin-left",
		"border", "border-top", "border-right", "border-bottom", "border-left",
		"border-color", "border-style", "border-width", "border-radius",
		"width", "height", "max-width", "min-width",
		"display", "float", "clear",
		"list-style", "list-style-type", "list-style-position",
	}
	p.AllowStyles(props...).Globally()
}
