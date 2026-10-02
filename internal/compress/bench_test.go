package compress

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// listPage builds a realistic inbox page: the shape of JSON the API will send,
// sized like a 25-item page. It is the S7 corpus idea in code.
func listPage() []byte {
	type row struct {
		ID      string `json:"id"`
		From    string `json:"from"`
		Name    string `json:"name"`
		Subject string `json:"subject"`
		Snippet string `json:"snippet"`
		Date    string `json:"date"`
		Unread  bool   `json:"unread"`
		Tagged  bool   `json:"tagged"`
	}
	subjects := []string{
		"Your order has shipped",
		"Re: lunch on Thursday?",
		"Security alert for your account",
		"Monthly invoice from the garden shop",
		"Quick question about the proposal",
		"Your subscription renews soon",
		"Re: Re: weekend plans",
		"Password reset requested",
		"Newsletter: what grew this week",
		"Invitation: autumn meetup",
	}
	rows := make([]row, 25)
	for i := range rows {
		rows[i] = row{
			ID:      fmt.Sprintf("msg-%04d", i),
			From:    fmt.Sprintf("sender%02d@example.test", i),
			Name:    fmt.Sprintf("Sender %d", i),
			Subject: subjects[i%len(subjects)],
			Snippet: "Hi, just following up on the note from earlier this week. Let me know if the timing still works for you and I will send the details over.",
			Date:    "2026-10-02T09:14:00Z",
			Unread:  i%3 == 0,
			Tagged:  i%5 == 0,
		}
	}
	body, err := json.Marshal(map[string]any{"messages": rows, "next_cursor": ""})
	if err != nil {
		panic(err)
	}
	return body
}

func TestCompressionBudget(t *testing.T) {
	t.Parallel()
	page := listPage()
	if len(page) < DefaultMinSize {
		t.Fatalf("the corpus is %d bytes, below the %d threshold; the budget proves nothing", len(page), DefaultMinSize)
	}
	// S7 measured 30-40% for a page like this. The budget is deliberately
	// loose: it exists to catch compression being dropped, not to micro-tune.
	const maxRatio = 0.6
	for _, enc := range []Encoding{Zstd, Brotli, Gzip} {
		t.Run(string(enc), func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(page)
			})).ServeHTTP(rec, request(enc))
			resp := rec.Result()
			if got := resp.Header.Get("Content-Encoding"); got != string(enc) {
				t.Fatalf("Content-Encoding = %q, want %q", got, enc)
			}
			ratio := float64(rec.Body.Len()) / float64(len(page))
			t.Logf("%s: %d -> %d bytes (%.1f%%)", enc, len(page), rec.Body.Len(), ratio*100)
			if ratio > maxRatio {
				t.Errorf("%s compressed to %.1f%% of the original, over the %.0f%% budget", enc, ratio*100, maxRatio*100)
			}
		})
	}
}

func request(enc Encoding) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Encoding", string(enc))
	return r
}

func benchMiddleware(b *testing.B, enc Encoding) {
	page := listPage()
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(page)
	}))
	r := request(enc)
	b.ReportAllocs()
	b.SetBytes(int64(len(page)))
	b.ResetTimer()
	for range b.N {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
	}
}

func BenchmarkMiddleware(b *testing.B) {
	cases := []struct {
		name string
		enc  Encoding
	}{
		{"zstd", Zstd},
		{"brotli", Brotli},
		{"gzip", Gzip},
		{"identity", Identity},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			benchMiddleware(b, tc.enc)
		})
	}
}

// BenchmarkZstdEncoderAlloc sizes one pooled encoder, which is the memory N5 is
// about: the library allocates one window buffer per concurrency level (the
// default is GOMAXPROCS) and keeps them for the life of the encoder.
func BenchmarkZstdEncoderAlloc(b *testing.B) {
	newEncoder := func(concurrency int) func() {
		opts := []zstd.EOption{zstd.WithEncoderLevel(zstd.SpeedDefault)}
		if concurrency > 0 {
			opts = append(opts, zstd.WithEncoderConcurrency(concurrency))
		}
		return func() {
			zw, err := zstd.NewWriter(io.Discard, opts...)
			if err != nil {
				b.Fatal(err)
			}
			zw.Close()
		}
	}
	b.Run("default-concurrency", func(b *testing.B) {
		create := newEncoder(0)
		b.ReportAllocs()
		for range b.N {
			create()
		}
	})
	b.Run("concurrency-1", func(b *testing.B) {
		create := newEncoder(1)
		b.ReportAllocs()
		for range b.N {
			create()
		}
	})
}
