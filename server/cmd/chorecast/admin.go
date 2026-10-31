package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/config"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/parents"
)

const adminUsage = `usage: chorecast admin <command> <username>

Account recovery on the server host, for when no owner can sign in. Safe to run while the
server is running (e.g. docker exec chorecast /chorecast admin reset-password mom).

commands:
  reset-password <username>   print a new temporary password and sign the parent out everywhere
  disable-2fa <username>      turn off two-factor sign-in and delete recovery codes`

// admin runs an admin subcommand against the database in CHORECAST_DATA_DIR.
func admin(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 2 {
		return errors.New(adminUsage)
	}
	store, err := db.Open(ctx, config.Load().DataDir)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	return runAdmin(ctx, parents.New(store, clock.Real{}), args[0], args[1], out)
}

func runAdmin(ctx context.Context, svc *parents.Service, cmd, username string, out io.Writer) error {
	u, err := svc.ByUsername(ctx, username)
	if err != nil {
		return err
	}
	switch cmd {
	case "reset-password":
		pw, err := svc.ResetPassword(ctx, u.ID)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "New temporary password for %s (%s): %s\nThey have been signed out everywhere. Ask them to change it on their account page.\n",
			u.DisplayName, u.Username.String, pw)
		return err
	case "disable-2fa":
		if err := svc.DisableTOTP(ctx, u.ID); err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "Two-factor sign-in is off for %s (%s). Their recovery codes were deleted.\n",
			u.DisplayName, u.Username.String)
		return err
	default:
		return errors.New(adminUsage)
	}
}
