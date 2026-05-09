// Package server builds the orchestrator's chi router and shared
// middleware. Handler() returns an http.Handler ready for
// http.ListenAndServe.
//
// Per-route handlers live in internal/handlers (T3.2+); this package
// only assembles them and the cross-cutting middleware.
package server

import (
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path/filepath"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/flowcase/flowcase/internal/log"
)

// Options bundles everything the server needs at construction time.
// Pass concrete instances; the server doesn't own these.
type Options struct {
	// SessionMgr is the scs manager from T2.8. LoadAndSave wraps the
	// whole router.
	SessionMgr *scs.SessionManager

	// StaticDir is mounted at /static/. In production this is the
	// orchestrator's `static/` checkout.
	StaticDir string

	// TemplateDir holds the orchestrator's HTML templates. The 404
	// page is the only one this package owns; handlers in T3.2+ load
	// the rest.
	TemplateDir string

	// FaviconPath points at the favicon.ico on disk. In production
	// this is `nginx/favicon.ico` from the legacy tree.
	FaviconPath string
}

// Server is the assembled router. Construct via New.
type Server struct {
	opts    Options
	tmpl404 *template.Template
}

// New parses templates the server owns and returns a ready Server.
// Returns an error rather than panicking so the caller can log
// `cfg.TemplateDir` paths in context.
func New(opts Options) (*Server, error) {
	if opts.SessionMgr == nil {
		return nil, fmt.Errorf("server.New: SessionMgr is required")
	}

	s := &Server{opts: opts}

	tmplPath := filepath.Join(opts.TemplateDir, "404.html.tmpl")
	tmpl, err := template.ParseFiles(tmplPath)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", tmplPath, err)
	}
	s.tmpl404 = tmpl

	return s, nil
}

// Handler returns the assembled http.Handler. Mount-time only; safe
// to call once and reuse.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()

	// Cross-cutting middleware. Order matters: RealIP populates
	// RemoteAddr from headers before logging sees it; RequestID
	// stamps a header before logging or downstream code needs it;
	// the slog logger formats both.
	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(s.requestLogger)

	// Static assets — the file server's 404 path emits a plain text
	// "404 page not found" body, which we override below by routing
	// non-existent files through chi.NotFound.
	if s.opts.StaticDir != "" {
		fileServer(r, "/static/", http.Dir(s.opts.StaticDir))
	}

	if s.opts.FaviconPath != "" {
		r.Get("/favicon.ico", func(w http.ResponseWriter, req *http.Request) {
			http.ServeFile(w, req, s.opts.FaviconPath)
		})
	}

	r.NotFound(s.handle404)
	r.MethodNotAllowed(s.handle405)

	// scs LoadAndSave loads the session at the start of each request,
	// commits at the end. Wrapping the chi router (rather than per-
	// route) means every handler downstream can call PutUserID /
	// GetUserID without extra plumbing.
	return s.opts.SessionMgr.LoadAndSave(r)
}

// fileServer mounts a chi-style /static/* route that serves files
// from `root`. Unknown paths fall through to chi.NotFound rather than
// leaking the std lib's "404 page not found" body.
func fileServer(r chi.Router, prefix string, root http.FileSystem) {
	if prefix == "" || prefix[len(prefix)-1] != '/' {
		panic("fileServer: prefix must end with /")
	}
	pattern := prefix + "*"
	stripped := http.StripPrefix(prefix, http.FileServer(notFoundForwardingFS{root}))
	r.Handle(pattern, stripped)
}

// notFoundForwardingFS wraps an http.FileSystem so missing files
// surface as fs.ErrNotExist; the file server's default 404 body is
// replaced with chi's NotFound handler that calls into the templated
// 404 page.
type notFoundForwardingFS struct{ inner http.FileSystem }

func (f notFoundForwardingFS) Open(name string) (http.File, error) {
	file, err := f.inner.Open(name)
	if err != nil {
		// Wrapping with fs.ErrNotExist makes http.FileServer surface
		// the underlying error to the client — our chi-level 404 then
		// catches the request.
		return nil, fmt.Errorf("%w: %v", fs.ErrNotExist, err)
	}
	return file, nil
}

func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		log.Info(
			"%s %s -> %d (%s) reqid=%s",
			r.Method,
			r.URL.RequestURI(),
			ww.Status(),
			time.Since(start).Round(time.Microsecond),
			middleware.GetReqID(r.Context()),
		)
	})
}

func (s *Server) handle404(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	if err := s.tmpl404.Execute(w, nil); err != nil {
		log.Error("rendering 404 template: %s", err)
	}
}

func (s *Server) handle405(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
