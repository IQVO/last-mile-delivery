package mcp_test

import (
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestListWaves(t *testing.T) {
	h := withRoute(t)
	got := h.ok(t, "list_waves", map[string]any{"stationNodeId": "node-ds-01", "serviceDate": "2026-10-24"})
	items, _ := got["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("list_waves = %v, want 1 wave", got)
	}
	w := items[0].(map[string]any)
	for k, v := range map[string]any{
		"waveId": "wave-1", "stationNodeId": "node-ds-01", "serviceDate": "2026-10-24",
		"inductCutoffAt": "2026-10-24T08:00:00Z", "windowStart": "2026-10-24T12:00:00Z", "windowEnd": "2026-10-24T20:00:00Z",
		"stopCapacity": 5.0, "assignedStops": 2.0, "remainingStops": 3.0, "state": "DISPATCHED",
	} {
		if w[k] != v {
			t.Errorf("%s = %v, want %v", k, w[k], v)
		}
	}
	if _, has := w["cancelReason"]; has {
		t.Errorf("cancelReason must be absent on a live wave: %v", w)
	}
}

func TestListWavesEmpty(t *testing.T) {
	h := newHarness(t)
	got := h.ok(t, "list_waves", map[string]any{"stationNodeId": "node-ds-01", "serviceDate": "2026-10-25"})
	items, _ := got["items"].([]any)
	if len(items) != 0 {
		t.Fatalf("a station and date with no wave = %v, want an empty list", got)
	}
}

func TestListWavesErrors(t *testing.T) {
	h := newHarness(t)
	// Malformed values reach the handler and fail with the REST slug.
	failWith(t, h.session, "list_waves", map[string]any{"stationNodeId": "node-ds-01", "serviceDate": "24-10-2026"}, "validation-failed")
	failWith(t, h.session, "list_waves", map[string]any{"stationNodeId": "", "serviceDate": "2026-10-24"}, "validation-failed")
	// A MISSING argument is rejected by the SDK's schema validation before
	// the handler runs (the schema marks every input required, like the REST
	// adapter rejects the request without the query parameter).
	res, err := h.session.CallTool(t.Context(), &sdk.CallToolParams{Name: "list_waves", Arguments: map[string]any{"stationNodeId": "node-ds-01"}})
	if err == nil && !res.IsError {
		t.Fatal("list_waves without serviceDate must be rejected")
	}
}

func TestGetWaveStops(t *testing.T) {
	h := withWave(t)
	got := h.ok(t, "get_wave_stops", map[string]any{"waveId": "wave-1"})
	// The structured content of a list result is the list itself.
	var stops []map[string]any
	switch v := got["items"].(type) {
	case []any:
		for _, item := range v {
			stops = append(stops, item.(map[string]any))
		}
	default:
		t.Fatalf("wave stops = %#v, want an items array", got)
	}
	if len(stops) != 2 {
		t.Fatalf("stops = %v, want 2 (one per stopKey)", stops)
	}
	first := stops[0]
	if first["stopKey"] != "stop-se-1" || first["destinationPostalPrefix"] != "12345" {
		t.Fatalf("first stop = %v", first)
	}
	tns, _ := first["trackingNumbers"].([]any)
	if len(tns) != 1 || tns[0] != "TRK-1" {
		t.Fatalf("first stop trackingNumbers = %v", first["trackingNumbers"])
	}
}

func TestGetWaveStopsOwnStopWithoutLabelData(t *testing.T) {
	h := newHarness(t)
	h.arrive(t, "e1", "TRK-9") // no label: the package is its own stop
	h.scheduleWave(t)
	got := h.ok(t, "get_wave_stops", map[string]any{"waveId": "wave-1"})
	items, _ := got["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("stops = %v, want 1", got)
	}
	stop := items[0].(map[string]any)
	if stop["stopKey"] != "pkg:TRK-9" {
		t.Fatalf("stopKey = %v, want pkg:TRK-9", stop["stopKey"])
	}
	if _, has := stop["destinationPostalPrefix"]; has {
		t.Fatalf("destinationPostalPrefix must be absent without label data: %v", stop)
	}
}

func TestGetWaveStopsErrors(t *testing.T) {
	h := newHarness(t)
	failWith(t, h.session, "get_wave_stops", map[string]any{"waveId": "wave-nope"}, "not-found")
}

func TestGetRoute(t *testing.T) {
	h := withRoute(t)
	got := h.ok(t, "get_route", map[string]any{"routeId": "route-1"})
	for k, v := range map[string]any{
		"routeId": "route-1", "waveId": "wave-1", "dspId": "dsp-1", "stationNodeId": "node-ds-01",
		"state": "OUT_FOR_DELIVERY", "stopCount": 2.0,
	} {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	stops, _ := got["stops"].([]any)
	if len(stops) != 2 {
		t.Fatalf("stops = %v", got["stops"])
	}
	second := stops[1].(map[string]any)
	if second["stopNo"] != 2.0 {
		t.Fatalf("second stop = %v", second)
	}
	if _, has := second["outcome"]; has {
		t.Fatalf("outcome must be absent until every package of the stop has an attempt: %v", second)
	}
	attempts, _ := got["attempts"].([]any)
	if len(attempts) != 1 {
		t.Fatalf("attempts = %v", got["attempts"])
	}
	a := attempts[0].(map[string]any)
	if a["trackingNumber"] != "TRK-1" || a["outcome"] != "DELIVERED" || a["at"] != "2026-10-24T13:00:00Z" {
		t.Fatalf("attempt = %v", a)
	}
	if _, has := a["failureReason"]; has {
		t.Fatalf("failureReason must be absent on a delivered attempt: %v", a)
	}
}

func TestGetRouteErrors(t *testing.T) {
	h := newHarness(t)
	failWith(t, h.session, "get_route", map[string]any{"routeId": "route-nope"}, "not-found")
}

func TestReadsEmitNothing(t *testing.T) {
	h := withRoute(t)
	before := len(h.outbox.Events())
	h.ok(t, "list_waves", map[string]any{"stationNodeId": "node-ds-01", "serviceDate": "2026-10-24"})
	h.ok(t, "get_wave_stops", map[string]any{"waveId": "wave-1"})
	h.ok(t, "get_route", map[string]any{"routeId": "route-1"})
	if after := len(h.outbox.Events()); after != before {
		t.Fatalf("outbox rows %d -> %d: a read tool appended an event", before, after)
	}
}
