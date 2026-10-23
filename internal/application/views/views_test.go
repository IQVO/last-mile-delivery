package views

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/history"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

var (
	t0 = time.Date(2026, 10, 24, 6, 0, 0, 0, time.UTC)
	t1 = t0.Add(2 * time.Hour)
)

func TestWaveFromCopiesEveryField(t *testing.T) {
	w, _, err := wave.Schedule(wave.NewWave{
		ID: "wave-1", StationNodeID: "node-ds-01", ServiceDate: "2026-10-24", InductCutoffAt: t1,
		WindowStart: t1.Add(time.Hour), WindowEnd: t1.Add(2 * time.Hour), StopCapacity: 3,
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Induct("TRK-1", "k-a", t0, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Cancel("closed", t0); err != nil {
		t.Fatal(err)
	}
	want := Wave{
		WaveID: "wave-1", StationNodeID: "node-ds-01", ServiceDate: "2026-10-24", InductCutoffAt: t1, WindowStart: t1.Add(time.Hour),
		WindowEnd: t1.Add(2 * time.Hour), StopCapacity: 3, AssignedStops: 1, RemainingStops: 2, State: "CANCELLED", CancelReason: "closed",
	}
	if got := WaveFrom(w); got != want {
		t.Fatalf("WaveFrom = %+v, want %+v", got, want)
	}
}

func TestRouteFromBuildsStopsAndAttempts(t *testing.T) {
	r, err := route.Restore(route.Snapshot{
		ID: "route-1", WaveID: "wave-1", DSPID: "dsp-1", StationNodeID: "node-ds-01", State: route.StateOutForDelivery, Version: 3,
		Stops:    []route.Stop{{No: 1, TrackingNumbers: []string{"TRK-1"}}, {No: 2, TrackingNumbers: []string{"TRK-2"}}},
		Attempts: []route.Attempt{{TrackingNumber: "TRK-1", Outcome: route.OutcomeFailed, FailureReason: "absent", At: t1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := Route{
		RouteID: "route-1", WaveID: "wave-1", DSPID: "dsp-1", StationNodeID: "node-ds-01", State: "OUT_FOR_DELIVERY", StopCount: 2,
		Stops: []Stop{
			{StopNo: 1, TrackingNumbers: []string{"TRK-1"}, Outcome: "FAILED"},
			{StopNo: 2, TrackingNumbers: []string{"TRK-2"}},
		},
		Attempts: []Attempt{{RouteID: "route-1", TrackingNumber: "TRK-1", Outcome: "FAILED", FailureReason: "absent", At: t1}},
	}
	if got := RouteFrom(r); !reflect.DeepEqual(got, want) {
		t.Fatalf("RouteFrom = %+v, want %+v", got, want)
	}
}

func TestPartnerAndHistoryViews(t *testing.T) {
	p, err := partner.Contract(partner.NewPartner{ID: "dsp-1", StationNodeID: "node-ds-01", VanCapacity: 7})
	if err != nil {
		t.Fatal(err)
	}
	if got := PartnerFrom(p); got != (Partner{DSPID: "dsp-1", StationNodeID: "node-ds-01", VanCapacity: 7, State: "ACTIVE"}) {
		t.Fatalf("PartnerFrom = %+v", got)
	}
	h, _, err := history.Arrive("TRK-1", "node-ds-01", "", true, t0, t0)
	if err != nil {
		t.Fatal(err)
	}
	if got := PackageHistoryFrom(h); got != (PackageHistory{TrackingNumber: "TRK-1", Status: "INDUCTED"}) {
		t.Fatalf("PackageHistoryFrom = %+v", got)
	}
}
