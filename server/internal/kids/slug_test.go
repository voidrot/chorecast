package kids

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
)

func TestSlugify(t *testing.T) {
	tests := map[string]string{
		"Sam":                                "sam",
		"Mary Jane":                          "mary-jane",
		"Zoë":                                "zoe",
		"  O'Neil!! ":                        "o-neil",
		"José María":                         "jose-maria",
		"R2-D2":                              "r2-d2",
		"李":                                  "kid",
		"123":                                "kid", // all digits would look like an id
		"":                                   "kid",
		strings.Repeat("a", 40):              strings.Repeat("a", 32),
		"abcdefghij abcdefghij abcdefghij x": "abcdefghij-abcdefghij-abcdefghij",
	}
	for in, want := range tests {
		got := Slugify(in)
		if got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
		if err := ValidateSlug(got); err != nil {
			t.Errorf("Slugify(%q) produced an invalid slug: %v", in, err)
		}
	}
}

func TestValidateSlug(t *testing.T) {
	for _, ok := range []string{"sam", "mary-jane", "kid2", "r2-d2"} {
		if err := ValidateSlug(ok); err != nil {
			t.Errorf("ValidateSlug(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Sam", "-sam", "sam-", "sa--m", "sam jane", "42", strings.Repeat("a", 33), "zoë"} {
		if err := ValidateSlug(bad); err == nil {
			t.Errorf("ValidateSlug(%q) accepted", bad)
		}
	}
}

func newDB(t *testing.T) *sqlc.Queries {
	t.Helper()
	d, err := db.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d.Queries
}

func create(t *testing.T, q *sqlc.Queries, name, role, slug string) sqlc.User {
	t.Helper()
	u, err := q.CreateUser(t.Context(), sqlc.CreateUserParams{
		DisplayName: name, Role: role, Slug: sql.NullString{String: slug, Valid: slug != ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestUniqueSlug(t *testing.T) {
	q := newDB(t)
	ctx := t.Context()
	for i, want := range []string{"sam", "sam-2", "sam-3"} {
		got, err := UniqueSlug(ctx, q, "sam")
		if err != nil || got != want {
			t.Fatalf("UniqueSlug #%d = %q, %v; want %q", i, got, err, want)
		}
		create(t, q, "Sam", "child", got)
	}
	long := strings.Repeat("a", 32)
	create(t, q, "A", "child", long)
	got, _ := UniqueSlug(ctx, q, long)
	if len(got) > 32 || !strings.HasSuffix(got, "-2") || ValidateSlug(got) != nil {
		t.Errorf("long slug suffix = %q", got)
	}
}

func TestResolve(t *testing.T) {
	q := newDB(t)
	ctx := t.Context()
	sam := create(t, q, "Sam", "child", "sam")
	parent := create(t, q, "Mom", "parent", "mom")

	for _, ref := range []string{"sam", strconv.FormatInt(sam.ID, 10)} {
		got, err := Resolve(ctx, q, ref)
		if err != nil || got.ID != sam.ID {
			t.Errorf("Resolve(%q) = %d, %v", ref, got.ID, err)
		}
	}
	for _, ref := range []string{"alex", strconv.FormatInt(parent.ID, 10), "mom", "99999999999999999999"} {
		_, err := Resolve(ctx, q, ref)
		var p *apierr.Problem
		if !errors.As(err, &p) || p.Status != 404 || p.Code != apierr.CodeKidNotFound {
			t.Errorf("Resolve(%q) err = %v, want 404 kid_not_found", ref, err)
		}
	}
}
