package compose_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jhillyerd/enmime"

	"github.com/AutumnsGrove/Ivy/compose"
)

// TestBuildHTMLBodySanitisesAndDerivesPlainText is the BodyHTML contract: the
// operator's HTML becomes the text/html part, narrowed to the same allow-list as
// rendered markdown, and the text/plain alternative is readable text derived from
// that sanitised HTML, so the two parts cannot disagree.
func TestBuildHTMLBodySanitisesAndDerivesPlainText(t *testing.T) {
	t.Parallel()
	m := base()
	m.Format = compose.BodyHTML
	m.Text = `<p>Hi <strong>there</strong>.</p><p><a href="https://example.test/docs">Docs</a></p>`
	raw, _, err := compose.Build(m)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	env := readEnvelope(t, raw)
	if !strings.Contains(env.HTML, "<strong>there</strong>") {
		t.Errorf("text/html part lost the operator's bold: %q", env.HTML)
	}
	if !strings.Contains(env.HTML, `href="https://example.test/docs"`) {
		t.Errorf("text/html part lost the operator's link: %q", env.HTML)
	}
	if strings.Contains(env.Text, "<") || strings.Contains(env.Text, ">") {
		t.Errorf("text/plain part still carries markup: %q", env.Text)
	}
	for _, want := range []string{"Hi there.", "Docs"} {
		if !strings.Contains(env.Text, want) {
			t.Errorf("text/plain part = %q, want it to contain %q", env.Text, want)
		}
	}
}

// TestBuildHTMLBodyStripsHostileMarkup is the outgoing-HTML hostile corpus. The
// author is the operator, but the HTML can carry pasted third-party markup, so
// scripts, frames, styles, event handlers and dangerous URL schemes must never
// reach another mailbox.
func TestBuildHTMLBodyStripsHostileMarkup(t *testing.T) {
	t.Parallel()
	m := base()
	m.Format = compose.BodyHTML
	m.Text = `<script>alert(1)</script>` +
		`<style>body{display:none}</style>` +
		`<iframe src="https://evil.test"></iframe>` +
		`<svg onload="alert(1)"></svg>` +
		`<img src="x" onerror="alert(1)">` +
		`<a href="javascript:alert(1)">bad</a>` +
		`<a href="data:text/html,<script>x</script>">data</a>` +
		`<p onclick="alert(1)">click</p>`
	raw, _, err := compose.Build(m)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	env := readEnvelope(t, raw)
	lower := strings.ToLower(env.HTML)
	for _, bad := range []string{"<script", "<style", "<iframe", "<svg", "javascript:", "data:", "onerror", "onload", "onclick"} {
		if strings.Contains(lower, bad) {
			t.Errorf("outgoing HTML still contains %q: %q", bad, env.HTML)
		}
	}
	for _, node := range htmlNodes(t, env.HTML) {
		for _, attr := range node.Attr {
			if strings.HasPrefix(strings.ToLower(attr.Key), "on") {
				t.Errorf("outgoing HTML kept event handler %q=%q", attr.Key, attr.Val)
			}
			if (node.Data == "a" || node.Data == "img") && strings.EqualFold(attr.Key, "href") || (node.Data == "img" && strings.EqualFold(attr.Key, "src")) {
				if !allowedScheme(attr.Val) {
					t.Errorf("outgoing HTML kept disallowed URL %q on <%s>", attr.Val, node.Data)
				}
			}
		}
	}
	if !strings.Contains(env.Text, "bad") || !strings.Contains(env.Text, "click") {
		t.Errorf("visible text was dropped with the hostile markup: %q", env.Text)
	}
}

// TestBuildHTMLBodyKeepsInlineCIDImage: an inline image the operator inserted is
// a cid: reference that must survive the outgoing policy (4g).
func TestBuildHTMLBodyKeepsInlineCIDImage(t *testing.T) {
	t.Parallel()
	m := base()
	m.Format = compose.BodyHTML
	m.Text = `<p>Look <img src="cid:chart@ivy" alt="chart"> here</p>`
	raw, _, err := compose.Build(m)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	env := readEnvelope(t, raw)
	if !strings.Contains(env.HTML, `src="cid:chart@ivy"`) {
		t.Errorf("outgoing HTML dropped the inline cid: %q", env.HTML)
	}
	if !strings.Contains(env.Text, "Look") || !strings.Contains(env.Text, "here") {
		t.Errorf("text/plain part = %q, want the surrounding text", env.Text)
	}
}

// TestBuildHTMLBodyPlainTextIsReadable checks the block-aware text conversion:
// paragraphs and breaks become line breaks, list items keep a marker, and
// entities decode.
func TestBuildHTMLBodyPlainTextIsReadable(t *testing.T) {
	t.Parallel()
	m := base()
	m.Format = compose.BodyHTML
	m.Text = `<p>One &amp; two</p><p>Three<br>Four</p><ul><li>alpha</li><li>beta</li></ul>`
	raw, _, err := compose.Build(m)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	env := readEnvelope(t, raw)
	for _, want := range []string{"One & two", "Three", "Four", "alpha", "beta"} {
		if !strings.Contains(env.Text, want) {
			t.Errorf("text/plain part = %q, want it to contain %q", env.Text, want)
		}
	}
	if !strings.Contains(env.Text, "\n") {
		t.Errorf("text/plain part %q has no line breaks; paragraphs and lists should be separated", env.Text)
	}
	if strings.Contains(env.Text, "&amp;") {
		t.Errorf("text/plain part %q did not decode entities", env.Text)
	}
}

// TestBuildHTMLBodyEmpty: an empty or tag-only HTML body is allowed and produces
// no HTML part when there is nothing at all.
func TestBuildHTMLBodyEmpty(t *testing.T) {
	t.Parallel()
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		m := base()
		m.Format = compose.BodyHTML
		m.Text = ""
		raw, _, err := compose.Build(m)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		env := readEnvelope(t, raw)
		if env.HTML != "" {
			t.Errorf("empty HTML body produced an HTML part: %q", env.HTML)
		}
	})
	t.Run("tag only", func(t *testing.T) {
		t.Parallel()
		m := base()
		m.Format = compose.BodyHTML
		m.Text = "<p></p>"
		raw, _, err := compose.Build(m)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		env := readEnvelope(t, raw)
		if strings.TrimSpace(env.Text) != "" {
			t.Errorf("tag-only HTML produced text %q, want empty", env.Text)
		}
	})
}

// readEnvelope parses a built message back for assertions.
func readEnvelope(t *testing.T, raw []byte) *enmime.Envelope {
	t.Helper()
	env, err := enmime.ReadEnvelope(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("read built message: %v", err)
	}
	return env
}
