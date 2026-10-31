// Package paging implements the API's cursor pagination. List operations embed Params in their
// input and return Page[T]. Cursors are opaque to clients: a versioned, base64url-encoded JSON
// keyset (e.g. the last item's sort key), so pages stay stable while rows are added.
package paging

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/voidrot/chorecast/server/internal/apierr"
)

const (
	DefaultLimit  = 50
	MaxLimit      = 100
	cursorVersion = "v1."
)

// Params are the query parameters of every list operation. Embed in a Huma input struct.
type Params struct {
	Cursor string `query:"cursor" doc:"Opaque cursor from a previous page's nextCursor. Omit for the first page."`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"50" doc:"Maximum number of items to return."`
}

// Size returns the effective page size.
func (p Params) Size() int {
	switch {
	case p.Limit <= 0:
		return DefaultLimit
	case p.Limit > MaxLimit:
		return MaxLimit
	}
	return p.Limit
}

// Page is the body of every list response.
type Page[T any] struct {
	Items      []T    `json:"items" nullable:"false"`
	NextCursor string `json:"nextCursor,omitempty" doc:"Pass as cursor to fetch the next page. Absent on the last page."`
}

// Encode returns the opaque cursor for a keyset value.
func Encode(key any) (string, error) {
	b, err := json.Marshal(key)
	if err != nil {
		return "", err
	}
	return cursorVersion + base64.RawURLEncoding.EncodeToString(b), nil
}

// Decode parses a cursor produced by Encode into K. An empty cursor yields the zero K and
// first=true. A malformed cursor is a 400 with code invalid_cursor.
func Decode[K any](cursor string) (key K, first bool, err error) {
	if cursor == "" {
		return key, true, nil
	}
	raw, ok := strings.CutPrefix(cursor, cursorVersion)
	if !ok {
		return key, false, invalid()
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return key, false, invalid()
	}
	if err := json.Unmarshal(b, &key); err != nil {
		return key, false, invalid()
	}
	return key, false, nil
}

func invalid() error {
	return apierr.New(http.StatusBadRequest, apierr.CodeInvalidCursor, "cursor is invalid or expired; restart from the first page")
}

// Build turns rows fetched with LIMIT size+1 into a page: if there is an extra row, it is
// dropped and NextCursor encodes key(last kept row).
func Build[T, K any](rows []T, size int, key func(T) K) (Page[T], error) {
	page := Page[T]{Items: rows}
	if page.Items == nil {
		page.Items = []T{}
	}
	if len(rows) <= size {
		return page, nil
	}
	page.Items = rows[:size]
	next, err := Encode(key(rows[size-1]))
	if err != nil {
		return Page[T]{}, err
	}
	page.NextCursor = next
	return page, nil
}
