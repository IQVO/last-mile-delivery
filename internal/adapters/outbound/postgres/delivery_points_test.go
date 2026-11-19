package postgres

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/deliverypoint"
)

var strikeAt = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

func TestAStrikeStoresEveryPackageAndWhichWereTold(t *testing.T) {
	in := []deliverypoint.Strike{
		{RouteID: "route-1", TrackingNumbers: []string{"TRK-1", "TRK-2"}, Category: deliverypoint.CategoryNotHome, At: strikeAt, Told: []string{"TRK-1"}},
		{RouteID: "route-2", TrackingNumbers: []string{"TRK-3"}, Category: deliverypoint.CategoryAccessProblem, At: strikeAt, Told: []string{"TRK-3"}},
		{RouteID: "route-3", TrackingNumbers: []string{"TRK-4"}, Category: deliverypoint.CategoryNotHome, At: strikeAt},
	}
	raw, err := encodeStrikes(in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeStrikes([]byte(raw))
	if err != nil || !reflect.DeepEqual(got, in) {
		t.Fatalf("decode = %+v, %v; want %+v", got, err, in)
	}
	// The previous release reads trackingNumber and notified: keep writing them.
	var legacy []legacyStrike
	if err := json.Unmarshal([]byte(raw), &legacy); err != nil {
		t.Fatal(err)
	}
	if want := "TRK-1/false TRK-3/true TRK-4/false"; legacyView(legacy) != want {
		t.Fatalf("legacy fields = %s, want %s", legacyView(legacy), want)
	}
}

// legacyStrike is the part of a stored strike the previous release reads.
type legacyStrike struct {
	TrackingNumber string `json:"trackingNumber"`
	Notified       bool   `json:"notified"`
}

func legacyView(in []legacyStrike) string {
	var out []string
	for _, s := range in {
		out = append(out, s.TrackingNumber+"/"+strconv.FormatBool(s.Notified))
	}
	return strings.Join(out, " ")
}

func TestARowOfThePreviousReleaseDecodesAsAStrikeOfOnePackage(t *testing.T) {
	raw := `[{"routeId":"route-1","trackingNumber":"TRK-1","category":"NOT_HOME","at":"2026-11-02T09:00:00Z","notified":true},` +
		`{"routeId":"route-2","trackingNumber":"TRK-2","category":"NOT_HOME","at":"2026-11-02T09:00:00Z","notified":false}]`
	got, err := decodeStrikes([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	want := []deliverypoint.Strike{
		{RouteID: "route-1", TrackingNumbers: []string{"TRK-1"}, Category: deliverypoint.CategoryNotHome, At: strikeAt, Told: []string{"TRK-1"}},
		{RouteID: "route-2", TrackingNumbers: []string{"TRK-2"}, Category: deliverypoint.CategoryNotHome, At: strikeAt},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decode = %+v, want %+v", got, want)
	}
}

func TestARowWithNoPackageDecodesToAStrikeRestoreRefuses(t *testing.T) {
	got, err := decodeStrikes([]byte(`[{"routeId":"route-1","category":"NOT_HOME","at":"2026-11-02T09:00:00Z"}]`))
	if err != nil || len(got) != 1 || len(got[0].TrackingNumbers) != 0 {
		t.Fatalf("decode = %+v, %v", got, err)
	}
	if _, err := decodeStrikes([]byte(`{`)); err == nil {
		t.Fatal("malformed json must fail")
	}
}
