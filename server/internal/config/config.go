// Package config loads server configuration from CHORECAST_* environment variables.
package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Addr    string // listen address
	DataDir string // SQLite database and uploads
	Dev     bool   // relaxes cookie security, enables CORS for the Vite dev server
	// PublicURL is how browsers and apps reach the server, e.g. https://chores.example.com. Used in
	// links the server prints or shares (setup URL, invites, pairing). Optional.
	PublicURL string

	OIDC OIDCConfig

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

func Load() Config {
	fromEnv := map[string]bool{}
	for _, k := range []string{
		"CHORECAST_ADDR", "CHORECAST_DATA_DIR", "CHORECAST_DEV", "CHORECAST_PUBLIC_URL",
		"CHORECAST_OIDC_ISSUER", "CHORECAST_OIDC_CLIENT_ID", "CHORECAST_OIDC_CLIENT_SECRET",
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
