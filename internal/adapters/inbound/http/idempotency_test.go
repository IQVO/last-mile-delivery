package http_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestAStateChangingPostNeedsAnIdempotencyKey(t *testing.T) {
	a := newApp(t)
	for _, path := range []string{"/waves", "/routes", "/partners", "/waves/w:cancel", "/waves/w:dispatch", "/waves/w:close-induction", "/routes/r:start", "/routes/r/attempts", "/partners/d:deactivate"} {
		r := a.do(http.MethodPost, path, "{}", nil)
		if r.Code != http.StatusBadRequest || r.slug(t) != "idempotency-key-required" {
			t.Errorf("POST %s without a key = %d %s", path, r.Code, r.Body)
		}
	}
	long := strings.Repeat("k", 201)
	if r := a.do(http.MethodPost, "/waves", waveBodyJSON, map[string]string{"Idempotency-Key": long}); r.Code != http.StatusBadRequest {
		t.Errorf("a 201 character key = %d", r.Code)
	}
	if n := a.store.Counts().Waves; n != 0 {
		t.Fatalf("a refused request created %d waves", n)
	}
}

func TestARepeatedKeyReplaysTheStoredResponseAndChangesNothing(t *testing.T) {
	a := newApp(t)
	h := map[string]string{"Idempotency-Key": "once"}
	first := a.do(http.MethodPost, "/waves", waveBodyJSON, h)
	second := a.do(http.MethodPost, "/waves", waveBodyJSON, h)
	if first.Code != 201 || second.Code != 201 || first.Body != second.Body {
		t.Fatalf("first %d %s / second %d %s", first.Code, first.Body, second.Code, second.Body)
	}
	if second.Header.Get("Idempotent-Replayed") != "true" || first.Header.Get("Idempotent-Replayed") != "" {
		t.Fatalf("replay header: first %q second %q", first.Header.Get("Idempotent-Replayed"), second.Header.Get("Idempotent-Replayed"))
	}
	if second.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("replayed Content-Type = %q", second.Header.Get("Content-Type"))
	}
	c := a.store.Counts()
	if c.Waves != 1 || c.OutboxEvents != 1 {
		t.Fatalf("waves %d outbox %d, want one each", c.Waves, c.OutboxEvents)
	}
}

func TestAKeyReusedWithAnotherBodyIsRefused(t *testing.T) {
	a := newApp(t)
	h := map[string]string{"Idempotency-Key": "k"}
	a.do(http.MethodPost, "/waves", waveBodyJSON, h)
	r := a.do(http.MethodPost, "/waves", strings.Replace(waveBodyJSON, `"stopCapacity":5`, `"stopCapacity":9`, 1), h)
	if r.Code != http.StatusUnprocessableEntity || r.slug(t) != "idempotency-key-reused" {
		t.Fatalf("reuse = %d %s", r.Code, r.Body)
	}
	if a.store.Counts().Waves != 1 {
		t.Fatal("the second body must not run")
	}
}

func TestAKeyIsScopedToTheResource(t *testing.T) {
	a := newApp(t)
	h := map[string]string{"Idempotency-Key": "shared"}
	if r := a.do(http.MethodPost, "/partners", `{"stationNodeId":"node-ds-01","vanCapacity":2}`, h); r.Code != 201 {
		t.Fatalf("first = %d %s", r.Code, r.Body)
	}
	// the same key on another resource is a new request, not a replay
	if r := a.do(http.MethodPost, "/waves", waveBodyJSON, h); r.Code != 201 || r.Header.Get("Idempotent-Replayed") != "" {
		t.Fatalf("other resource = %d %s", r.Code, r.Body)
	}
}

func TestOnlyASuccessIsStored(t *testing.T) {
	a := newApp(t)
	h := map[string]string{"Idempotency-Key": "retry-me"}
	bad := strings.Replace(waveBodyJSON, "node-ds-01", "node-x", 1)
	if r := a.do(http.MethodPost, "/waves", bad, h); r.Code != 422 {
		t.Fatalf("bad = %d %s", r.Code, r.Body)
	}
	if a.idem.Stored() != 0 {
		t.Fatal("a failure must not be stored")
	}
	// a client may fix the request and retry under the same key
	if r := a.do(http.MethodPost, "/waves", waveBodyJSON, h); r.Code != 201 {
		t.Fatalf("retry = %d %s", r.Code, r.Body)
	}
	if a.idem.Stored() != 1 {
		t.Fatalf("stored = %d", a.idem.Stored())
	}
}
