// Package kids holds kid-specific logic. Kids have a stable slug ("sam", "mary-jane") that the
// API accepts anywhere a kid id is accepted, so scripts and URLs read naturally. A slug is
// derived from the display name at creation and does not change when the name changes.
package kids

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
)

const maxSlugLen = 32

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Slugify derives a slug candidate from a display name: accents are stripped, letters and
// digits kept, anything else collapses to single hyphens. Names with no usable characters
// (e.g. only CJK) fall back to "kid".
func Slugify(name string) string {
	ascii, _, _ := transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn))), name)
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(ascii) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			hyphen = false
		case b.Len() > 0 && !hyphen:
			b.WriteByte('-')
			hyphen = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > maxSlugLen {
		s = strings.TrimRight(s[:maxSlugLen], "-")
	}
	if s == "" || isDigits(s) {
		return "kid"
	}
	return s
}

// ValidateSlug reports whether s is a valid slug: lowercase letters, digits and single hyphens,
// at most 32 characters, and not all digits (all-digit refs are ids).
func ValidateSlug(s string) error {
	if len(s) == 0 || len(s) > maxSlugLen || !slugPattern.MatchString(s) || isDigits(s) {
		return fmt.Errorf("invalid slug %q: use 1-%d lowercase letters, digits and hyphens, not only digits", s, maxSlugLen)
	}
	return nil
}

// UniqueSlug returns base, or base-2, base-3, ... if taken.
func UniqueSlug(ctx context.Context, q *sqlc.Queries, base string) (string, error) {
	for n := 1; ; n++ {
		cand := base
		if n > 1 {
			suffix := "-" + strconv.Itoa(n)
			cand = strings.TrimRight(base[:min(len(base), maxSlugLen-len(suffix))], "-") + suffix
		}
		taken, err := q.SlugTaken(ctx, sql.NullString{String: cand, Valid: true})
		if err != nil {
			return "", err
		}
		if !taken {
			return cand, nil
		}
	}
}

// Resolve finds a kid by a path reference: an all-digit ref is an id, anything else a slug.
// Unknown kids (including ids of parents) are a 404 with code kid_not_found.
func Resolve(ctx context.Context, q *sqlc.Queries, ref string) (sqlc.User, error) {
	var (
		kid sqlc.User
		err error
	)
	if isDigits(ref) {
		id, perr := strconv.ParseInt(ref, 10, 64)
		if perr != nil {
			return sqlc.User{}, notFound(ref)
		}
		kid, err = q.GetKidByID(ctx, id)
	} else {
		kid, err = q.GetKidBySlug(ctx, sql.NullString{String: ref, Valid: true})
	}
	if errors.Is(err, sql.ErrNoRows) {
		return sqlc.User{}, notFound(ref)
	}
	return kid, err
}

func notFound(ref string) error {
	return apierr.New(http.StatusNotFound, apierr.CodeKidNotFound, fmt.Sprintf("no kid matches %q", ref))
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
