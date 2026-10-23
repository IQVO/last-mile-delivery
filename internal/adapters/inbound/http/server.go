// Package http is the REST adapter (apis/openapi.yaml): chi routes, RFC 9457
// problem responses, the Idempotency-Key middleware and the liveness and
// readiness probes. It imports use cases and views, never an aggregate's
// internals beyond the typed ids and errors it maps.
package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
)

// Server holds the use cases the handlers call.
type Server struct {
	ScheduleWave      *usecases.ScheduleWave
	CloseInduction    *usecases.CloseInduction
	DispatchWave      *usecases.DispatchWave
	CancelWave        *usecases.CancelWave
	ListWaves         *usecases.ListWaves
	GetWaveStops      *usecases.GetWaveStops
	PlanRoute         *usecases.PlanRoute
	StartRoute        *usecases.StartRoute
	RecordAttempt     *usecases.RecordAttempt
	GetRoute          *usecases.GetRoute
	ContractPartner   *usecases.ContractPartner
	DeactivatePartner *usecases.DeactivatePartner

	// Idempotency wraps every state changing POST; nil disables it (tests).
	Idempotency func(http.Handler) http.Handler
	// Readiness gates /readyz; nil is always ready.
	Readiness *Readiness
	// Metrics, when set, is served at /metrics.
	Metrics http.Handler
}

// NewRouter wires the routes of apis/openapi.yaml plus /healthz, /readyz and,
// when configured, /metrics.
func NewRouter(s *Server) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(rejectNUL)

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", s.handleReadyz)
	if s.Metrics != nil {
		r.Method(http.MethodGet, "/metrics", s.Metrics)
	}

	r.Get("/waves", s.handleListWaves)
	r.Get("/waves/{waveId}/stops", s.handleGetWaveStops)
	r.Get("/routes/{routeId}", s.handleGetRoute)

	r.Group(func(r chi.Router) {
		if s.Idempotency != nil {
			r.Use(s.Idempotency)
		}
		r.Post("/waves", s.handleScheduleWave)
		r.Post("/waves/{waveId}:close-induction", s.handleCloseInduction)
		r.Post("/waves/{waveId}:dispatch", s.handleDispatchWave)
		r.Post("/waves/{waveId}:cancel", s.handleCancelWave)
		r.Post("/routes", s.handlePlanRoute)
		r.Post("/routes/{routeId}:start", s.handleStartRoute)
		r.Post("/routes/{routeId}/attempts", s.handleRecordAttempt)
		r.Post("/partners", s.handleContractPartner)
		r.Post("/partners/{dspId}:deactivate", s.handleDeactivatePartner)
	})
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, notFoundResource, "no resource matches the requested path")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, problem{http.StatusMethodNotAllowed, "method-not-allowed", "Method not allowed"}, "the method is not allowed on this resource")
	})
	return r
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
}
