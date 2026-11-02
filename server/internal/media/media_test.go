package media_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/voidrot/chorecast/server/internal/db/sqlc"
)

// UploadPhoto relies on this to reject a duplicate that raced past its early check: the second
// insert of a hash returns no row.
func TestRecordMediaHashReportsAnExistingHash(t *testing.T) {
	_, _, store := newSigner(t)
	p := sqlc.RecordMediaHashParams{Sha256: make([]byte, 32), Dhash: 7, CreatedAt: "2026-10-31T12:00:00Z"}
	if _, err := store.RecordMediaHash(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordMediaHash(t.Context(), p); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("second insert: err = %v, want sql.ErrNoRows", err)
	}
}
