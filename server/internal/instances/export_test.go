package instances

import (
	"context"
	"testing"

	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/events"
)

// SetMissInTx stands in for day close's miss for the test's duration.
func SetMissInTx(t *testing.T, fn func(context.Context, *events.Tx, clock.Clock, int64) error) {
	prev := missInTx
	missInTx = fn
	t.Cleanup(func() { missInTx = prev })
}
