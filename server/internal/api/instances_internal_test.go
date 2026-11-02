package api

import (
	"database/sql"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/events"
	"github.com/voidrot/chorecast/server/internal/instances"
	"github.com/voidrot/chorecast/server/internal/jobs"
)

// TestCompleteChecksKidAccessWithoutKidID: a principal that may complete chores but not act for
// every kid (such as a future API key grant) is refused an instance whose assignee it can't
// access, also when it doesn't name the kid. No principal kind like that exists yet, so the
// handler is called directly with one.
func TestCompleteChecksKidAccessWithoutKidID(t *testing.T) {
	d, err := db.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	clk := clock.NewFake(time.Date(2026, 10, 31, 18, 0, 0, 0, time.UTC))
	quiet := slog.New(slog.DiscardHandler)
	bus := events.NewBus(d, jobs.NewRunner(d.Queries, clk, quiet), clk, quiet)
	h := &handlers{instances: instances.New(d, bus, clk)}
	ctx := t.Context()
	now := db.Timestamp(clk.Now())
	if err := d.CreateHousehold(ctx, sqlc.CreateHouseholdParams{
		Name: "Test", Timezone: "America/Chicago", DayEnd: "21:00", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	kid, err := d.CreateUser(ctx, sqlc.CreateUserParams{DisplayName: "Sam", Role: "child"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.SQL.ExecContext(ctx, "INSERT INTO chores (id, name, created_at, updated_at) VALUES (1, 'Dishes', ?, ?)", now, now); err != nil {
		t.Fatal(err)
	}
	id, err := d.InsertChoreInstance(ctx, sqlc.InsertChoreInstanceParams{
		ChoreID: 1, KidID: sql.NullInt64{Int64: kid.ID, Valid: true}, OccurrenceDate: "2026-10-31",
		DueAt: "2026-11-01T02:00:00.000Z", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	limited := auth.WithPrincipal(ctx, &auth.Principal{Kind: auth.KindAPI})
	_, err = h.completeInstance(limited, &CompleteChoreInstanceInput{Instance: id})
	var p *apierr.Problem
	if !errors.As(err, &p) || p.Status != 403 || p.Code != apierr.CodeForbidden {
		t.Errorf("complete without kidId: err = %v, want 403 forbidden", err)
	}
	inst, err := h.instances.Get(ctx, id)
	if err != nil || inst.Status != "open" || inst.CompletedBy != 0 {
		t.Errorf("after the refusal: %+v, %v", inst, err)
	}

	parent := auth.WithPrincipal(ctx, &auth.Principal{Kind: auth.KindParent, UserID: 99})
	if out, err := h.completeInstance(parent, &CompleteChoreInstanceInput{Instance: id}); err != nil || out.Body.CompletedBy != kid.ID {
		t.Errorf("a parent completes for the assignee: %+v, %v", out, err)
	}
}
