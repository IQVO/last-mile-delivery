package report

import (
	"math"
	"time"
)

// RouteRow is a RouteDay plus the route-efficiency KPIs of ADR 0012. Every
// mean is nil when its denominator is zero.
type RouteRow struct {
	RouteDay
	StopsPerRoute            *float64
	PackagesPerStop          *float64
	DeclaredCostPerStopCents *float64
	CostPerPackageCents      *float64
}

// RouteEfficiencyRows derives each row's KPIs. Order is preserved; a nil input
// yields an empty, non-nil slice.
func RouteEfficiencyRows(days []RouteDay) []RouteRow {
	out := make([]RouteRow, 0, len(days))
	for _, d := range days {
		out = append(out, RouteRow{
			RouteDay:                 d,
			StopsPerRoute:            Ratio(float64(d.Stops), d.Routes),
			PackagesPerStop:          Ratio(float64(d.Packages), d.Stops),
			DeclaredCostPerStopCents: Ratio(float64(d.CostedCents), d.CostedStops),
			CostPerPackageCents:      Ratio(float64(d.CostedCents), d.CostedPackages),
		})
	}
	return out
}

// AttemptRow is an AttemptDay plus the attempt-outcome KPIs of ADR 0012.
type AttemptRow struct {
	AttemptDay
	Attempts                int
	FirstAttemptSuccessRate *float64
	MeanAttemptsPerPackage  *float64
}

// AttemptOutcomeRows derives each row's KPIs. Order is preserved; a nil
// input yields an empty, non-nil slice.
func AttemptOutcomeRows(days []AttemptDay) []AttemptRow {
	out := make([]AttemptRow, 0, len(days))
	for _, d := range days {
		out = append(out, AttemptRow{
			AttemptDay:              d,
			Attempts:                d.DeliveredAttempts + d.FailedAttempts,
			FirstAttemptSuccessRate: Ratio(float64(d.PackagesFirstAttemptSuccess), d.PackagesAttempted),
			MeanAttemptsPerPackage:  Ratio(float64(d.AttemptsOfPackagesAttempted), d.PackagesAttempted),
		})
	}
	return out
}

// WaveRow is a WaveDay plus its utilisation (inducted stops over the scheduled
// stop capacity; nil when the capacity is zero).
type WaveRow struct {
	WaveDay
	Utilisation *float64
}

// WaveUtilisationRows derives each row's utilisation. Order is preserved; a nil
// input yields an empty, non-nil slice.
func WaveUtilisationRows(days []WaveDay) []WaveRow {
	out := make([]WaveRow, 0, len(days))
	for _, d := range days {
		out = append(out, WaveRow{WaveDay: d, Utilisation: Ratio(float64(d.StopsInducted), d.StopCapacity)})
	}
	return out
}

// EtaRow is an EtaDay plus the E3 KPI of ADR 0013: the share of deliveries with
// an ETA that fell inside the window (nil when none had an ETA).
type EtaRow struct {
	EtaDay
	EtaHitRate *float64
}

// EtaWindowRows derives each row's hit rate. Order is preserved; a nil input
// yields an empty, non-nil slice.
func EtaWindowRows(days []EtaDay) []EtaRow {
	out := make([]EtaRow, 0, len(days))
	for _, d := range days {
		out = append(out, EtaRow{EtaDay: d, EtaHitRate: Ratio(float64(d.DeliveriesInside), d.DeliveriesWithEta)})
	}
	return out
}

// Ratio is num/den over a defined denominator: nil when den is not positive
// (the KPI is undefined, never a misleading 0).
func Ratio(num float64, den int) *float64 {
	if den <= 0 {
		return nil
	}
	r := num / float64(den)
	return &r
}

// Freshness is the projection's freshness: AsOf is the CloudEvents time of the
// newest event applied and LagSeconds is now - AsOf. Both are nil while the
// projection has applied nothing.
type Freshness struct {
	AsOf       *time.Time
	LagSeconds *float64
}

// ComputeFreshness derives the lag from the newest applied event time. A clock
// that is behind the event (skew) reads as zero lag, never negative.
func ComputeFreshness(asOf *time.Time, now time.Time) Freshness {
	if asOf == nil {
		return Freshness{}
	}
	// math.Max, not `if lag < 0`: both yield 0 at the boundary, which would
	// leave an equivalent `<` -> `<=` mutant.
	lag := math.Max(0, now.Sub(*asOf).Seconds())
	utc := asOf.UTC()
	return Freshness{AsOf: &utc, LagSeconds: &lag}
}
