package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/repository"
	"github.com/claudioed/last-mile-delivery/internal/domain/history"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// SweepLimit bounds how many waiting packages one ScheduleWave inducts (ADR 0002, A19).
const SweepLimit = 1000

// stopInfo returns the stop key and assignment id of a package from the
// PackageStopView; a package with no label data is its own stop (A4).
func (w writer) stopInfo(ctx context.Context, trackingNumber string) (stopKey, assignmentID string, err error) {
	ps, err := w.PackageStops.Get(ctx, trackingNumber)
	ok, err := found(err)
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "pkg:" + trackingNumber, "", nil
	}
	return ps.StopKey, ps.AssignmentID, nil
}

// place tries the eligible waves of the station in order (A6): the OPEN waves
// whose cutoff is after at, earliest first. The first wave that accepts the
// package is saved and its id returned with the raised events; no wave is the
// empty id.
func (w writer) place(ctx context.Context, station, trackingNumber, stopKey string, at time.Time) (wave.ID, []shared.Event, error) {
	waves, err := w.Waves.List(ctx, repository.WaveFilter{StationNodeID: station, State: wave.StateOpen, InductCutoffAfter: at})
	if err != nil {
		return "", nil, err
	}
	now := w.now()
	for _, wv := range waves {
		events, err := wv.Induct(trackingNumber, stopKey, at, now)
		if errors.Is(err, wave.ErrWaveFull) || errors.Is(err, wave.ErrInductionClosed) {
			continue
		}
		if errors.Is(err, wave.ErrAlreadyInducted) {
			return wv.ID(), nil, nil
		}
		if err != nil {
			return "", nil, err
		}
		if err := w.Waves.Save(ctx, wv); err != nil {
			return "", nil, err
		}
		return wv.ID(), events, nil
	}
	return "", nil, nil
}

// arrive handles a package that reached the station (policy InductOnArrival): a
// new package is inducted or deferred; a waiting one is retried; a package
// already inducted, delivered or returned is ignored (A18).
func (w writer) arrive(ctx context.Context, station, trackingNumber string, at time.Time) ([]shared.Event, error) {
	h, err := w.Histories.Get(ctx, trackingNumber)
	hit, err := found(err)
	if err != nil {
		return nil, err
	}
	if !hit {
		return w.arriveNew(ctx, station, trackingNumber, at)
	}
	if h.Status() != history.StatusAtStation {
		return nil, nil
	}
	return w.retryWaiting(ctx, h, at)
}

func (w writer) arriveNew(ctx context.Context, station, trackingNumber string, at time.Time) ([]shared.Event, error) {
	stopKey, assignmentID, err := w.stopInfo(ctx, trackingNumber)
	if err != nil {
		return nil, err
	}
	id, events, err := w.place(ctx, station, trackingNumber, stopKey, at)
	if err != nil {
		return nil, err
	}
	h, hev, err := history.Arrive(trackingNumber, station, assignmentID, id != "", at, w.now())
	if err != nil {
		return nil, err
	}
	if err := w.Histories.Save(ctx, h); err != nil {
		return nil, err
	}
	return append(events, hev...), nil
}

// retryWaiting inducts a waiting package when a wave takes it now; nothing is
// raised when none does (the package was already announced as deferred).
func (w writer) retryWaiting(ctx context.Context, h *history.History, at time.Time) ([]shared.Event, error) {
	stopKey, _, err := w.stopInfo(ctx, h.TrackingNumber())
	if err != nil {
		return nil, err
	}
	id, events, err := w.place(ctx, h.StationNodeID(), h.TrackingNumber(), stopKey, at)
	if err != nil || id == "" {
		return nil, err
	}
	if err := h.Place(); err != nil {
		return nil, err
	}
	if err := w.Histories.Save(ctx, h); err != nil {
		return nil, err
	}
	return events, nil
}

// sweepWaiting inducts the packages that wait at the station (policy
// InductDeferredOnSchedule), oldest first, at most SweepLimit.
func (w writer) sweepWaiting(ctx context.Context, station string) ([]shared.Event, error) {
	waiting, err := w.Histories.ListWaiting(ctx, station, SweepLimit)
	if err != nil {
		return nil, err
	}
	var events []shared.Event
	for _, h := range waiting {
		ev, err := w.retryWaiting(ctx, h, w.now())
		if err != nil {
			return nil, err
		}
		events = append(events, ev...)
	}
	return events, nil
}

// reinduct moves an inducted package whose wave can not run it (cancelled, or a
// dispatch leftover) onto the next eligible wave; with none it is unplaced and
// PackageInductionDeferred is raised (A10, A11, A19).
func (w writer) reinduct(ctx context.Context, station, trackingNumber, stopKey string) ([]shared.Event, error) {
	h, err := w.Histories.Get(ctx, trackingNumber)
	if err != nil {
		return nil, err
	}
	id, events, err := w.place(ctx, station, trackingNumber, stopKey, w.now())
	if err != nil || id != "" {
		return events, err
	}
	hev, err := h.Unplace(w.now())
	if err != nil {
		return nil, err
	}
	if err := w.Histories.Save(ctx, h); err != nil {
		return nil, err
	}
	return hev, nil
}
