package analyticsstore_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/analytics/report"
)

// store is what both twins implement: the writer and the reader port.
type store interface {
	report.Projection
	report.Reader
}

// runContract is THE contract of the analytical store (ADR 0012). It runs
// against MemoryStore (memory_test.go) and against real Postgres
// (postgres_integration_test.go); newStore returns an EMPTY store.
func runContract(t *testing.T, newStore func(t *testing.T) store) {
	t.Run("an empty store answers empty, non-nil reports", func(t *testing.T) { contractEmpty(t, newStore(t)) })
	t.Run("a replayed id is a no-op", func(t *testing.T) { contractReplay(t, newStore(t)) })
	t.Run("route efficiency per day and station", func(t *testing.T) { contractRoutes(t, newStore(t)) })
	t.Run("attempt outcomes per day and station", func(t *testing.T) { contractAttempts(t, newStore(t)) })
	t.Run("wave utilisation per service date and station", func(t *testing.T) { contractWaves(t, newStore(t)) })
	t.Run("the latest cost declaration wins", func(t *testing.T) { contractCostLastWins(t, newStore(t)) })
	t.Run("the freshness basis is the newest CloudEvents time", func(t *testing.T) { contractLastEvent(t, newStore(t)) })
	t.Run("an invalid fact is rejected and leaves nothing", func(t *testing.T) { contractRejected(t, newStore(t)) })
}

var (
	ctx       = context.Background()
	nov3      = time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	nov4      = nov3.Add(24 * time.Hour)
	fullRange = report.Range{From: nov3, To: nov3.Add(48 * time.Hour)}
)

// facts applies events with fresh ids; At is the business instant + 1 minute.
type facts struct {
	t   *testing.T
	s   store
	seq int
}

func (f *facts) apply(e report.Event) {
	f.t.Helper()
	f.seq++
	e.EventID = fmt.Sprintf("evt-%03d", f.seq)
	e.At = e.OccurredAt.Add(time.Minute)
	applied, err := f.s.Apply(ctx, e)
	if err != nil || !applied {
		f.t.Fatalf("apply %s %s: applied=%v err=%v", e.Kind, e.EventID, applied, err)
	}
}

func (f *facts) wave(id, station string, date time.Time, capacity int) {
	f.apply(report.Event{Kind: report.KindWaveScheduled, WaveID: id, StationNodeID: station, ServiceDate: date,
		StopCapacity: capacity, OccurredAt: date.Add(-24 * time.Hour)})
}

func (f *facts) induct(id string, assigned ...int) {
	for _, n := range assigned {
		f.apply(report.Event{Kind: report.KindWaveCapacityChanged, WaveID: id, AssignedStops: n, OccurredAt: nov3.Add(-time.Hour)})
	}
}

func (f *facts) cost(id string, cents int64, at time.Time) {
	f.apply(report.Event{Kind: report.KindWaveCostDeclared, WaveID: id, CostPerStopCents: cents, OccurredAt: at})
}

func (f *facts) cancel(id string) {
	f.apply(report.Event{Kind: report.KindWaveCancelled, WaveID: id, OccurredAt: nov3.Add(-2 * time.Hour)})
}

func (f *facts) plan(route, wave string, stops int) {
	f.apply(report.Event{Kind: report.KindRoutePlanned, RouteID: route, WaveID: wave, DSPID: "dsp-1", StopCount: stops,
		OccurredAt: nov3.Add(-3 * time.Hour)})
}

func (f *facts) out(route, station string, tracking ...string) {
	for _, tn := range tracking {
		f.apply(report.Event{Kind: report.KindOutForDelivery, RouteID: route, TrackingNumber: tn, StationNodeID: station,
			OccurredAt: nov3.Add(-4 * time.Hour)})
	}
}

func (f *facts) attempt(route, tracking, outcome string, at time.Time) {
	f.apply(report.Event{Kind: report.KindDeliveryAttempted, RouteID: route, TrackingNumber: tracking, Outcome: outcome, OccurredAt: at})
}

func (f *facts) returned(tracking string, at time.Time) {
	f.apply(report.Event{Kind: report.KindReturnInitiated, TrackingNumber: tracking, OccurredAt: at})
}

const (
	del  = report.OutcomeDelivered
	fail = report.OutcomeFailed
)

func h(d time.Time, hours int) time.Time { return d.Add(time.Duration(hours) * time.Hour) }

// seedWaves writes the waves of the scenario. Range [nov3, nov5).
func seedWaves(f *facts) {
	f.wave("W1", "ST1", nov3, 10)
	f.induct("W1", 1, 3, 2) // reordered: the highest (3) is kept
	f.cost("W1", 150, h(nov3, -1))
	f.cost("W1", 100, h(nov3, -5)) // an older declaration arriving late loses
	f.wave("W2", "ST1", nov3, 5)
	f.induct("W2", 2)
	f.cancel("W2")
	f.wave("W6", "ST1", nov3, 4)
	f.cost("W6", 50, h(nov3, -1))
	f.wave("W3", "ST2", nov4, 0)
	f.wave("W4", "ST1", nov4.Add(24*time.Hour), 7) // service date = To: excluded
	f.wave("W5", "ST1", nov3.Add(-24*time.Hour), 3)
}

// seedRoutes writes the routes and attempts of the scenario (see the ADR's
// KPI definitions).
func seedRoutes(f *facts) {
	// R1 (W1, cost 150): 2 stops, A B C; completed nov3 12:00. Its plan arrives last.
	f.out("R1", "ST1", "A", "B", "C")
	f.attempt("R1", "A", del, h(nov3, 10))
	f.attempt("R1", "B", fail, h(nov3, 11))
	f.attempt("R1", "C", del, h(nov3, 12))
	f.plan("R1", "W1", 2)
	// R8 (W6, cost 50): 3 stops, I J K L, all delivered nov3 14:00.
	f.plan("R8", "W6", 3)
	f.out("R8", "ST1", "I", "J", "K", "L")
	for _, tn := range []string{"I", "J", "K", "L"} {
		f.attempt("R8", tn, del, h(nov3, 14))
	}
	// R2 (W3, no cost) at ST2: D failed nov4 09:00; the attempt arrives before its out-for-delivery.
	f.plan("R2", "W3", 1)
	f.attempt("R2", "D", fail, h(nov4, 9))
	f.out("R2", "ST2", "D")
	// R3 (W1): B re-attempted and delivered nov4 10:00, E never attempted -> not completed.
	f.plan("R3", "W1", 2)
	f.out("R3", "ST1", "B", "E")
	f.attempt("R3", "B", del, h(nov4, 10))
	// R4: fully attempted but never planned -> no route row, but its attempt counts.
	f.out("R4", "ST1", "F")
	f.attempt("R4", "F", del, h(nov3, 8))
	// R5: completed exactly at To -> excluded everywhere.
	f.plan("R5", "W1", 1)
	f.out("R5", "ST1", "G")
	f.attempt("R5", "G", del, fullRange.To)
	// R6: an attempt whose out-for-delivery was never projected -> station UNKNOWN.
	f.attempt("R6", "H", fail, h(nov4, 13))
	// Returns: D at its latest attempt's station, Z without any attempt, one before the range.
	f.returned("D", h(nov4, 15))
	f.returned("Z", h(nov4, 16))
	f.returned("A", nov3.Add(-time.Second))
}

func seedScenario(t *testing.T, s store) {
	t.Helper()
	f := &facts{t: t, s: s}
	seedWaves(f)
	seedRoutes(f)
}

func contractEmpty(t *testing.T, s store) {
	routes, err := s.RouteEfficiency(ctx, fullRange)
	if err != nil || routes == nil || len(routes) != 0 {
		t.Fatalf("routes = %#v, %v; want empty non-nil", routes, err)
	}
	attempts, err := s.AttemptOutcomes(ctx, fullRange)
	if err != nil || attempts == nil || len(attempts) != 0 {
		t.Fatalf("attempts = %#v, %v; want empty non-nil", attempts, err)
	}
	waves, err := s.WaveUtilisation(ctx, fullRange)
	if err != nil || waves == nil || len(waves) != 0 {
		t.Fatalf("waves = %#v, %v; want empty non-nil", waves, err)
	}
	if last, err := s.LastEventAt(ctx); err != nil || last != nil {
		t.Fatalf("last = %v, %v; want nil", last, err)
	}
}

func contractReplay(t *testing.T, s store) {
	e := report.Event{Kind: report.KindDeliveryAttempted, EventID: "evt-1", RouteID: "R1", TrackingNumber: "A",
		Outcome: del, At: h(nov3, 1), OccurredAt: nov3}
	if applied, err := s.Apply(ctx, e); err != nil || !applied {
		t.Fatalf("first apply: %v %v", applied, err)
	}
	if applied, err := s.Apply(ctx, e); err != nil || applied {
		t.Fatalf("replay: applied=%v err=%v; want a no-op", applied, err)
	}
	got, err := s.AttemptOutcomes(ctx, fullRange)
	if err != nil || len(got) != 1 || got[0].DeliveredAttempts != 1 || got[0].Station != report.UnknownStation() {
		t.Fatalf("attempts after a replay = %+v, %v; want one delivered attempt", got, err)
	}
}

// sameDays compares rows after checking every day is a UTC day.
func sameDays[T any](t *testing.T, what string, got, want []T, day func(T) time.Time) {
	t.Helper()
	for i := range got {
		if day(got[i]).Location() != time.UTC {
			t.Errorf("%s row %d: day %v is not UTC", what, i, day(got[i]))
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %+v\nwant %+v", what, got, want)
	}
}

func contractRoutes(t *testing.T, s store) {
	seedScenario(t, s)
	got, err := s.RouteEfficiency(ctx, fullRange)
	if err != nil {
		t.Fatal(err)
	}
	want := []report.RouteDay{
		{Day: nov3, Station: "ST1", Routes: 2, Stops: 5, Packages: 7, CostedStops: 5, CostedPackages: 7, CostedCents: 150*2 + 50*3},
		{Day: nov4, Station: "ST2", Routes: 1, Stops: 1, Packages: 1},
	}
	sameDays(t, "routes", got, want, func(d report.RouteDay) time.Time { return d.Day })
}

func contractAttempts(t *testing.T, s store) {
	seedScenario(t, s)
	got, err := s.AttemptOutcomes(ctx, fullRange)
	if err != nil {
		t.Fatal(err)
	}
	want := []report.AttemptDay{
		{Day: nov3, Station: "ST1", DeliveredAttempts: 7, FailedAttempts: 1, PackagesAttempted: 8,
			PackagesFirstAttemptSuccess: 7, AttemptsOfPackagesAttempted: 9},
		{Day: nov4, Station: "ST1", DeliveredAttempts: 1},
		{Day: nov4, Station: "ST2", FailedAttempts: 1, PackagesAttempted: 1, AttemptsOfPackagesAttempted: 1, ReturnsInitiated: 1},
		{Day: nov4, Station: report.UnknownStation(), FailedAttempts: 1, PackagesAttempted: 1, AttemptsOfPackagesAttempted: 1,
			ReturnsInitiated: 1},
	}
	sameDays(t, "attempts", got, want, func(d report.AttemptDay) time.Time { return d.Day })
	one, err := s.AttemptOutcomes(ctx, report.Range{From: nov4, To: fullRange.To})
	if err != nil || len(one) != 3 || !one[0].Day.Equal(nov4) {
		t.Fatalf("a one-day range = %+v, %v", one, err)
	}
}

func contractWaves(t *testing.T, s store) {
	seedScenario(t, s)
	got, err := s.WaveUtilisation(ctx, fullRange)
	if err != nil {
		t.Fatal(err)
	}
	want := []report.WaveDay{
		{Day: nov3, Station: "ST1", WavesScheduled: 3, WavesCancelled: 1, StopCapacity: 19, StopsInducted: 5},
		{Day: nov4, Station: "ST2", WavesScheduled: 1},
	}
	sameDays(t, "waves", got, want, func(d report.WaveDay) time.Time { return d.Day })
}

func contractCostLastWins(t *testing.T, s store) {
	f := &facts{t: t, s: s}
	f.wave("W1", "ST1", nov3, 10)
	f.cost("W1", 100, h(nov3, -5))
	f.cost("W1", 0, h(nov3, -1)) // a declared zero is a cost, and the latest
	f.cost("W1", 900, h(nov3, -3))
	f.plan("R1", "W1", 2)
	f.out("R1", "ST1", "A")
	f.attempt("R1", "A", del, h(nov3, 9))
	got, err := s.RouteEfficiency(ctx, fullRange)
	if err != nil || len(got) != 1 || got[0].CostedStops != 2 || got[0].CostedPackages != 1 || got[0].CostedCents != 0 {
		t.Fatalf("routes = %+v, %v; want the latest declaration (0 cents) over 2 costed stops", got, err)
	}
}

func contractLastEvent(t *testing.T, s store) {
	f := &facts{t: t, s: s}
	f.returned("A", h(nov3, 5))
	f.returned("B", h(nov3, 2))
	last, err := s.LastEventAt(ctx)
	newest := h(nov3, 5).Add(time.Minute)
	if err != nil || last == nil || !last.Equal(newest) || last.Location() != time.UTC {
		t.Fatalf("last = %v, %v; want %v UTC (the newest, not the last applied)", last, err, newest)
	}
}

func contractRejected(t *testing.T, s store) {
	bad := []report.Event{
		{Kind: report.KindDeliveryAttempted, EventID: "evt-bad-1", RouteID: "R1", TrackingNumber: "A", Outcome: "LOST", At: nov3, OccurredAt: nov3},
		{Kind: "PackageInductionDeferred", EventID: "evt-bad-2", TrackingNumber: "A", At: nov3, OccurredAt: nov3},
		{Kind: report.KindRoutePlanned, EventID: "evt-bad-3", RouteID: "R1", WaveID: "W1", At: nov3, OccurredAt: nov3},
	}
	for _, e := range bad {
		if applied, err := s.Apply(ctx, e); applied || !errors.Is(err, report.ErrRejected) {
			t.Errorf("%s: applied=%v err=%v; want ErrRejected", e.EventID, applied, err)
		}
	}
	if last, err := s.LastEventAt(ctx); err != nil || last != nil {
		t.Fatalf("a rejected fact must leave no mark, last = %v, %v", last, err)
	}
}
