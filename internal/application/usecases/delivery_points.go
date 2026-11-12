package usecases

import (
	"context"
	"strings"

	"github.com/claudioed/last-mile-delivery/internal/domain/deliverypoint"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

// packageStopPrefix marks the stop key of a package that has no label data: it
// is its own stop, has no address key and gets no profile (ADR 0014).
const packageStopPrefix = "pkg:"

// updateProfile folds a recorded attempt into the DeliveryPointProfile of its
// stop, inside the unit of work of RecordAttempt, and returns the events the
// profile raised. A package with no address key (no label data) has no profile
// and raises nothing. The profile never changes the attempt, the route or the
// history: it only learns from them.
func (w writer) updateProfile(ctx context.Context, r *route.Route, cmd RecordAttemptCommand) ([]shared.Event, error) {
	stopKey, _, err := w.stopInfo(ctx, cmd.TrackingNumber)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(stopKey, packageStopPrefix) {
		return nil, nil
	}
	p, err := w.DeliveryPoints.Get(ctx, stopKey)
	hit, err := found(err)
	if err != nil {
		return nil, err
	}
	if !hit {
		if p, err = deliverypoint.New(stopKey); err != nil {
			return nil, err
		}
	}
	events, err := p.Observe(deliverypoint.Observation{
		RouteID: string(r.ID()), TrackingNumber: cmd.TrackingNumber, Outcome: cmd.Outcome, Detail: cmd.Detail, At: cmd.At,
	}, w.now())
	if err != nil {
		return nil, err
	}
	if err := w.DeliveryPoints.Save(ctx, p); err != nil {
		return nil, err
	}
	return events, nil
}

// flagReasons lists the reason of every DeliveryPointFlagged among the events,
// for the metric that is recorded once the unit of work has committed.
func flagReasons(events []shared.Event) []string {
	var out []string
	for _, ev := range events {
		if f, ok := ev.(deliverypoint.DeliveryPointFlagged); ok {
			out = append(out, string(f.Reason))
		}
	}
	return out
}
