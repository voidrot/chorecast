package config

import "testing"

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
