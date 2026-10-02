package mime

import "bytes"

// exceedsMultipartDepth reports whether raw opens more than MaxMultipartDepth
// multipart boundaries at once. It reads lines, never MIME: a multipart
// Content-Type header pushes its boundary, and a closing "--boundary--" line pops
// back to it. A message that never closes its boundaries (truncated, or built to
// hurt) keeps growing the stack, which is exactly the shape enmime handles in
// exponential time. It allocates only for the boundary strings and stops at the
// first line that crosses the limit.
func exceedsMultipartDepth(raw []byte) bool {
	var stack []string
	for rest := raw; len(rest) > 0; {
		line, next := splitLine(rest)
		rest = next

		if len(line) >= 2 && line[0] == '-' && line[1] == '-' {
			popClosed(&stack, line)
			continue
		}
		if !hasPrefixFold(line, "content-type:") {
			continue
		}
		// A header may be folded over several lines; gather its continuations.
		value := append([]byte(nil), line...)
		for len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t') {
			cont, after := splitLine(rest)
			value = append(append(value, ' '), cont...)
			rest = after
		}
		if boundary, ok := multipartBoundary(value); ok {
			stack = append(stack, boundary)
			if len(stack) > MaxMultipartDepth {
				return true
			}
		}
	}
	return false
}

// splitLine returns the first line without its terminator and the remainder.
func splitLine(b []byte) (line, rest []byte) {
	i := bytes.IndexByte(b, '\n')
	if i < 0 {
		return bytes.TrimRight(b, "\r"), nil
	}
	return bytes.TrimRight(b[:i], "\r"), b[i+1:]
}

// popClosed drops the boundaries closed by a "--name--" delimiter line.
func popClosed(stack *[]string, line []byte) {
	if len(line) < 4 || line[len(line)-1] != '-' || line[len(line)-2] != '-' {
		return
	}
	name := string(line[2 : len(line)-2])
	for i := len(*stack) - 1; i >= 0; i-- {
		if (*stack)[i] == name {
			*stack = (*stack)[:i]
			return
		}
	}
}

// multipartBoundary extracts the boundary parameter from a multipart
// Content-Type header value.
func multipartBoundary(value []byte) (string, bool) {
	lower := bytes.ToLower(value)
	if !bytes.Contains(lower, []byte("multipart/")) {
		return "", false
	}
	i := bytes.Index(lower, []byte("boundary="))
	if i < 0 {
		return "", false
	}
	v := value[i+len("boundary="):]
	if len(v) > 0 && v[0] == '"' {
		v = v[1:]
		if end := bytes.IndexByte(v, '"'); end >= 0 {
			return string(v[:end]), true
		}
		return string(v), true
	}
	if end := bytes.IndexAny(v, "; \t"); end >= 0 {
		v = v[:end]
	}
	return string(v), len(v) > 0
}

func hasPrefixFold(line []byte, prefix string) bool {
	return len(line) >= len(prefix) && bytes.EqualFold(line[:len(prefix)], []byte(prefix))
}

// headerOnly returns the header block of raw with its Content-Type declarations
// removed, so the headers parse as an empty text message instead of as a
// multipart with no body.
func headerOnly(raw []byte) []byte {
	var out []byte
	for rest := raw; len(rest) > 0; {
		line, next := splitLine(rest)
		rest = next
		if len(line) == 0 {
			break
		}
		if hasPrefixFold(line, "content-type:") {
			for len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t') {
				_, rest = splitLine(rest)
			}
			continue
		}
		out = append(append(out, line...), '\r', '\n')
	}
	return append(out, '\r', '\n')
}
