package analyticsstore

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/claudioed/last-mile-delivery/internal/analytics/report"
)

// etaSQL: the DELIVERED attempts of the range, each with its route's station
// (UNKNOWN until the out-for-delivery fact is projected) and the ETA window
// that StartRoute assigned to the same route and package (none when the ETA
// row was never projected); per UTC day of the attempt and station, the
// deliveries that had a window and how many fell inside it (both bounds
// included), before it and after it (ADR 0013).
//
//	$1 from, $2 to, $3 unknown station, $4 DELIVERED
const etaSQL = `
	SELECT (a.attempted_at AT TIME ZONE 'UTC')::date AS day, COALESCE(p.station_node_id, $3) AS station,
	       count(e.route_id),
	       count(*) FILTER (WHERE a.attempted_at >= e.eta_start AND a.attempted_at <= e.eta_end),
	       count(*) FILTER (WHERE a.attempted_at < e.eta_start),
	       count(*) FILTER (WHERE a.attempted_at > e.eta_end)
	FROM delivery_attempts a
	LEFT JOIN route_packages p ON p.route_id = a.route_id AND p.tracking_number = a.tracking_number
	LEFT JOIN stop_eta_windows e ON e.route_id = a.route_id AND e.tracking_number = a.tracking_number
	WHERE a.outcome = $4 AND a.attempted_at >= $1 AND a.attempted_at < $2
	GROUP BY day, station
	ORDER BY day, station COLLATE "C"`

// EtaWindow implements report.Reader.
func (r *Reader) EtaWindow(ctx context.Context, rg report.Range) ([]report.EtaDay, error) {
	return collect(ctx, r.pool, etaSQL, func(rows pgx.Rows) (report.EtaDay, error) {
		var d report.EtaDay
		err := rows.Scan(&d.Day, &d.Station, &d.DeliveriesWithEta, &d.DeliveriesInside, &d.DeliveriesBefore, &d.DeliveriesAfter)
		d.Day = d.Day.UTC()
		return d, err
	}, rg.From, rg.To, report.UnknownStation(), report.OutcomeDelivered)
}
