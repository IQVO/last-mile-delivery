package features_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	handler "github.com/claudioed/last-mile-delivery/internal/adapters/inbound/http"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/memory"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
)

// httpWorld drives the real router of apis/openapi.yaml over the scenario's
// in-memory service, with the Idempotency-Key middleware in place: the REST
// contract (problem slugs, list and read endpoints, replayed responses) is a
// scenario concern, not only a unit-test one.
type httpWorld struct {
	w     *world
	idem  *memory.Idempotency
	srv   http.Handler
	reply httpReply
	last  lastRequest
}

// lastRequest is the request most recently sent, for a replay step.
type lastRequest struct {
	method, path, key, body string
}

// httpReply is the response most recently received.
type httpReply struct {
	code   int
	header http.Header
	body   string
}

// reset wires a fresh router over the (already reset) scenario world.
func (h *httpWorld) reset(w *world) {
	h.w = w
	h.idem = memory.NewIdempotency(handler.ErrKeyReused, nil)
	h.srv = handler.NewRouter(&handler.Server{
		ScheduleWave: usecases.NewScheduleWave(w.deps), CloseInduction: usecases.NewCloseInduction(w.deps), DispatchWave: usecases.NewDispatchWave(w.deps),
		CancelWave: usecases.NewCancelWave(w.deps), ListWaves: usecases.NewListWaves(w.deps), GetWaveStops: usecases.NewGetWaveStops(w.deps),
		PlanRoute: usecases.NewPlanRoute(w.deps), StartRoute: usecases.NewStartRoute(w.deps), RecordAttempt: usecases.NewRecordAttempt(w.deps),
		GetRoute: usecases.NewGetRoute(w.deps), ContractPartner: usecases.NewContractPartner(w.deps), DeactivatePartner: usecases.NewDeactivatePartner(w.deps),
		Idempotency: handler.RequireIdempotencyKey(h.idem),
	}, nil, "")
}

func (h *httpWorld) send(method, path, key, body string) error {
	h.last = lastRequest{method: method, path: path, key: key, body: body}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set(handler.IdempotencyKeyHeader, key)
	}
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	h.reply = httpReply{code: res.StatusCode, header: res.Header, body: string(b)}
	return nil
}

func (h *httpWorld) repeat() error {
	return h.send(h.last.method, h.last.path, h.last.key, h.last.body)
}

func (h *httpWorld) json() (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(h.reply.body), &m); err != nil {
		return nil, fmt.Errorf("the response body is not a JSON object: %s", h.reply.body)
	}
	return m, nil
}

// dig walks a dotted path (items.0.waveId) through decoded JSON.
func dig(node any, path string) (any, bool) {
	cur := node
	for _, seg := range strings.Split(path, ".") {
		next, ok := step(cur, seg)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

func step(node any, seg string) (any, bool) {
	switch n := node.(type) {
	case map[string]any:
		v, ok := n[seg]
		return v, ok
	case []any:
		i, err := strconv.Atoi(seg)
		if err != nil || i < 0 || i >= len(n) {
			return nil, false
		}
		return n[i], true
	}
	return nil, false
}

func (h *httpWorld) assertStatus(code int) error {
	if h.reply.code != code {
		return fmt.Errorf("the response is %d %s, want %d", h.reply.code, h.reply.body, code)
	}
	return nil
}

func (h *httpWorld) assertProblem(slug string) error {
	if ct := h.reply.header.Get("Content-Type"); ct != "application/problem+json" {
		return fmt.Errorf("Content-Type is %q for %d %s, want application/problem+json", ct, h.reply.code, h.reply.body)
	}
	m, err := h.json()
	if err != nil {
		return err
	}
	if m["type"] != slug {
		return fmt.Errorf("the problem type is %v, want %q (%s)", m["type"], slug, h.reply.body)
	}
	return nil
}

func (h *httpWorld) assertField(path, want string) error {
	m, err := h.json()
	if err != nil {
		return err
	}
	got, ok := dig(m, path)
	if !ok {
		return fmt.Errorf("the response has no %s field (%s)", path, h.reply.body)
	}
	if s := fmt.Sprintf("%v", got); s != want {
		return fmt.Errorf("the response field %s is %q, want %q", path, s, want)
	}
	return nil
}

func (h *httpWorld) assertArrayLen(field string, n int) error {
	m, err := h.json()
	if err != nil {
		return err
	}
	arr, ok := m[field].([]any)
	if !ok {
		return fmt.Errorf("the response has no %s array (%s)", field, h.reply.body)
	}
	if len(arr) != n {
		return fmt.Errorf("the response has %d %s, want %d", len(arr), field, n)
	}
	return nil
}

// assertStopHolds finds the stop by its key and compares the packages as a
// set, so the stop order cannot matter.
func (h *httpWorld) assertStopHolds(key, list string) error {
	m, err := h.json()
	if err != nil {
		return err
	}
	items, _ := m["items"].([]any)
	want := quoted(list)
	for _, it := range items {
		stop, _ := it.(map[string]any)
		if stop["stopKey"] != key {
			continue
		}
		return comparePackages(key, stop["trackingNumbers"], want)
	}
	return fmt.Errorf("the response has no stop %s (%s)", key, h.reply.body)
}

func comparePackages(key string, got any, want []string) error {
	packages, _ := got.([]any)
	held := map[string]bool{}
	for _, tn := range packages {
		if s, ok := tn.(string); ok {
			held[s] = true
		}
	}
	for _, tn := range want {
		if !held[tn] {
			return fmt.Errorf("the stop %s holds %v, want %v", key, packages, want)
		}
	}
	if len(held) != len(want) {
		return fmt.Errorf("the stop %s holds %v, want %v", key, packages, want)
	}
	return nil
}

func (h *httpWorld) assertReplayed() error {
	if v := h.reply.header.Get("Idempotent-Replayed"); v != "true" {
		return fmt.Errorf("Idempotent-Replayed is %q, want true (%s)", v, h.reply.body)
	}
	return nil
}

func registerHTTPSteps(sc *godog.ScenarioContext, h *httpWorld) {
	sc.Step(`^the client sends a "([^"]*)" to "([^"]*)" with key "([^"]*)" and body:$`, func(method, path, key string, body *godog.DocString) error {
		return h.send(method, path, key, body.Content)
	})
	sc.Step(`^the client sends a "([^"]*)" to "([^"]*)" with body:$`, func(method, path string, body *godog.DocString) error {
		return h.send(method, path, "", body.Content)
	})
	sc.Step(`^the same request is sent again$`, h.repeat)
	sc.Step(`^the client requests "([^"]*)"$`, func(path string) error {
		return h.send(http.MethodGet, path, "", "")
	})
	sc.Step(`^the response is (\d+)$`, h.assertStatus)
	sc.Step(`^the response problem is "([^"]*)"$`, h.assertProblem)
	sc.Step(`^the response field "([^"]*)" is "([^"]*)"$`, h.assertField)
	sc.Step(`^the response has (\d+) items?$`, func(n int) error { return h.assertArrayLen("items", n) })
	sc.Step(`^the response has (\d+) attempts$`, func(n int) error { return h.assertArrayLen("attempts", n) })
	sc.Step(`^the stop "([^"]*)" holds the packages (.+)$`, h.assertStopHolds)
	sc.Step(`^the response replays the stored response$`, h.assertReplayed)
}
