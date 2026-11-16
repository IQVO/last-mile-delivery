package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// serveAllow sends one request through h and returns the recorded response.
func serveAllow(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, http.NoBody))
	return rec
}

// TestWrongMethodCarriesAllowHeader pins RFC 9110 section 15.5.6: a 405 MUST
// list the methods the resource does support. chi sets Allow only in its
// DEFAULT 405 handler; once a custom handler is registered (the RFC 9457 one)
// it is called as-is, so the handler derives the list from the router itself.
func TestWrongMethodCarriesAllowHeader(t *testing.T) {
	metrics := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	routers := map[string]http.Handler{
		"api":         NewRouter(&Server{}, nil, ""),
		"api+metrics": NewRouter(&Server{Metrics: metrics}, nil, ""),
	}
	cases := []struct {
		router, method, path, wantAllow string
	}{
		{"api", http.MethodDelete, "/healthz", "GET"},
		{"api", http.MethodPost, "/readyz", "GET"},
		{"api", http.MethodDelete, "/waves", "GET, POST"},
		{"api", http.MethodPost, "/waves/w1/stops", "GET"},
		{"api", http.MethodGet, "/waves/w1/induction-closures", "POST"},
		{"api", http.MethodGet, "/waves/w1/dispatches", "POST"},
		{"api", http.MethodGet, "/waves/w1/cancellations", "POST"},
		{"api", http.MethodGet, "/waves/w1/cost-declarations", "POST"},
		{"api", http.MethodGet, "/waves/w1/emission-declarations", "POST"},
		{"api", http.MethodGet, "/routes", "POST"},
		{"api", http.MethodDelete, "/routes/r1", "GET"},
		{"api", http.MethodGet, "/routes/r1/starts", "POST"},
		{"api", http.MethodGet, "/routes/r1/attempts", "POST"},
		{"api", http.MethodGet, "/partners", "POST"},
		{"api", http.MethodGet, "/partners/d1/deactivations", "POST"},
		{"api+metrics", http.MethodDelete, "/metrics", "GET"},
	}
	for _, tc := range cases {
		t.Run(tc.router+" "+tc.method+" "+tc.path, func(t *testing.T) {
			rec := serveAllow(routers[tc.router], tc.method, tc.path)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Errorf("Content-Type = %q, want application/problem+json", got)
			}
			if got := rec.Header().Get("Allow"); got != tc.wantAllow {
				t.Errorf("Allow = %q, want %q", got, tc.wantAllow)
			}
		})
	}
}

// TestUnknownPathHasNoAllowHeader: a 404 is not a 405, so it lists no methods.
func TestUnknownPathHasNoAllowHeader(t *testing.T) {
	rec := serveAllow(NewRouter(&Server{}, nil, ""), http.MethodDelete, "/nowhere")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "" {
		t.Errorf("Allow = %q on an unknown path, want empty", got)
	}
}

// TestAllowedMethodsListsEveryRegisteredMethod covers a path with several
// methods, so the list is derived from the router and not hard-coded.
func TestAllowedMethodsListsEveryRegisteredMethod(t *testing.T) {
	r := chi.NewRouter()
	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	r.Get("/x", noop)
	r.Post("/x", noop)
	r.Delete("/x", noop)
	r.Get("/y", noop)
	if got, want := allowedMethods(r, "/x"), "GET, POST, DELETE"; got != want {
		t.Errorf("allowedMethods(/x) = %q, want %q", got, want)
	}
	if got, want := allowedMethods(r, "/y"), "GET"; got != want {
		t.Errorf("allowedMethods(/y) = %q, want %q", got, want)
	}
	if got := allowedMethods(r, "/nope"); got != "" {
		t.Errorf("allowedMethods(/nope) = %q, want empty", got)
	}
}
