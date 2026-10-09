package gateway

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// scanTags bounds how far into a body the page background is looked for. The
// page's own colour is declared on the outermost body, table or div, so a
// colour past the first screenful of markup is a button or a banner, not the page.
const scanTags = 40

// darkLuma is the brightness (0..1) below which a background counts as dark.
const darkLuma = 0.4

// mailIsDark reports whether rich mail already paints itself on a dark page, in
// which case the night reader leaves it as the sender made it rather than
// inverting it into a light one. The sanitizer keeps only inline styles and
// bgcolor, so the first opaque background among the opening tags is the page's.
// No declared background means the sender assumed a light page.
func mailIsDark(htmlStr string) bool {
	z := html.NewTokenizer(strings.NewReader(htmlStr))
	for seen := 0; seen < scanTags; {
		switch z.Next() {
		case html.ErrorToken:
			return false
		case html.StartTagToken, html.SelfClosingTagToken:
			seen++
			if luma, ok := declaredBackground(z.Token()); ok {
				return luma < darkLuma
			}
		}
	}
	return false
}

// declaredBackground is the brightness of the opaque background a tag declares,
// from bgcolor or an inline background-color; ok is false when it declares none
// (or one that is transparent or not a colour this can read).
func declaredBackground(tok html.Token) (luma float64, ok bool) {
	for _, a := range tok.Attr {
		switch a.Key {
		case "bgcolor":
			if l, ok := lumaOf(a.Val); ok {
				return l, true
			}
		case "style":
			for _, decl := range strings.Split(a.Val, ";") {
				prop, val, found := strings.Cut(decl, ":")
				if !found || strings.ToLower(strings.TrimSpace(prop)) != "background-color" {
					continue
				}
				if l, ok := lumaOf(val); ok {
					return l, true
				}
			}
		}
	}
	return 0, false
}

var namedLuma = map[string]float64{"black": 0, "white": 1, "silver": 0.75, "gray": 0.5, "grey": 0.5, "navy": 0.06}

// lumaOf reads a CSS colour as a brightness. It understands hex, rgb()/rgba()
// and a few names; anything else, and anything mostly transparent, is not a
// colour it can speak for.
func lumaOf(v string) (float64, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	if l, ok := namedLuma[v]; ok {
		return l, true
	}
	var r, g, b float64
	switch {
	case strings.HasPrefix(v, "#"):
		hex := v[1:]
		if len(hex) == 3 {
			hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
		}
		if len(hex) != 6 {
			return 0, false
		}
		n, err := strconv.ParseUint(hex, 16, 32)
		if err != nil {
			return 0, false
		}
		r, g, b = float64(n>>16&0xff), float64(n>>8&0xff), float64(n&0xff)
	case strings.HasPrefix(v, "rgb"):
		open, closing := strings.IndexByte(v, '('), strings.LastIndexByte(v, ')')
		if open < 0 || closing < open {
			return 0, false
		}
		parts := strings.FieldsFunc(v[open+1:closing], func(c rune) bool { return c == ',' || c == ' ' || c == '/' })
		if len(parts) < 3 {
			return 0, false
		}
		var ch [3]float64
		for i := range ch {
			f, err := strconv.ParseFloat(parts[i], 64)
			if err != nil {
				return 0, false
			}
			ch[i] = f
		}
		if len(parts) > 3 {
			if a, err := strconv.ParseFloat(parts[3], 64); err != nil || a < 0.5 {
				return 0, false
			}
		}
		r, g, b = ch[0], ch[1], ch[2]
	default:
		return 0, false
	}
	return (0.299*r + 0.587*g + 0.114*b) / 255, true
}
