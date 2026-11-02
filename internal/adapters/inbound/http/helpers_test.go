package http_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	handler "github.com/claudioed/last-mile-delivery/internal/adapters/inbound/http"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/memory"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
)

var t0 = time.Date(2026, 10, 24, 6, 0, 0, 0, time.UTC)

// app is the REST adapter over the in-memory service.
type app struct {
	t     *testing.T
	store *memory.Store
	deps  usecases.Deps
	idem  *memory.Idempotency
	srv   http.Handler
	n     int
}

func newApp(t *testing.T) *app {
	t.Helper()
	store := memory.NewStore()
	deps := usecases.Deps{
		Waves: store.Waves(), Routes: store.Routes(), Partners: store.Partners(), Histories: store.Histories(), Stations: store.Stations(),
		ServiceAreas: store.ServiceAreas(), PackageStops: store.PackageStops(), Inbound: store.Inbound(), Inbox: store.Inbox(),
		Outbox: store.Outbox(), UoW: store.UnitOfWork(), Clock: memory.NewClock(t0), IDs: &memory.SequentialIDs{},
	}
	idem := memory.NewIdempotency(nil)
	a := &app{t: t, store: store, deps: deps, idem: idem}
	a.srv = handler.NewRouter(&handler.Server{
		ScheduleWave: usecases.NewScheduleWave(deps), CloseInduction: usecases.NewCloseInduction(deps), DispatchWave: usecases.NewDispatchWave(deps),
		CancelWave: usecases.NewCancelWave(deps), ListWaves: usecases.NewListWaves(deps), GetWaveStops: usecases.NewGetWaveStops(deps),
		PlanRoute: usecases.NewPlanRoute(deps), StartRoute: usecases.NewStartRoute(deps), RecordAttempt: usecases.NewRecordAttempt(deps),
		GetRoute: usecases.NewGetRoute(deps), ContractPartner: usecases.NewContractPartner(deps), DeactivatePartner: usecases.NewDeactivatePartner(deps),
		Idempotency: handler.RequireIdempotencyKey(idem),
	}, nil, "")
	if err := deps.Stations.Save(context.Background(), views.Station{NodeID: "node-ds-01", NodeType: views.DeliveryStation, RegionID: "region-se"}); err != nil {
		t.Fatal(err)
	}
	return a
}

// reply is one HTTP response.
type reply struct {
	Code   int
	Header http.Header
	Body   string
}

func (r reply) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.Body), &m); err != nil {
		t.Fatalf("body is not a JSON object: %s", r.Body)
	}
	return m
}

func (r reply) slug(t *testing.T) string {
	t.Helper()
	if ct := r.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q for %d %s, want application/problem+json", ct, r.Code, r.Body)
	}
	s, _ := r.json(t)["type"].(string)
	return s
}

func (a *app) do(method, path, body string, headers map[string]string) reply {
	a.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	a.srv.ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return reply{Code: res.StatusCode, Header: res.Header, Body: string(b)}
}

func (a *app) get(path string) reply { return a.do(http.MethodGet, path, "", nil) }

// post sends a POST with a fresh Idempotency-Key.
func (a *app) post(path, body string) reply {
	a.n++
	return a.do(http.MethodPost, path, body, map[string]string{"Idempotency-Key": "key-" + string(rune('a'+a.n%26)) + strings.Repeat("x", a.n)})
}

const waveBodyJSON = `{"stationNodeId":"node-ds-01","serviceDate":"2026-10-24","inductCutoffAt":"2026-10-24T08:00:00Z","windowStart":"2026-10-24T12:00:00Z","windowEnd":"2026-10-24T20:00:00Z","stopCapacity":5}`
