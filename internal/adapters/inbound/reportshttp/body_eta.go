package reportshttp

import "github.com/claudioed/last-mile-delivery/internal/analytics/report"

// The wire body of GET /reports/eta-window (ADR 0013). etaHitRate is null when
// no delivery of the day and station had an ETA.

type etaDayBody struct {
	Day               string   `json:"day"`
	StationNodeID     string   `json:"stationNodeId"`
	DeliveriesWithEta int      `json:"deliveriesWithEta"`
	DeliveriesInside  int      `json:"deliveriesInsideEta"`
	DeliveriesBefore  int      `json:"deliveriesBeforeEta"`
	DeliveriesAfter   int      `json:"deliveriesAfterEta"`
	EtaHitRate        *float64 `json:"etaHitRate"`
}

type etaReportBody struct {
	From string       `json:"from"`
	To   string       `json:"to"`
	Days []etaDayBody `json:"days"`
}

func etaBody(rg report.Range, rows []report.EtaRow) etaReportBody {
	out := etaReportBody{From: rg.FirstDay(), To: rg.LastDay(), Days: make([]etaDayBody, 0, len(rows))}
	for _, r := range rows {
		out.Days = append(out.Days, etaDayBody{
			Day: report.FormatDay(r.Day), StationNodeID: r.Station, DeliveriesWithEta: r.DeliveriesWithEta,
			DeliveriesInside: r.DeliveriesInside, DeliveriesBefore: r.DeliveriesBefore, DeliveriesAfter: r.DeliveriesAfter,
			EtaHitRate: r.EtaHitRate,
		})
	}
	return out
}
