package main

import (
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"
)

// stateMain models the separate, backed-up local-state file: tags, rules, snoozes, settings and
// the per-call API cost ledger (one row per remote call, plus one per embedded message).
func stateMain(work string, messages int) {
	_ = os.MkdirAll(work, 0o700)
	p := filepath.Join(work, "state.db")
	for _, s := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(p + s)
	}
	db, err := sql.Open("sqlite", "file:"+p+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		panic(err)
	}
	db.SetMaxOpenConns(1)
	for _, s := range []string{
		`CREATE TABLE tags (id INTEGER PRIMARY KEY, slug TEXT UNIQUE, name TEXT, color TEXT, created_at INTEGER)`,
		`CREATE TABLE message_tags (account TEXT NOT NULL, content_key BLOB NOT NULL, tag_id INTEGER NOT NULL, source TEXT, at INTEGER, PRIMARY KEY (account, content_key, tag_id))`,
		`CREATE TABLE rules (id INTEGER PRIMARY KEY, name TEXT, conditions TEXT, actions TEXT, enabled INTEGER)`,
		`CREATE TABLE snoozes (account TEXT, content_key BLOB, until INTEGER, PRIMARY KEY (account, content_key))`,
		`CREATE TABLE image_allow (sender TEXT PRIMARY KEY)`,
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE api_calls (id INTEGER PRIMARY KEY, ts INTEGER NOT NULL, provider TEXT, endpoint TEXT, model TEXT, feature TEXT,
			account TEXT, content_key BLOB, input_tokens INTEGER, output_tokens INTEGER, cost_usd REAL, cost_estimated INTEGER,
			latency_ms INTEGER, outcome TEXT, call_id INTEGER)`,
		`CREATE INDEX api_calls_ts ON api_calls(ts)`,
		`CREATE INDEX api_calls_feature ON api_calls(feature, ts)`,
	} {
		if _, err := db.Exec(s); err != nil {
			panic(err)
		}
	}
	r := rand.New(rand.NewSource(7))
	key := func() []byte { b := make([]byte, 32); r.Read(b); return b }
	tx, _ := db.Begin()
	for i := 1; i <= 60; i++ {
		_, _ = tx.Exec(`INSERT INTO tags VALUES (?,?,?,?,?)`, i, fmt.Sprintf("tag-%d", i), fmt.Sprintf("Tag name %d", i), "#7cc4ff", 1700000000)
	}
	for i := 0; i < messages*3/10; i++ { // 30% of messages carry a tag
		_, _ = tx.Exec(`INSERT OR IGNORE INTO message_tags VALUES (?,?,?,?,?)`, "acc1", key(), 1+r.Intn(60), "rule", 1700000000+int64(i))
	}
	for i := 0; i < 150; i++ {
		_, _ = tx.Exec(`INSERT INTO rules VALUES (?,?,?,?,?)`, i, "rule", `{"from":"x@example.com","check":"looks like a receipt"}`, `{"add_tag":"receipts"}`, 1)
	}
	for i := 0; i < 3000; i++ {
		_, _ = tx.Exec(`INSERT INTO snoozes VALUES (?,?,?)`, "acc1", key(), 1800000000+int64(i))
	}
	for i := 0; i < 800; i++ {
		_, _ = tx.Exec(`INSERT INTO image_allow VALUES (?)`, fmt.Sprintf("sender%d@example.com", i))
	}
	// Ledger: every embedded message (one row each, cost allocated by token share), plus a Jev
	// call per message for the opted-in mail, plus occasional chat calls.
	var call int64
	ins := func(endpoint, model, feature string, in, out int, cost float64, i int) {
		_, _ = tx.Exec(`INSERT INTO api_calls (ts,provider,endpoint,model,feature,account,content_key,input_tokens,output_tokens,cost_usd,cost_estimated,latency_ms,outcome,call_id) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			1700000000+int64(i)*30, "openrouter", endpoint, model, feature, "acc1", key(), in, out, cost, 0, 200+r.Intn(400), "ok", call)
	}
	for i := 0; i < messages; i++ {
		if i%32 == 0 {
			call++
		}
		ins("embeddings", "baai/bge-m3", "search_index", 420, 0, 0.0000042, i)
		if i%2 == 0 {
			ins("systemone", "jev-latest", "triage", 1164, 300, 0.000049, i)
		}
	}
	_ = tx.Commit()
	_, _ = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	fi, _ := os.Stat(p)
	var rows int
	_ = db.QueryRow(`SELECT count(*) FROM api_calls`).Scan(&rows)
	fmt.Printf("state.db for %d messages: %d ledger rows, file %.1f MiB\n", messages, rows, float64(fi.Size())/(1<<20))
	snap := filepath.Join(work, "state-snap.db")
	_ = os.Remove(snap)
	t0 := time.Now()
	_, _ = db.Exec(`VACUUM INTO ?`, snap)
	sfi, _ := os.Stat(snap)
	fmt.Printf("VACUUM INTO: %v, snapshot %.1f MiB; 15 daily snapshots uncompressed would be %.0f MiB\n", time.Since(t0).Round(time.Millisecond), float64(sfi.Size())/(1<<20), 15*float64(sfi.Size())/(1<<20))
	_ = os.Remove(snap)
}
