package auth

import (
	"crypto/subtle"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	totpPeriod = 30 // seconds
	totpSkew   = 1  // accept the previous and next step too, for clock drift
)

// NewTOTPKey generates a TOTP secret for account and returns it (base32) with the otpauth://
// URL authenticator apps scan as a QR code.
func NewTOTPKey(issuer, account string) (secret, url string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: issuer, AccountName: account, Period: totpPeriod})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// VerifyTOTP checks a 6-digit code against secret at now, allowing ±1 step of drift. It returns
// the matched time step, which must be greater than lastStep: a code is accepted at most once.
func VerifyTOTP(secret, code string, now time.Time, lastStep int64) (step int64, ok bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return 0, false
	}
	current := now.Unix() / totpPeriod
	for s := current - totpSkew; s <= current+totpSkew; s++ {
		if s <= lastStep {
			continue
		}
		want, err := totp.GenerateCodeCustom(secret, time.Unix(s*totpPeriod, 0), totp.ValidateOpts{
			Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
		})
		if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}

// CurrentTOTPStep is the time step at now (for marking an enrolment code as used).
func CurrentTOTPStep(now time.Time) int64 { return now.Unix() / totpPeriod }
