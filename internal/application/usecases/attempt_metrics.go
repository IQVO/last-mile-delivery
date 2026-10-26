package usecases

import (
	"context"
	"errors"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

// rejectionReasons maps a RecordAttempt failure to the problem slug the REST
// adapter answers with (same vocabulary as apis/openapi.yaml), in match order.
var rejectionReasons = []struct {
	err    error
	reason string
}{
	{route.ErrPackageNotOnRoute, "package-not-on-route"},
	{route.ErrRouteNotStarted, "route-not-started"},
	{shared.ErrAlreadyDelivered, "already-delivered"},
	{route.ErrAttemptAlreadyRecorded, "attempt-already-recorded"},
	{shared.ErrIllegalTransition, "illegal-transition"},
	{ports.ErrConcurrentUpdate, "concurrent-update"},
	{ports.ErrNotFound, "not-found"},
	{shared.ErrValidation, "validation-failed"},
}

// internalErrorReason is the reason of every failure that is not a refusal the
// caller can act on (a database error, a bug).
const internalErrorReason = "internal-error"

// recordOutcome counts one attempt report: delivered or failed when err is nil,
// else rejected with the reason of err.
func (uc *RecordAttempt) recordOutcome(ctx context.Context, outcome route.Outcome, err error) {
	switch {
	case err != nil:
		uc.metrics().AttemptRejected(ctx, rejectionReason(err))
	case outcome == route.OutcomeDelivered:
		uc.metrics().AttemptDelivered(ctx)
	default:
		uc.metrics().AttemptFailed(ctx)
	}
}

func rejectionReason(err error) string {
	for _, r := range rejectionReasons {
		if errors.Is(err, r.err) {
			return r.reason
		}
	}
	return internalErrorReason
}
