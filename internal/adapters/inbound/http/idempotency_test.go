package http_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestAStateChangingPostNeedsAnIdempotencyKey(t *testing.T) {
	a := newApp(t)
	for _, path := range []string{"/waves", "/routes", "/partners", "/waves/w/cancellations", "/waves/w/dispatches", "/waves/w/induction-closures", "/routes/r/starts", "/routes/r/attempts", "/partners/d/deactivations"} {
		r := a.do(http.MethodPost, path, "{}", nil)
		if r.Code != http.StatusBadRequest || r.slug(t) != "idempotency-key-required" {
			t.Errorf("POST %s without a key = %d %s", path, r.Code, r.Body)
		}
	}
	for name, key := range map[string]string{"129 characters": strings.Repeat("k", 129), "control character": "bad\x01key", "non ASCII": "chavé"} {
		r := a.do(http.MethodPost, "/waves", waveBodyJSON, map[string]string{"Idempotency-Key": key})
		if r.Code != http.StatusBadRequest || r.slug(t) != "idempotency-key-invalid" {
			t.Errorf("%s key = %d %s", name, r.Code, r.Body)
		}
	}
	if r := a.do(http.MethodPost, "/waves", waveBodyJSON, map[string]string{"Idempotency-Key": strings.Repeat("k", 128)}); r.Code != http.StatusCreated {
		t.Errorf("a 128 character key = %d", r.Code)
	}
	if n := a.store.Counts().Waves; n != 1 {
		t.Fatalf("only the 128 character key may create a wave, got %d", n)
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
	if second.Header.Get("Idempotency-Replayed") != "true" || first.Header.Get("Idempotency-Replayed") != "" {
		t.Fatalf("replay header: first %q second %q", first.Header.Get("Idempotency-Replayed"), second.Header.Get("Idempotency-Replayed"))
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
	if r.Code != http.StatusConflict || r.slug(t) != "idempotency-key-reused" {
		t.Fatalf("reuse = %d %s", r.Code, r.Body)
	}
	if a.store.Counts().Waves != 1 {
		t.Fatal("the second body must not run")
	}
}

func TestAKeyIsGlobalAcrossRoutes(t *testing.T) {
	a := newApp(t)
	h := map[string]string{"Idempotency-Key": "shared"}
	if r := a.do(http.MethodPost, "/partners", `{"stationNodeId":"node-ds-01","vanCapacity":2}`, h); r.Code != 201 {
		t.Fatalf("first = %d %s", r.Code, r.Body)
	}
	// the same key on another route is the same key with another fingerprint
	if r := a.do(http.MethodPost, "/waves", waveBodyJSON, h); r.Code != http.StatusConflict || r.slug(t) != "idempotency-key-reused" {
		t.Fatalf("other route = %d %s", r.Code, r.Body)
	}
}

func TestAFourXXIsStoredAndReplayed(t *testing.T) {
	a := newApp(t)
	h := map[string]string{"Idempotency-Key": "bad-once"}
	bad := strings.Replace(waveBodyJSON, "node-ds-01", "node-x", 1)
	first := a.do(http.MethodPost, "/waves", bad, h)
	second := a.do(http.MethodPost, "/waves", bad, h)
	if first.Code != 422 || second.Code != 422 || first.Body != second.Body || second.Header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("first %d %s / second %d %s", first.Code, first.Body, second.Code, second.Body)
	}
	if second.Header.Get("Content-Type") != "application/problem+json" {
		t.Fatalf("replayed Content-Type = %q", second.Header.Get("Content-Type"))
	}
}

func TestRetiredRoutesAreGone(t *testing.T) {
	a := newApp(t)
	for _, path := range []string{"/waves/w:close-induction", "/waves/w:dispatch", "/waves/w:cancel", "/routes/r:start", "/partners/d:deactivate"} {
		r := a.post(path, "{}")
		if r.Code != http.StatusNotFound && r.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want 404 or 405", path, r.Code)
		}
	}
}
