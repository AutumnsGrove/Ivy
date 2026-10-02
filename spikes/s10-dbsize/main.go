// Spike S10: how big will the DB and its backups get, and how long do they take?
// usage: s10 <docs-dir> <messages> <work-dir>
// Builds a synthetic mailbox in a real SQLite file with the planned shape: mirror rows, the raw
// RFC 822 blob zstd-compressed, body text with an FTS5 index, and an int8 768-dim embedding.
package main

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite"
)

func main() {
	docs, work := os.Args[1], os.Args[3]
	var n int
	fmt.Sscan(os.Args[2], &n)
	_ = os.MkdirAll(work, 0o700)
	dbPath := filepath.Join(work, "ivy.db")
	for _, s := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(dbPath + s)
	}

	attRate := 0.12
	if v := os.Getenv("S10_ATT"); v != "" {
		fmt.Sscan(v, &attRate)
	}
	words := loadWords(docs)
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		panic(err)
	}
	db.SetMaxOpenConns(1)
	for _, s := range []string{
		`CREATE TABLE messages (id INTEGER PRIMARY KEY, account TEXT NOT NULL, folder TEXT NOT NULL, uid INTEGER NOT NULL,
			thread_id INTEGER, internal_date INTEGER NOT NULL, from_addr TEXT, subject TEXT, snippet TEXT, flags INTEGER, size INTEGER, has_attachments INTEGER)`,
		`CREATE UNIQUE INDEX messages_uid ON messages(account, folder, uid)`,
		`CREATE INDEX messages_date ON messages(account, folder, internal_date DESC)`,
		`CREATE INDEX messages_thread ON messages(thread_id)`,
		`CREATE TABLE raw_blobs (message_id INTEGER PRIMARY KEY, blob BLOB NOT NULL)`,
		`CREATE TABLE body_text (message_id INTEGER PRIMARY KEY, text TEXT NOT NULL)`,
		`CREATE VIRTUAL TABLE fts USING fts5(subject, from_addr, text, content='', tokenize='porter unicode61')`,
		`CREATE TABLE embeddings (message_id INTEGER PRIMARY KEY, model TEXT, vec BLOB NOT NULL)`,
		`CREATE TABLE tags (message_id INTEGER NOT NULL, tag TEXT NOT NULL, PRIMARY KEY (message_id, tag))`,
	} {
		if _, err := db.Exec(s); err != nil {
			panic(fmt.Sprintf("%s: %v", s, err))
		}
	}
	enc, _ := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault), zstd.WithEncoderConcurrency(1))
	r := rand.New(rand.NewSource(42))

	var rawTotal, blobTotal, attCount int64
	start := time.Now()
	tx, _ := db.Begin()
	for i := 1; i <= n; i++ {
		textLen := int(math.Exp(r.NormFloat64()*0.9 + math.Log(3000))) // median 3 KB body
		textLen = max(200, min(textLen, 120000))
		body := sentence(r, words, textLen)
		subject := sentence(r, words, 40)
		from := fmt.Sprintf("%s <%s@example.com>", sentence(r, words, 14), fmt.Sprint("user", r.Intn(4000)))
		var raw bytes.Buffer
		fmt.Fprintf(&raw, "From: %s\r\nTo: me@example.com\r\nSubject: %s\r\nDate: Mon, 01 Jan 2024 10:00:00 +0000\r\nMessage-ID: <%d@example.com>\r\nMIME-Version: 1.0\r\n", from, subject, i)
		hasAtt := 0
		if r.Float64() < attRate { // by default 12% carry an attachment: random bytes, like a compressed PDF or JPEG
			hasAtt = 1
			attCount++
			sz := int(math.Exp(r.NormFloat64()*1.1 + math.Log(150000)))
			sz = max(5000, min(sz, 8<<20))
			att := make([]byte, sz)
			r.Read(att)
			raw.WriteString("Content-Type: multipart/mixed; boundary=B\r\n\r\n--B\r\nContent-Type: text/plain\r\n\r\n" + body + "\r\n--B\r\nContent-Type: application/pdf\r\nContent-Transfer-Encoding: base64\r\n\r\n")
			enc64 := base64.StdEncoding.EncodeToString(att)
			for len(enc64) > 76 {
				raw.WriteString(enc64[:76] + "\r\n")
				enc64 = enc64[76:]
			}
			raw.WriteString(enc64 + "\r\n--B--\r\n")
		} else if r.Float64() < 0.6 { // HTML alternative adds markup
			raw.WriteString("Content-Type: multipart/alternative; boundary=A\r\n\r\n--A\r\nContent-Type: text/plain\r\n\r\n" + body + "\r\n--A\r\nContent-Type: text/html\r\n\r\n<html><body><div style=\"font-family:sans-serif\"><p>" + strings.ReplaceAll(body, ". ", ".</p><p>") + "</p></div></body></html>\r\n--A--\r\n")
		} else {
			raw.WriteString("Content-Type: text/plain\r\n\r\n" + body + "\r\n")
		}
		rawTotal += int64(raw.Len())
		z := enc.EncodeAll(raw.Bytes(), nil)
		blobTotal += int64(len(z))

		vec := make([]byte, 768)
		r.Read(vec)
		snippet := body
		if len(snippet) > 160 {
			snippet = snippet[:160]
		}
		date := 1700000000 + int64(i)*600
		_, _ = tx.Exec(`INSERT INTO messages VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, i, "acc1", "INBOX", i, i/3, date, from, subject, snippet, 1, raw.Len(), hasAtt)
		_, _ = tx.Exec(`INSERT INTO raw_blobs VALUES (?,?)`, i, z)
		_, _ = tx.Exec(`INSERT INTO body_text VALUES (?,?)`, i, body)
		_, _ = tx.Exec(`INSERT INTO fts(rowid, subject, from_addr, text) VALUES (?,?,?,?)`, i, subject, from, body)
		_, _ = tx.Exec(`INSERT INTO embeddings VALUES (?,?,?)`, i, "nomic-embed-text", vec)
		if r.Float64() < 0.3 {
			_, _ = tx.Exec(`INSERT INTO tags VALUES (?,?)`, i, "receipts")
		}
		if i%500 == 0 {
			_ = tx.Commit()
			tx, _ = db.Begin()
		}
	}
	_ = tx.Commit()
	build := time.Since(start)
	_, _ = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)

	fi, _ := os.Stat(dbPath)
	fmt.Printf("%d messages (%d with attachments), built in %v (%.0f msgs/s)\n", n, attCount, build.Round(time.Second), float64(n)/build.Seconds())
	fmt.Printf("raw RFC 822 total %.1f MiB; zstd'd blobs %.1f MiB (%.0f%% of raw)\n", float64(rawTotal)/(1<<20), float64(blobTotal)/(1<<20), 100*float64(blobTotal)/float64(rawTotal))
	fmt.Printf("DB file: %.1f MiB => %.1f KiB per message\n", float64(fi.Size())/(1<<20), float64(fi.Size())/1024/float64(n))

	rows, err := db.Query(`SELECT name, SUM(pgsize) FROM dbstat WHERE aggregate=TRUE GROUP BY name ORDER BY 2 DESC`)
	if err != nil {
		fmt.Println("dbstat unavailable:", err)
	} else {
		fmt.Println("by object:")
		for rows.Next() {
			var name string
			var sz int64
			_ = rows.Scan(&name, &sz)
			if sz > fi.Size()/200 {
				fmt.Printf("  %-26s %8.1f MiB %5.1f%%\n", name, float64(sz)/(1<<20), 100*float64(sz)/float64(fi.Size()))
			}
		}
		rows.Close()
	}

	// Backup: a consistent snapshot with VACUUM INTO, then zstd of the snapshot.
	snap := filepath.Join(work, "snap.db")
	_ = os.Remove(snap)
	t0 := time.Now()
	if _, err := db.Exec(`VACUUM INTO ?`, snap); err != nil {
		panic(err)
	}
	vac := time.Since(t0)
	sfi, _ := os.Stat(snap)
	// Stream the snapshot through zstd into a counter: reading it whole would need as much RAM
	// again as the DB, which the board does not have.
	sf, _ := os.Open(snap)
	cw := &countWriter{}
	zw, _ := zstd.NewWriter(cw, zstd.WithEncoderLevel(zstd.SpeedDefault), zstd.WithEncoderConcurrency(1))
	t1 := time.Now()
	_, _ = io.Copy(zw, sf)
	_ = zw.Close()
	sf.Close()
	fmt.Printf("VACUUM INTO: %v (%.1f MiB snapshot); zstd of snapshot: %.1f MiB (%.0f%%) in %v\n", vac.Round(time.Millisecond), float64(sfi.Size())/(1<<20), float64(cw.n)/(1<<20), 100*float64(cw.n)/float64(sfi.Size()), time.Since(t1).Round(time.Millisecond))

	// Query sanity at this size: FTS and a list page.
	t2 := time.Now()
	var c int
	_ = db.QueryRow(`SELECT count(*) FROM fts WHERE fts MATCH 'database'`).Scan(&c)
	fmt.Printf("FTS match 'database': %d hits in %v\n", c, time.Since(t2).Round(time.Microsecond))
	t3 := time.Now()
	rs, _ := db.Query(`SELECT id, subject, snippet FROM messages WHERE account='acc1' AND folder='INBOX' ORDER BY internal_date DESC LIMIT 50`)
	for rs.Next() {
	}
	rs.Close()
	fmt.Printf("list page (50 newest): %v\n", time.Since(t3).Round(time.Microsecond))
	_ = os.Remove(snap)
}

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

func loadWords(dir string) []string {
	var w []string
	files, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	for _, f := range files {
		b, _ := os.ReadFile(f)
		w = append(w, strings.Fields(string(b))...)
	}
	return w
}

// sentence returns about n characters of prose from a random starting point.
func sentence(r *rand.Rand, w []string, n int) string {
	var sb strings.Builder
	i := r.Intn(len(w))
	for sb.Len() < n {
		sb.WriteString(w[i%len(w)])
		sb.WriteByte(' ')
		i++
	}
	s := sb.String()
	return s[:min(n, len(s))]
}
