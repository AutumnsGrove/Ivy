package store

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// ContentKey is the stable identity of a message across folders, UIDs and
// mailbox rebuilds (ARCHITECTURE.md 3): the SHA-256 of the lower-cased,
// angle-bracket-stripped Message-ID, or of rawHeader when that header is
// missing. Every derived row in either database keys on it, never on a row id.
func ContentKey(messageID string, rawHeader []byte) string {
	id := normalizeMessageID(messageID)
	if id == "" {
		return hash(rawHeader)
	}
	return hash([]byte(id))
}

func normalizeMessageID(messageID string) string {
	id := strings.TrimSpace(messageID)
	if len(id) >= 2 && id[0] == '<' && id[len(id)-1] == '>' {
		id = id[1 : len(id)-1]
	}
	return strings.ToLower(strings.TrimSpace(id))
}

func hash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
