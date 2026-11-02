package config

import (
	"testing"
	"time"
)

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
