package reportshttp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestWrongMethodCarriesAllowHeader pins RFC 9110 section 15.5.6: a 405 MUST
// list the methods the resource does support. chi sets Allow only in its
// DEFAULT 405 handler; once a custom handler is registered (the RFC 9457 one)
// it is called as-is, so the handler derives the list from the router itself.
func TestWrongMethodCarriesAllowHeader(t *testing.T) {
	router := NewRouter(&Server{}, nil, "")
	cases := []struct {
		method, path string
	}{
		{http.MethodDelete, "/healthz"},
		{http.MethodPost, "/readyz"},
		{http.MethodPost, "/reports/route-efficiency"},
		{http.MethodPut, "/reports/attempt-outcomes"},
		{http.MethodDelete, "/reports/wave-utilisation"},
		{http.MethodPatch, "/reports/eta-window"},
		{http.MethodPost, "/reports/freshness"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, http.NoBody))
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Errorf("Content-Type = %q, want application/problem+json", got)
			}
			if got := rec.Header().Get("Allow"); got != "GET" {
				t.Errorf("Allow = %q, want GET", got)
			}
		})
	}
}

// TestUnknownPathHasNoAllowHeader: a 404 is not a 405, so it lists no methods.
func TestUnknownPathHasNoAllowHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	NewRouter(&Server{}, nil, "").ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/nowhere", http.NoBody))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "" {
		t.Errorf("Allow = %q on an unknown path, want empty", got)
	}
}

// TestAllowedMethodsListsEveryRegisteredMethod covers a path with several
// methods, so the list is derived from the router and not hard-coded to GET.
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
