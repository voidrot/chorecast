// Package config loads server configuration from CHORECAST_* environment variables.
package config

import (
	"os"
	"strconv"
)

type Config struct {
	Addr    string // listen address
	DataDir string // SQLite database and uploads
	Dev     bool   // relaxes cookie security, enables CORS for the Vite dev server

	OIDC OIDCConfig
}

// OIDCConfig enables optional SSO for parent accounts (Authentik, Authelia, Keycloak, PocketID...).
type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
}

func (o OIDCConfig) Enabled() bool { return o.Issuer != "" && o.ClientID != "" }

func Load() Config {
	return Config{
		Addr:    env("CHORECAST_ADDR", ":8080"),
		DataDir: env("CHORECAST_DATA_DIR", "./data"),
		Dev:     envBool("CHORECAST_DEV"),
		OIDC: OIDCConfig{
			Issuer:       os.Getenv("CHORECAST_OIDC_ISSUER"),
			ClientID:     os.Getenv("CHORECAST_OIDC_CLIENT_ID"),
			ClientSecret: os.Getenv("CHORECAST_OIDC_CLIENT_SECRET"),
		},
	}
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envBool(key string) bool {
	b, _ := strconv.ParseBool(os.Getenv(key))
	return b
}
