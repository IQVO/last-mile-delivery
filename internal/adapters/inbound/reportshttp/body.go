package reportshttp

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/analytics/report"
)

// Wire bodies (camelCase, apis/openapi.yaml tag `reports`). A KPI whose
// denominator is zero is null.

type routeDayBody struct {
	Day                      string   `json:"day"`
	StationNodeID            string   `json:"stationNodeId"`
	RoutesCompleted          int      `json:"routesCompleted"`
	StopsPerRoute            *float64 `json:"stopsPerRoute"`
	PackagesPerStop          *float64 `json:"packagesPerStop"`
	DeclaredCostPerStopCents *float64 `json:"declaredCostPerStopCents"`
	CostPerPackageCents      *float64 `json:"costPerPackageCents"`
}

type routeReportBody struct {
	From string         `json:"from"`
	To   string         `json:"to"`
	Days []routeDayBody `json:"days"`
}

func routesBody(rg report.Range, rows []report.RouteRow) routeReportBody {
	out := routeReportBody{From: rg.FirstDay(), To: rg.LastDay(), Days: make([]routeDayBody, 0, len(rows))}
	for _, r := range rows {
		out.Days = append(out.Days, routeDayBody{
			Day: report.FormatDay(r.Day), StationNodeID: r.Station, RoutesCompleted: r.Routes,
			StopsPerRoute: r.StopsPerRoute, PackagesPerStop: r.PackagesPerStop,
			DeclaredCostPerStopCents: r.DeclaredCostPerStopCents, CostPerPackageCents: r.CostPerPackageCents,
		})
	}
	return out
}

type attemptDayBody struct {
	Day                             string   `json:"day"`
	StationNodeID                   string   `json:"stationNodeId"`
	Attempts                        int      `json:"attempts"`
	DeliveredAttempts               int      `json:"deliveredAttempts"`
	FailedAttempts                  int      `json:"failedAttempts"`
	PackagesAttempted               int      `json:"packagesAttempted"`
	PackagesWithFirstAttemptSuccess int      `json:"packagesWithFirstAttemptSuccess"`
	FirstAttemptSuccessRate         *float64 `json:"firstAttemptSuccessRate"`
	MeanAttemptsPerPackage          *float64 `json:"meanAttemptsPerPackage"`
	ReturnsInitiated                int      `json:"returnsInitiated"`
}

type attemptReportBody struct {
	From string           `json:"from"`
	To   string           `json:"to"`
	Days []attemptDayBody `json:"days"`
}

func attemptsBody(rg report.Range, rows []report.AttemptRow) attemptReportBody {
	out := attemptReportBody{From: rg.FirstDay(), To: rg.LastDay(), Days: make([]attemptDayBody, 0, len(rows))}
	for _, r := range rows {
		out.Days = append(out.Days, attemptDayBody{
			Day: report.FormatDay(r.Day), StationNodeID: r.Station, Attempts: r.Attempts,
			DeliveredAttempts: r.DeliveredAttempts, FailedAttempts: r.FailedAttempts, PackagesAttempted: r.PackagesAttempted,
			PackagesWithFirstAttemptSuccess: r.PackagesFirstAttemptSuccess, FirstAttemptSuccessRate: r.FirstAttemptSuccessRate,
			MeanAttemptsPerPackage: r.MeanAttemptsPerPackage, ReturnsInitiated: r.ReturnsInitiated,
		})
	}
	return out
}

type waveDayBody struct {
	Day            string   `json:"day"`
	StationNodeID  string   `json:"stationNodeId"`
	WavesScheduled int      `json:"wavesScheduled"`
	WavesCancelled int      `json:"wavesCancelled"`
	StopCapacity   int      `json:"stopCapacity"`
	StopsInducted  int      `json:"stopsInducted"`
	Utilisation    *float64 `json:"utilisation"`
}

type waveReportBody struct {
	From string        `json:"from"`
	To   string        `json:"to"`
	Days []waveDayBody `json:"days"`
}

func wavesBody(rg report.Range, rows []report.WaveRow) waveReportBody {
	out := waveReportBody{From: rg.FirstDay(), To: rg.LastDay(), Days: make([]waveDayBody, 0, len(rows))}
	for _, r := range rows {
		out.Days = append(out.Days, waveDayBody{
			Day: report.FormatDay(r.Day), StationNodeID: r.Station, WavesScheduled: r.WavesScheduled,
			WavesCancelled: r.WavesCancelled, StopCapacity: r.StopCapacity, StopsInducted: r.StopsInducted, Utilisation: r.Utilisation,
		})
	}
	return out
}

type freshnessBody struct {
	AsOf       *time.Time `json:"asOf"`
	LagSeconds *float64   `json:"lagSeconds"`
}

// problem is one RFC 9457 problem type: its status, slug and title.
type problem struct {
	status int
	slug   string
	title  string
}

var (
	invalidRange          = problem{http.StatusBadRequest, "invalid-report-range", "The report range is invalid"}
	notFound              = problem{http.StatusNotFound, "resource-not-found", "Resource not found"}
	methodNotAllowed      = problem{http.StatusMethodNotAllowed, "method-not-allowed", "Method not allowed"}
	dependencyUnavailable = problem{http.StatusServiceUnavailable, "dependency-unavailable", "A runtime dependency is unavailable"}
	internalError         = problem{http.StatusInternalServerError, "internal-error", "Internal server error"}
)

type problemBody struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

func writeProblem(w http.ResponseWriter, r *http.Request, p problem, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.status)
	_ = json.NewEncoder(w).Encode(problemBody{Type: p.slug, Title: p.title, Status: p.status, Detail: detail, Instance: r.URL.EscapedPath()})
}

// logSafe strips line breaks so a logged value cannot forge a log line.
var logSafe = strings.NewReplacer("\n", " ", "\r", " ")

// writeInternal logs err and answers a 500 whose detail never leaks it.
func writeInternal(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "report query failed", "error", logSafe.Replace(err.Error()), "path", logSafe.Replace(r.URL.Path))
	writeProblem(w, r, internalError, "an unexpected error occurred")
}
