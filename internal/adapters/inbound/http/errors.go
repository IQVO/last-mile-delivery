package http

import (
	"errors"
	"net/http"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// errMalformedRequest marks a body that is not valid JSON for the operation.
var errMalformedRequest = errors.New("request body is not valid JSON for this operation")

// problem is one RFC 9457 error type. The `type` of the body is the slug: the
// shared slugs not-found, illegal-transition, concurrent-update and
// validation-failed, plus one slug per model Err name (kebab-case, no Err
// prefix), as apis/openapi.yaml documents.
type problem struct {
	status int
	slug   string
	title  string
}

var internalProblem = problem{http.StatusInternalServerError, "internal-error", "Internal server error"}

var (
	idempotencyKeyRequired = problem{http.StatusBadRequest, "idempotency-key-required", "Idempotency-Key header is required"}
	idempotencyKeyInvalid  = problem{http.StatusBadRequest, "idempotency-key-invalid", "Idempotency-Key header is invalid"}
	idempotencyKeyReused   = problem{http.StatusConflict, "idempotency-key-reused", "Idempotency-Key was already used with a different request"}
	invalidLimit           = problem{http.StatusBadRequest, "invalid-limit", "The limit is invalid"}
	invalidCursor          = problem{http.StatusBadRequest, "invalid-cursor", "The cursor is invalid"}
	unknownQueryParameter  = problem{http.StatusBadRequest, "unknown-query-parameter", "The query parameter is not declared by this collection"}
	validationFailed       = problem{http.StatusUnprocessableEntity, "validation-failed", "Validation failed"}
)

// problemCatalogue maps a domain or application error to its problem. The
// first match wins, so the specific errors come before the shared ones.
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{errMalformedRequest, validationFailed},
	{wave.ErrInvalidWaveTimes, problem{http.StatusUnprocessableEntity, "invalid-wave-times", "The wave times must run cutoff, window start, window end"}},
	{partner.ErrInvalidVanCapacity, problem{http.StatusUnprocessableEntity, "invalid-van-capacity", "The van capacity must be greater than zero"}},
	{usecases.ErrUnknownStation, problem{http.StatusUnprocessableEntity, "unknown-station", "The station is not a known delivery station"}},
	{route.ErrPackageNotOnRoute, problem{http.StatusNotFound, "package-not-on-route", "The package is not on the route"}},
	{route.ErrRouteOverCapacity, problem{http.StatusConflict, "route-over-capacity", "The route has more stops than the van holds"}},
	{route.ErrRouteNotStarted, problem{http.StatusConflict, "route-not-started", "The route is not out for delivery"}},
	{shared.ErrAlreadyDelivered, problem{http.StatusConflict, "already-delivered", "The package was already delivered"}},
	{route.ErrAttemptAlreadyRecorded, problem{http.StatusConflict, "attempt-already-recorded", "An attempt of this package is already recorded on the route"}},
	{route.ErrPackageNotInducted, problem{http.StatusConflict, "package-not-inducted", "The package is not inducted in the wave"}},
	{route.ErrPackageAlreadyRouted, problem{http.StatusConflict, "package-already-routed", "The package is already on a route"}},
	{route.ErrStopKeyMismatch, problem{http.StatusConflict, "stop-key-mismatch", "The packages of one stop must share one stop key"}},
	{route.ErrPartnerInactive, problem{http.StatusConflict, "partner-inactive", "The partner is inactive"}},
	{route.ErrPartnerStationMismatch, problem{http.StatusConflict, "partner-station-mismatch", "The partner belongs to another station"}},
	{route.ErrRouteIncomplete, problem{http.StatusConflict, "route-incomplete", "The route still has packages without an attempt"}},
	{wave.ErrWaveFull, problem{http.StatusConflict, "wave-full", "The wave has no stop capacity left"}},
	{wave.ErrInductionClosed, problem{http.StatusConflict, "induction-closed", "Induction of the wave is closed"}},
	{wave.ErrAlreadyInducted, problem{http.StatusConflict, "already-inducted", "The package is already inducted in the wave"}},
	{shared.ErrIllegalTransition, problem{http.StatusConflict, "illegal-transition", "The state change is not allowed from the current state"}},
	{ports.ErrConcurrentUpdate, problem{http.StatusConflict, "concurrent-update", "The resource was modified by another request; re-fetch it and retry"}},
	{ports.ErrNotFound, problem{http.StatusNotFound, "not-found", "Resource not found"}},
	{shared.ErrValidation, validationFailed},
}

func problemFor(err error) problem {
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry.p
		}
	}
	return internalProblem
}
