package kafka

import "github.com/claudioed/last-mile-delivery/internal/domain/route"

// stopEtaData is the `data` of StopEtaWindowAssigned (ADR 0013). trackingNumbers
// is never null; the CloudEvents time is the route start instant.
type stopEtaData struct {
	RouteID         string   `json:"routeId"`
	StopNo          int      `json:"stopNo"`
	StopKey         string   `json:"stopKey"`
	TrackingNumbers []string `json:"trackingNumbers"`
	EtaStart        string   `json:"etaStart"`
	EtaEnd          string   `json:"etaEnd"`
}

func stopEtaPayload(e route.StopEtaWindowAssigned) stopEtaData {
	tns := e.TrackingNumbers
	if tns == nil {
		tns = []string{}
	}
	return stopEtaData{
		RouteID: string(e.RouteID), StopNo: e.StopNo, StopKey: e.StopKey, TrackingNumbers: tns,
		EtaStart: ts(e.EtaStart), EtaEnd: ts(e.EtaEnd),
	}
}
