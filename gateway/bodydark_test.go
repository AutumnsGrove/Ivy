package gateway

import "testing"

// mailIsDark decides whether rich mail already paints itself dark, so the night
// reader leaves it alone instead of inverting it into a light page. The
// sanitizer keeps only inline styles and bgcolor, so those are all there is to
// read; no declared background means the sender assumed a light page.
func TestMailIsDark(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		html string
		want bool
	}{
		{"no colours at all", `<p>Hello</p>`, false},
		{"white table", `<table bgcolor="#ffffff"><tr><td>x</td></tr></table>`, false},
		{"dark bgcolor attribute", `<table bgcolor="#111111"><tr><td>x</td></tr></table>`, true},
		{"short hex", `<table bgcolor="#000"><tr><td>x</td></tr></table>`, true},
		{"dark inline background", `<div style="background-color:#1a1a2e;color:#fff">x</div>`, true},
		{"rgb function", `<div style="background-color: rgb(20, 20, 30)">x</div>`, true},
		{"rgba function", `<div style="background-color:rgba(255,255,255,0.9)">x</div>`, false},
		{"named black", `<body bgcolor="black"><p>x</p></body>`, true},
		{"named white", `<body bgcolor="white"><p>x</p></body>`, false},
		{"light grey outer, dark inner", `<table bgcolor="#f4f4f4"><tr><td style="background-color:#000">x</td></tr></table>`, false},
		{"transparent is skipped, next one decides", `<div style="background-color:transparent"><div style="background-color:#111">x</div></div>`, true},
		{"a dark button deep in a light mail", `<p>a</p><p>b</p><table bgcolor="#fff"><tr><td><a style="background-color:#000">go</a></td></tr></table>`, false},
		{"garbage colour", `<div style="background-color:var(--x)">x</div>`, false},
		{"empty", ``, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := mailIsDark(tc.html); got != tc.want {
				t.Errorf("mailIsDark(%q) = %v, want %v", tc.html, got, tc.want)
			}
		})
	}
}

// A huge body must not make the heuristic walk all of it: the page's own
// background is declared near the top or not at all.
func TestMailIsDarkLooksOnlyNearTheTop(t *testing.T) {
	t.Parallel()
	html := ""
	for range 200 {
		html += "<p>filler</p>"
	}
	html += `<div style="background-color:#000">late</div>`
	if mailIsDark(html) {
		t.Error("a dark background far past the top must not decide the page")
	}
}
