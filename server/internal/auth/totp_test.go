package auth

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/voidrot/chorecast/server/internal/clock"
)

func codeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	c, err := totp.GenerateCodeCustom(secret, at, totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestVerifyTOTP(t *testing.T) {
	secret, url, err := NewTOTPKey("Chorecast (Home)", "mom")
	if err != nil || !strings.HasPrefix(url, "otpauth://totp/") || !strings.Contains(url, "secret="+secret) {
		t.Fatalf("NewTOTPKey = %q %q %v", secret, url, err)
	}
	now := time.Date(2026, 10, 31, 12, 0, 10, 0, time.UTC)
	step := CurrentTOTPStep(now)

	got, ok := VerifyTOTP(secret, codeAt(t, secret, now), now, 0)
	if !ok || got != step {
		t.Fatalf("current code: step %d ok %v (want %d)", got, ok, step)
	}
	if _, ok := VerifyTOTP(secret, codeAt(t, secret, now), now, step); ok {
		t.Error("a code was accepted twice")
	}
	if got, ok := VerifyTOTP(secret, codeAt(t, secret, now.Add(-30*time.Second)), now, 0); !ok || got != step-1 {
		t.Errorf("previous step (drift) rejected: %d %v", got, ok)
	}
	if _, ok := VerifyTOTP(secret, codeAt(t, secret, now.Add(-90*time.Second)), now, 0); ok {
		t.Error("a code three steps old was accepted")
	}
	if _, ok := VerifyTOTP(secret, "12345", now, 0); ok {
		t.Error("a 5-digit code was accepted")
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, err := NewRecoveryCodes(RecoveryCodeCount)
	if err != nil || len(codes) != RecoveryCodeCount {
		t.Fatalf("NewRecoveryCodes = %v, %v", codes, err)
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 14 || c[4] != '-' || c[9] != '-' || seen[c] {
			t.Errorf("bad or duplicate code %q", c)
		}
		seen[c] = true
	}
	loose := strings.ToUpper(strings.ReplaceAll(codes[0], "-", " "))
	if !bytes.Equal(HashRecoveryCode(codes[0]), HashRecoveryCode(" "+loose+" ")) {
		t.Error("hash depends on case, spaces or hyphens")
	}
	if bytes.Equal(HashRecoveryCode(codes[0]), HashRecoveryCode(codes[1])) {
		t.Error("different codes hash equal")
	}
}

func TestLimiter(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	l := NewLimiter(clk, 3, 10*time.Minute)
	for range 2 {
		l.Fail("a")
	}
	if blocked, _ := l.Blocked("a"); blocked {
		t.Fatal("blocked below the limit")
	}
	l.Fail("a")
	clk.Advance(4 * time.Minute)
	if blocked, wait := l.Blocked("a"); !blocked || wait != 6*time.Minute {
		t.Fatalf("Blocked = %v %v, want true 6m", blocked, wait)
	}
	if blocked, _ := l.Blocked("b"); blocked {
		t.Error("other keys are blocked")
	}
	clk.Advance(6 * time.Minute)
	if blocked, _ := l.Blocked("a"); blocked {
		t.Error("still blocked after the window")
	}
	l.Fail("a")
	l.Reset("a")
	for range 2 {
		l.Fail("a")
	}
	if blocked, _ := l.Blocked("a"); blocked {
		t.Error("Reset didn't clear failures")
	}
}
