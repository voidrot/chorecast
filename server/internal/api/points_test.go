package api_test

import (
	"fmt"
	"net/url"
	"testing"
)

type pointsEntryJSON struct {
	ID, KidID, Amount, BalanceAfter, ReversesID, ReversedByID, CreatedBy int64
	RequestedAmount                                                      *int64
	Kind, Note, LocalDate                                                string
}

type pointsPageJSON struct {
	Items      []pointsEntryJSON
	NextCursor string
}

func TestPointsLedger(t *testing.T) {
	s := newAuthServer(t)
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	momID := decode[struct{ UserID int64 }](t, s.do("GET", "/api/v1/me", nil, mom)).UserID
	adjust := func(kid string, body map[string]any) pointsEntryJSON {
		t.Helper()
		rec := s.do("POST", "/api/v1/kids/"+kid+"/points/adjustments", body, mom)
		wantCode(t, "adjust "+kid, rec, 201, "")
		return decode[pointsEntryJSON](t, rec)
	}

	e := adjust("sam", map[string]any{"amount": 20, "note": " good week "})
	if e.Kind != "adjustment" || e.Amount != 20 || e.BalanceAfter != 20 || e.Note != "good week" || e.CreatedBy != momID ||
		e.LocalDate != "2026-10-31" || e.RequestedAmount != nil {
		t.Errorf("adjustment = %+v", e)
	}
	// Negative balances are off by default: the deduction is clamped and records the request.
	clamped := adjust("sam", map[string]any{"amount": -50, "note": "broke the lamp"})
	if clamped.Amount != -20 || clamped.RequestedAmount == nil || *clamped.RequestedAmount != -50 || clamped.BalanceAfter != 0 {
		t.Errorf("clamped = %+v", clamped)
	}
	third := adjust("sam", map[string]any{"amount": 7, "note": "helped out"})
	adjust("sam-2", map[string]any{"amount": 3, "note": "sibling"})

	wantCode(t, "no note", s.do("POST", "/api/v1/kids/sam/points/adjustments", map[string]any{"amount": 5, "note": "   "}, mom), 422, "validation_failed")
	wantCode(t, "zero", s.do("POST", "/api/v1/kids/sam/points/adjustments", map[string]any{"amount": 0, "note": "x"}, mom), 422, "validation_failed")
	wantCode(t, "unknown kid", s.do("POST", "/api/v1/kids/nobody/points/adjustments", map[string]any{"amount": 5, "note": "x"}, mom), 404, "kid_not_found")

	// Reversal: once, with a note, only through the entry's own kid.
	reversal := fmt.Sprintf("/api/v1/kids/sam/points/entries/%d/reversal", third.ID)
	wantCode(t, "reverse without note", s.do("POST", reversal, map[string]any{"note": " "}, mom), 422, "validation_failed")
	wantCode(t, "reverse via sibling", s.do("POST", fmt.Sprintf("/api/v1/kids/sam-2/points/entries/%d/reversal", third.ID), map[string]any{"note": "x"}, mom), 404, "points_entry_not_found")
	rec := s.do("POST", reversal, map[string]any{"note": "counted twice"}, mom)
	wantCode(t, "reverse", rec, 201, "")
	rev := decode[pointsEntryJSON](t, rec)
	if rev.Kind != "reversal" || rev.Amount != -7 || rev.ReversesID != third.ID || rev.BalanceAfter != 0 || rev.CreatedBy != momID {
		t.Errorf("reversal = %+v", rev)
	}
	wantCode(t, "reverse twice", s.do("POST", reversal, map[string]any{"note": "again"}, mom), 409, "already_reversed")

	// The ledger pages newest first with running balances.
	var all []pointsEntryJSON
	q := url.Values{"limit": {"2"}}
	for {
		rec := s.do("GET", "/api/v1/kids/sam/points/entries?"+q.Encode(), nil, mom)
		wantCode(t, "list", rec, 200, "")
		page := decode[pointsPageJSON](t, rec)
		all = append(all, page.Items...)
		if page.NextCursor == "" {
			break
		}
		q.Set("cursor", page.NextCursor)
	}
	ids := []int64{rev.ID, third.ID, clamped.ID, e.ID}
	balances := []int64{0, 7, 0, 20}
	if len(all) != len(ids) {
		t.Fatalf("listed %+v", all)
	}
	for i, got := range all {
		if got.ID != ids[i] || got.BalanceAfter != balances[i] {
			t.Errorf("entry %d = %+v, want id %d balanceAfter %d", i, got, ids[i], balances[i])
		}
	}
	if all[1].ReversedByID != rev.ID {
		t.Errorf("reversed entry = %+v", all[1])
	}
	wantCode(t, "bad cursor", s.do("GET", "/api/v1/kids/sam/points/entries?cursor=junk", nil, mom), 400, "invalid_cursor")

	rec = s.do("GET", "/api/v1/kids/sam-2/points", nil, mom)
	if b := decode[struct{ Balance, Available int64 }](t, rec); rec.Code != 200 || b.Balance != 3 || b.Available != 3 {
		t.Errorf("sibling balance = %d %s", rec.Code, rec.Body)
	}
}

func TestPointsAccess(t *testing.T) {
	s := newAuthServerWith(t, configWithPublicURL())
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	wantCode(t, "seed", s.do("POST", "/api/v1/kids/sam/points/adjustments", map[string]any{"amount": 9, "note": "start"}, mom), 201, "")
	entryID := decode[pointsPageJSON](t, s.do("GET", "/api/v1/kids/sam/points/entries", nil, mom)).Items[0].ID
	_, samDevice := pairDevice(t, s, pair(t, s, mom, "sam").Code)
	_, shared := pairDevice(t, s, pair(t, s, mom, "").Code)

	// A kid reads their own points, not a sibling's, and can't change any.
	rec := s.bearer("GET", "/api/v1/kids/sam/points", samDevice, nil)
	if b := decode[struct{ Balance int64 }](t, rec); rec.Code != 200 || b.Balance != 9 {
		t.Errorf("own balance = %d %s", rec.Code, rec.Body)
	}
	wantCode(t, "own ledger", s.bearer("GET", "/api/v1/kids/sam/points/entries", samDevice, nil), 200, "")
	wantCode(t, "sibling balance", s.bearer("GET", "/api/v1/kids/sam-2/points", samDevice, nil), 403, "forbidden")
	wantCode(t, "sibling ledger", s.bearer("GET", "/api/v1/kids/sam-2/points/entries", samDevice, nil), 403, "forbidden")
	wantCode(t, "kid adjusts", s.bearer("POST", "/api/v1/kids/sam/points/adjustments", samDevice, map[string]any{"amount": 100, "note": "mine"}), 403, "insufficient_scope")
	wantCode(t, "kid reverses", s.bearer("POST", fmt.Sprintf("/api/v1/kids/sam/points/entries/%d/reversal", entryID), samDevice, map[string]any{"note": "no"}), 403, "insufficient_scope")

	// A shared device without a kid session sees no points.
	wantCode(t, "shared device balance", s.bearer("GET", "/api/v1/kids/sam/points", shared, nil), 403, "insufficient_scope")
	wantCode(t, "anonymous", s.do("GET", "/api/v1/kids/sam/points", nil), 401, "unauthenticated")

	rec = s.do("GET", "/api/v1/kids/sam/points", nil, mom)
	if b := decode[struct{ Balance int64 }](t, rec); b.Balance != 9 {
		t.Errorf("balance after refused writes = %s", rec.Body)
	}
}
