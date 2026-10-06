// Package secrets keeps one mailbox password per account in its own private
// file. A password never enters a database or a backup (STANDARDS.md 2): the
// state snapshot is copied off the board, so anything in it is exposed with it.
package secrets

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// MaxPasswordBytes bounds a stored password. Provider app passwords are a few
// dozen characters, so this is generous while still refusing a pasted file.
const MaxPasswordBytes = 1024

// ErrNotFound means no password has been stored for the account.
var ErrNotFound = errors.New("no stored password")

// validID is deliberately narrower than config's account ids: the id becomes a
// file name, so it may not contain a separator, a dot prefix or upper case.
var validID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ValidID reports whether an account id can name a secret file. YAML accounts
// may use ids that cannot; they simply never have one.
func ValidID(id string) bool { return validID.MatchString(id) }

func path(dir, id string) (string, error) {
	if !ValidID(id) {
		return "", fmt.Errorf("invalid account id for a secret file")
	}
	return filepath.Join(dir, id), nil
}

// Write stores the password atomically: a temp file created private from the
// start (so there is no window where it is readable), synced, then renamed over
// the old one. A crash leaves the previous password or the new one, never half.
// Errors never include the password.
func Write(dir, id, password string) error {
	p, err := path(dir, id)
	if err != nil {
		return err
	}
	switch {
	case password == "":
		return errors.New("password is empty")
	case len(password) > MaxPasswordBytes:
		return fmt.Errorf("password is longer than %d bytes", MaxPasswordBytes)
	case strings.ContainsAny(password, "\r\n\x00"):
		return errors.New("password contains a line break or NUL")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create secrets dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create secret temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // a no-op once the rename has succeeded
	if _, err := tmp.WriteString(password); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write secret: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync secret: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close secret: %w", err)
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return fmt.Errorf("replace secret: %w", err)
	}
	return nil
}

// Read returns the stored password. It refuses a file readable by anyone but
// the owner: that is the same rule `ivy doctor` applies to .env, and a loose
// mode means something else already handled the file.
func Read(dir, id string) (string, error) {
	p, err := path(dir, id)
	if err != nil {
		return "", err
	}
	f, err := os.Open(p) //nolint:gosec // G304: p is dir joined to an id the regexp restricts to [a-z0-9_-]
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("open secret: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("stat secret: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("secret file %s is readable by others (mode %o); run chmod 600", p, info.Mode().Perm())
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxPasswordBytes+1))
	if err != nil {
		return "", fmt.Errorf("read secret: %w", err)
	}
	if len(b) > MaxPasswordBytes {
		return "", fmt.Errorf("secret file %s is larger than %d bytes", p, MaxPasswordBytes)
	}
	return string(b), nil
}
