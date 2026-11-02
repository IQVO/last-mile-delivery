//go:build integration

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	inboundhttp "github.com/claudioed/last-mile-delivery/internal/adapters/inbound/http"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/postgres"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/testing/pgtest"
)

type idemReply struct {
	code     int
	body     string
	replayed string
}

func idemPost(t *testing.T, url, key, body string) idemReply {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return idemReply{res.StatusCode, string(b), res.Header.Get("Idempotency-Replayed")}
}

// The Idempotency-Key contract of the REST guideline over the real router and a
// real Postgres: fresh, byte-identical replay, 409 on another body or route,
// 400 without a key, a cached 4xx, and one effect for concurrent requests.
func TestIdempotencyContractOverRealPostgres(t *testing.T) {
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ad := postgresAdapters(pool)
	if err := ad.deps.Stations.Save(ctx, views.Station{NodeID: "node-ds-01", NodeType: views.DeliveryStation, RegionID: "region-se"}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(inboundhttp.NewRouter(buildServer(ad, nil), nil, ""))
	t.Cleanup(srv.Close)
	count := func(table string) int {
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	partner := `{"stationNodeId":"node-ds-01","vanCapacity":3}`

	first := idemPost(t, srv.URL+"/partners", "k-1", partner)
	second := idemPost(t, srv.URL+"/partners", "k-1", partner)
	if first.code != 201 || first.replayed != "" || second.code != 201 || second.replayed != "true" || first.body != second.body {
		t.Fatalf("fresh then replay: %+v / %+v", first, second)
	}
	if n := count("delivery_service_partners"); n != 1 {
		t.Fatalf("partners = %d after a replay", n)
	}
	if r := idemPost(t, srv.URL+"/partners", "k-1", `{"stationNodeId":"node-ds-01","vanCapacity":4}`); r.code != 409 || !strings.Contains(r.body, "idempotency-key-reused") {
		t.Fatalf("other body = %+v", r)
	}
	if r := idemPost(t, srv.URL+"/waves", "k-1", partner); r.code != 409 {
		t.Fatalf("other route = %+v", r)
	}
	if r := idemPost(t, srv.URL+"/partners", "", partner); r.code != 400 || !strings.Contains(r.body, "idempotency-key-required") {
		t.Fatalf("no key = %+v", r)
	}
	if r := idemPost(t, srv.URL+"/partners", strings.Repeat("k", 129), partner); r.code != 400 || !strings.Contains(r.body, "idempotency-key-invalid") {
		t.Fatalf("long key = %+v", r)
	}

	bad := `{"stationNodeId":"node-x","vanCapacity":3}`
	a, b := idemPost(t, srv.URL+"/partners", "k-bad", bad), idemPost(t, srv.URL+"/partners", "k-bad", bad)
	if a.code != 422 || b.code != 422 || b.replayed != "true" || a.body != b.body {
		t.Fatalf("a 4xx must be cached: %+v / %+v", a, b)
	}

	var wg sync.WaitGroup
	var created, replays atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := idemPost(t, srv.URL+"/partners", "k-race", `{"stationNodeId":"node-ds-01","vanCapacity":5}`)
			if r.code != 201 {
				t.Errorf("concurrent = %+v", r)
			}
			if r.replayed == "true" {
				replays.Add(1)
			} else {
				created.Add(1)
			}
		}()
	}
	wg.Wait()
	if created.Load() != 1 || replays.Load() != 7 {
		t.Fatalf("created %d replays %d", created.Load(), replays.Load())
	}
	if n := count("delivery_service_partners"); n != 2 {
		t.Fatalf("partners = %d, want 2 (k-1 and k-race)", n)
	}
	if n := count("idempotency_keys"); n != 3 {
		t.Fatalf("idempotency_keys = %d, want 3 (%s)", n, fmt.Sprint("k-1, k-bad, k-race"))
	}
}
