package gui

import (
	"embed"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
)

// webFS embeds the SPA and its assets at build time (F3: fully offline). The
// token gate wraps every lookup, static files included (DEC-014).
//
//go:embed web
var webFS embed.FS

// staticPrefix is the URL root of embedded assets.
const staticPrefix = "/static/"

// handleIndex serves the single-page app entry and plants the session cookie
// (BG-001 fix, 0号 ruling): browser sub-resource requests cannot replay the
// query token of the entry URL, so the validated first hit hands the same
// credential to the cookie jar for everything the page loads next. HttpOnly
// keeps the cookie away from scripts (the SPA keeps its own header-channel
// copy in sessionStorage), SameSite=Strict adds CSRF depth on top of the
// Origin check, and Path=/ scopes it to this service.
func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    s.token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	serveWebFile(w, r, "web/index.html")
}

// handleStatic serves embedded assets below /static/. The lookup name is
// cleaned against a synthetic root first, so no request byte can walk out of
// the embedded tree (path.Clean("/../x") is "/x").
func (s *server) handleStatic(w http.ResponseWriter, r *http.Request) {
	name := path.Clean("/" + strings.TrimPrefix(r.URL.Path, staticPrefix))
	if name == "/" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	serveWebFile(w, r, "web"+name)
}

// serveWebFile streams one embedded file and answers 404 with the shared JSON
// error body for absent names. ServeContent derives Content-Type from the
// file extension; the zero modtime keeps responses free of Last-Modified.
func serveWebFile(w http.ResponseWriter, r *http.Request, name string) {
	f, err := webFS.Open(name)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()

	seeker, ok := f.(io.ReadSeeker)
	if !ok {
		// Unreachable for embed.FS files, which are all seekable; kept as a
		// loud guard instead of a silent cast.
		writeError(w, http.StatusInternalServerError, "asset is not seekable")
		return
	}
	http.ServeContent(w, r, path.Base(name), time.Time{}, seeker)
}
