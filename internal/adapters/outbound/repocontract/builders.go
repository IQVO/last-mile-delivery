package repocontract

import (
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/history"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// NewWave builds an OPEN wave of the station with the cutoff hours after Now.
func NewWave(t testing.TB, id wave.ID, station, date string, cutoffHours int) *wave.DeliveryWave {
	t.Helper()
	cutoff := Now.Add(time.Duration(cutoffHours) * time.Hour)
	w, _, err := wave.Schedule(wave.NewWave{
		ID: id, StationNodeID: station, ServiceDate: date, InductCutoffAt: cutoff,
		WindowStart: cutoff.Add(4 * time.Hour), WindowEnd: cutoff.Add(12 * time.Hour), StopCapacity: 5,
	}, Now)
	noErr(t, err)
	return w
}

// NewPartner builds an ACTIVE partner.
func NewPartner(t testing.TB, id partner.ID, station string) *partner.DeliveryServicePartner {
	t.Helper()
	p, err := partner.Contract(partner.NewPartner{ID: id, StationNodeID: station, VanCapacity: 3})
	noErr(t, err)
	return p
}

// NewHistory builds the history of a package that waits at the station.
func NewHistory(t testing.TB, tn, station string, arrivedAfter time.Duration) *history.History {
	t.Helper()
	h, _, err := history.Arrive(tn, station, "asg-"+tn, false, Now.Add(arrivedAfter), Now)
	noErr(t, err)
	return h
}

// StartedRoute builds a route of two stops (TRK-1 and TRK-2 at stop 1, TRK-3 at
// stop 2) planned in a fresh INDUCTION_CLOSED wave of node-ds-01 and started.
func StartedRoute(t testing.TB, id route.ID, waveID wave.ID) (*route.Route, *wave.DeliveryWave) {
	t.Helper()
	w := NewWave(t, waveID, "node-ds-01", "2026-10-24", 2)
	for _, in := range [][2]string{{"TRK-1", "k-a"}, {"TRK-2", "k-a"}, {"TRK-3", "k-b"}} {
		_, err := w.Induct(in[0], in[1], Now, Now)
		noErr(t, err)
	}
	noErr(t, w.CloseInduction())
	r, _, err := route.Plan(id, []route.Stop{{No: 1, TrackingNumbers: []string{"TRK-1", "TRK-2"}}, {No: 2, TrackingNumbers: []string{"TRK-3"}}},
		route.Planning{Wave: w, Partner: NewPartner(t, "dsp-1", "node-ds-01")}, Now)
	noErr(t, err)
	_, err = r.Start(Now)
	noErr(t, err)
	return r, w
}
