// Package blobstore is the content-addressed, de-duplicated, append-only store
// for the raw bytes of disabled messages.
//
// A message the server has deleted is the one piece of mail Ivy cannot rebuild
// from IMAP, so its bytes are copied here at the moment it is disabled, outside
// both databases (ARCHITECTURE.md section 9). The store is written to, never
// pruned: identical bytes share one file, and the backup copies new files to
// every target and keeps them regardless of the 15-day snapshot rule.
package blobstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	dirPerm  = 0o700
	filePerm = 0o600
	tempPre  = ".tmp-"
)

// Store is one blob directory on disk.
type Store struct {
	dir string
}

// Open returns the store rooted at dir, creating it if needed.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("blobstore: empty directory")
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, fmt.Errorf("blobstore: create %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// Dir is the store's root, so a backup or restore can copy the whole tree.
func (s *Store) Dir() string { return s.dir }

// pathFor validates a hex SHA-256 and returns the sharded absolute path. The
// shard (first two hex characters) keeps a large store from being one directory
// of a hundred thousand names.
func (s *Store) pathFor(hash string) (string, error) {
	if len(hash) != sha256.Size*2 || strings.IndexFunc(hash, notHex) >= 0 {
		return "", fmt.Errorf("blobstore: invalid content hash %q", hash)
	}
	return filepath.Join(s.dir, hash[:2], hash), nil
}

// Path is the absolute path a stored hash would have. It panics on a malformed
// hash: callers only pass hashes this store returned.
func (s *Store) Path(hash string) string {
	p, err := s.pathFor(hash)
	if err != nil {
		panic(err)
	}
	return p
}

// Has reports whether the store already holds the given hash.
func (s *Store) Has(hash string) bool {
	p, err := s.pathFor(hash)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// Put streams r into the store and returns the SHA-256 of its bytes, so the
// same message stored twice costs one file. Bytes already present are discarded
// without rewriting the existing file.
func (s *Store) Put(ctx context.Context, r io.Reader) (string, int64, error) {
	tmp, err := os.CreateTemp(s.dir, tempPre+"*") // 0600 and a unique name
	if err != nil {
		return "", 0, fmt.Errorf("blobstore: create temp: %w", err)
	}
	hasher := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(tmp, hasher), &contextReader{ctx: ctx, r: r})
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	switch {
	case copyErr != nil:
		_ = os.Remove(tmp.Name())
		return "", n, fmt.Errorf("blobstore: write: %w", copyErr)
	case syncErr != nil:
		_ = os.Remove(tmp.Name())
		return "", n, fmt.Errorf("blobstore: sync: %w", syncErr)
	case closeErr != nil:
		_ = os.Remove(tmp.Name())
		return "", n, fmt.Errorf("blobstore: close: %w", closeErr)
	}

	hash := hex.EncodeToString(hasher.Sum(nil))
	dst := s.Path(hash)
	if _, err := os.Stat(dst); err == nil {
		// Already stored: keep the original file and drop the duplicate.
		_ = os.Remove(tmp.Name())
		return hash, n, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		_ = os.Remove(tmp.Name())
		return "", n, fmt.Errorf("blobstore: create shard: %w", err)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		_ = os.Remove(tmp.Name())
		return "", n, fmt.Errorf("blobstore: place %s: %w", hash, err)
	}
	// fsync the shard so a power loss cannot leave the directory entry missing
	// after we have told the row its blob is safe.
	if d, err := os.Open(filepath.Dir(dst)); err == nil { //nolint:gosec // G304: our own shard dir
		_ = d.Sync()
		_ = d.Close()
	}
	return hash, n, nil
}

func notHex(r rune) bool {
	return (r < '0' || r > '9') && (r < 'a' || r > 'f')
}

// contextReader stops a long copy when its context is cancelled.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *contextReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
