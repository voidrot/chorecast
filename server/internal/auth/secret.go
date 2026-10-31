package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2Params are argon2id cost parameters. Memory is in KiB.
type Argon2Params struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLen     uint32
	KeyLen      uint32
}

// DefaultArgon2 follows the OWASP minimum (19 MiB, 2 iterations, 1 lane), cheap enough for a
// Raspberry Pi while still slowing down offline guessing.
var DefaultArgon2 = Argon2Params{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLen: 16, KeyLen: 32}

var errMalformedHash = errors.New("malformed argon2id hash")

// HashSecret hashes a password or PIN with argon2id into a self-describing PHC string:
// $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>.
func HashSecret(secret string) (string, error) { return hashSecret(secret, DefaultArgon2) }

func hashSecret(secret string, p Argon2Params) (string, error) {
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(secret), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Iterations, p.Parallelism, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifySecret reports whether secret matches encoded, and whether the hash uses parameters
// other than DefaultArgon2 and should be re-hashed after a successful login.
func VerifySecret(secret, encoded string) (ok, needsRehash bool, err error) {
	p, salt, key, err := parseHash(encoded)
	if err != nil {
		return false, false, err
	}
	got := argon2.IDKey([]byte(secret), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLen)
	if subtle.ConstantTimeCompare(got, key) != 1 {
		return false, false, nil
	}
	return true, p != DefaultArgon2, nil
}

func parseHash(encoded string) (Argon2Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return Argon2Params{}, nil, nil, errMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return Argon2Params{}, nil, nil, errMalformedHash
	}
	var p Argon2Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return Argon2Params{}, nil, nil, errMalformedHash
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return Argon2Params{}, nil, nil, errMalformedHash
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil || len(key) == 0 || p.Iterations == 0 || p.Parallelism == 0 {
		return Argon2Params{}, nil, nil, errMalformedHash
	}
	p.SaltLen, p.KeyLen = uint32(len(salt)), uint32(len(key)) //nolint:gosec // lengths of decoded hash parts are small
	return p, salt, key, nil
}
