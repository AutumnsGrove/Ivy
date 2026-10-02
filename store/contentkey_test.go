package store

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestContentKeyNormalisesMessageID(t *testing.T) {
	t.Parallel()
	want := sha256hex("abc@example.com")
	cases := []string{
		`<ABC@Example.com>`,
		`abc@example.com`,
		`  <abc@example.com>  `,
		`<abc@example.com>`,
	}
	for _, id := range cases {
		if got := ContentKey(id, nil); got != want {
			t.Errorf("ContentKey(%q) = %s, want %s", id, got, want)
		}
	}
}

func TestContentKeyFallsBackToHeaderBlock(t *testing.T) {
	t.Parallel()
	header := []byte("Subject: hi\r\nFrom: a@b.test\r\n\r\n")

	first := ContentKey("", header)
	if first != ContentKey("", []byte("Subject: hi\r\nFrom: a@b.test\r\n\r\n")) {
		t.Error("same header block produced different keys")
	}
	if first == ContentKey("", []byte("Subject: other\r\n\r\n")) {
		t.Error("different header blocks collided")
	}
	if len(first) != 64 {
		t.Errorf("key length = %d, want 64", len(first))
	}
}

func TestContentKeyEmptyEverythingIsStable(t *testing.T) {
	t.Parallel()
	if got, want := ContentKey("", nil), sha256hex(""); got != want {
		t.Errorf("ContentKey(\"\", nil) = %s, want %s", got, want)
	}
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
