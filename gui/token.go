package gui

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"strconv"
)

// loopbackHost is the only host the service binds and answers on (RD-004-②:
// hard-coded, no configuration surface to weaken it).
const loopbackHost = "127.0.0.1"

// localhostHost is the alias accepted beside the loopback literal when a
// client addresses the service by name (DEC-303 Host pinning).
const localhostHost = "localhost"

// tokenEntropy is the token size in bytes: 16 bytes = 128 bit (DEC-302).
const tokenEntropy = 16

// Header and query names of the two token channels (DEC-302), plus the
// session cookie the index page plants for browser sub-resource requests
// (BG-001: those requests cannot replay the entry URL's query token).
const (
	tokenHeader       = "X-SCCV-Token"
	tokenQuery        = "token"
	sessionCookieName = "sccv-token"
)

// Origin/referrer defense headers and values (DEC-303).
const (
	originHeader         = "Origin"
	referrerPolicyHeader = "Referrer-Policy"
	referrerPolicyValue  = "no-referrer"
)

// newToken mints the per-process access token: 128 bits from crypto/rand,
// hex-encoded so it travels safely in URLs and headers (DEC-302). Failure is
// loud — without a token the service must refuse to start, never fall back to
// a guessable credential.
func newToken() (string, error) {
	buf := make([]byte, tokenEntropy)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// authorize reports whether the request carries the expected token through
// any channel: X-SCCV-Token header, query string, or the sccv-token cookie
// the index page sets after validating the entry URL (BG-001). A request with
// none of the three fails exactly as before, keeping the frozen TC-018
// semantics: no credential means 401 unauthorized. The digest comparison is
// constant time (DEC-302) so response timing cannot narrow the token, and
// running it once over whichever credential the request carries keeps the
// timing shape identical for all channels.
func authorize(r *http.Request, expected string) bool {
	supplied := credentialFrom(r)
	return subtle.ConstantTimeCompare([]byte(supplied), []byte(expected)) == 1
}

// credentialFrom collects the request credential: header first, then query
// string, then the session cookie. Empty string when the request carries
// none, which can never match the expected token.
func credentialFrom(r *http.Request) string {
	supplied := r.Header.Get(tokenHeader)
	if supplied == "" {
		supplied = r.URL.Query().Get(tokenQuery)
	}
	if supplied == "" {
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			supplied = cookie.Value
		}
	}
	return supplied
}

// gate wraps the whole route table with the defense stack of DEC-303: every
// response carries Referrer-Policy: no-referrer, the Host must name this
// service, state-changing requests must not carry a foreign Origin, and the
// token check runs last so a bad Host or Origin learns nothing about the
// token before being rejected.
func (s *server) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(referrerPolicyHeader, referrerPolicyValue)
		if !s.hostAllowed(r) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		if !s.originAllowed(r) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		if !authorize(r, s.token) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostAllowed accepts only the two loopback spellings of this service with
// its own port (DEC-303 Host pinning against DNS rebinding).
func (s *server) hostAllowed(r *http.Request) bool {
	ownPort := strconv.Itoa(s.port)
	return r.Host == net.JoinHostPort(loopbackHost, ownPort) ||
		r.Host == net.JoinHostPort(localhostHost, ownPort)
}

// originAllowed blocks browser-initiated state changes from anywhere but the
// service itself (DEC-303 CSRF depth). A missing Origin header marks a
// non-browser client (curl, tests) and passes; only the exact loopback origin
// of this instance is honored otherwise.
func (s *server) originAllowed(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return true
	}
	origin := r.Header.Get(originHeader)
	if origin == "" {
		return true
	}
	ownOrigin := "http://" + net.JoinHostPort(loopbackHost, strconv.Itoa(s.port))
	return origin == ownOrigin
}
