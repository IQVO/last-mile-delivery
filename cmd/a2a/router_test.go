package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	inbounda2a "github.com/claudioed/last-mile-delivery/internal/adapters/inbound/a2a"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/memory"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
)

// newTestAgent builds the router exactly as run() does (newAgent), over the
// in-memory repositories holding one wave at node-ds-01. The memory adapter is
// a test double: it is imported by this _test file only, never by the binary.
func newTestAgent(t *testing.T) http.Handler {
	t.Helper()
	t0 := time.Date(2026, 10, 24, 6, 0, 0, 0, time.UTC)
	store := memory.NewStore()
	d := usecases.Deps{
		Waves: store.Waves(), Routes: store.Routes(), Partners: store.Partners(), Histories: store.Histories(), Stations: store.Stations(),
		ServiceAreas: store.ServiceAreas(), PackageStops: store.PackageStops(), Inbound: store.Inbound(), Inbox: store.Inbox(),
		Outbox: store.Outbox(), UoW: store.UnitOfWork(), Clock: memory.NewClock(t0), IDs: &memory.SequentialIDs{},
	}
	if err := d.Stations.Save(context.Background(), views.Station{NodeID: "node-ds-01", NodeType: views.DeliveryStation, RegionID: "region-se"}); err != nil {
		t.Fatal(err)
	}
	if _, err := usecases.NewScheduleWave(d).Execute(context.Background(), usecases.ScheduleWaveCommand{
		StationNodeID: "node-ds-01", ServiceDate: "2026-10-24",
		InductCutoffAt: t0.Add(2 * time.Hour), WindowStart: t0.Add(6 * time.Hour), WindowEnd: t0.Add(14 * time.Hour), StopCapacity: 5,
	}); err != nil {
		t.Fatal(err)
	}
	deps := inbounda2a.Deps{ListWaves: usecases.NewListWaves(d), GetRoute: usecases.NewGetRoute(d)}
	return newAgent(deps, testAgentURL, "test", quietLogger(), "last-mile-delivery-a2a")
}

func TestRouter_HealthzServesOK(t *testing.T) {
	h := newTestAgent(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz: got %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != `{"status":"ok"}` {
		t.Fatalf("GET /healthz body = %q, want {\"status\":\"ok\"}", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("GET /healthz Content-Type = %q, want application/json", ct)
	}
}

func TestRouter_ServesTheAgentCardAtTheWellKnownPath(t *testing.T) {
	h := newTestAgent(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET agent card: got %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"name":"last-mile-delivery"`, testAgentURL, `"list_waves"`, `"get_route"`} {
		if !strings.Contains(body, want) {
			t.Errorf("agent card lacks %s: %s", want, body)
		}
	}
}

// TestRouter_BothMountsReachTheJSONRPCHandler pins the fleet's
// <CTX>_A2A_ENDPOINT convention: the endpoint answers at "/" and at "/a2a",
// and the use cases behind it are the wired ones (the seeded wave is read).
func TestRouter_BothMountsReachTheJSONRPCHandler(t *testing.T) {
	h := newTestAgent(t)
	for _, path := range []string{"/", "/a2a"} {
		t.Run(path, func(t *testing.T) {
			body := `{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"data":{"skill":"list_waves","arguments":{"stationNodeId":"node-ds-01","serviceDate":"2026-10-24"}}}]}}}`
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("A2A-Version", "1.0")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			raw, _ := io.ReadAll(rec.Body)
			if rec.Code == http.StatusNotFound || !strings.Contains(string(raw), `"jsonrpc"`) {
				t.Fatalf("POST %s: %d %q does not look like a JSON-RPC reply", path, rec.Code, raw)
			}
			if !strings.Contains(string(raw), `"waveId":"wave-1"`) {
				t.Fatalf("POST %s: reply %q lacks the seeded wave read through the wired use case", path, raw)
			}
		})
	}
}

// TestRouter_ACardFetchedFromTheBinaryIsDialable runs the discovery flow a
// delegating agent runs against the real router: fetch the card, build a
// client from it, call a skill.
func TestRouter_ACardFetchedFromTheBinaryIsDialable(t *testing.T) {
	srv := httptest.NewServer(newTestAgent(t))
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + a2asrv.WellKnownAgentCardPath)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("card fetch = %v / %v", res, err)
	}
	defer func() { _ = res.Body.Close() }()
	var card a2a.AgentCard
	if err := json.NewDecoder(res.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	// The card advertises the configured URL, not the test server's: point the
	// client at the test server the way a gateway-rewritten route would.
	card.SupportedInterfaces[0].URL = srv.URL + "/a2a"
	client, err := a2aclient.NewFromCard(context.Background(), &card)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{
		"skill": "list_waves", "arguments": map[string]any{"stationNodeId": "node-ds-01", "serviceDate": "2026-10-24"},
	}))})
	if err != nil {
		t.Fatal(err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) < 2 {
		t.Fatalf("reply = %T", resp)
	}
	if text, _ := msg.Parts[1].Content.(a2a.Text); !strings.Contains(string(text), "wave-1") {
		t.Fatalf("summary = %q, want the seeded wave listed", text)
	}
}

// The binary reads no credentials: a request with any Authorization header
// behaves exactly like one without (fleet decision, no auth).
func TestNoAuthIsRequired(t *testing.T) {
	h := newTestAgent(t)
	for _, auth := range []string{"", "Bearer nonsense"} {
		req := httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("agent card with Authorization=%q = %d, want 200", auth, rec.Code)
		}
	}
}
