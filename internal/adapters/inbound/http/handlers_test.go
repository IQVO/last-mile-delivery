package http_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
)

func (a *app) arrive(id string, tns ...string) {
	a.t.Helper()
	err := usecases.NewInductOnArrival(a.deps).Execute(context.Background(), usecases.LoadArrivedCommand{
		EventID: id, EventType: "LoadArrived", LoadID: "load-" + id, DepartureID: "dep", DestinationNodeID: "node-ds-01",
		NodeType: views.DeliveryStation, TrackingNumbers: tns, ArrivedAt: t0,
	})
	if err != nil {
		a.t.Fatal(err)
	}
}

// expect fails the test unless r has the status and, per field, the value.
func expect(t *testing.T, step string, r reply, code int, fields map[string]any) reply {
	t.Helper()
	if r.Code != code {
		t.Fatalf("%s = %d %s, want %d", step, r.Code, r.Body, code)
	}
	body := r.json(t)
	for k, want := range fields {
		if body[k] != want {
			t.Fatalf("%s: %s = %v, want %v (%s)", step, k, body[k], want, r.Body)
		}
	}
	return r
}

func TestTheWholeDeliveryDayThroughHTTP(t *testing.T) {
	a := newApp(t)
	expect(t, "schedule", a.post("/waves", waveBodyJSON), 201, map[string]any{"waveId": "wave-1", "state": "OPEN", "remainingStops": float64(5)})
	expect(t, "contract", a.post("/partners", `{"stationNodeId":"node-ds-01","vanCapacity":2}`), 201, map[string]any{"dspId": "dsp-1", "state": "ACTIVE"})
	a.arrive("e1", "TRK-1", "TRK-2")

	stops := a.get("/waves/wave-1/stops")
	if stops.Code != http.StatusOK || !strings.Contains(stops.Body, `"TRK-1"`) || !strings.Contains(stops.Body, `"stopKey"`) {
		t.Fatalf("stops = %d %s", stops.Code, stops.Body)
	}
	expect(t, "close", a.post("/waves/wave-1/induction-closures", ""), 200, map[string]any{"state": "INDUCTION_CLOSED"})
	route := `{"waveId":"wave-1","dspId":"dsp-1","stops":[{"stopNo":1,"trackingNumbers":["TRK-1"]},{"stopNo":2,"trackingNumbers":["TRK-2"]}]}`
	expect(t, "plan", a.post("/routes", route), 201, map[string]any{"routeId": "route-1", "stopCount": float64(2)})
	early := expect(t, "start before dispatch", a.post("/routes/route-1/starts", ""), 409, nil)
	if early.slug(t) != "illegal-transition" {
		t.Fatalf("start before dispatch: %s", early.Body)
	}
	expect(t, "dispatch", a.post("/waves/wave-1/dispatches", ""), 200, map[string]any{"state": "DISPATCHED"})
	expect(t, "start", a.post("/routes/route-1/starts", ""), 200, map[string]any{"state": "OUT_FOR_DELIVERY"})

	attempt := func(tn, at string) reply {
		return a.post("/routes/route-1/attempts", `{"trackingNumber":"`+tn+`","outcome":"DELIVERED","at":"`+at+`"}`)
	}
	expect(t, "attempt", attempt("TRK-1", "2026-10-24T13:00:00Z"), 201, map[string]any{"outcome": "DELIVERED"})
	again := expect(t, "second delivery", attempt("TRK-1", "2026-10-24T13:05:00Z"), 409, nil)
	if again.slug(t) != "already-delivered" {
		t.Fatalf("second delivery: %s", again.Body)
	}
	expect(t, "attempt 2", attempt("TRK-2", "2026-10-24T13:10:00Z"), 201, nil)

	done := expect(t, "route", a.get("/routes/route-1"), 200, map[string]any{"state": "COMPLETED"})
	if !strings.Contains(done.Body, `"outcome":"DELIVERED"`) || strings.Contains(done.Body, "failureReason") {
		t.Fatalf("route body = %s", done.Body)
	}
	list := a.get("/waves?stationNodeId=node-ds-01&serviceDate=2026-10-24")
	if list.Code != http.StatusOK || !strings.Contains(list.Body, `"state":"COMPLETED"`) {
		t.Fatalf("list = %d %s", list.Code, list.Body)
	}
	expect(t, "deactivate", a.post("/partners/dsp-1/deactivations", ""), 200, map[string]any{"state": "INACTIVE"})
}

func TestCancelWaveThroughHTTP(t *testing.T) {
	a := newApp(t)
	a.post("/waves", waveBodyJSON)
	r := a.post("/waves/wave-1/cancellations", `{"reason":"station closed"}`)
	if r.Code != http.StatusOK || r.json(t)["state"] != "CANCELLED" || r.json(t)["cancelReason"] != "station closed" {
		t.Fatalf("cancel = %d %s", r.Code, r.Body)
	}
	if r := a.post("/waves/wave-1/cancellations", `{"reason":"again"}`); r.Code != http.StatusConflict || r.slug(t) != "illegal-transition" {
		t.Fatalf("cancel twice = %d %s", r.Code, r.Body)
	}
}

func TestEmptyListsAreEmptyArrays(t *testing.T) {
	a := newApp(t)
	if r := a.get("/waves?stationNodeId=node-ds-01&serviceDate=2026-10-25"); r.Code != http.StatusOK || strings.TrimSpace(r.Body) != `{"items":[]}` {
		t.Fatalf("empty list = %d %s", r.Code, r.Body)
	}
	a.post("/waves", waveBodyJSON)
	if r := a.get("/waves/wave-1/stops"); r.Code != http.StatusOK || strings.TrimSpace(r.Body) != `{"items":[]}` {
		t.Fatalf("no stops = %d %s", r.Code, r.Body)
	}
}

func TestProblemMapping(t *testing.T) {
	a := newApp(t)
	a.post("/waves", waveBodyJSON)
	a.post("/partners", `{"stationNodeId":"node-ds-01","vanCapacity":1}`)
	a.arrive("e1", "TRK-1", "TRK-2")
	cases := []struct {
		name         string
		method, path string
		body         string
		code         int
		slug         string
	}{
		{"unknown wave", "GET", "/waves/nope/stops", "", 404, "not-found"},
		{"unknown route", "GET", "/routes/nope", "", 404, "not-found"},
		{"unknown path", "GET", "/nothing", "", 404, "resource-not-found"},
		{"wrong method", "DELETE", "/waves", "", 405, "method-not-allowed"},
		{"unknown partner", "POST", "/partners/nope/deactivations", "", 404, "not-found"},
		{"wave times out of order", "POST", "/waves", strings.Replace(waveBodyJSON, `"inductCutoffAt":"2026-10-24T08:00:00Z"`, `"inductCutoffAt":"2026-10-24T13:00:00Z"`, 1), 422, "invalid-wave-times"},
		{"unknown station", "POST", "/waves", strings.Replace(waveBodyJSON, "node-ds-01", "node-x", 1), 422, "unknown-station"},
		{"bad service date", "POST", "/waves", strings.Replace(waveBodyJSON, "2026-10-24\"", "tomorrow\"", 1), 422, "validation-failed"},
		{"unknown field", "POST", "/waves", strings.Replace(waveBodyJSON, `{`, `{"address":"x",`, 1), 422, "validation-failed"},
		{"empty body", "POST", "/waves", "", 422, "validation-failed"},
		{"trailing data", "POST", "/partners", `{"stationNodeId":"node-ds-01","vanCapacity":1} {}`, 422, "validation-failed"},
		{"zero van", "POST", "/partners", `{"stationNodeId":"node-ds-01","vanCapacity":0}`, 422, "invalid-van-capacity"},
		{"list without station", "GET", "/waves?serviceDate=2026-10-24", "", 422, "validation-failed"},
		{"list with a bad date", "GET", "/waves?stationNodeId=node-ds-01&serviceDate=x", "", 422, "validation-failed"},
		{"plan before induction closed", "POST", "/routes", `{"waveId":"wave-1","dspId":"dsp-1","stops":[{"stopNo":1,"trackingNumbers":["TRK-1"]}]}`, 409, "illegal-transition"},
		{"cancel without a reason", "POST", "/waves/wave-1/cancellations", `{"reason":""}`, 422, "validation-failed"},
		{"bad outcome", "POST", "/routes/nope/attempts", `{"trackingNumber":"T","outcome":"LOST","at":"2026-10-24T13:00:00Z"}`, 422, "validation-failed"},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var r reply
			if c.method == "POST" {
				r = a.do("POST", c.path, c.body, map[string]string{"Idempotency-Key": "k" + strings.Repeat("z", i+1)})
			} else {
				r = a.do(c.method, c.path, c.body, nil)
			}
			if r.Code != c.code || r.slug(t) != c.slug {
				t.Fatalf("%s %s = %d %s, want %d %s", c.method, c.path, r.Code, r.Body, c.code, c.slug)
			}
			body := r.json(t)
			if body["instance"] != c.path && !strings.Contains(c.path, "?") {
				t.Errorf("instance = %v", body["instance"])
			}
		})
	}
}

func TestRouteOverCapacityAndPackageNotOnRoute(t *testing.T) {
	a := newApp(t)
	a.post("/waves", waveBodyJSON)
	a.post("/partners", `{"stationNodeId":"node-ds-01","vanCapacity":1}`)
	a.arrive("e1", "TRK-1", "TRK-2")
	a.post("/waves/wave-1/induction-closures", "")
	two := `{"waveId":"wave-1","dspId":"dsp-1","stops":[{"stopNo":1,"trackingNumbers":["TRK-1"]},{"stopNo":2,"trackingNumbers":["TRK-2"]}]}`
	if r := a.post("/routes", two); r.Code != http.StatusConflict || r.slug(t) != "route-over-capacity" {
		t.Fatalf("over capacity = %d %s", r.Code, r.Body)
	}
	one := `{"waveId":"wave-1","dspId":"dsp-1","stops":[{"stopNo":1,"trackingNumbers":["TRK-1"]}]}`
	planned := a.post("/routes", one)
	if planned.Code != http.StatusCreated {
		t.Fatalf("plan = %d %s", planned.Code, planned.Body)
	}
	if r := a.post("/routes", `{"waveId":"wave-1","dspId":"dsp-1","stops":[{"stopNo":1,"trackingNumbers":["TRK-1"]}]}`); r.Code != http.StatusConflict || r.slug(t) != "package-already-routed" {
		t.Fatalf("routed twice = %d %s", r.Code, r.Body)
	}
	a.post("/waves/wave-1/dispatches", "")
	id := planned.json(t)["routeId"].(string)
	a.post("/routes/"+id+"/starts", "")
	if r := a.post("/routes/"+id+"/attempts", `{"trackingNumber":"TRK-2","outcome":"DELIVERED","at":"2026-10-24T13:00:00Z"}`); r.Code != http.StatusNotFound || r.slug(t) != "package-not-on-route" {
		t.Fatalf("foreign package = %d %s", r.Code, r.Body)
	}
}

func TestHealthz(t *testing.T) {
	a := newApp(t)
	if r := a.get("/healthz"); r.Code != 200 || strings.TrimSpace(r.Body) != `{"status":"ok"}` {
		t.Fatalf("healthz = %d %s", r.Code, r.Body)
	}
	if r := a.get("/readyz"); r.Code != 200 {
		t.Fatalf("readyz = %d %s", r.Code, r.Body)
	}
}
