package kafka_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	kafkaconsumer "github.com/claudioed/last-mile-delivery/internal/adapters/inbound/kafka"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

func envelope(id, typ, data string) []byte {
	return []byte(fmt.Sprintf(`{"specversion":"1.0","id":%q,"source":"/retailnetwork/test","type":%q,"subject":"s","datacontenttype":"application/json","time":"2026-10-24T06:00:00Z","data":%s}`, id, typ, data))
}

func nodeRegistered(id, node, nodeType string) []byte {
	return envelope(id, kafkaconsumer.TypeNodeRegistered, fmt.Sprintf(`{"nodeId":%q,"nodeType":%q,"regionId":"region-se","timezone":"America/Sao_Paulo","extra":1}`, node, nodeType))
}

func loadArrived(id, node string, tns ...string) []byte {
	return loadArrivedAt(id, node, "DELIVERY_STATION", tns...)
}

func loadArrivedAt(id, node, nodeType string, tns ...string) []byte {
	list := `"` + strings.Join(tns, `","`) + `"`
	return envelope(id, kafkaconsumer.TypeLoadArrived, fmt.Sprintf(
		`{"loadId":"load-1","departureId":"dep-1","destinationNodeId":%q,"nodeType":%q,"trackingNumbers":[%s],"arrivedAt":"2026-10-24T06:00:00Z"}`, node, nodeType, list))
}

func handle(t *testing.T, c *kafkaconsumer.Consumer, value []byte) error {
	t.Helper()
	return c.HandleMessage(context.Background(), value)
}

func TestNodeRegisteredKeepsDeliveryStationsOnly(t *testing.T) {
	s := newService()
	c := s.topology()
	for _, m := range [][]byte{nodeRegistered("e1", "node-ds-01", "DELIVERY_STATION"), nodeRegistered("e2", "node-hub-01", "HUB")} {
		if err := handle(t, c, m); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.deps.Stations.Get(context.Background(), "node-ds-01"); err != nil || got.RegionID != "region-se" || got.Timezone != "America/Sao_Paulo" {
		t.Fatalf("station = %+v, %v", got, err)
	}
	if n := s.store.Counts().Stations; n != 1 {
		t.Fatalf("stations = %d, want only the delivery station", n)
	}
}

func TestServiceAreaAssignedKeepsTheHighestVersion(t *testing.T) {
	s := newService()
	c := s.topology()
	area := func(id string, v int, station string) []byte {
		return envelope(id, kafkaconsumer.TypeServiceAreaAssigned, fmt.Sprintf(`{"postalPrefix":"01310","deliveryStationNodeId":%q,"regionId":"r","assignmentVersion":%d}`, station, v))
	}
	for _, m := range [][]byte{area("e1", 2, "node-b"), area("e2", 1, "node-a"), area("e3", 3, "node-c")} {
		if err := handle(t, c, m); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.deps.ServiceAreas.Get(context.Background(), "01310")
	if err != nil || got.StationNodeID != "node-c" || got.AssignmentVersion != 3 {
		t.Fatalf("area = %+v, %v", got, err)
	}
}

func TestLabelIssuedFeedsThePackageStopView(t *testing.T) {
	s := newService()
	m := envelope("e1", kafkaconsumer.TypeLabelIssued, `{"labelRef":"l","trackingNumber":"TRK-1","assignmentId":"asg-1","stopKey":"k1:abc","destinationPostalPrefix":"01310"}`)
	if err := handle(t, s.shipping(), m); err != nil {
		t.Fatal(err)
	}
	got, err := s.deps.PackageStops.Get(context.Background(), "TRK-1")
	if err != nil || got.StopKey != "k1:abc" || got.AssignmentID != "asg-1" || got.DestinationPostalPrefix != "01310" {
		t.Fatalf("package stop = %+v, %v", got, err)
	}
}

func TestLoadArrivedInductsOnceEvenWhenRedelivered(t *testing.T) {
	s := newService()
	ctx := context.Background()
	if err := handle(t, s.topology(), nodeRegistered("n1", "node-ds-01", "DELIVERY_STATION")); err != nil {
		t.Fatal(err)
	}
	cutoff := t0.Add(2 * time.Hour)
	v, err := usecases.NewScheduleWave(s.deps).Execute(ctx, usecases.ScheduleWaveCommand{
		StationNodeID: "node-ds-01", ServiceDate: "2026-10-24", InductCutoffAt: cutoff, WindowStart: cutoff.Add(time.Hour), WindowEnd: cutoff.Add(5 * time.Hour), StopCapacity: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	msg := loadArrived("e1", "node-ds-01", "TRK-1", "TRK-2")
	for i := 0; i < 2; i++ {
		if err := handle(t, s.linehaul(), msg); err != nil {
			t.Fatal(err)
		}
	}
	w, err := s.deps.Waves.Get(ctx, wave.ID(v.WaveID))
	if err != nil || len(w.Inductions()) != 2 {
		t.Fatalf("inductions = %v, %v", w, err)
	}
	if n := s.store.Counts().ProcessedEvents; n != 2 {
		t.Fatalf("processed events = %d, want the node event and the load event once each", n)
	}
}

func TestLoadArrivedAtAnotherNodeTypeIsAppliedAsIgnored(t *testing.T) {
	s := newService()
	if err := handle(t, s.linehaul(), loadArrivedAt("e1", "node-hub-01", "HUB", "TRK-1")); err != nil {
		t.Fatalf("a deterministic ignore must not be an error: %v", err)
	}
	if c := s.store.Counts(); c.Histories != 0 {
		t.Fatalf("histories = %d, want none", c.Histories)
	}
}

func TestEverythingElseIsIgnoredOrSkipped(t *testing.T) {
	s := newService()
	c := s.topology()
	c.DLQ = nil // without a dead-letter writer a poison message is skipped, not parked
	for name, m := range map[string][]byte{
		"an unknown type":        envelope("e1", "com.retailnetwork.logistics.node-topology.node.NodeRetired", `{}`),
		"not a cloudevent":       []byte(`{"hello":"world"}`),
		"not even json":          []byte(`<<<`),
		"an undecodable payload": envelope("e2", kafkaconsumer.TypeNodeRegistered, `{"nodeId":7}`),
	} {
		if err := handle(t, c, m); err != nil {
			t.Errorf("%s: HandleMessage = %v, want nil (skipped)", name, err)
		}
	}
	if c := s.store.Counts(); c.Stations != 0 || c.ProcessedEvents != 0 {
		t.Fatalf("counts = %+v, want nothing stored", c)
	}
	// A decodable event with no usable key is applied as ignored and marked processed.
	if err := handle(t, c, envelope("e3", kafkaconsumer.TypeNodeRegistered, `{"nodeType":"DELIVERY_STATION"}`)); err != nil {
		t.Fatal(err)
	}
	if c := s.store.Counts(); c.Stations != 0 || c.ProcessedEvents != 1 {
		t.Fatalf("counts = %+v, want the event claimed and no station", c)
	}
}

func TestPoisonMessagesGoToTheDeadLetterTopicAtOnce(t *testing.T) {
	s := newService()
	c := s.topology()
	dlq := &fakeDLQWriter{}
	c.DLQ = dlq
	bad := [][]byte{[]byte(`<<<`), envelope("e2", kafkaconsumer.TypeNodeRegistered, `{"nodeId":7}`), []byte(`{"hello":"world"}`)}
	reader := newFakeReader(bad...)
	if err := runUntilCommits(t, c, reader, len(bad)); err != context.Canceled {
		t.Fatalf("Run = %v", err)
	}
	got := dlq.published()
	if len(got) != len(bad) {
		t.Fatalf("dead letters = %d, want %d", len(got), len(bad))
	}
	for _, m := range got {
		if m.Topic != kafkaconsumer.TopologyTopic+".dlq" {
			t.Errorf("dead letter topic = %s", m.Topic)
		}
	}
}

func TestRunCommitsAfterTheEffectAndRetriesTransientFailures(t *testing.T) {
	s := newService()
	c := s.shipping()
	reader := newFakeReader(
		envelope("e1", kafkaconsumer.TypeLabelIssued, `{"trackingNumber":"TRK-1","stopKey":"k1:a"}`),
		envelope("e2", kafkaconsumer.TypeLabelIssued, `{"trackingNumber":"TRK-2","stopKey":"k1:b"}`),
	)
	if err := runUntilCommits(t, c, reader, 2); err != context.Canceled {
		t.Fatalf("Run = %v", err)
	}
	if n := s.store.Counts().PackageStops; n != 2 {
		t.Fatalf("package stops = %d", n)
	}
	want := []string{"fetch:0", "commit:0", "fetch:1", "commit:1"}
	if got := reader.log(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

var _ = views.DeliveryStation
