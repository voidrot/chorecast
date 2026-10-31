package auth

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"
)

// WebSessionTTL is how long a web admin session lasts.
const WebSessionTTL = 30 * 24 * time.Hour

// RequestInfo is what handlers need to know about the HTTP request behind an API call.
type RequestInfo struct {
	HTTPS     bool   // arrived over HTTPS, directly or via a TLS-terminating proxy
	IP        string // client IP, for rate limiting
	UserAgent string
}

type requestInfoKey struct{}

// CaptureRequestInfo is net/http middleware storing RequestInfo in the request context.
//
// Behind a reverse proxy the client IP comes from X-Real-IP or the first X-Forwarded-For entry.
// Those headers can be forged by clients that reach the server directly; that only lets them
// dodge the per-IP login limit, not the per-account ones. A forged X-Forwarded-Proto on plain
// HTTP only makes the browser drop the (then Secure) session cookie.
func CaptureRequestInfo(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := RequestInfo{
			HTTPS:     r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
			IP:        clientIP(r),
			UserAgent: r.UserAgent(),
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestInfoKey{}, info)))
	})
}

func clientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(first)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RequestInfoFrom returns the request's info (zero value outside CaptureRequestInfo).
func RequestInfoFrom(ctx context.Context) RequestInfo {
	info, _ := ctx.Value(requestInfoKey{}).(RequestInfo)
	return info
}

// SessionCookieFor returns the cookie carrying a new web session token. It is Secure when the
// request came over HTTPS; plain-HTTP LAN installs still work.
func SessionCookieFor(ctx context.Context, token string) http.Cookie {
	return http.Cookie{ //nolint:gosec // Secure is set per request (HTTPS or not); plain-HTTP LAN installs must work
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(WebSessionTTL / time.Second),
		HttpOnly: true,
		Secure:   RequestInfoFrom(ctx).HTTPS,
		SameSite: http.SameSiteLaxMode,
	}
}

// ClearSessionCookie returns a cookie that deletes the session cookie.
func ClearSessionCookie(ctx context.Context) http.Cookie {
	c := SessionCookieFor(ctx, "") //nolint:gosec // attributes come from SessionCookieFor
	c.MaxAge = -1
	return c
}
