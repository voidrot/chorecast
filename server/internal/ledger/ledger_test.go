package ledger_test

import (
	"database/sql"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/events"
	"github.com/voidrot/chorecast/server/internal/jobs"
	"github.com/voidrot/chorecast/server/internal/ledger"
	"github.com/voidrot/chorecast/server/internal/paging"
)

type fixture struct {
	t        *testing.T
	db       *db.DB
	clk      *clock.Fake
	bus      *events.Bus
	svc      *ledger.Service
	sam, ada int64 // kids; ada's day ends at 19:00
	mom      int64
}

// newFixture is a household in America/Chicago whose day ends at 21:00, with negative
// balances off, two kids and a parent. The clock is 2026-10-31 12:00 UTC (07:00 local).
func newFixture(t *testing.T) *fixture {
	t.Helper()
	d, err := db.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	clk := clock.NewFake(time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC))
	quiet := slog.New(slog.DiscardHandler)
	bus := events.NewBus(d, jobs.NewRunner(d.Queries, clk, quiet), clk, quiet)
	if err := d.CreateHousehold(t.Context(), sqlc.CreateHouseholdParams{
		Name: "Test", Timezone: "America/Chicago", DayEnd: "21:00", WeekStart: 0, CreatedAt: db.Timestamp(clk.Now()),
	}); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, db: d, clk: clk, bus: bus, svc: ledger.New(d, bus, clk)}
	user := func(name, role string) int64 {
		u, err := d.CreateUser(t.Context(), sqlc.CreateUserParams{DisplayName: name, Role: role})
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	f.sam, f.ada, f.mom = user("Sam", "child"), user("Ada", "child"), user("Mom", "parent")
	f.exec("UPDATE users SET day_end_override = '19:00' WHERE id = ?", f.ada)
	return f
}

func (f *fixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.SQL.ExecContext(f.t.Context(), query, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) allowNegative(on bool) {
	f.t.Helper()
	v := 0
	if on {
		v = 1
	}
	f.exec("UPDATE household SET allow_negative_balance = ?", v)
}

func (f *fixture) post(in ledger.Posting) (ledger.PointsEntry, error) {
	var out ledger.PointsEntry
	err := f.bus.InTx(f.t.Context(), func(tx *events.Tx) error {
		var err error
		out, err = ledger.Post(f.t.Context(), tx, f.clk, in)
		return err
	})
	return out, err
}

func (f *fixture) mustPost(in ledger.Posting) ledger.PointsEntry {
	f.t.Helper()
	e, err := f.post(in)
	if err != nil {
		f.t.Fatalf("post %+v: %v", in, err)
	}
	return e
}

func (f *fixture) reverse(id int64, note string) (ledger.PointsEntry, error) {
	var out ledger.PointsEntry
	err := f.bus.InTx(f.t.Context(), func(tx *events.Tx) error {
		var err error
		out, err = ledger.Reverse(f.t.Context(), tx, f.clk, id, note, f.mom)
		return err
	})
	return out, err
}

func (f *fixture) balance(kid int64) int64 {
	f.t.Helper()
	b, err := ledger.Balance(f.t.Context(), f.db.Queries, kid)
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

func (f *fixture) postedEvents() []events.Envelope {
	f.t.Helper()
	rows, err := f.db.ListUndispatchedEvents(f.t.Context(), 1000)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []events.Envelope
	for _, r := range rows {
		if r.Type == (ledger.EntryPosted{}).EventType() {
			out = append(out, events.Envelope{ID: r.ID, Type: r.Type, Payload: []byte(r.Payload)})
		}
	}
	return out
}

func adjust(kid, amount int64) ledger.Posting {
	return ledger.Posting{KidID: kid, Kind: ledger.KindAdjustment, Amount: amount, Note: "test"}
}

func chore(kid, instance, amount int64) ledger.Posting {
	return ledger.Posting{KidID: kid, Kind: ledger.KindChore, Amount: amount,
		Source: ledger.Source{Kind: ledger.SourceChoreInstance, ID: instance}}
}

func penalty(kid, instance, amount int64) ledger.Posting {
	return ledger.Posting{KidID: kid, Kind: ledger.KindChorePenalty, Amount: amount,
		Source: ledger.Source{Kind: ledger.SourceChoreInstance, ID: instance}}
}

func wantStatus(t *testing.T, what string, err error, status int) {
	t.Helper()
	var se huma.StatusError
	if !errors.As(err, &se) || se.GetStatus() != status {
		t.Fatalf("%s: err = %v, want status %d", what, err, status)
	}
}

func TestReverseOnce(t *testing.T) {
	f := newFixture(t)
	credit := f.mustPost(chore(f.sam, 7, 10))

	rev, err := f.reverse(credit.ID, "  wasn't done  ")
	if err != nil {
		t.Fatal(err)
	}
	if rev.Kind != ledger.KindReversal || rev.Amount != -10 || rev.ReversesID != credit.ID || rev.Note != "wasn't done" ||
		rev.SourceKind != ledger.SourceChoreInstance || rev.SourceID != 7 || rev.CreatedBy != f.mom || rev.BalanceAfter != 0 {
		t.Errorf("reversal = %+v", rev)
	}
	if _, err := f.reverse(credit.ID, "again"); !errors.Is(err, ledger.ErrAlreadyReversed) {
		t.Errorf("second reversal: err = %v, want ErrAlreadyReversed", err)
	}
	if b := f.balance(f.sam); b != 0 {
		t.Errorf("balance after refused double reversal = %d, want 0", b)
	}

	// Undoing the undo reverses the reversal (re-approval, un-excusing).
	again, err := f.reverse(rev.ID, "it was done after all")
	if err != nil || again.Amount != 10 || f.balance(f.sam) != 10 {
		t.Errorf("reversing the reversal = %+v, %v; balance %d", again, err, f.balance(f.sam))
	}

	if _, err := f.reverse(credit.ID+100, "nope"); !errors.Is(err, ledger.ErrEntryNotFound) {
		t.Errorf("unknown entry: err = %v", err)
	}
	_, err = f.reverse(again.ID, "   ")
	wantStatus(t, "reversal without a note", err, http.StatusUnprocessableEntity)

	// The database refuses a second reversal too.
	err = f.bus.InTx(t.Context(), func(tx *events.Tx) error {
		_, err := tx.Q.InsertPointsEntry(t.Context(), sqlc.InsertPointsEntryParams{
			KidID: f.sam, Kind: ledger.KindReversal, Amount: -10, LocalDate: "2026-10-31", Note: "raw",
			ReversesID: sql.NullInt64{Int64: credit.ID, Valid: true}, CreatedAt: db.Timestamp(f.clk.Now()),
		})
		return err
	})
	if err == nil {
		t.Error("UNIQUE(reverses_id) allowed a second reversal")
	}
}

func TestClampWhenNegativeBalanceIsOff(t *testing.T) {
	f := newFixture(t)
	f.mustPost(adjust(f.sam, 5))

	e := f.mustPost(penalty(f.sam, 1, -8))
	if e.Amount != -5 || e.RequestedAmount == nil || *e.RequestedAmount != -8 || e.BalanceAfter != 0 {
		t.Errorf("clamped penalty = %+v", e)
	}
	// At zero, a deduction clamps to 0 but is still recorded.
	e = f.mustPost(adjust(f.sam, -3))
	if e.Amount != 0 || e.RequestedAmount == nil || *e.RequestedAmount != -3 {
		t.Errorf("deduction at zero = %+v", e)
	}
	// A deduction that fits isn't clamped.
	f.mustPost(adjust(f.sam, 4))
	if e := f.mustPost(adjust(f.sam, -4)); e.Amount != -4 || e.RequestedAmount != nil {
		t.Errorf("fitting deduction = %+v", e)
	}

	// With negative balances on, nothing is clamped.
	f.allowNegative(true)
	if e := f.mustPost(adjust(f.sam, -6)); e.Amount != -6 || e.RequestedAmount != nil || e.BalanceAfter != -6 {
		t.Errorf("deduction with negatives on = %+v", e)
	}
	// Switched back off while below zero: deductions clamp to 0, credits still count.
	f.allowNegative(false)
	if e := f.mustPost(penalty(f.sam, 2, -2)); e.Amount != 0 || *e.RequestedAmount != -2 {
		t.Errorf("deduction below zero = %+v", e)
	}
	if b := f.balance(f.sam); b != -6 {
		t.Errorf("balance = %d, want -6", b)
	}

	// Reversals are the exact negation and never clamped: undoing a credit can go below zero.
	g := newFixture(t)
	credit := g.mustPost(chore(g.sam, 1, 10))
	g.mustPost(adjust(g.sam, -7))
	rev, err := g.reverse(credit.ID, "not done")
	if err != nil || rev.Amount != -10 || rev.RequestedAmount != nil || g.balance(g.sam) != -7 {
		t.Errorf("reversal = %+v, %v; balance %d", rev, err, g.balance(g.sam))
	}
}

func TestPostAtMostOncePerSource(t *testing.T) {
	f := newFixture(t)
	first := f.mustPost(chore(f.sam, 7, 10))

	dup, err := f.post(chore(f.sam, 7, 10))
	if !errors.Is(err, ledger.ErrAlreadyPosted) || dup.ID != first.ID {
		t.Fatalf("repeat post = %+v, %v; want the first entry and ErrAlreadyPosted", dup, err)
	}
	if b := f.balance(f.sam); b != 10 {
		t.Errorf("balance after repeat = %d, want 10", b)
	}
	if n := len(f.postedEvents()); n != 1 {
		t.Errorf("%d entry_posted events, want 1", n)
	}

	// Another kind, kid or source is a different entry; adjustments with a source repeat freely.
	f.mustPost(penalty(f.sam, 7, -1))
	f.mustPost(chore(f.ada, 7, 10))
	f.mustPost(chore(f.sam, 8, 10))
	recurring := ledger.Posting{KidID: f.sam, Kind: ledger.KindAdjustment, Amount: 5, Note: "allowance",
		Source: ledger.Source{Kind: "recurring_adjustment", ID: 1}}
	f.mustPost(recurring)
	f.mustPost(recurring)
	if b := f.balance(f.sam); b != 10-1+10+5+5 {
		t.Errorf("balance = %d", b)
	}

	// After a reversal the source still counts as posted: undo the reversal instead.
	rev, err := f.reverse(first.ID, "undo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.post(chore(f.sam, 7, 10)); !errors.Is(err, ledger.ErrAlreadyPosted) {
		t.Errorf("re-post after reversal: err = %v", err)
	}
	if _, err := f.reverse(rev.ID, "redo"); err != nil {
		t.Error(err)
	}

	// A rolled-back transaction leaves no entry and no event.
	before := len(f.postedEvents())
	boom := errors.New("boom")
	err = f.bus.InTx(t.Context(), func(tx *events.Tx) error {
		if _, err := ledger.Post(t.Context(), tx, f.clk, chore(f.sam, 99, 10)); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || len(f.postedEvents()) != before {
		t.Errorf("rollback: err = %v, events %d -> %d", err, before, len(f.postedEvents()))
	}
	if _, err := f.post(chore(f.sam, 99, 10)); err != nil {
		t.Errorf("post after rollback: %v", err)
	}
}

func TestEntryPostedEvent(t *testing.T) {
	f := newFixture(t)
	e := f.mustPost(chore(f.sam, 3, 4))
	evs := f.postedEvents()
	if len(evs) != 1 {
		t.Fatalf("%d events", len(evs))
	}
	got, err := events.Decode[ledger.EntryPosted](evs[0])
	want := ledger.EntryPosted{EntryID: e.ID, KidID: f.sam, Kind: ledger.KindChore, Amount: 4, BalanceAfter: 4,
		LocalDate: caltime.Date{Year: 2026, Month: 10, Day: 31}}
	if err != nil || got != want {
		t.Errorf("event = %+v, %v; want %+v", got, err, want)
	}
}

func TestLocalDateFollowsKidDayEnd(t *testing.T) {
	f := newFixture(t)
	oct31 := caltime.Date{Year: 2026, Month: 10, Day: 31}
	nov1 := oct31.AddDays(1)

	// 20:00 in Chicago: Sam's day (ends 21:00) is still Oct 31, Ada's (ends 19:00) is Nov 1.
	f.clk.Set(time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC))
	if e := f.mustPost(adjust(f.sam, 1)); e.LocalDate != oct31 {
		t.Errorf("sam's date = %s", e.LocalDate)
	}
	if e := f.mustPost(adjust(f.ada, 1)); e.LocalDate != nov1 {
		t.Errorf("ada's date = %s", e.LocalDate)
	}
	// A caller's date (e.g. the day being closed) wins.
	if e := f.mustPost(ledger.Posting{KidID: f.sam, Kind: ledger.KindChorePenalty, Amount: 0, LocalDate: oct31.AddDays(-1),
		Source: ledger.Source{Kind: ledger.SourceChoreInstance, ID: 1}}); e.LocalDate != oct31.AddDays(-1) {
		t.Errorf("explicit date = %s", e.LocalDate)
	}
}

func TestPostValidation(t *testing.T) {
	f := newFixture(t)
	for name, in := range map[string]ledger.Posting{
		"zero adjustment":      adjust(f.sam, 0),
		"adjustment, no note":  {KidID: f.sam, Kind: ledger.KindAdjustment, Amount: 5, Note: "  "},
		"chore without source": {KidID: f.sam, Kind: ledger.KindChore, Amount: 5},
		"negative chore":       chore(f.sam, 1, -5),
		"positive penalty":     penalty(f.sam, 1, 5),
		"reversal via Post":    {KidID: f.sam, Kind: ledger.KindReversal, Amount: 5, Note: "x"},
		"unknown kind":         {KidID: f.sam, Kind: "bribe", Amount: 5},
		"too large":            adjust(f.sam, ledger.MaxAmount+1),
		"half a source":        {KidID: f.sam, Kind: ledger.KindAdjustment, Amount: 5, Note: "x", Source: ledger.Source{Kind: "x"}},
	} {
		_, err := f.post(in)
		wantStatus(t, name, err, http.StatusUnprocessableEntity)
	}
	_, err := f.post(adjust(f.mom, 5))
	wantStatus(t, "a parent isn't a kid", err, http.StatusNotFound)
	if b := f.balance(f.sam); b != 0 {
		t.Errorf("balance after refused posts = %d", b)
	}
}

// TestBalanceIsSumOfEntries runs random operation sequences against a simple model: the
// balance always equals the sum of the entries, and the listed running balances agree.
func TestBalanceIsSumOfEntries(t *testing.T) {
	for seed := range uint64(5) {
		f := newFixture(t)
		rng := rand.New(rand.NewPCG(seed, 26)) //nolint:gosec // seeded on purpose: failures must reproduce
		type modelEntry struct {
			id, amount int64
			reversed   bool
		}
		var model []modelEntry
		balance := func() int64 {
			var sum int64
			for _, e := range model {
				sum += e.amount
			}
			return sum
		}
		allowNegative := false
		var instance, lastChore int64
		for step := range 300 {
			var in ledger.Posting
			switch op := rng.IntN(10); {
			case op < 3:
				in = adjust(f.sam, rng.Int64N(41)-20)
				if in.Amount == 0 {
					in.Amount = 1
				}
			case op < 5:
				instance++
				lastChore = instance
				in = chore(f.sam, instance, rng.Int64N(15))
			case op < 7:
				instance++
				in = penalty(f.sam, instance, -rng.Int64N(15))
			case op < 8 && lastChore != 0:
				// A repeat of an earlier chore's posting (a retried job): must change nothing.
				if _, err := f.post(chore(f.sam, lastChore, 3)); !errors.Is(err, ledger.ErrAlreadyPosted) {
					t.Fatalf("seed %d step %d: repeat post err = %v", seed, step, err)
				}
			case op < 9 && len(model) > 0:
				i := rng.IntN(len(model))
				rev, err := f.reverse(model[i].id, "oops")
				switch {
				case model[i].reversed:
					if !errors.Is(err, ledger.ErrAlreadyReversed) {
						t.Fatalf("seed %d step %d: double reversal err = %v", seed, step, err)
					}
				case err != nil:
					t.Fatal(err)
				default:
					model[i].reversed = true
					model = append(model, modelEntry{id: rev.ID, amount: -model[i].amount})
				}
			default:
				allowNegative = !allowNegative
				f.allowNegative(allowNegative)
			}
			if in.Kind != "" {
				want := in.Amount
				if bal := balance(); want < 0 && !allowNegative {
					want = max(want, -max(bal, 0))
				}
				e, err := f.post(in)
				if err != nil {
					t.Fatalf("seed %d step %d: post %+v: %v", seed, step, in, err)
				}
				if e.Amount != want {
					t.Fatalf("seed %d step %d: posted %d, want %d (balance %d, negatives %v)", seed, step, e.Amount, want, balance(), allowNegative)
				}
				model = append(model, modelEntry{id: e.ID, amount: e.Amount})
			}
			if got := f.balance(f.sam); got != balance() {
				t.Fatalf("seed %d step %d: balance %d, model %d", seed, step, got, balance())
			}
		}

		// Page through the ledger: newest first, running balances consistent across pages.
		var listed []ledger.PointsEntry
		params := paging.Params{Limit: 17}
		for {
			page, err := f.svc.List(t.Context(), f.sam, params)
			if err != nil {
				t.Fatal(err)
			}
			listed = append(listed, page.Items...)
			if page.NextCursor == "" {
				break
			}
			params.Cursor = page.NextCursor
		}
		if len(listed) != len(model) {
			t.Fatalf("seed %d: listed %d entries, model %d", seed, len(listed), len(model))
		}
		running := balance()
		for i, e := range listed {
			m := model[len(model)-1-i]
			if e.ID != m.id || e.BalanceAfter != running || (e.ReversedByID != 0) != m.reversed {
				t.Fatalf("seed %d: entry %d = %+v, want id %d balanceAfter %d reversed %v", seed, i, e, m.id, running, m.reversed)
			}
			running -= e.Amount
		}
		if running != 0 {
			t.Fatalf("seed %d: running balance ends at %d", seed, running)
		}
		if b, err := f.svc.Balance(t.Context(), f.sam); err != nil || b.Balance != balance() || b.Available != b.Balance {
			t.Fatalf("seed %d: Balance = %+v, %v", seed, b, err)
		}
	}
}
