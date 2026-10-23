package repocontract

import (
	"context"
	"testing"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
)

var readModelCases = []named{
	{"a station is stored once and a miss is ErrNotFound", stationCase},
	{"a service area is stored only with a higher assignment version", serviceAreaCase},
	{"the first label data of a package wins", packageStopCase},
	{"an arrival is stored once per load and package", inboundCase},
}

var inboxCases = []named{
	{"a claim is true once and false for every repeat of the event id", inboxClaim},
	{"a claim rolls back with the unit of work", inboxRollback},
}

func stationCase(t *testing.T, f Fixture) {
	_, err := f.Stations.Get(ctxb(), "node-ds-01")
	wantErr(t, err, ports.ErrNotFound)
	first := views.Station{NodeID: "node-ds-01", NodeType: views.DeliveryStation, RegionID: "region-se", Timezone: "America/Sao_Paulo"}
	noErr(t, f.Stations.Save(ctxb(), first))
	noErr(t, f.Stations.Save(ctxb(), views.Station{NodeID: "node-ds-01", NodeType: views.DeliveryStation, RegionID: "region-x", Timezone: "UTC"}))
	got, err := f.Stations.Get(ctxb(), "node-ds-01")
	noErr(t, err)
	if got != first {
		t.Fatalf("station = %+v, want the first save %+v", got, first)
	}
}

func serviceAreaCase(t *testing.T, f Fixture) {
	_, err := f.ServiceAreas.Get(ctxb(), "01310")
	wantErr(t, err, ports.ErrNotFound)
	save := func(station string, version int64) bool {
		applied, err := f.ServiceAreas.Save(ctxb(), views.ServiceArea{PostalPrefix: "01310", StationNodeID: station, RegionID: "region-se", AssignmentVersion: version})
		noErr(t, err)
		return applied
	}
	if !save("node-ds-01", 2) {
		t.Fatal("the first assignment must be applied")
	}
	if save("node-ds-02", 1) || save("node-ds-02", 2) {
		t.Fatal("a lower or equal version must not be applied")
	}
	got, err := f.ServiceAreas.Get(ctxb(), "01310")
	noErr(t, err)
	if got.StationNodeID != "node-ds-01" || got.AssignmentVersion != 2 {
		t.Fatalf("area = %+v", got)
	}
	if !save("node-ds-02", 3) {
		t.Fatal("a higher version must be applied")
	}
	got, err = f.ServiceAreas.Get(ctxb(), "01310")
	noErr(t, err)
	if got.StationNodeID != "node-ds-02" || got.AssignmentVersion != 3 {
		t.Fatalf("area = %+v", got)
	}
}

func packageStopCase(t *testing.T, f Fixture) {
	_, err := f.PackageStops.Get(ctxb(), "TRK-1")
	wantErr(t, err, ports.ErrNotFound)
	first := views.PackageStop{TrackingNumber: "TRK-1", StopKey: "k-a", DestinationPostalPrefix: "01310", AssignmentID: "asg-1"}
	noErr(t, f.PackageStops.Save(ctxb(), first))
	noErr(t, f.PackageStops.Save(ctxb(), views.PackageStop{TrackingNumber: "TRK-1", StopKey: "k-b"}))
	got, err := f.PackageStops.Get(ctxb(), "TRK-1")
	noErr(t, err)
	if got != first {
		t.Fatalf("package stop = %+v, want %+v", got, first)
	}
	noErr(t, f.PackageStops.Save(ctxb(), views.PackageStop{TrackingNumber: "TRK-2", StopKey: "k-c"}))
	got, err = f.PackageStops.Get(ctxb(), "TRK-2")
	noErr(t, err)
	if got.StopKey != "k-c" || got.DestinationPostalPrefix != "" || got.AssignmentID != "" {
		t.Fatalf("optional fields must round-trip empty: %+v", got)
	}
}

func inboundCase(t *testing.T, f Fixture) {
	_, err := f.Inbound.Get(ctxb(), "load-1", "TRK-1")
	wantErr(t, err, ports.ErrNotFound)
	first := views.InboundPackage{LoadID: "load-1", TrackingNumber: "TRK-1", DepartureID: "dep-1", StationNodeID: "node-ds-01", ArrivedAt: Now}
	noErr(t, f.Inbound.Save(ctxb(), first))
	noErr(t, f.Inbound.Save(ctxb(), views.InboundPackage{LoadID: "load-1", TrackingNumber: "TRK-1", DepartureID: "dep-2", StationNodeID: "node-ds-02", ArrivedAt: Now.Add(1)}))
	got, err := f.Inbound.Get(ctxb(), "load-1", "TRK-1")
	noErr(t, err)
	if got.DepartureID != "dep-1" || !got.ArrivedAt.Equal(Now) || got.StationNodeID != "node-ds-01" {
		t.Fatalf("inbound = %+v, want the first save", got)
	}
	noErr(t, f.Inbound.Save(ctxb(), views.InboundPackage{LoadID: "load-2", TrackingNumber: "TRK-1", StationNodeID: "node-ds-01", ArrivedAt: Now}))
	if _, err := f.Inbound.Get(ctxb(), "load-2", "TRK-1"); err != nil {
		t.Fatalf("the same package in another load is a new arrival: %v", err)
	}
}

func inboxClaim(t *testing.T, f Fixture) {
	for i, want := range []bool{true, false, false} {
		got, err := f.Inbox.Claim(ctxb(), "11111111-2222-4333-8444-555555555501", "LoadArrived")
		noErr(t, err)
		if got != want {
			t.Fatalf("claim %d = %v, want %v", i+1, got, want)
		}
	}
	got, err := f.Inbox.Claim(ctxb(), "11111111-2222-4333-8444-555555555502", "LoadArrived")
	noErr(t, err)
	if !got {
		t.Fatal("another event id is a new claim")
	}
}

func inboxRollback(t *testing.T, f Fixture) {
	const id = "11111111-2222-4333-8444-555555555503"
	err := f.UoW.Do(ctxb(), func(ctx context.Context) error {
		ok, err := f.Inbox.Claim(ctx, id, "LabelIssued")
		noErr(t, err)
		if !ok {
			t.Fatal("the first claim must be new")
		}
		return errBoom
	})
	wantErr(t, err, errBoom)
	ok, err := f.Inbox.Claim(ctxb(), id, "LabelIssued")
	noErr(t, err)
	if !ok {
		t.Fatal("a claim made in a failed unit must not survive it")
	}
}
