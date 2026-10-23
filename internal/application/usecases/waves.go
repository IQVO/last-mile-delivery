package usecases

import (
	"context"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// ScheduleWaveCommand is the input of ScheduleWave.
type ScheduleWaveCommand struct {
	StationNodeID  string
	ServiceDate    string
	InductCutoffAt time.Time
	WindowStart    time.Time
	WindowEnd      time.Time
	StopCapacity   int
}

// ScheduleWave schedules an OPEN wave and inducts the packages that wait at the
// station into the waves that now take them.
type ScheduleWave struct{ writer }

// NewScheduleWave builds the use case.
func NewScheduleWave(d Deps) *ScheduleWave { return &ScheduleWave{writer{d}} }

// Execute checks the station (ErrUnknownStation), schedules the wave, saves it
// with DeliveryWaveScheduled and runs the InductDeferredOnSchedule policy.
func (uc *ScheduleWave) Execute(ctx context.Context, cmd ScheduleWaveCommand) (views.Wave, error) {
	return transact(ctx, uc.UoW, func(ctx context.Context) (views.Wave, error) {
		if err := uc.requireStation(ctx, cmd.StationNodeID); err != nil {
			return views.Wave{}, err
		}
		w, events, err := wave.Schedule(wave.NewWave{
			ID: uc.IDs.NewWaveID(), StationNodeID: cmd.StationNodeID, ServiceDate: cmd.ServiceDate,
			InductCutoffAt: cmd.InductCutoffAt, WindowStart: cmd.WindowStart, WindowEnd: cmd.WindowEnd, StopCapacity: cmd.StopCapacity,
		}, uc.now())
		if err != nil {
			return views.Wave{}, err
		}
		if err := uc.Waves.Save(ctx, w); err != nil {
			return views.Wave{}, err
		}
		swept, err := uc.sweepWaiting(ctx, cmd.StationNodeID)
		if err != nil {
			return views.Wave{}, err
		}
		if err := uc.publish(ctx, append(events, swept...)); err != nil {
			return views.Wave{}, err
		}
		// The sweep may have inducted into the new wave: return it as saved.
		saved, err := uc.Waves.Get(ctx, w.ID())
		if err != nil {
			return views.Wave{}, err
		}
		return views.WaveFrom(saved), nil
	})
}

// mutate loads a wave, applies one command and saves it with its events.
func (w writer) mutateWave(ctx context.Context, id wave.ID, apply func(*wave.DeliveryWave) ([]shared.Event, error)) (*wave.DeliveryWave, error) {
	wv, err := w.Waves.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	events, err := apply(wv)
	if err != nil {
		return nil, err
	}
	if err := w.Waves.Save(ctx, wv); err != nil {
		return nil, err
	}
	return wv, w.publish(ctx, events)
}

// CloseInduction closes induction of an OPEN wave.
type CloseInduction struct{ writer }

// NewCloseInduction builds the use case.
func NewCloseInduction(d Deps) *CloseInduction { return &CloseInduction{writer{d}} }

// Execute moves the wave OPEN to INDUCTION_CLOSED.
func (uc *CloseInduction) Execute(ctx context.Context, id wave.ID) (views.Wave, error) {
	return transact(ctx, uc.UoW, func(ctx context.Context) (views.Wave, error) {
		wv, err := uc.mutateWave(ctx, id, func(w *wave.DeliveryWave) ([]shared.Event, error) { return nil, w.CloseInduction() })
		if err != nil {
			return views.Wave{}, err
		}
		return views.WaveFrom(wv), nil
	})
}

// DispatchWave dispatches an INDUCTION_CLOSED wave.
type DispatchWave struct{ writer }

// NewDispatchWave builds the use case.
func NewDispatchWave(d Deps) *DispatchWave { return &DispatchWave{writer{d}} }

// Execute dispatches the wave, re-inducts the packages no route holds (A11) and
// completes the wave when it has no route left to run (A9).
func (uc *DispatchWave) Execute(ctx context.Context, id wave.ID) (views.Wave, error) {
	return transact(ctx, uc.UoW, func(ctx context.Context) (views.Wave, error) {
		wv, err := uc.mutateWave(ctx, id, func(w *wave.DeliveryWave) ([]shared.Event, error) { return nil, w.Dispatch() })
		if err != nil {
			return views.Wave{}, err
		}
		if err := uc.reinductLeftovers(ctx, wv); err != nil {
			return views.Wave{}, err
		}
		done, err := uc.completeWaveIfDone(ctx, wv.ID())
		if err != nil {
			return views.Wave{}, err
		}
		return views.WaveFrom(done), nil
	})
}

// reinductLeftovers moves the packages inducted in the wave that no route of it
// holds onto the next wave.
func (w writer) reinductLeftovers(ctx context.Context, wv *wave.DeliveryWave) error {
	routed, err := w.Routes.PackagesInWave(ctx, wv.ID())
	if err != nil {
		return err
	}
	held := make(map[string]bool, len(routed))
	for _, tn := range routed {
		held[tn] = true
	}
	for _, in := range wv.Inductions() {
		if held[in.TrackingNumber] {
			continue
		}
		events, err := w.reinduct(ctx, wv.StationNodeID(), in.TrackingNumber, in.StopKey)
		if err != nil {
			return err
		}
		if err := w.publish(ctx, events); err != nil {
			return err
		}
	}
	return nil
}

// completeWaveIfDone completes a DISPATCHED wave when every route of it is
// COMPLETED (policy CompleteWaveOnLastRoute, A9); otherwise it returns the wave
// as stored.
func (w writer) completeWaveIfDone(ctx context.Context, id wave.ID) (*wave.DeliveryWave, error) {
	wv, err := w.Waves.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	open, err := w.Routes.CountUncompleted(ctx, id)
	if err != nil || open > 0 || wv.State() != wave.StateDispatched {
		return wv, err
	}
	if err := wv.Complete(); err != nil {
		return nil, err
	}
	return wv, w.Waves.Save(ctx, wv)
}

// CancelWaveCommand is the input of CancelWave.
type CancelWaveCommand struct {
	WaveID wave.ID
	Reason string
}

// CancelWave cancels an OPEN wave and re-inducts its packages.
type CancelWave struct{ writer }

// NewCancelWave builds the use case.
func NewCancelWave(d Deps) *CancelWave { return &CancelWave{writer{d}} }

// Execute cancels the wave (DeliveryWaveCancelled) and moves every package it
// holds onto the next eligible wave (G17); a package no wave takes is deferred.
func (uc *CancelWave) Execute(ctx context.Context, cmd CancelWaveCommand) (views.Wave, error) {
	return transact(ctx, uc.UoW, func(ctx context.Context) (views.Wave, error) {
		wv, err := uc.mutateWave(ctx, cmd.WaveID, func(w *wave.DeliveryWave) ([]shared.Event, error) {
			return w.Cancel(cmd.Reason, uc.now())
		})
		if err != nil {
			return views.Wave{}, err
		}
		for _, in := range wv.Inductions() {
			events, err := uc.reinduct(ctx, wv.StationNodeID(), in.TrackingNumber, in.StopKey)
			if err != nil {
				return views.Wave{}, err
			}
			if err := uc.publish(ctx, events); err != nil {
				return views.Wave{}, err
			}
		}
		return views.WaveFrom(wv), nil
	})
}
