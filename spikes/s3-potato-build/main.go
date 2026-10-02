// Spike S3: does pure-Go SQLite (with FTS5) build and run on the target board?
package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	stmts := []string{
		`CREATE VIRTUAL TABLE m USING fts5(subject, body, tokenize='porter unicode61')`,
		`INSERT INTO m VALUES ('Your receipt', 'Thank you for your payment')`,
		`INSERT INTO m VALUES ('Garden news', 'Ten shade plants that forgive neglect')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			log.Fatalf("%s: %v", s, err)
		}
	}
	var subj string
	if err := db.QueryRow(`SELECT subject FROM m WHERE m MATCH 'plants'`).Scan(&subj); err != nil {
		log.Fatal(err)
	}
	var ver string
	_ = db.QueryRow(`SELECT sqlite_version()`).Scan(&ver)
	fmt.Println("sqlite", ver, "fts5 match:", subj)
}
