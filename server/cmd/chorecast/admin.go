package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/config"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/media"
	"github.com/voidrot/chorecast/server/internal/parents"
)

const adminUsage = `usage: chorecast admin <command> [<username>]

Account recovery and maintenance on the server host, for when no owner can sign in. Safe to run
while the server is running (e.g. docker exec chorecast /chorecast admin reset-password mom).

commands:
  reset-password <username>   print a new temporary password and sign the parent out everywhere
  disable-2fa <username>      turn off two-factor sign-in and delete recovery codes
  rotate-media-key            replace the secret media links are signed with; links already
                              handed out stop working once the server restarts (restart it)`

// admin runs an admin subcommand against the database in CHORECAST_DATA_DIR.
func admin(ctx context.Context, args []string, out io.Writer) error {
	want := 2 // command and username
	if len(args) > 0 && args[0] == "rotate-media-key" {
		want = 1
	}
	if len(args) != want {
		return errors.New(adminUsage)
	}
	store, err := db.Open(ctx, config.Load().DataDir)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	if args[0] == "rotate-media-key" {
		return rotateMediaKey(ctx, store, clock.Real{}, out)
	}
	return runAdmin(ctx, parents.New(store, clock.Real{}), args[0], args[1], out)
}

func rotateMediaKey(ctx context.Context, store *db.DB, c clock.Clock, out io.Writer) error {
	if err := media.RotateURLSecret(ctx, store.Queries, c); err != nil {
		return err
	}
	_, err := fmt.Fprintln(out, "The media link secret was replaced. Restart the server to use it; links handed out before then stop working.")
	return err
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
