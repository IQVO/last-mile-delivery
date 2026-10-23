package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/application/repository"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/history"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

var errFault = errors.New("injected port failure")

// faults fails the failAt-th port call (1-based) of a run; failAt 0 never fails.
type faults struct{ failAt, n int }

func (f *faults) hit() error {
	f.n++
	if f.n == f.failAt {
		return errFault
	}
	return nil
}

type faultyWaves struct {
	ports.WaveRepository
	f *faults
}

func (r faultyWaves) Get(ctx context.Context, id wave.ID) (*wave.DeliveryWave, error) {
	if err := r.f.hit(); err != nil {
		return nil, err
	}
	return r.WaveRepository.Get(ctx, id)
}

func (r faultyWaves) List(ctx context.Context, fl repository.WaveFilter) ([]*wave.DeliveryWave, error) {
	if err := r.f.hit(); err != nil {
		return nil, err
	}
	return r.WaveRepository.List(ctx, fl)
}

func (r faultyWaves) Save(ctx context.Context, w *wave.DeliveryWave) error {
	if err := r.f.hit(); err != nil {
		return err
	}
	return r.WaveRepository.Save(ctx, w)
}

type faultyRoutes struct {
	ports.RouteRepository
	f *faults
}

func (r faultyRoutes) Get(ctx context.Context, id route.ID) (*route.Route, error) {
	if err := r.f.hit(); err != nil {
		return nil, err
	}
	return r.RouteRepository.Get(ctx, id)
}

func (r faultyRoutes) PackagesInWave(ctx context.Context, id wave.ID) ([]string, error) {
	if err := r.f.hit(); err != nil {
		return nil, err
	}
	return r.RouteRepository.PackagesInWave(ctx, id)
}

func (r faultyRoutes) CountUncompleted(ctx context.Context, id wave.ID) (int, error) {
	if err := r.f.hit(); err != nil {
		return 0, err
	}
	return r.RouteRepository.CountUncompleted(ctx, id)
}

func (r faultyRoutes) Save(ctx context.Context, rt *route.Route) error {
	if err := r.f.hit(); err != nil {
		return err
	}
	return r.RouteRepository.Save(ctx, rt)
}

type faultyPartners struct {
	ports.PartnerRepository
	f *faults
}

func (r faultyPartners) Get(ctx context.Context, id partner.ID) (*partner.DeliveryServicePartner, error) {
	if err := r.f.hit(); err != nil {
		return nil, err
	}
	return r.PartnerRepository.Get(ctx, id)
}

func (r faultyPartners) Save(ctx context.Context, p *partner.DeliveryServicePartner) error {
	if err := r.f.hit(); err != nil {
		return err
	}
	return r.PartnerRepository.Save(ctx, p)
}

type faultyHistories struct {
	ports.HistoryRepository
	f *faults
}

func (r faultyHistories) Get(ctx context.Context, tn string) (*history.History, error) {
	if err := r.f.hit(); err != nil {
		return nil, err
	}
	return r.HistoryRepository.Get(ctx, tn)
}

func (r faultyHistories) ListWaiting(ctx context.Context, station string, limit int) ([]*history.History, error) {
	if err := r.f.hit(); err != nil {
		return nil, err
	}
	return r.HistoryRepository.ListWaiting(ctx, station, limit)
}

func (r faultyHistories) Save(ctx context.Context, h *history.History) error {
	if err := r.f.hit(); err != nil {
		return err
	}
	return r.HistoryRepository.Save(ctx, h)
}

type faultyStations struct {
	ports.StationRepository
	f *faults
}

func (r faultyStations) Get(ctx context.Context, id string) (views.Station, error) {
	if err := r.f.hit(); err != nil {
		return views.Station{}, err
	}
	return r.StationRepository.Get(ctx, id)
}

func (r faultyStations) Save(ctx context.Context, s views.Station) error {
	if err := r.f.hit(); err != nil {
		return err
	}
	return r.StationRepository.Save(ctx, s)
}

type faultyAreas struct {
	ports.ServiceAreaRepository
	f *faults
}

func (r faultyAreas) Save(ctx context.Context, a views.ServiceArea) (bool, error) {
	if err := r.f.hit(); err != nil {
		return false, err
	}
	return r.ServiceAreaRepository.Save(ctx, a)
}

type faultyStops struct {
	ports.PackageStopRepository
	f *faults
}

func (r faultyStops) Get(ctx context.Context, tn string) (views.PackageStop, error) {
	if err := r.f.hit(); err != nil {
		return views.PackageStop{}, err
	}
	return r.PackageStopRepository.Get(ctx, tn)
}

func (r faultyStops) Save(ctx context.Context, s views.PackageStop) error {
	if err := r.f.hit(); err != nil {
		return err
	}
	return r.PackageStopRepository.Save(ctx, s)
}

type faultyInbound struct {
	ports.InboundPackageRepository
	f *faults
}

func (r faultyInbound) Save(ctx context.Context, p views.InboundPackage) error {
	if err := r.f.hit(); err != nil {
		return err
	}
	return r.InboundPackageRepository.Save(ctx, p)
}

type faultyInbox struct {
	ports.Inbox
	f *faults
}

func (r faultyInbox) Claim(ctx context.Context, id, typ string) (bool, error) {
	if err := r.f.hit(); err != nil {
		return false, err
	}
	return r.Inbox.Claim(ctx, id, typ)
}

type faultyOutbox struct {
	ports.EventOutbox
	f *faults
}

func (r faultyOutbox) Append(ctx context.Context, ev ...shared.Event) error {
	if err := r.f.hit(); err != nil {
		return err
	}
	return r.EventOutbox.Append(ctx, ev...)
}

// faulty wraps every port of d so that the failAt-th call fails.
func faulty(d usecases.Deps, f *faults) usecases.Deps {
	d.Waves, d.Routes, d.Partners = faultyWaves{d.Waves, f}, faultyRoutes{d.Routes, f}, faultyPartners{d.Partners, f}
	d.Histories, d.Stations, d.ServiceAreas = faultyHistories{d.Histories, f}, faultyStations{d.Stations, f}, faultyAreas{d.ServiceAreas, f}
	d.PackageStops, d.Inbound, d.Inbox, d.Outbox = faultyStops{d.PackageStops, f}, faultyInbound{d.Inbound, f}, faultyInbox{d.Inbox, f}, faultyOutbox{d.Outbox, f}
	return d
}

// scenario builds a prepared service and the one operation to break.
type scenario struct {
	name  string
	build func(*testing.T) (*env, func(usecases.Deps) error)
}

func ctxb() context.Context { return context.Background() }

func scenarios() []scenario {
	at := t0.Add(8 * time.Hour)
	return []scenario{
		{"ScheduleWave with a waiting package", func(t *testing.T) (*env, func(usecases.Deps) error) {
			e := newEnv(t)
			e.station(t, "node-ds-01")
			e.arrive(t, "ev-1", "TRK-1")
			return e, func(d usecases.Deps) error {
				_, err := usecases.NewScheduleWave(d).Execute(ctxb(), usecases.ScheduleWaveCommand{
					StationNodeID: "node-ds-01", ServiceDate: "2026-10-24", InductCutoffAt: t0.Add(2 * time.Hour),
					WindowStart: t0.Add(6 * time.Hour), WindowEnd: t0.Add(12 * time.Hour), StopCapacity: 2,
				})
				return err
			}
		}},
		{"InductOnArrival of a new package", func(t *testing.T) (*env, func(usecases.Deps) error) {
			e := newEnv(t)
			e.station(t, "node-ds-01")
			e.wave(t, 2, 5)
			e.label(t, "lb-1", "TRK-1", "k-a")
			return e, func(d usecases.Deps) error {
				return usecases.NewInductOnArrival(d).Execute(ctxb(), e.arrival("ev-9", "TRK-1"))
			}
		}},
		{"InductOnArrival of a waiting package", func(t *testing.T) (*env, func(usecases.Deps) error) {
			e := newEnv(t)
			e.station(t, "node-ds-01")
			e.arrive(t, "ev-1", "TRK-1")
			e.wave(t, 2, 0)
			return e, func(d usecases.Deps) error {
				return usecases.NewInductOnArrival(d).Execute(ctxb(), e.arrival("ev-9", "TRK-1"))
			}
		}},
		{"InductOnArrival by the StationView", func(t *testing.T) (*env, func(usecases.Deps) error) {
			e := newEnv(t)
			e.station(t, "node-ds-01")
			return e, func(d usecases.Deps) error {
				cmd := e.arrival("ev-9", "TRK-1")
				cmd.NodeType = ""
				return usecases.NewInductOnArrival(d).Execute(ctxb(), cmd)
			}
		}},
		{"CloseInduction", func(t *testing.T) (*env, func(usecases.Deps) error) {
			e := newEnv(t)
			e.station(t, "node-ds-01")
			e.wave(t, 2, 5)
			return e, func(d usecases.Deps) error {
				_, err := usecases.NewCloseInduction(d).Execute(ctxb(), "wave-1")
				return err
			}
		}},
		{"CancelWave with packages", func(t *testing.T) (*env, func(usecases.Deps) error) {
			e := newEnv(t)
			e.station(t, "node-ds-01")
			e.wave(t, 2, 5)
			e.wave(t, 30, 5)
			e.arrive(t, "ev-1", "TRK-1")
			return e, func(d usecases.Deps) error {
				_, err := usecases.NewCancelWave(d).Execute(ctxb(), usecases.CancelWaveCommand{WaveID: "wave-1", Reason: "closed"})
				return err
			}
		}},
		{"DispatchWave with a leftover", func(t *testing.T) (*env, func(usecases.Deps) error) {
			e := newEnv(t)
			e.station(t, "node-ds-01")
			e.wave(t, 2, 5)
			e.wave(t, 30, 5)
			e.partner(t, 5)
			e.arrive(t, "ev-1", "TRK-1", "TRK-2")
			e.closeAndPlan(t, "wave-1", "dsp-1", stop(1, "TRK-1"))
			return e, func(d usecases.Deps) error {
				_, err := usecases.NewDispatchWave(d).Execute(ctxb(), "wave-1")
				return err
			}
		}},
		{"DispatchWave with no route", func(t *testing.T) (*env, func(usecases.Deps) error) {
			e := newEnv(t)
			e.station(t, "node-ds-01")
			e.wave(t, 2, 5)
			_, err := usecases.NewCloseInduction(e.deps).Execute(ctxb(), "wave-1")
			noErr(t, err)
			return e, func(d usecases.Deps) error {
				_, err := usecases.NewDispatchWave(d).Execute(ctxb(), "wave-1")
				return err
			}
		}},
		{"PlanRoute", func(t *testing.T) (*env, func(usecases.Deps) error) {
			e := newEnv(t)
			e.station(t, "node-ds-01")
			e.wave(t, 2, 5)
			e.partner(t, 5)
			e.arrive(t, "ev-1", "TRK-1")
			_, err := usecases.NewCloseInduction(e.deps).Execute(ctxb(), "wave-1")
			noErr(t, err)
			return e, func(d usecases.Deps) error {
				_, err := usecases.NewPlanRoute(d).Execute(ctxb(), usecases.PlanRouteCommand{WaveID: "wave-1", DSPID: "dsp-1", Stops: []route.Stop{stop(1, "TRK-1")}})
				return err
			}
		}},
		{"StartRoute", func(t *testing.T) (*env, func(usecases.Deps) error) {
			e := dispatchedRoute(t, false)
			return e, func(d usecases.Deps) error {
				_, err := usecases.NewStartRoute(d).Execute(ctxb(), "route-1")
				return err
			}
		}},
		{"RecordAttempt delivered, completing route and wave", func(t *testing.T) (*env, func(usecases.Deps) error) {
			e := dispatchedRoute(t, false)
			e.start(t)
			_, err := e.attempt("TRK-1", route.OutcomeDelivered, "", at)
			noErr(t, err)
			_, err = e.attempt("TRK-2", route.OutcomeDelivered, "", at)
			noErr(t, err)
			return e, func(d usecases.Deps) error {
				_, err := usecases.NewRecordAttempt(d).Execute(ctxb(), usecases.RecordAttemptCommand{RouteID: "route-1", TrackingNumber: "TRK-3", Outcome: route.OutcomeDelivered, At: at})
				return err
			}
		}},
		{"RecordAttempt failed, next wave", func(t *testing.T) (*env, func(usecases.Deps) error) {
			e := dispatchedRoute(t, true)
			e.start(t)
			return e, func(d usecases.Deps) error {
				_, err := usecases.NewRecordAttempt(d).Execute(ctxb(), usecases.RecordAttemptCommand{RouteID: "route-1", TrackingNumber: "TRK-3", Outcome: route.OutcomeFailed, FailureReason: "absent", At: at})
				return err
			}
		}},
		{"ContractPartner", func(t *testing.T) (*env, func(usecases.Deps) error) {
			e := newEnv(t)
			e.station(t, "node-ds-01")
			return e, func(d usecases.Deps) error {
				_, err := usecases.NewContractPartner(d).Execute(ctxb(), usecases.ContractPartnerCommand{StationNodeID: "node-ds-01", VanCapacity: 3})
				return err
			}
		}},
		{"DeactivatePartner", func(t *testing.T) (*env, func(usecases.Deps) error) {
			e := newEnv(t)
			e.station(t, "node-ds-01")
			e.partner(t, 3)
			return e, func(d usecases.Deps) error {
				_, err := usecases.NewDeactivatePartner(d).Execute(ctxb(), "dsp-1")
				return err
			}
		}},
		{"ListWaves", func(t *testing.T) (*env, func(usecases.Deps) error) {
			e := newEnv(t)
			e.station(t, "node-ds-01")
			e.wave(t, 2, 5)
			return e, func(d usecases.Deps) error {
				_, err := usecases.NewListWaves(d).Execute(ctxb(), "node-ds-01", "2026-10-24")
				return err
			}
		}},
		{"GetWaveStops", func(t *testing.T) (*env, func(usecases.Deps) error) {
			e := newEnv(t)
			e.station(t, "node-ds-01")
			e.wave(t, 2, 5)
			e.arrive(t, "ev-1", "TRK-1")
			return e, func(d usecases.Deps) error {
				_, err := usecases.NewGetWaveStops(d).Execute(ctxb(), "wave-1")
				return err
			}
		}},
		{"GetRoute", func(t *testing.T) (*env, func(usecases.Deps) error) {
			e := dispatchedRoute(t, false)
			return e, func(d usecases.Deps) error {
				_, err := usecases.NewGetRoute(d).Execute(ctxb(), "route-1")
				return err
			}
		}},
		{"ApplyNodeRegistered", func(t *testing.T) (*env, func(usecases.Deps) error) {
			e := newEnv(t)
			return e, func(d usecases.Deps) error {
				return usecases.NewApplyNodeRegistered(d).Execute(ctxb(), usecases.NodeRegisteredCommand{EventID: "ev-1", NodeID: "node-ds-01", NodeType: views.DeliveryStation})
			}
		}},
		{"ApplyServiceAreaAssigned", func(t *testing.T) (*env, func(usecases.Deps) error) {
			e := newEnv(t)
			return e, func(d usecases.Deps) error {
				return usecases.NewApplyServiceAreaAssigned(d).Execute(ctxb(), usecases.ServiceAreaAssignedCommand{EventID: "ev-1", PostalPrefix: "01310", StationNodeID: "node-ds-01", AssignmentVersion: 1})
			}
		}},
		{"ApplyLabelIssued", func(t *testing.T) (*env, func(usecases.Deps) error) {
			e := newEnv(t)
			return e, func(d usecases.Deps) error {
				return usecases.NewApplyLabelIssued(d).Execute(ctxb(), usecases.LabelIssuedCommand{EventID: "ev-1", TrackingNumber: "TRK-1", StopKey: "k-a"})
			}
		}},
	}
}

// TestEveryPortFailureIsReturnedAndRollsTheUnitBack breaks, one at a time, every
// port call each use case makes: the error must come back unchanged and a failed
// write must leave the store exactly as it was (the unit of work is atomic).
func TestEveryPortFailureIsReturnedAndRollsTheUnitBack(t *testing.T) {
	for _, sc := range scenarios() {
		t.Run(sc.name, func(t *testing.T) {
			for failAt := 1; ; failAt++ {
				e, op := sc.build(t)
				before := e.store.Counts()
				f := &faults{failAt: failAt}
				err := op(faulty(e.deps, f))
				if f.n < failAt { // the operation finished before reaching the failing call
					noErr(t, err)
					if failAt == 1 {
						t.Fatal("the scenario makes no port call")
					}
					return
				}
				wantIs(t, err, errFault)
				if e.store.Counts() != before {
					t.Fatalf("port call %d failed but the store changed: %+v -> %+v", failAt, before, e.store.Counts())
				}
			}
		})
	}
}
