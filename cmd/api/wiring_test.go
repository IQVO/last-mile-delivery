package main

import (
	"context"
	"testing"
)

func TestBuildServerWiresEveryUseCase(t *testing.T) {
	ad := postgresAdapters(nil) // construction never dials
	s := buildServer(ad, nil)
	for name, wired := range map[string]bool{
		"ScheduleWave": s.ScheduleWave != nil, "CloseInduction": s.CloseInduction != nil, "DispatchWave": s.DispatchWave != nil,
		"CancelWave": s.CancelWave != nil, "ListWaves": s.ListWaves != nil, "GetWaveStops": s.GetWaveStops != nil,
		"PlanRoute": s.PlanRoute != nil, "StartRoute": s.StartRoute != nil, "RecordAttempt": s.RecordAttempt != nil,
		"GetRoute": s.GetRoute != nil, "ContractPartner": s.ContractPartner != nil, "DeactivatePartner": s.DeactivatePartner != nil,
		"Idempotency": s.Idempotency != nil,
	} {
		if !wired {
			t.Errorf("%s is not wired", name)
		}
	}
}

func TestConsumerSpecsBuildTheThreeConsumersOnTheirTopics(t *testing.T) {
	want := map[string]string{
		"linehaul":      "retailnetwork.linehaul-transportation.events",
		"node-topology": "retailnetwork.node-topology.events",
		"shipping":      "retailnetwork.shipping-and-tracking.events",
	}
	specs := consumerSpecs(postgresAdapters(nil))
	if len(specs) != len(want) {
		t.Fatalf("specs = %d", len(specs))
	}
	for _, spec := range specs {
		c := spec.build([]string{"127.0.0.1:1"}, "g", testLogger())
		if c.Topic != want[spec.name] || c.DLQ == nil {
			t.Errorf("%s: topic %q dlq %v", spec.name, c.Topic, c.DLQ)
		}
		_ = c.Close()
	}
}

func TestReadinessNamesPostgresAndKafka(t *testing.T) {
	g := newReadiness(func(context.Context) error { return context.DeadlineExceeded }, []string{"127.0.0.1:1"})
	failing := g.Failing(context.Background())
	if len(failing) != 2 || failing[0] != "postgres" || failing[1] != "kafka" {
		t.Fatalf("failing = %v", failing)
	}
}
