package main

import (
	"bytes"
	"database/sql"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/media"
	"github.com/voidrot/chorecast/server/internal/parents"
)

func TestAdminRecovery(t *testing.T) {
	store, err := db.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	hash, _ := auth.HashSecret("old password")
	u, err := store.CreateUser(t.Context(), sqlc.CreateUserParams{
		DisplayName: "Mom", Role: "parent", IsOwner: 1,
		Username: sql.NullString{String: "mom", Valid: true}, PasswordHash: sql.NullString{String: hash, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetPendingTOTPSecret(t.Context(), sqlc.SetPendingTOTPSecretParams{TotpSecret: sql.NullString{String: "X", Valid: true}, ID: u.ID}); err != nil {
		t.Fatal(err)
	}
	_ = store.EnableTOTP(t.Context(), sqlc.EnableTOTPParams{ID: u.ID})
	token, _, _ := auth.NewStore(store.Queries, clock.Real{}).Issue(t.Context(), auth.IssueParams{Kind: auth.TokenApp, UserID: u.ID})
	svc := parents.New(store, clock.Real{})

	var out bytes.Buffer
	if err := runAdmin(t.Context(), svc, "reset-password", "MOM", &out); err != nil {
		t.Fatal(err)
	}
	pw := regexp.MustCompile(`: ([a-z0-9-]{14})\n`).FindStringSubmatch(out.String())
	if pw == nil {
		t.Fatalf("no password in %q", out.String())
	}
	got, _ := store.GetUser(t.Context(), u.ID)
	if ok, _, _ := auth.VerifySecret(pw[1], got.PasswordHash.String); !ok {
		t.Error("printed password doesn't match the stored hash")
	}
	if p, _ := auth.NewStore(store.Queries, clock.Real{}).Authenticate(t.Context(), token, auth.TokenApp); p != nil {
		t.Error("sessions survived a password reset")
	}

	out.Reset()
	if err := runAdmin(t.Context(), svc, "disable-2fa", "mom", &out); err != nil || !strings.Contains(out.String(), "off for Mom") {
		t.Fatalf("disable-2fa: %v %q", err, out.String())
	}
	if got, _ := store.GetUser(t.Context(), u.ID); got.TotpEnabled != 0 || got.TotpSecret.Valid {
		t.Errorf("2FA still on: %+v", got)
	}

	if err := runAdmin(t.Context(), svc, "reset-password", "nobody", &out); err == nil || !strings.Contains(err.Error(), "no parent") {
		t.Errorf("unknown user: %v", err)
	}
	if err := runAdmin(t.Context(), svc, "explode", "mom", &out); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("unknown command: %v", err)
	}
}

func TestAdminRotateMediaKey(t *testing.T) {
	store, err := db.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	signer := media.NewSigner(store.Queries, clock.Real{})
	key, _ := media.NewKey()
	exp := clock.Real{}.Now().Add(time.Minute)
	sig, _ := signer.Sign(t.Context(), key, exp)

	var out bytes.Buffer
	if err := rotateMediaKey(t.Context(), store, clock.Real{}, &out); err != nil || !strings.Contains(out.String(), "Restart the server") {
		t.Fatalf("rotate-media-key: %v %q", err, out.String())
	}
	if ok, _ := media.NewSigner(store.Queries, clock.Real{}).Verify(t.Context(), key, strconv.FormatInt(exp.Unix(), 10), sig); ok {
		t.Error("a link signed before rotation still verifies with the stored secret")
	}
	if err := admin(t.Context(), []string{"rotate-media-key", "extra"}, &out); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("extra argument: %v", err)
	}
}
