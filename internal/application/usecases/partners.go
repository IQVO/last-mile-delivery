package usecases

import (
	"context"

	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
)

// ContractPartnerCommand is the input of ContractPartner.
type ContractPartnerCommand struct {
	StationNodeID string
	VanCapacity   int
}

// ContractPartner contracts a delivery service partner to a station.
type ContractPartner struct{ writer }

// NewContractPartner builds the use case.
func NewContractPartner(d Deps) *ContractPartner { return &ContractPartner{writer{d}} }

// Execute checks the station (ErrUnknownStation) and saves an ACTIVE partner.
func (uc *ContractPartner) Execute(ctx context.Context, cmd ContractPartnerCommand) (views.Partner, error) {
	return transact(ctx, uc.UoW, func(ctx context.Context) (views.Partner, error) {
		if err := uc.requireStation(ctx, cmd.StationNodeID); err != nil {
			return views.Partner{}, err
		}
		p, err := partner.Contract(partner.NewPartner{ID: uc.IDs.NewPartnerID(), StationNodeID: cmd.StationNodeID, VanCapacity: cmd.VanCapacity})
		if err != nil {
			return views.Partner{}, err
		}
		if err := uc.Partners.Save(ctx, p); err != nil {
			return views.Partner{}, err
		}
		return views.PartnerFrom(p), nil
	})
}

// DeactivatePartner deactivates a partner (G34).
type DeactivatePartner struct{ writer }

// NewDeactivatePartner builds the use case.
func NewDeactivatePartner(d Deps) *DeactivatePartner { return &DeactivatePartner{writer{d}} }

// Execute moves the partner ACTIVE to INACTIVE.
func (uc *DeactivatePartner) Execute(ctx context.Context, id partner.ID) (views.Partner, error) {
	return transact(ctx, uc.UoW, func(ctx context.Context) (views.Partner, error) {
		p, err := uc.Partners.Get(ctx, id)
		if err != nil {
			return views.Partner{}, err
		}
		if err := p.Deactivate(); err != nil {
			return views.Partner{}, err
		}
		if err := uc.Partners.Save(ctx, p); err != nil {
			return views.Partner{}, err
		}
		return views.PartnerFrom(p), nil
	})
}
