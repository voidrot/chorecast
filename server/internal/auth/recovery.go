package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"strings"
)

// RecoveryCodeCount is how many recovery codes enabling 2FA creates.
const RecoveryCodeCount = 10

// Unambiguous lowercase letters and digits (no 0/o, 1/l/i).
const recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// NewRecoveryCodes returns n codes like "k7mp-q2xd-9fht" (12 characters, about 59 bits each).
func NewRecoveryCodes(n int) ([]string, error) { return newCodes(n, 12) }

// NewShortCode returns one code of length characters in groups of four, e.g. "k7mp-q2xd" for 8:
// typeable codes for short-lived uses such as device pairing. Hash with HashRecoveryCode.
func NewShortCode(length int) (string, error) {
	codes, err := newCodes(1, length)
	if err != nil {
		return "", err
	}
	return codes[0], nil
}

func newCodes(n, length int) ([]string, error) {
	codes := make([]string, n)
	buf := make([]byte, length)
	for i := range codes {
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		var b strings.Builder
		for j, c := range buf {
			if j > 0 && j%4 == 0 {
				b.WriteByte('-')
			}
			// 31 symbols: the modulo bias (256 % 31 = 8) costs well under a bit per code.
			b.WriteByte(recoveryAlphabet[int(c)%len(recoveryAlphabet)])
		}
		codes[i] = b.String()
	}
	return codes, nil
}

// HashRecoveryCode returns the stored form of a recovery code. Input is normalized (case,
// spaces and hyphens ignored) so codes can be typed loosely. Codes are random enough that a
// plain SHA-256 is adequate, and it allows lookup by hash.
func HashRecoveryCode(code string) []byte {
	norm := strings.Map(func(r rune) rune {
		switch {
		case r == '-' || r == ' ':
			return -1
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		}
		return r
	}, strings.TrimSpace(code))
	h := sha256.Sum256([]byte(norm))
	return h[:]
}
