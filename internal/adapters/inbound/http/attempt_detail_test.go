package http_test

import (
	"net/http"
	"strings"
	"testing"
)

// startedRoute plans and starts route-1 over TRK-1..TRK-n, one stop each.
func startedRoute(t *testing.T, tns ...string) *app {
	t.Helper()
	a := newApp(t)
	a.post("/waves", waveBodyJSON)
	a.post("/partners", `{"stationNodeId":"node-ds-01","vanCapacity":4}`)
	a.arrive("e1", tns...)
	a.post("/waves/wave-1/induction-closures", "")
	stops := ""
	for i, tn := range tns {
		if i > 0 {
			stops += ","
		}
		stops += `{"stopNo":` + string(rune('1'+i)) + `,"trackingNumbers":["` + tn + `"]}`
	}
	if r := a.post("/routes", `{"waveId":"wave-1","dspId":"dsp-1","stops":[`+stops+`]}`); r.Code != http.StatusCreated {
		t.Fatalf("plan = %d %s", r.Code, r.Body)
	}
	a.post("/waves/wave-1/dispatches", "")
	if r := a.post("/routes/route-1/starts", ""); r.Code != http.StatusOK {
		t.Fatalf("start = %d %s", r.Code, r.Body)
	}
	return a
}

func TestRecordAttemptReturnsAndStoresTheOutcomeDetail(t *testing.T) {
	a := startedRoute(t, "TRK-1", "TRK-2", "TRK-3")
	failed := `{"trackingNumber":"TRK-1","outcome":"FAILED","failureReason":"gate locked","outcomeDetail":"ACCESS_PROBLEM","at":"2026-10-24T13:00:00Z"}`
	expect(t, "failed with detail", a.post("/routes/route-1/attempts", failed), 201, map[string]any{"outcomeDetail": "ACCESS_PROBLEM", "failureReason": "gate locked"})
	safe := `{"trackingNumber":"TRK-2","outcome":"DELIVERED","outcomeDetail":"SAFE_PLACE_USED","at":"2026-10-24T13:10:00Z"}`
	expect(t, "delivered with detail", a.post("/routes/route-1/attempts", safe), 201, map[string]any{"outcomeDetail": "SAFE_PLACE_USED"})
	plain := `{"trackingNumber":"TRK-3","outcome":"DELIVERED","at":"2026-10-24T13:20:00Z"}`
	r := expect(t, "delivered without detail", a.post("/routes/route-1/attempts", plain), 201, nil)
	if _, present := r.json(t)["outcomeDetail"]; present {
		t.Fatalf("an unreported detail must be omitted: %s", r.Body)
	}
	got := a.get("/routes/route-1")
	if got.Code != http.StatusOK || !containsAll(got.Body, `"outcomeDetail":"ACCESS_PROBLEM"`, `"outcomeDetail":"SAFE_PLACE_USED"`) {
		t.Fatalf("get route = %d %s", got.Code, got.Body)
	}
}

func TestRecordAttemptDetailMatrixIs422(t *testing.T) {
	cases := []struct{ name, body string }{
		{"safe place on a failure", `{"trackingNumber":"TRK-1","outcome":"FAILED","failureReason":"x","outcomeDetail":"SAFE_PLACE_USED","at":"2026-10-24T13:00:00Z"}`},
		{"not home on a delivery", `{"trackingNumber":"TRK-1","outcome":"DELIVERED","outcomeDetail":"NOT_HOME","at":"2026-10-24T13:00:00Z"}`},
		{"other on a delivery", `{"trackingNumber":"TRK-1","outcome":"DELIVERED","outcomeDetail":"OTHER","at":"2026-10-24T13:00:00Z"}`},
		{"unknown value", `{"trackingNumber":"TRK-1","outcome":"FAILED","failureReason":"x","outcomeDetail":"LOST","at":"2026-10-24T13:00:00Z"}`},
	}
	a := startedRoute(t, "TRK-1")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := a.post("/routes/route-1/attempts", c.body)
			if r.Code != http.StatusUnprocessableEntity || r.slug(t) != "validation-failed" {
				t.Fatalf("= %d %s, want 422 validation-failed", r.Code, r.Body)
			}
		})
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
