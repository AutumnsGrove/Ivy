package store

import "errors"

// ErrNotFound reports a lookup that matched no row. Handlers map it to a 404
// with the stable code the UI knows (STANDARDS.md section 6).
var ErrNotFound = errors.New("not found")
