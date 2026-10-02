package render_test

import (
	"strings"
	"testing"

	"github.com/AutumnsGrove/Ivy/mime"
	"github.com/AutumnsGrove/Ivy/render"
)

// bodyBench builds a representative HTML message: headings, a table, a couple
// of inline images and some presentational styling.
func bodyBench() mime.Parsed {
	var b strings.Builder
	b.WriteString(`<html><body><h1>Weekly report</h1><p style="color:#333">Hello,`)
	for i := 0; i < 40; i++ {
		b.WriteString(`<span style="font-weight:bold">item `)
		b.WriteString(strings.Repeat("x", 64))
		b.WriteString(`</span> `)
	}
	b.WriteString(`</p><table><tr><td>a</td><td>b</td></tr></table>`)
	b.WriteString(`<img src="cid:logo@example.com" alt="logo">`)
	b.WriteString(`<img src="https://cdn.example/photo.png" width="600" height="400" alt="photo">`)
	b.WriteString(`</body></html>`)
	return mime.Parsed{HTML: b.String(), Text: strings.Repeat("plain text ", 100)}
}

func BenchmarkBody(b *testing.B) {
	p := bodyBench()
	b.ReportAllocs()
	for b.Loop() {
		_ = render.Body(p, render.Options{MessageID: "m1"})
	}
}

func BenchmarkBodyAllowedRemote(b *testing.B) {
	p := bodyBench()
	opts := render.Options{MessageID: "m1", AllowRemoteImages: true}
	b.ReportAllocs()
	for b.Loop() {
		_ = render.Body(p, opts)
	}
}
