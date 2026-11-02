package config

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// A set but invalid value is logged as ignored, so a KEEP of 0 doesn't silently become 7.
func TestInvalidSettingWarns(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	t.Setenv("CHORECAST_BACKUP_KEEP", "0")
	t.Setenv("CHORECAST_BACKUP_AFTER_DAY_END", "")
	Load()
	if !strings.Contains(buf.String(), "CHORECAST_BACKUP_KEEP") {
		t.Errorf("no warning for CHORECAST_BACKUP_KEEP=0: %q", buf.String())
	}
	if strings.Contains(buf.String(), "CHORECAST_BACKUP_AFTER_DAY_END") {
		t.Errorf("an unset variable was warned about: %q", buf.String())
	}
}

func TestBackupConfig(t *testing.T) {
	for env, want := range map[string]int{"": 7, "3": 3, "0": 7, "-1": 7, "many": 7} {
		t.Setenv("CHORECAST_BACKUP_KEEP", env)
		if got := Load().Backup.Keep; got != want {
			t.Errorf("CHORECAST_BACKUP_KEEP=%q: %d, want %d", env, got, want)
		}
	}
	for env, want := range map[string]time.Duration{
		"": time.Hour, "0s": 0, "90m": 90 * time.Minute, " 2h ": 2 * time.Hour, "24h": time.Hour, "-1h": time.Hour, "soon": time.Hour,
	} {
		t.Setenv("CHORECAST_BACKUP_AFTER_DAY_END", env)
		if got := Load().Backup.AfterDayEnd; got != want {
			t.Errorf("CHORECAST_BACKUP_AFTER_DAY_END=%q: %v, want %v", env, got, want)
		}
	}
}

func TestMediaMaxPhotoMB(t *testing.T) {
	for env, want := range map[string]int64{"": 15 << 20, "4": 4 << 20, " 30 ": 30 << 20, "0": 15 << 20, "-2": 15 << 20, "lots": 15 << 20} {
		t.Setenv("CHORECAST_MEDIA_MAX_PHOTO_MB", env)
		c := Load()
		if got := c.Media.MaxPhotoBytes(); got != want {
			t.Errorf("CHORECAST_MEDIA_MAX_PHOTO_MB=%q: %d bytes, want %d", env, got, want)
		}
		if c.FromEnv["CHORECAST_MEDIA_MAX_PHOTO_MB"] != (env != "") {
			t.Errorf("CHORECAST_MEDIA_MAX_PHOTO_MB=%q: FromEnv = %v", env, c.FromEnv["CHORECAST_MEDIA_MAX_PHOTO_MB"])
		}
	}
	if got := (MediaConfig{}).MaxPhotoBytes(); got != DefaultMaxPhotoMB<<20 {
		t.Errorf("zero config: %d bytes", got)
	}
}
