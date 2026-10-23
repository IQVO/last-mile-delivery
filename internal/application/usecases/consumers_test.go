package usecases_test

import (
	"context"
	"testing"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
)

func TestApplyNodeRegisteredKeepsOnlyDeliveryStations(t *testing.T) {
	e := newEnv(t)
	uc := usecases.NewApplyNodeRegistered(e.deps)
	ctx := context.Background()
	registered := func(id, node, typ string) error {
		return uc.Execute(ctx, usecases.NodeRegisteredCommand{EventID: id, EventType: "NodeRegistered", NodeID: node, NodeType: typ, RegionID: "region-se", Timezone: "America/Sao_Paulo"})
	}
	noErr(t, registered("ev-1", "node-ds-01", "DELIVERY_STATION"))
	noErr(t, registered("ev-2", "node-fc-01", "FULFILLMENT_CENTER"))
	noErr(t, registered("ev-3", "", "DELIVERY_STATION"))
	st, err := e.deps.Stations.Get(ctx, "node-ds-01")
	noErr(t, err)
	if st.RegionID != "region-se" || st.Timezone != "America/Sao_Paulo" {
		t.Fatalf("station = %+v", st)
	}
	if _, err := e.deps.Stations.Get(ctx, "node-fc-01"); err == nil {
		t.Fatal("a fulfillment center is not a station")
	}
	if n := e.store.Counts(); n.Stations != 1 || n.ProcessedEvents != 3 {
		t.Fatalf("counts = %+v", n)
	}
	noErr(t, registered("ev-1", "node-ds-09", "DELIVERY_STATION")) // redelivered id: nothing happens
	if e.store.Counts().Stations != 1 {
		t.Fatal("a redelivered event must change nothing")
	}
}

func TestApplyServiceAreaAssignedKeepsTheHighestVersion(t *testing.T) {
	e := newEnv(t)
	uc := usecases.NewApplyServiceAreaAssigned(e.deps)
	ctx := context.Background()
	assign := func(id, prefix, station string, version int64) {
		noErr(t, uc.Execute(ctx, usecases.ServiceAreaAssignedCommand{
			EventID: id, EventType: "ServiceAreaAssigned", PostalPrefix: prefix, StationNodeID: station, RegionID: "region-se", AssignmentVersion: version,
		}))
	}
	assign("ev-1", "01310", "node-ds-01", 2)
	assign("ev-2", "01310", "node-ds-02", 1) // lower version: ignored
	assign("ev-3", "01310", "node-ds-02", 2) // equal version: ignored
	a, err := e.deps.ServiceAreas.Get(ctx, "01310")
	noErr(t, err)
	if a.StationNodeID != "node-ds-01" || a.AssignmentVersion != 2 {
		t.Fatalf("area = %+v", a)
	}
	assign("ev-4", "01310", "node-ds-02", 3)
	a, err = e.deps.ServiceAreas.Get(ctx, "01310")
	noErr(t, err)
	if a.StationNodeID != "node-ds-02" || a.AssignmentVersion != 3 {
		t.Fatalf("area = %+v", a)
	}
	assign("ev-5", "", "node-ds-01", 1)
	assign("ev-6", "02000", "", 1)
	assign("ev-7", "03000", "node-ds-01", 0)
	if e.store.Counts().ServiceAreas != 1 {
		t.Fatalf("malformed assignments must be ignored: %+v", e.store.Counts())
	}
}

func TestApplyLabelIssuedKeepsTheFirstValues(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.label(t, "lb-1", "TRK-1", "k-a")
	uc := usecases.NewApplyLabelIssued(e.deps)
	noErr(t, uc.Execute(ctx, usecases.LabelIssuedCommand{EventID: "lb-2", EventType: "LabelIssued", TrackingNumber: "TRK-1", StopKey: "k-other"}))
	noErr(t, uc.Execute(ctx, usecases.LabelIssuedCommand{EventID: "lb-3", EventType: "LabelIssued", TrackingNumber: "TRK-2"}))
	noErr(t, uc.Execute(ctx, usecases.LabelIssuedCommand{EventID: "lb-4", EventType: "LabelIssued", TrackingNumber: "", StopKey: "k-b"}))
	got, err := e.deps.PackageStops.Get(ctx, "TRK-1")
	noErr(t, err)
	if got.StopKey != "k-a" || got.AssignmentID != "asg-TRK-1" || got.DestinationPostalPrefix != "01310" {
		t.Fatalf("package stop = %+v", got)
	}
	if _, err := e.deps.PackageStops.Get(ctx, "TRK-2"); err == nil || e.store.Counts().PackageStops != 1 {
		t.Fatalf("a label without a stopKey must be ignored: %v", err)
	}
	_ = ports.ErrNotFound
}
