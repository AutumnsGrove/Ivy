package store

import (
	"context"
	"errors"
	"testing"
)

// A rollback runs the previous image on a database the newer image already
// migrated. Opening it anyway runs old code on a schema it does not know, so a
// database newer than the binary is refused with a message that says why.
func TestOpenRefusesADatabaseNewerThanTheBinary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, name := range []string{"mirror", "state"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			dbs, err := Open(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			db := dbs.Mirror
			if name == "state" {
				db = dbs.State
			}
			if _, err := db.Write.ExecContext(ctx, "PRAGMA user_version = 9999"); err != nil {
				t.Fatal(err)
			}
			if err := dbs.Close(); err != nil {
				t.Fatal(err)
			}

			_, err = Open(ctx, dir)
			if !errors.Is(err, ErrSchemaNewer) {
				t.Fatalf("Open = %v, want ErrSchemaNewer", err)
			}
		})
	}
}
