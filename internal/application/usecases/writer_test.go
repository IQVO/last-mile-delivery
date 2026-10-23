package usecases_test

import (
	"context"
	"testing"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// flakyWaves fails the first failures Saves with ErrConcurrentUpdate.
type flakyWaves struct {
	ports.WaveRepository
	failures int
	saves    int
}

func (f *flakyWaves) Save(ctx context.Context, w *wave.DeliveryWave) error {
	f.saves++
	if f.saves <= f.failures {
		return ports.ErrConcurrentUpdate
	}
	return f.WaveRepository.Save(ctx, w)
}

func TestTransactRetriesOnceOnAConcurrentUpdate(t *testing.T) {
	e := newEnv(t)
	e.station(t, "node-ds-01")
	flaky := &flakyWaves{WaveRepository: e.deps.Waves, failures: 1}
	deps := e.deps
	deps.Waves = flaky
	v, err := usecases.NewCloseInduction(deps).Execute(context.Background(), wave.ID(e.wave(t, 2, 1).WaveID))
	noErr(t, err)
	if v.State != "INDUCTION_CLOSED" || flaky.saves != 2 {
		t.Fatalf("state=%s saves=%d, want INDUCTION_CLOSED after a retry", v.State, flaky.saves)
	}
}

func TestTransactReportsAPersistentConflictUnchanged(t *testing.T) {
	e := newEnv(t)
	e.station(t, "node-ds-01")
	w := e.wave(t, 2, 1)
	flaky := &flakyWaves{WaveRepository: e.deps.Waves, failures: 5}
	deps := e.deps
	deps.Waves = flaky
	e.wantUnchanged(t, func() {
		_, err := usecases.NewCloseInduction(deps).Execute(context.Background(), wave.ID(w.WaveID))
		wantIs(t, err, ports.ErrConcurrentUpdate)
	})
	if flaky.saves != 2 {
		t.Fatalf("saves = %d, want exactly one retry", flaky.saves)
	}
}
