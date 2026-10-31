package auth

import (
	"context"
	"net/http"
	"time"
)

// WebSessionTTL is how long a web admin session lasts.
const WebSessionTTL = 30 * 24 * time.Hour

type httpsKey struct{}

// DetectHTTPS is net/http middleware recording whether the request arrived over HTTPS, directly
// or via a TLS-terminating reverse proxy (X-Forwarded-Proto), so session cookies can be marked
// Secure. A spoofed header on plain HTTP only makes the browser drop the cookie.
func DetectHTTPS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		https := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), httpsKey{}, https)))
	})
}

// SessionCookieFor returns the cookie carrying a new web session token. It is Secure when the
// request came over HTTPS; plain-HTTP LAN installs still work.
func SessionCookieFor(ctx context.Context, token string) http.Cookie {
	https, _ := ctx.Value(httpsKey{}).(bool)
	return http.Cookie{ //nolint:gosec // Secure is set per request (HTTPS or not); plain-HTTP LAN installs must work
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(WebSessionTTL / time.Second),
		HttpOnly: true,
		Secure:   https,
		SameSite: http.SameSiteLaxMode,
	}
}
