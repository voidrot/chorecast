// Package config loads server configuration from CHORECAST_* environment variables.
package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr    string // listen address
	DataDir string // SQLite database and uploads
	Dev     bool   // relaxes cookie security, enables CORS for the Vite dev server
	// PublicURL is how browsers and apps reach the server, e.g. https://chores.example.com. Used in
	// links the server prints or shares (setup URL, invites, pairing). Optional.
	PublicURL string

	OIDC   OIDCConfig
	Media  MediaConfig
	Backup BackupConfig

	// FromEnv records which CHORECAST_* variables were set, so the admin can show those settings
	// as "set by environment".
	FromEnv map[string]bool
}

// OIDCConfig enables optional SSO for parent accounts (Authentik, Authelia, Keycloak, PocketID...).
type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
}

func (o OIDCConfig) Enabled() bool { return o.Issuer != "" && o.ClientID != "" }

// MediaConfig limits uploaded evidence (docs/features/approvals-evidence.md, Configuration).
type MediaConfig struct {
	MaxPhotoMB int // largest accepted photo upload, in MiB
}

// DefaultMaxPhotoMB is used when CHORECAST_MEDIA_MAX_PHOTO_MB is unset or not a positive number.
const DefaultMaxPhotoMB = 15

// MaxPhotoBytes is MaxPhotoMB in bytes, falling back to the default for a zero config.
func (m MediaConfig) MaxPhotoBytes() int64 {
	mb := m.MaxPhotoMB
	if mb <= 0 {
		mb = DefaultMaxPhotoMB
	}
	return int64(min(mb, 1<<30)) << 20 // the cap only keeps absurd values from overflowing
}

// BackupConfig schedules the backups the server writes to <DataDir>/backups
// (docs/features/household.md, Backups & export).
type BackupConfig struct {
	Keep        int           // scheduled backups kept; older ones are deleted
	AfterDayEnd time.Duration // delay after the household day end, in [0, 24h)
}

// Defaults for CHORECAST_BACKUP_KEEP and CHORECAST_BACKUP_AFTER_DAY_END, used when unset or invalid.
const (
	DefaultBackupKeep        = 7
	DefaultBackupAfterDayEnd = time.Hour
)

func Load() Config {
	fromEnv := map[string]bool{}
	for _, k := range []string{
		"CHORECAST_ADDR", "CHORECAST_DATA_DIR", "CHORECAST_DEV", "CHORECAST_PUBLIC_URL",
		"CHORECAST_OIDC_ISSUER", "CHORECAST_OIDC_CLIENT_ID", "CHORECAST_OIDC_CLIENT_SECRET",
		"CHORECAST_MEDIA_MAX_PHOTO_MB", "CHORECAST_BACKUP_KEEP", "CHORECAST_BACKUP_AFTER_DAY_END",
	} {
		if v, ok := os.LookupEnv(k); ok && v != "" {
			fromEnv[k] = true
		}
	}
	return Config{
		FromEnv:   fromEnv,
		Addr:      env("CHORECAST_ADDR", ":8080"),
		DataDir:   env("CHORECAST_DATA_DIR", "./data"),
		Dev:       envBool("CHORECAST_DEV"),
		PublicURL: strings.TrimRight(os.Getenv("CHORECAST_PUBLIC_URL"), "/"),
		OIDC: OIDCConfig{
			Issuer:       os.Getenv("CHORECAST_OIDC_ISSUER"),
			ClientID:     os.Getenv("CHORECAST_OIDC_CLIENT_ID"),
			ClientSecret: os.Getenv("CHORECAST_OIDC_CLIENT_SECRET"),
		},
		Media: MediaConfig{MaxPhotoMB: envPositiveInt("CHORECAST_MEDIA_MAX_PHOTO_MB", DefaultMaxPhotoMB)},
		Backup: BackupConfig{
			Keep:        envPositiveInt("CHORECAST_BACKUP_KEEP", DefaultBackupKeep),
			AfterDayEnd: envDayOffset("CHORECAST_BACKUP_AFTER_DAY_END", DefaultBackupAfterDayEnd),
		},
	}
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// envPositiveInt reads a positive integer, falling back to def when unset or invalid.
func envPositiveInt(key string, def int) int {
	v := os.Getenv(key)
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		warnInvalid(key, v, def)
		return def
	}
	return n
}

// warnInvalid logs that a set variable was ignored for its default; unset ones aren't mentioned.
func warnInvalid(key, value string, def any) {
	if strings.TrimSpace(value) != "" {
		slog.Warn("ignoring an invalid setting; using the default", "env", key, "value", value, "default", def)
	}
}

// envDayOffset reads a Go duration such as "1h" or "90m" in [0, 24h), falling back to def when
// unset or invalid.
func envDayOffset(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil || d < 0 || d >= 24*time.Hour {
		warnInvalid(key, v, def.String())
		return def
	}
	return d
}

func envBool(key string) bool {
	b, _ := strconv.ParseBool(os.Getenv(key))
	return b
}
