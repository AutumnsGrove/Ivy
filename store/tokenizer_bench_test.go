package store

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// BenchmarkFTS5Tokenizers compares the two candidate tokenizers the docs leave
// open: unicode61 with diacritics folding against trigram. It is the
// measure-first step behind the choice in migration 13 (next_steps.md, 3f).
// The corpus is synthetic and small; the ratio and the query shape are the
// point, not the absolute time.
func BenchmarkFTS5Tokenizers(b *testing.B) {
	words := []string{"domain", "renewal", "invoice", "quarterly", "report", "café", "naïve", "meeting", "photos", "receipt"}
	for _, tok := range []string{"unicode61 remove_diacritics 2", "trigram"} {
		b.Run(strings.Fields(tok)[0], func(b *testing.B) {
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(fmt.Sprintf(
				`CREATE VIRTUAL TABLE fts USING fts5(body, tokenize = '%s')`, tok)); err != nil {
				b.Fatal(err)
			}
			const docs = 5000
			for i := 0; i < docs; i++ {
				var sb strings.Builder
				for j := 0; j < 60; j++ {
					sb.WriteString(words[(i+j)%len(words)])
					sb.WriteByte(' ')
				}
				if _, err := db.Exec(`INSERT INTO fts(rowid, body) VALUES (?, ?)`, i+1, sb.String()); err != nil {
					b.Fatal(err)
				}
			}
			var size int64
			if err := db.QueryRow(`SELECT sum(length(block)) FROM fts_data`).Scan(&size); err != nil {
				b.Fatal(err)
			}
			b.Logf("tokenizer=%q index_bytes=%d", tok, size)

			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				var c int
				if err := db.QueryRow(`SELECT count(*) FROM fts WHERE fts MATCH '"invoice" "report"'`).Scan(&c); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
