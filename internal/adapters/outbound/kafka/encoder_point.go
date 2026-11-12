package kafka

import (
	"github.com/claudioed/last-mile-delivery/internal/domain/deliverypoint"
)

// pointFlaggedData is the CloudEvents `data` of DeliveryPointFlagged (ADR 0014):
// the opaque stop key, why, how many strikes are open, the packages newly
// affected (1 to 5) and the attempt instant that triggered it. No address, name
// or free text; no optional fields.
type pointFlaggedData struct {
	StopKey         string   `json:"stopKey"`
	Reason          string   `json:"reason"`
	Failures        int      `json:"failures"`
	TrackingNumbers []string `json:"trackingNumbers"`
	FlaggedAt       string   `json:"flaggedAt"`
}

func pointFlaggedPayload(e deliverypoint.DeliveryPointFlagged) pointFlaggedData {
	tns := e.TrackingNumbers
	if tns == nil {
		tns = []string{}
	}
	return pointFlaggedData{StopKey: e.StopKey, Reason: string(e.Reason), Failures: e.Failures, TrackingNumbers: tns, FlaggedAt: ts(e.FlaggedAt)}
}
