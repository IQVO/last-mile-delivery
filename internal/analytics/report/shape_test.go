package report_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/analytics/report"
)

func ptr(v float64) *float64 { return &v }

func TestRouteEfficiencyRowsDerivesTheKPIs(t *testing.T) {
	day := utcDay(2026, 11, 3)
	in := report.RouteDay{Day: day, Station: "st1", Routes: 2, Stops: 5, Packages: 8, CostedStops: 3, CostedPackages: 4, CostedCents: 1500}
	got := report.RouteEfficiencyRows([]report.RouteDay{in})
	want := []report.RouteRow{{RouteDay: in, StopsPerRoute: ptr(2.5), PackagesPerStop: ptr(1.6),
		DeclaredCostPerStopCents: ptr(500), CostPerPackageCents: ptr(375)}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %+v\nwant %+v", got, want)
	}
}

func TestRouteEfficiencyRowsWithoutACostIsNull(t *testing.T) {
	got := report.RouteEfficiencyRows([]report.RouteDay{{Day: utcDay(2026, 11, 3), Station: "st1", Routes: 1, Stops: 2, Packages: 2}})
	if got[0].DeclaredCostPerStopCents != nil || got[0].CostPerPackageCents != nil {
		t.Fatalf("an undeclared cost must be null, got %+v", got[0])
	}
	if *got[0].StopsPerRoute != 2 || *got[0].PackagesPerStop != 1 {
		t.Fatalf("row = %+v", got[0])
	}
	if empty := report.RouteEfficiencyRows(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("nil input must yield an empty, non-nil slice, got %#v", empty)
	}
}

func TestRouteEfficiencyRowsDeclaredZeroCostIsZeroNotNull(t *testing.T) {
	got := report.RouteEfficiencyRows([]report.RouteDay{{Routes: 1, Stops: 2, Packages: 3, CostedStops: 2, CostedPackages: 3}})
	if got[0].DeclaredCostPerStopCents == nil || *got[0].DeclaredCostPerStopCents != 0 || *got[0].CostPerPackageCents != 0 {
		t.Fatalf("a declared zero cost must read 0, got %+v", got[0])
	}
}

func TestAttemptOutcomeRowsDerivesTheKPIs(t *testing.T) {
	in := report.AttemptDay{Day: utcDay(2026, 11, 3), Station: "st1", DeliveredAttempts: 3, FailedAttempts: 2,
		PackagesAttempted: 4, PackagesFirstAttemptSuccess: 3, AttemptsOfPackagesAttempted: 6, ReturnsInitiated: 1}
	got := report.AttemptOutcomeRows([]report.AttemptDay{in})
	want := []report.AttemptRow{{AttemptDay: in, Attempts: 5, FirstAttemptSuccessRate: ptr(0.75), MeanAttemptsPerPackage: ptr(1.5)}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %+v\nwant %+v", got, want)
	}
}

func TestAttemptOutcomeRowsWithoutFirstAttemptsIsNull(t *testing.T) {
	got := report.AttemptOutcomeRows([]report.AttemptDay{{Day: utcDay(2026, 11, 4), Station: "st1", FailedAttempts: 2, ReturnsInitiated: 1}})
	if got[0].FirstAttemptSuccessRate != nil || got[0].MeanAttemptsPerPackage != nil || got[0].Attempts != 2 {
		t.Fatalf("row = %+v", got[0])
	}
	if empty := report.AttemptOutcomeRows(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("nil input must yield an empty, non-nil slice, got %#v", empty)
	}
}

func TestWaveUtilisationRows(t *testing.T) {
	in := []report.WaveDay{
		{Day: utcDay(2026, 11, 3), Station: "st1", WavesScheduled: 2, WavesCancelled: 1, StopCapacity: 40, StopsInducted: 30},
		{Day: utcDay(2026, 11, 3), Station: "st2", WavesScheduled: 1, StopCapacity: 0},
	}
	got := report.WaveUtilisationRows(in)
	if len(got) != 2 || *got[0].Utilisation != 0.75 || got[0].WaveDay != in[0] || got[1].Utilisation != nil {
		t.Fatalf("rows = %+v", got)
	}
	if empty := report.WaveUtilisationRows(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("nil input must yield an empty, non-nil slice, got %#v", empty)
	}
}

func TestRatio(t *testing.T) {
	cases := []struct {
		num  float64
		den  int
		want *float64
	}{{1, 4, ptr(0.25)}, {3, 3, ptr(1)}, {0, 5, ptr(0)}, {2, 0, nil}, {2, -1, nil}, {1, 1, ptr(1)}}
	for _, c := range cases {
		if got := report.Ratio(c.num, c.den); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Ratio(%v, %d) = %v, want %v", c.num, c.den, got, c.want)
		}
	}
}

func TestComputeFreshness(t *testing.T) {
	if f := report.ComputeFreshness(nil, now); f.AsOf != nil || f.LagSeconds != nil {
		t.Fatalf("nothing applied must give nil/nil, got %+v", f)
	}
	asOf := time.Date(2026, 11, 8, 18, 29, 0, 0, time.UTC) // now is 18:30 UTC
	f := report.ComputeFreshness(&asOf, now)
	if f.AsOf == nil || !f.AsOf.Equal(asOf) || f.AsOf.Location() != time.UTC || f.LagSeconds == nil || *f.LagSeconds != 60 {
		t.Fatalf("freshness = %+v", f)
	}
	future := now.Add(time.Minute)
	if f := report.ComputeFreshness(&future, now); *f.LagSeconds != 0 {
		t.Fatalf("a clock behind the event must read zero lag, got %v", *f.LagSeconds)
	}
}

func TestEtaWindowRowsDerivesTheHitRate(t *testing.T) {
	day := utcDay(2026, 11, 3)
	in := []report.EtaDay{
		{Day: day, Station: "st1", DeliveriesWithEta: 4, DeliveriesInside: 3, DeliveriesBefore: 1},
		{Day: day, Station: "st2", DeliveriesWithEta: 2, DeliveriesAfter: 2},
		{Day: day, Station: "st3"},
	}
	got := report.EtaWindowRows(in)
	if len(got) != 3 || got[0].EtaHitRate == nil || *got[0].EtaHitRate != 0.75 || got[1].EtaHitRate == nil || *got[1].EtaHitRate != 0 {
		t.Fatalf("rows = %+v, want hit rates 0.75 and 0", got)
	}
	if got[2].EtaHitRate != nil {
		t.Fatalf("no delivery had an ETA: the rate must be null, got %v", *got[2].EtaHitRate)
	}
	if got[0].EtaDay != in[0] {
		t.Fatalf("the counts must pass through: %+v", got[0])
	}
	if empty := report.EtaWindowRows(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("nil input must yield an empty, non-nil slice, got %#v", empty)
	}
}
