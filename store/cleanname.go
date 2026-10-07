package store

import "strings"

// CleanName is a display name as a person would read it. Some servers return
// the header's quotes with the name (a name like claude[bot] is only legal in
// quotes), so one wrapping pair is dropped and its escapes undone. Quotes that
// are part of the name, or a name that is not a single quoted string, stay.
func CleanName(name string) string {
	name = strings.TrimSpace(name)
	if len(name) < 2 || name[0] != '"' || name[len(name)-1] != '"' {
		return name
	}
	var b strings.Builder
	inner := name[1 : len(name)-1]
	for i := 0; i < len(inner); i++ {
		switch c := inner[i]; {
		case c == '\\' && i+1 < len(inner):
			i++
			b.WriteByte(inner[i])
		case c == '"':
			return name // an unescaped quote inside: not one quoted string
		default:
			b.WriteByte(c)
		}
	}
	return strings.TrimSpace(b.String())
}
