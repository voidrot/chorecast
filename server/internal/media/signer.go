package media

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
)

// serverKeyName is the server_keys row holding the server's master secret.
const serverKeyName = "server"

// urlKeyPurpose labels the key derived from the master secret for media URLs, so the secret can
// back other keys without them being interchangeable.
const urlKeyPurpose = "chorecast media url v1"

// Signer signs and verifies media URLs with HMAC-SHA256. The key is derived from the server's
// master secret, which is generated (32 random bytes) and stored in the database on first use.
type Signer struct {
	q     *sqlc.Queries
	clock clock.Clock

	mu  sync.Mutex
	key []byte
}

// NewSigner returns a signer that loads its key from q lazily, on the first Sign or Verify.
func NewSigner(q *sqlc.Queries, c clock.Clock) *Signer { return &Signer{q: q, clock: c} }

func (s *Signer) loadKey(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key != nil {
		return s.key, nil
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	// Insert-if-absent, then read back: concurrent first uses (or several processes) agree.
	if err := s.q.InsertServerKey(ctx, sqlc.InsertServerKeyParams{
		Name: serverKeyName, Key: secret, CreatedAt: db.Timestamp(s.clock.Now()),
	}); err != nil {
		return nil, fmt.Errorf("media: store server key: %w", err)
	}
	master, err := s.q.GetServerKey(ctx, serverKeyName)
	if err != nil {
		return nil, fmt.Errorf("media: load server key: %w", err)
	}
	m := hmac.New(sha256.New, master)
	m.Write([]byte(urlKeyPurpose))
	s.key = m.Sum(nil)
	return s.key, nil
}

func mac(key []byte, storageKey string, exp int64) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(storageKey))
	m.Write([]byte{0})
	m.Write([]byte(strconv.FormatInt(exp, 10)))
	return m.Sum(nil)
}

// Sign returns the URL signature for storageKey valid until expires.
func (s *Signer) Sign(ctx context.Context, storageKey string, expires time.Time) (string, error) {
	key, err := s.loadKey(ctx)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(mac(key, storageKey, expires.Unix())), nil
}

// Verify reports whether sig is a valid signature for storageKey and exp (Unix seconds) and exp
// hasn't passed. The comparison is constant-time.
func (s *Signer) Verify(ctx context.Context, storageKey, exp, sig string) (bool, error) {
	key, err := s.loadKey(ctx)
	if err != nil {
		return false, err
	}
	e, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || strconv.FormatInt(e, 10) != exp {
		return false, nil
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return false, nil
	}
	if !hmac.Equal(got, mac(key, storageKey, e)) {
		return false, nil
	}
	return s.clock.Now().Unix() < e, nil
}
