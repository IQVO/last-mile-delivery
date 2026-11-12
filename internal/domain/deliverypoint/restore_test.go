package deliverypoint

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

func flaggedSnapshot() Snapshot {
	return Snapshot{
		StopKey: key, Attempts: 5, Delivered: 2, SafePlaceUsed: 1, FailedNotHome: 1, FailedAccess: 1, FailedOther: 1,
		Strikes: []Strike{
			{RouteID: "route-1", TrackingNumber: "TRK-1", Category: CategoryNotHome, At: day0, Notified: true},
			{RouteID: "route-2", TrackingNumber: "TRK-2", Category: CategoryAccessProblem, At: days(1), Notified: true},
		},
		FlaggedAt: days(1), LastAttemptAt: days(3), Version: 5,
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	p := fresh(t)
	notHome(t, p, "route-1", "TRK-1", day0)
	flagged(t, notHome(t, p, "route-2", "TRK-2", days(1)))
	got, err := Restore(p.Snapshot())
	wantNoErr(t, err)
	if !reflect.DeepEqual(got.Snapshot(), p.Snapshot()) {
		t.Fatalf("round trip changed the profile:\n got %+v\nwant %+v", got.Snapshot(), p.Snapshot())
	}
}

func TestEveryFieldRoundTripsAndNothingAliases(t *testing.T) {
	want := flaggedSnapshot()
	p, err := Restore(want)
	wantNoErr(t, err)
	if got := p.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %+v, want %+v", got, want)
	}
	snap := p.Snapshot()
	snap.Strikes[0].TrackingNumber = "TRK-X"
	if p.Snapshot().Strikes[0].TrackingNumber != "TRK-1" {
		t.Fatal("Snapshot aliases the profile")
	}
	if p.Attempts() != 5 || p.Delivered() != 2 || p.SafePlaceUsed() != 1 || p.FailedNotHome() != 1 || p.FailedAccess() != 1 || p.FailedOther() != 1 {
		t.Fatal("accessors wrong")
	}
	if !p.Flagged() || p.FlagReason() != CategoryAccessProblem || p.OpenStrikes() != 2 || !p.LastAttemptAt().Equal(days(3)) || p.Version() != 5 {
		t.Fatal("derived accessors wrong")
	}
}

func TestRestoreNormalisesInstantsToUTC(t *testing.T) {
	zone := time.FixedZone("x", 3600)
	s := flaggedSnapshot()
	s.LastAttemptAt, s.FlaggedAt = s.LastAttemptAt.In(zone), s.FlaggedAt.In(zone)
	s.Strikes[0].At = s.Strikes[0].At.In(zone)
	p, err := Restore(s)
	wantNoErr(t, err)
	got := p.Snapshot()
	if got.LastAttemptAt.Location() != time.UTC || got.FlaggedAt.Location() != time.UTC || got.Strikes[0].At.Location() != time.UTC {
		t.Fatalf("instants not in UTC: %+v", got)
	}
}

func TestRestoreAccepts(t *testing.T) {
	open := flaggedSnapshot()
	one := flaggedSnapshot()
	one.Strikes, one.FlaggedAt = one.Strikes[:1], time.Time{}
	none := Snapshot{StopKey: key, Attempts: 1, FailedOther: 1, LastAttemptAt: day0, Version: 1}
	five := flaggedSnapshot()
	five.Strikes = nil
	for i := 1; i <= MaxStrikes; i++ {
		five.Strikes = append(five.Strikes, Strike{RouteID: "route-" + string(rune('0'+i)), TrackingNumber: "TRK-1", Category: CategoryNotHome, At: day0})
	}
	for name, s := range map[string]Snapshot{"flagged": open, "one strike": one, "no strike": none, "a full ring": five} {
		if _, err := Restore(s); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRestoreRejectsCorruptRows(t *testing.T) {
	cases := map[string]func(*Snapshot){
		"empty stop key":                func(s *Snapshot) { s.StopKey = "" },
		"package stop key":              func(s *Snapshot) { s.StopKey = "pkg:TRK-1" },
		"stop key too long":             func(s *Snapshot) { s.StopKey = strings.Repeat("a", shared.MaxStopKey+1) },
		"version zero":                  func(s *Snapshot) { s.Version = 0 },
		"no last attempt":               func(s *Snapshot) { s.LastAttemptAt = time.Time{} },
		"negative counter":              func(s *Snapshot) { s.FailedOther, s.Attempts = -1, 3 },
		"counter above int32":           func(s *Snapshot) { s.Delivered = shared.MaxInt32 + 1 },
		"attempts is not the sum":       func(s *Snapshot) { s.Attempts = 6 },
		"safe place above delivered":    func(s *Snapshot) { s.SafePlaceUsed = 3 },
		"six strikes":                   func(s *Snapshot) { s.Strikes = sixStrikes() },
		"uncounted category":            func(s *Snapshot) { s.Strikes[0].Category = "OTHER" },
		"blank strike category":         func(s *Snapshot) { s.Strikes[0].Category = "" },
		"two strikes share a route":     func(s *Snapshot) { s.Strikes[1].RouteID = "route-1" },
		"strike without a tracking no.": func(s *Snapshot) { s.Strikes[0].TrackingNumber = "" },
		"strike without an instant":     func(s *Snapshot) { s.Strikes[0].At = time.Time{} },
		"flag without two strikes":      func(s *Snapshot) { s.Strikes = s.Strikes[:1] },
		"two strikes without a flag":    func(s *Snapshot) { s.FlaggedAt = time.Time{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := flaggedSnapshot()
			mutate(&s)
			p, err := Restore(s)
			if !errors.Is(err, shared.ErrInvalidSnapshot) || p != nil {
				t.Fatalf("Restore = %v, %v, want an invalid snapshot", p, err)
			}
		})
	}
}

func sixStrikes() []Strike {
	var out []Strike
	for i := 1; i <= MaxStrikes+1; i++ {
		out = append(out, Strike{RouteID: "route-" + string(rune('0'+i)), TrackingNumber: "TRK-1", Category: CategoryNotHome, At: day0})
	}
	return out
}
