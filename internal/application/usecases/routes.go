package usecases

import (
	"context"
	"fmt"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// PlanRouteCommand is the input of PlanRoute.
type PlanRouteCommand struct {
	WaveID wave.ID
	DSPID  partner.ID
	Stops  []route.Stop
}

// PlanRoute plans a route of a partner in a wave.
type PlanRoute struct{ writer }

// NewPlanRoute builds the use case.
func NewPlanRoute(d Deps) *PlanRoute { return &PlanRoute{writer{d}} }

// Execute loads the wave and the partner (read only), plans the route under the
// rules of ADR 0002 A12 and saves it with RoutePlanned.
func (uc *PlanRoute) Execute(ctx context.Context, cmd PlanRouteCommand) (views.Route, error) {
	return transact(ctx, uc.UoW, func(ctx context.Context) (views.Route, error) {
		wv, err := uc.Waves.Get(ctx, cmd.WaveID)
		if err != nil {
			return views.Route{}, err
		}
		p, err := uc.Partners.Get(ctx, cmd.DSPID)
		if err != nil {
			return views.Route{}, err
		}
		routed, err := uc.Routes.PackagesInWave(ctx, wv.ID())
		if err != nil {
			return views.Route{}, err
		}
		held := make(map[string]bool, len(routed))
		for _, tn := range routed {
			held[tn] = true
		}
		r, events, err := route.Plan(uc.IDs.NewRouteID(), cmd.Stops, route.Planning{Wave: wv, Partner: p, Routed: held}, uc.now())
		if err != nil {
			return views.Route{}, err
		}
		if err := uc.Routes.Save(ctx, r); err != nil {
			return views.Route{}, err
		}
		if err := uc.publish(ctx, events); err != nil {
			return views.Route{}, err
		}
		return views.RouteFrom(r), nil
	})
}

// StartRoute starts a PLANNED route whose wave is DISPATCHED.
type StartRoute struct{ writer }

// NewStartRoute builds the use case.
func NewStartRoute(d Deps) *StartRoute { return &StartRoute{writer{d}} }

// Execute starts the route and saves it with one PackageOutForDelivery per package (A14).
func (uc *StartRoute) Execute(ctx context.Context, id route.ID) (views.Route, error) {
	return transact(ctx, uc.UoW, func(ctx context.Context) (views.Route, error) {
		r, err := uc.Routes.Get(ctx, id)
		if err != nil {
			return views.Route{}, err
		}
		wv, err := uc.Waves.Get(ctx, r.WaveID())
		if err != nil {
			return views.Route{}, err
		}
		if wv.State() != wave.StateDispatched {
			return views.Route{}, fmt.Errorf("%w: StartRoute needs wave %s DISPATCHED but it is %s", shared.ErrIllegalTransition, wv.ID(), wv.State())
		}
		events, err := r.Start(uc.now())
		if err != nil {
			return views.Route{}, err
		}
		if err := uc.Routes.Save(ctx, r); err != nil {
			return views.Route{}, err
		}
		if err := uc.publish(ctx, events); err != nil {
			return views.Route{}, err
		}
		return views.RouteFrom(r), nil
	})
}

// RecordAttemptCommand is the input of RecordAttempt.
type RecordAttemptCommand struct {
	RouteID        route.ID
	TrackingNumber string
	Outcome        route.Outcome
	FailureReason  string
	At             time.Time
}

// RecordAttempt records a delivery attempt and runs the policies it triggers.
type RecordAttempt struct{ writer }

// NewRecordAttempt builds the use case.
func NewRecordAttempt(d Deps) *RecordAttempt { return &RecordAttempt{writer{d}} }

// Execute records the attempt on the route (DeliveryAttempted), updates the
// package history (delivered at most once across routes; failures counted), runs
// RetryNextWave on a failure (A18, A19), completes the route when its last
// package is recorded (A16) and the wave when its last route completes (A9).
func (uc *RecordAttempt) Execute(ctx context.Context, cmd RecordAttemptCommand) (views.Attempt, error) {
	return transact(ctx, uc.UoW, func(ctx context.Context) (views.Attempt, error) {
		r, err := uc.Routes.Get(ctx, cmd.RouteID)
		if err != nil {
			return views.Attempt{}, err
		}
		events, err := r.RecordAttempt(cmd.TrackingNumber, cmd.Outcome, cmd.FailureReason, cmd.At, uc.now())
		if err != nil {
			return views.Attempt{}, err
		}
		more, err := uc.updateHistory(ctx, r, cmd)
		if err != nil {
			return views.Attempt{}, err
		}
		if err := uc.Routes.Save(ctx, r); err != nil {
			return views.Attempt{}, err
		}
		if err := uc.publish(ctx, append(events, more...)); err != nil {
			return views.Attempt{}, err
		}
		if err := uc.completeRouteIfDone(ctx, r); err != nil {
			return views.Attempt{}, err
		}
		a, _ := r.AttemptOf(cmd.TrackingNumber)
		return views.AttemptFrom(r.ID(), a), nil
	})
}

// updateHistory applies the attempt to the package's history and, on a failure
// that does not end in a return, looks for the next wave.
func (w writer) updateHistory(ctx context.Context, r *route.Route, cmd RecordAttemptCommand) ([]shared.Event, error) {
	h, err := w.Histories.Get(ctx, cmd.TrackingNumber)
	if err != nil {
		return nil, err
	}
	if cmd.Outcome == route.OutcomeDelivered {
		if err := h.RecordDelivered(); err != nil {
			return nil, err
		}
		return nil, w.Histories.Save(ctx, h)
	}
	var waveEvents []shared.Event
	placed := false
	if !h.WouldReturn() {
		stopKey, _, err := w.stopInfo(ctx, cmd.TrackingNumber)
		if err != nil {
			return nil, err
		}
		id, evs, err := w.place(ctx, r.StationNodeID(), cmd.TrackingNumber, stopKey, cmd.At)
		if err != nil {
			return nil, err
		}
		placed, waveEvents = id != "", evs
	}
	hev, err := h.RecordFailure(placed, w.now())
	if err != nil {
		return nil, err
	}
	return append(waveEvents, hev...), w.Histories.Save(ctx, h)
}

// completeRouteIfDone completes the route once every package has an attempt and
// then the wave once its last route completed.
func (w writer) completeRouteIfDone(ctx context.Context, r *route.Route) error {
	if !r.AllAttempted() {
		return nil
	}
	if err := r.Complete(); err != nil {
		return err
	}
	if err := w.Routes.Save(ctx, r); err != nil {
		return err
	}
	_, err := w.completeWaveIfDone(ctx, r.WaveID())
	return err
}
