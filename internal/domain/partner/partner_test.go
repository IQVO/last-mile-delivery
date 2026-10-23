package partner

import (
	"errors"
	"testing"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

func wantIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

func contracted(t *testing.T) *DeliveryServicePartner {
	t.Helper()
	p, err := Contract(NewPartner{ID: "dsp-1", StationNodeID: "node-ds-01", VanCapacity: 30})
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	return p
}

func TestContract(t *testing.T) {
	p := contracted(t)
	if p.ID() != "dsp-1" || p.StationNodeID() != "node-ds-01" || p.VanCapacity() != 30 || p.State() != StateActive || !p.Active() || p.Version() != 1 {
		t.Fatalf("partner = %+v", p.Snapshot())
	}
}

func TestContractValidation(t *testing.T) {
	cases := []struct {
		name string
		in   NewPartner
		want error
	}{
		{"zero capacity", NewPartner{ID: "dsp-1", StationNodeID: "n", VanCapacity: 0}, ErrInvalidVanCapacity},
		{"negative capacity", NewPartner{ID: "dsp-1", StationNodeID: "n", VanCapacity: -4}, ErrInvalidVanCapacity},
		{"capacity above the ceiling", NewPartner{ID: "dsp-1", StationNodeID: "n", VanCapacity: MaxVanCapacity + 1}, ErrInvalidVanCapacity},
		{"bad id", NewPartner{ID: "", StationNodeID: "n", VanCapacity: 1}, shared.ErrValidation},
		{"bad station", NewPartner{ID: "dsp-1", StationNodeID: "-n", VanCapacity: 1}, shared.ErrValidation},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := Contract(c.in)
			wantIs(t, err, c.want)
			if p != nil {
				t.Fatal("a refused contract returned a partner")
			}
		})
	}
	for _, capacity := range []int{1, MaxVanCapacity} {
		if _, err := Contract(NewPartner{ID: "dsp-1", StationNodeID: "n", VanCapacity: capacity}); err != nil {
			t.Errorf("capacity %d must be accepted: %v", capacity, err)
		}
	}
}

func TestDeactivate(t *testing.T) {
	p := contracted(t)
	if err := p.Deactivate(); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	if p.State() != StateInactive || p.Active() || p.Version() != 2 {
		t.Fatalf("partner = %+v", p.Snapshot())
	}
	wantIs(t, p.Deactivate(), shared.ErrIllegalTransition)
	if p.Version() != 2 {
		t.Fatal("a refused deactivation changed the version")
	}
}

func TestRestore(t *testing.T) {
	good := Snapshot{ID: "dsp-1", StationNodeID: "n", VanCapacity: 1, State: StateInactive, Version: 1}
	p, err := Restore(good)
	if err != nil || p.Snapshot() != good {
		t.Fatalf("Restore = %+v, %v", p, err)
	}
	if _, err := Restore(Snapshot{ID: "dsp-1", StationNodeID: "n", VanCapacity: MaxVanCapacity, State: StateActive, Version: 9}); err != nil {
		t.Fatalf("ceiling capacity: %v", err)
	}
	bad := []Snapshot{
		{ID: "", StationNodeID: "n", VanCapacity: 1, State: StateActive, Version: 1},
		{ID: "dsp-1", StationNodeID: "", VanCapacity: 1, State: StateActive, Version: 1},
		{ID: "dsp-1", StationNodeID: "n", VanCapacity: 0, State: StateActive, Version: 1},
		{ID: "dsp-1", StationNodeID: "n", VanCapacity: MaxVanCapacity + 1, State: StateActive, Version: 1},
		{ID: "dsp-1", StationNodeID: "n", VanCapacity: 1, State: "GONE", Version: 1},
		{ID: "dsp-1", StationNodeID: "n", VanCapacity: 1, State: StateActive, Version: 0},
	}
	for i, s := range bad {
		if p, err := Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) || p != nil {
			t.Errorf("case %d: Restore = %v, %v, want ErrInvalidSnapshot", i, p, err)
		}
	}
}
