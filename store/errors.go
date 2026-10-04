package store

import "errors"

// ErrNotFound reports a lookup that matched no row. Handlers map it to a 404
// with the stable code the UI knows (STANDARDS.md section 6).
var ErrNotFound = errors.New("not found")

// ErrBadCursor reports a list cursor that is not the opaque value the store
// issued. Handlers map it to a 400; a hostile or truncated cursor is a client
// error, not a server fault.
var ErrBadCursor = errors.New("bad cursor")

// ErrNotDisabled reports a purge asked for a message that is not hidden. Purge
// is the one erasure Ivy has, and only mail the server already dropped may reach
// it, so a live row is refused rather than destroyed (ARCHITECTURE.md 4).
var ErrNotDisabled = errors.New("message is not disabled")
