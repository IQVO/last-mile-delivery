//go:build integration

package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/claudioed/last-mile-delivery/internal/adapters/inbound/http"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/postgres"
	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
	"github.com/claudioed/last-mile-delivery/internal/testing/pgtest"
)

// racingWaves is the real Postgres wave repository with one defect injected: for
// its first `losses` Get calls another connection commits a change to the same
// wave right after the read, so the request's own save finds the version moved on
// and Postgres answers it with zero updated rows (the optimistic-lock conflict a
// concurrent writer causes). Both attempts of the use case's one in-transaction
// retry can lose, which is how the conflict reaches the client as a 409.
type racingWaves struct {
	ports.WaveRepository
	pool   *pgxpool.Pool
	losses atomic.Int32
}

func (r *racingWaves) Get(ctx context.Context, id wave.ID) (*wave.DeliveryWave, error) {
	w, err := r.WaveRepository.Get(ctx, id)
	if err != nil || r.losses.Add(-1) < 0 {
		return w, err
	}
	_, err = r.pool.Exec(context.Background(), `UPDATE delivery_waves SET version = version + 1 WHERE wave_id = $1`, string(id))
	return w, err
}

const replayWaveBody = `{"stationNodeId":"node-ds-01","serviceDate":"2026-10-24","inductCutoffAt":"2099-10-24T08:00:00Z","windowStart":"2099-10-24T12:00:00Z","windowEnd":"2099-10-24T20:00:00Z","stopCapacity":5}`

type replayWorld struct {
	pool *pgxpool.Pool
	ad   adapters
	url  string
}

func newReplayWorld(t *testing.T, waves *racingWaves) *replayWorld {
	t.Helper()
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ad := postgresAdapters(pool)
	if waves != nil {
		waves.WaveRepository, waves.pool = ad.deps.Waves, pool
		ad.deps.Waves = waves
	}
	if err := ad.deps.Stations.Save(ctx, views.Station{NodeID: "node-ds-01", NodeType: views.DeliveryStation, RegionID: "region-se"}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(inboundhttp.NewRouter(buildServer(ad, nil), nil, ""))
	t.Cleanup(srv.Close)
	return &replayWorld{pool: pool, ad: ad, url: srv.URL}
}

func (w *replayWorld) keyRows(t *testing.T, key string) int {
	t.Helper()
	var n int
	if err := w.pool.QueryRow(context.Background(), `SELECT count(*) FROM idempotency_keys WHERE idempotency_key = $1`, key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// waveID schedules a wave under its own key and returns its id.
func (w *replayWorld) waveID(t *testing.T) string {
	t.Helper()
	r := idemPost(t, w.url+"/waves", "k-setup", replayWaveBody)
	if r.code != 201 {
		t.Fatalf("setup wave = %+v", r)
	}
	var id string
	if err := w.pool.QueryRow(context.Background(), `SELECT wave_id FROM delivery_waves`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// A concurrent-update 409 is RETRYABLE: nothing the loser did is kept, no key row
// remains, the same key under the same body executes again and, once the
// conflict is gone, its success is stored and replayed.
func TestConcurrentUpdateReleasesTheKeySoTheRetryRuns(t *testing.T) {
	waves := &racingWaves{}
	w := newReplayWorld(t, waves)
	id := w.waveID(t)
	path := w.url + "/waves/" + id + "/cost-declarations"
	const body = `{"costPerStopCents":250}`

	waves.losses.Store(2) // both attempts of the use case lose to the other writer
	lost := idemPost(t, path, "k-race", body)
	if lost.code != 409 || !strings.Contains(lost.body, `"concurrent-update"`) || lost.replayed != "" {
		t.Fatalf("the loser = %+v, want 409 concurrent-update", lost)
	}
	if n := w.keyRows(t, "k-race"); n != 0 {
		t.Fatalf("the loser's key rows = %d, want 0: a RETRYABLE refusal releases the key", n)
	}

	retry := idemPost(t, path, "k-race", body)
	if retry.code != 201 || retry.replayed != "" {
		t.Fatalf("the retry under the same key = %+v, want a fresh 201 (it must execute)", retry)
	}
	if n := w.keyRows(t, "k-race"); n != 1 {
		t.Fatalf("key rows after the success = %d, want 1: a 2xx is stored", n)
	}
	again := idemPost(t, path, "k-race", body)
	if again.code != 201 || again.replayed != "true" || again.body != retry.body {
		t.Fatalf("third request = %+v, want the stored 201 replayed", again)
	}
	// a different body under a released key is a new request, not a key reuse
	if other := idemPost(t, path, "k-race-2", `{"costPerStopCents":300}`); other.code != 201 {
		t.Fatalf("a new key = %+v", other)
	}
}

// unknown-station is RETRYABLE: the station is a fact another context delivers
// later. The refusal is not stored; once the station is known the same key works.
func TestUnknownStationIsNotStoredSoTheKeyWorksOnceTheStationArrives(t *testing.T) {
	w := newReplayWorld(t, nil)
	body := strings.Replace(replayWaveBody, "node-ds-01", "node-ds-02", 1)

	first := idemPost(t, w.url+"/waves", "k-station", body)
	if first.code != 422 || !strings.Contains(first.body, `"unknown-station"`) {
		t.Fatalf("first = %+v, want 422 unknown-station", first)
	}
	if n := w.keyRows(t, "k-station"); n != 0 {
		t.Fatalf("key rows after unknown-station = %d, want 0", n)
	}
	if err := w.ad.deps.Stations.Save(context.Background(), views.Station{NodeID: "node-ds-02", NodeType: views.DeliveryStation, RegionID: "region-se"}); err != nil {
		t.Fatal(err)
	}
	retry := idemPost(t, w.url+"/waves", "k-station", body)
	if retry.code != 201 || retry.replayed != "" {
		t.Fatalf("retry = %+v, want a fresh 201 now that the station is known", retry)
	}
	if again := idemPost(t, w.url+"/waves", "k-station", body); again.code != 201 || again.replayed != "true" {
		t.Fatalf("third = %+v, want the stored 201 replayed", again)
	}
}

// Control: a FINAL refusal (a function of the request alone) is still stored and
// replayed.
func TestAFinalRefusalIsStillStoredAndReplayed(t *testing.T) {
	w := newReplayWorld(t, nil)
	bad := strings.Replace(replayWaveBody, `"windowStart":"2099-10-24T12:00:00Z"`, `"windowStart":"2099-10-24T07:00:00Z"`, 1)
	if bad == replayWaveBody {
		t.Fatal("the scripted edit did not match")
	}
	first := idemPost(t, w.url+"/waves", "k-final", bad)
	if first.code != 422 || !strings.Contains(first.body, `"invalid-wave-times"`) {
		t.Fatalf("first = %+v, want 422 invalid-wave-times", first)
	}
	if n := w.keyRows(t, "k-final"); n != 1 {
		t.Fatalf("key rows = %d, want 1: a FINAL refusal is stored", n)
	}
	if again := idemPost(t, w.url+"/waves", "k-final", bad); again.code != 422 || again.replayed != "true" || again.body != first.body {
		t.Fatalf("replay = %+v, want the stored 422", again)
	}
}
