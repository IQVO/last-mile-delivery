package usecases_test

import (
	"context"
	"testing"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

func TestContractAndDeactivatePartner(t *testing.T) {
	e := newEnv(t)
	e.station(t, "node-ds-01")
	p := e.partner(t, 30)
	if p.DSPID != "dsp-1" || p.State != "ACTIVE" || p.VanCapacity != 30 || p.StationNodeID != "node-ds-01" {
		t.Fatalf("partner = %+v", p)
	}
	e.wantEvents(t) // the model gives the partner no event
	uc := usecases.NewDeactivatePartner(e.deps)
	got, err := uc.Execute(context.Background(), "dsp-1")
	noErr(t, err)
	if got.State != "INACTIVE" {
		t.Fatalf("state = %s", got.State)
	}
	e.wantUnchanged(t, func() {
		_, err := uc.Execute(context.Background(), "dsp-1")
		wantIs(t, err, shared.ErrIllegalTransition)
		_, err = uc.Execute(context.Background(), "dsp-404")
		wantIs(t, err, ports.ErrNotFound)
	})
}

func TestContractPartnerRefusals(t *testing.T) {
	e := newEnv(t)
	uc := usecases.NewContractPartner(e.deps)
	e.wantUnchanged(t, func() {
		_, err := uc.Execute(context.Background(), usecases.ContractPartnerCommand{StationNodeID: "node-ds-01", VanCapacity: 5})
		wantIs(t, err, usecases.ErrUnknownStation)
	})
	e.station(t, "node-ds-01")
	e.wantUnchanged(t, func() {
		_, err := uc.Execute(context.Background(), usecases.ContractPartnerCommand{StationNodeID: "node-ds-01", VanCapacity: 0})
		wantIs(t, err, partner.ErrInvalidVanCapacity)
	})
}
