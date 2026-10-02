package devstack_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/AutumnsGrove/Ivy/internal/devstack"
)

func TestWriteQR(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := devstack.WriteQR(&buf, "http://100.64.0.1:8787"); err != nil {
		t.Fatalf("WriteQR: %v", err)
	}
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 10 {
		t.Fatalf("QR has %d rows, want at least 10", len(lines))
	}
	if !strings.ContainsAny(out, "█▀▄") {
		t.Fatal("QR output has no block characters")
	}
	width := len([]rune(lines[0]))
	for i, line := range lines {
		if got := len([]rune(line)); got != width {
			t.Fatalf("row %d width = %d, want %d", i, got, width)
		}
	}
}
