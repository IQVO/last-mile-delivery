package http

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

func validationError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", shared.ErrValidation, fmt.Sprintf(format, args...))
}

func (s *Server) handleScheduleWave(w http.ResponseWriter, r *http.Request) {
	var req scheduleWaveRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if _, err := time.Parse(time.DateOnly, req.ServiceDate); err != nil {
		writeError(w, r, validationError("serviceDate must be a date (YYYY-MM-DD)"))
		return
	}
	v, err := s.ScheduleWave.Execute(r.Context(), usecases.ScheduleWaveCommand{
		StationNodeID: req.StationNodeID, ServiceDate: req.ServiceDate, InductCutoffAt: req.InductCutoffAt,
		WindowStart: req.WindowStart, WindowEnd: req.WindowEnd, StopCapacity: req.StopCapacity,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toWaveBody(v))
}

func (s *Server) handleCloseInduction(w http.ResponseWriter, r *http.Request) {
	v, err := s.CloseInduction.Execute(r.Context(), wave.ID(chi.URLParam(r, "waveId")))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toWaveBody(v))
}

func (s *Server) handleDispatchWave(w http.ResponseWriter, r *http.Request) {
	v, err := s.DispatchWave.Execute(r.Context(), wave.ID(chi.URLParam(r, "waveId")))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toWaveBody(v))
}

func (s *Server) handleCancelWave(w http.ResponseWriter, r *http.Request) {
	var req cancelWaveRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.CancelWave.Execute(r.Context(), usecases.CancelWaveCommand{WaveID: wave.ID(chi.URLParam(r, "waveId")), Reason: req.Reason})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toWaveBody(v))
}

func (s *Server) handlePlanRoute(w http.ResponseWriter, r *http.Request) {
	var req planRouteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	stops := make([]route.Stop, 0, len(req.Stops))
	for _, st := range req.Stops {
		stops = append(stops, route.Stop{No: st.StopNo, TrackingNumbers: st.TrackingNumbers})
	}
	v, err := s.PlanRoute.Execute(r.Context(), usecases.PlanRouteCommand{WaveID: wave.ID(req.WaveID), DSPID: partner.ID(req.DSPID), Stops: stops})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toRouteBody(v))
}

func (s *Server) handleGetRoute(w http.ResponseWriter, r *http.Request) {
	v, err := s.GetRoute.Execute(r.Context(), route.ID(chi.URLParam(r, "routeId")))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toRouteBody(v))
}

func (s *Server) handleStartRoute(w http.ResponseWriter, r *http.Request) {
	v, err := s.StartRoute.Execute(r.Context(), route.ID(chi.URLParam(r, "routeId")))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toRouteBody(v))
}

func (s *Server) handleRecordAttempt(w http.ResponseWriter, r *http.Request) {
	var req recordAttemptRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	outcome := route.Outcome(req.Outcome)
	if outcome != route.OutcomeDelivered && outcome != route.OutcomeFailed {
		writeError(w, r, validationError("outcome must be DELIVERED or FAILED"))
		return
	}
	v, err := s.RecordAttempt.Execute(r.Context(), usecases.RecordAttemptCommand{
		RouteID: route.ID(chi.URLParam(r, "routeId")), TrackingNumber: req.TrackingNumber, Outcome: outcome, FailureReason: req.FailureReason, At: req.At,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toAttemptBody(v))
}

func (s *Server) handleContractPartner(w http.ResponseWriter, r *http.Request) {
	var req contractPartnerRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.ContractPartner.Execute(r.Context(), usecases.ContractPartnerCommand{StationNodeID: req.StationNodeID, VanCapacity: req.VanCapacity})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toPartnerBody(v))
}

func (s *Server) handleDeactivatePartner(w http.ResponseWriter, r *http.Request) {
	v, err := s.DeactivatePartner.Execute(r.Context(), partner.ID(chi.URLParam(r, "dspId")))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toPartnerBody(v))
}
