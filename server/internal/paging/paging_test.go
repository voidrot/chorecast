package paging

import (
	"errors"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
)

type row struct{ ID int64 }

type key struct {
	ID int64 `json:"id"`
}

func rows(ids ...int64) []row {
	out := make([]row, len(ids))
	for i, id := range ids {
		out[i] = row{id}
	}
	return out
}

func keyOf(r row) key { return key(r) }

func TestBuildAndRoundTrip(t *testing.T) {
	// Fetched size+1 rows: one extra means there is a next page.
	page, err := Build(rows(1, 2, 3), 2, keyOf)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("page = %+v", page)
	}
	k, first, err := Decode[key](page.NextCursor)
	if err != nil || first || k.ID != 2 {
		t.Fatalf("Decode = %+v %v %v", k, first, err)
	}

	last, _ := Build(rows(3), 2, keyOf)
	if last.NextCursor != "" || len(last.Items) != 1 {
		t.Errorf("last page = %+v", last)
	}
	empty, _ := Build[row, key](nil, 2, keyOf)
	if empty.Items == nil {
		t.Error("empty page must have [] items, not null")
	}
}

func TestDecodeFirstAndInvalid(t *testing.T) {
	if _, first, err := Decode[key](""); !first || err != nil {
		t.Errorf("empty cursor: first=%v err=%v", first, err)
	}
	for _, bad := range []string{"garbage", "v1.!!!", "v1.bm90IGpzb24", "v2.eyJpZCI6MX0"} {
		_, _, err := Decode[key](bad)
		var se huma.StatusError
		if !errors.As(err, &se) || se.GetStatus() != 400 || se.(*apierr.Problem).Code != apierr.CodeInvalidCursor {
			t.Errorf("Decode(%q) err = %v, want 400 invalid_cursor", bad, err)
		}
	}
}

func TestSize(t *testing.T) {
	for in, want := range map[int]int{0: DefaultLimit, -1: DefaultLimit, 10: 10, 1000: MaxLimit} {
		if got := (Params{Limit: in}).Size(); got != want {
			t.Errorf("Size(%d) = %d, want %d", in, got, want)
		}
	}
}
