package reportshttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/adapters/inbound/reportshttp"
	"github.com/claudioed/last-mile-delivery/internal/analytics/report"
)

// fakeReader answers fixed rows and records the range it was asked for.
type fakeReader struct {
	routes   []report.RouteDay
	attempts []report.AttemptDay
	waves    []report.WaveDay
	etas     []report.EtaDay
	last     *time.Time
	err      error
	asked    report.Range
}

func (f *fakeReader) RouteEfficiency(_ context.Context, r report.Range) ([]report.RouteDay, error) {
	f.asked = r
	return f.routes, f.err
}

func (f *fakeReader) AttemptOutcomes(_ context.Context, r report.Range) ([]report.AttemptDay, error) {
	f.asked = r
	return f.attempts, f.err
}

func (f *fakeReader) WaveUtilisation(_ context.Context, r report.Range) ([]report.WaveDay, error) {
	f.asked = r
	return f.waves, f.err
}

func (f *fakeReader) EtaWindow(_ context.Context, r report.Range) ([]report.EtaDay, error) {
	f.asked = r
	return f.etas, f.err
}

func (f *fakeReader) LastEventAt(context.Context) (*time.Time, error) { return f.last, f.err }

type fakePing struct{ err error }

func (p fakePing) Ping(context.Context) error { return p.err }

var (
	now  = time.Date(2026, 11, 8, 18, 30, 0, 0, time.UTC)
	nov3 = time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
)

func serve(t *testing.T, s *reportshttp.Server, method, target string) (int, string, map[string]any) {
	t.Helper()
	if s.Now == nil {
		s.Now = func() time.Time { return now }
	}
	h := reportshttp.NewRouter(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, target, nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s %s: body %q is not JSON: %v", method, target, rec.Body.String(), err)
	}
	return rec.Code, rec.Header().Get("Content-Type"), body
}

func TestRouteEfficiencyReport(t *testing.T) {
	r := &fakeReader{routes: []report.RouteDay{
		{Day: nov3, Station: "ST1", Routes: 2, Stops: 5, Packages: 8, CostedStops: 4, CostedPackages: 5, CostedCents: 1000},
		{Day: nov3, Station: "ST2", Routes: 1, Stops: 2, Packages: 2},
	}}
	code, ct, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, "/reports/route-efficiency?from=2026-11-01&to=2026-11-07")
	if code != http.StatusOK || ct != "application/json" {
		t.Fatalf("status %d content-type %s", code, ct)
	}
	want := map[string]any{"from": "2026-11-01", "to": "2026-11-07", "days": []any{
		map[string]any{"day": "2026-11-03", "stationNodeId": "ST1", "routesCompleted": 2.0, "stopsPerRoute": 2.5,
			"packagesPerStop": 1.6, "declaredCostPerStopCents": 250.0, "costPerPackageCents": 200.0},
		map[string]any{"day": "2026-11-03", "stationNodeId": "ST2", "routesCompleted": 1.0, "stopsPerRoute": 2.0,
			"packagesPerStop": 1.0, "declaredCostPerStopCents": nil, "costPerPackageCents": nil},
	}}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %v\nwant %v", body, want)
	}
	if !r.asked.From.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) || !r.asked.To.Equal(time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("asked range = %+v, want [11-01, 11-08)", r.asked)
	}
}

func TestAttemptOutcomesReport(t *testing.T) {
	r := &fakeReader{attempts: []report.AttemptDay{
		{Day: nov3, Station: "ST1", DeliveredAttempts: 3, FailedAttempts: 1, PackagesAttempted: 4, PackagesFirstAttemptSuccess: 3,
			AttemptsOfPackagesAttempted: 5, ReturnsInitiated: 1},
		{Day: nov3.Add(24 * time.Hour), Station: "UNKNOWN", ReturnsInitiated: 2},
	}}
	code, _, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, "/reports/attempt-outcomes")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	want := map[string]any{"from": "2026-10-10", "to": "2026-11-08", "days": []any{
		map[string]any{"day": "2026-11-03", "stationNodeId": "ST1", "attempts": 4.0, "deliveredAttempts": 3.0, "failedAttempts": 1.0,
			"packagesAttempted": 4.0, "packagesWithFirstAttemptSuccess": 3.0, "firstAttemptSuccessRate": 0.75,
			"meanAttemptsPerPackage": 1.25, "returnsInitiated": 1.0},
		map[string]any{"day": "2026-11-04", "stationNodeId": "UNKNOWN", "attempts": 0.0, "deliveredAttempts": 0.0, "failedAttempts": 0.0,
			"packagesAttempted": 0.0, "packagesWithFirstAttemptSuccess": 0.0, "firstAttemptSuccessRate": nil,
			"meanAttemptsPerPackage": nil, "returnsInitiated": 2.0},
	}}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %v\nwant %v", body, want)
	}
}

func TestWaveUtilisationReport(t *testing.T) {
	r := &fakeReader{waves: []report.WaveDay{
		{Day: nov3, Station: "ST1", WavesScheduled: 2, WavesCancelled: 1, StopCapacity: 40, StopsInducted: 30},
		{Day: nov3, Station: "ST2", WavesScheduled: 1},
	}}
	code, _, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, "/reports/wave-utilisation?from=2026-11-03&to=2026-11-03")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	want := map[string]any{"from": "2026-11-03", "to": "2026-11-03", "days": []any{
		map[string]any{"day": "2026-11-03", "stationNodeId": "ST1", "wavesScheduled": 2.0, "wavesCancelled": 1.0,
			"stopCapacity": 40.0, "stopsInducted": 30.0, "utilisation": 0.75},
		map[string]any{"day": "2026-11-03", "stationNodeId": "ST2", "wavesScheduled": 1.0, "wavesCancelled": 0.0,
			"stopCapacity": 0.0, "stopsInducted": 0.0, "utilisation": nil},
	}}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %v\nwant %v", body, want)
	}
}

func TestEtaWindowReport(t *testing.T) {
	r := &fakeReader{etas: []report.EtaDay{
		{Day: nov3, Station: "ST1", DeliveriesWithEta: 4, DeliveriesInside: 3, DeliveriesBefore: 1},
		{Day: nov3, Station: "UNKNOWN"},
	}}
	code, ct, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, "/reports/eta-window?from=2026-11-03&to=2026-11-03")
	if code != http.StatusOK || ct != "application/json" {
		t.Fatalf("status %d content-type %s", code, ct)
	}
	want := map[string]any{"from": "2026-11-03", "to": "2026-11-03", "days": []any{
		map[string]any{"day": "2026-11-03", "stationNodeId": "ST1", "deliveriesWithEta": 4.0, "deliveriesInsideEta": 3.0,
			"deliveriesBeforeEta": 1.0, "deliveriesAfterEta": 0.0, "etaHitRate": 0.75},
		map[string]any{"day": "2026-11-03", "stationNodeId": "UNKNOWN", "deliveriesWithEta": 0.0, "deliveriesInsideEta": 0.0,
			"deliveriesBeforeEta": 0.0, "deliveriesAfterEta": 0.0, "etaHitRate": nil},
	}}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %v\nwant %v", body, want)
	}
	if r.asked.FirstDay() != "2026-11-03" {
		t.Fatalf("asked range = %+v", r.asked)
	}
}

func reportPaths() []string {
	return []string{"/reports/route-efficiency", "/reports/attempt-outcomes", "/reports/wave-utilisation", "/reports/eta-window"}
}

func TestEmptyReportsAreEmptyArrays(t *testing.T) {
	for _, path := range reportPaths() {
		_, _, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, path)
		if days, ok := body["days"].([]any); !ok || len(days) != 0 {
			t.Errorf("%s days = %#v, want []", path, body["days"])
		}
	}
}

func TestFreshness(t *testing.T) {
	_, _, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, "/reports/freshness")
	if v, ok := body["asOf"]; !ok || v != nil || body["lagSeconds"] != nil {
		t.Fatalf("before the first event = %v, want explicit nulls", body)
	}
	asOf := now.Add(-90 * time.Second)
	_, _, body = serve(t, &reportshttp.Server{Reader: &fakeReader{last: &asOf}}, http.MethodGet, "/reports/freshness")
	if body["asOf"] != "2026-11-08T18:28:30Z" || body["lagSeconds"] != 90.0 {
		t.Fatalf("freshness = %v", body)
	}
}

func assertProblem(t *testing.T, code int, ct string, body map[string]any, wantCode int, wantType string) {
	t.Helper()
	if code != wantCode || ct != "application/problem+json" || body["type"] != wantType || body["status"] != float64(wantCode) {
		t.Fatalf("got %d %s %v, want %d problem %s", code, ct, body, wantCode, wantType)
	}
}

func TestInvalidRangeIsAProblem(t *testing.T) {
	for _, path := range reportPaths() {
		for _, q := range []string{"?from=2026-11-05&to=2026-11-04", "?from=nope", "?to=2026-13-01", "?from=2025-01-01&to=2026-11-01"} {
			code, ct, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, path+q)
			assertProblem(t, code, ct, body, http.StatusBadRequest, "invalid-report-range")
		}
	}
}

func TestReaderFailureIsAnOpaque500(t *testing.T) {
	r := &fakeReader{err: errors.New("pq: password authentication failed for secret-role")}
	for _, path := range append(reportPaths(), "/reports/freshness") {
		code, ct, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, path)
		assertProblem(t, code, ct, body, http.StatusInternalServerError, "internal-error")
		if strings.Contains(body["detail"].(string), "secret") {
			t.Fatalf("%s leaked the cause: %v", path, body)
		}
	}
}

func TestUnknownPathAndWriteMethodsAreProblems(t *testing.T) {
	code, ct, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, "/reports/nope")
	assertProblem(t, code, ct, body, http.StatusNotFound, "resource-not-found")
	code, ct, body = serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodPost, "/reports/freshness")
	assertProblem(t, code, ct, body, http.StatusMethodNotAllowed, "method-not-allowed")
}

func TestHealthAndReadiness(t *testing.T) {
	if code, _, body := serve(t, &reportshttp.Server{}, http.MethodGet, "/healthz"); code != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("healthz = %d %v", code, body)
	}
	if code, _, body := serve(t, &reportshttp.Server{Ping: fakePing{}}, http.MethodGet, "/readyz"); code != http.StatusOK || body["status"] != "ready" {
		t.Fatalf("readyz = %d %v", code, body)
	}
	if code, _, body := serve(t, &reportshttp.Server{}, http.MethodGet, "/readyz"); code != http.StatusOK || body["status"] != "ready" {
		t.Fatalf("readyz without a pinger = %d %v", code, body)
	}
	code, ct, body := serve(t, &reportshttp.Server{Ping: fakePing{err: errors.New("down")}}, http.MethodGet, "/readyz")
	assertProblem(t, code, ct, body, http.StatusServiceUnavailable, "dependency-unavailable")
	draining := &reportshttp.Server{}
	draining.SetNotReady()
	if code, _, body := serve(t, draining, http.MethodGet, "/readyz"); code != http.StatusServiceUnavailable || body["status"] != "not_ready" {
		t.Fatalf("draining readyz = %d %v", code, body)
	}
}
