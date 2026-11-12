package kafka

import (
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

// attemptedGolden wraps the golden prefix and suffix around an exact data object so the
// envelope of DeliveryAttempted stays defined in one place.
func attemptedGolden(data string) string {
	return `{"specversion":"1.0","id":"` + fixedID + `","source":"/retailnetwork/last-mile-delivery","type":"com.retailnetwork.logistics.last-mile-delivery.route.DeliveryAttempted","subject":"route-81c2","datacontenttype":"application/json","dataschema":"urn:retailnetwork:last-mile-delivery:events:DeliveryAttempted:v1","time":"2026-10-24T06:15:00Z","data":` + data + `}`
}

func TestDeliveryAttemptedOutcomeDetailGolden(t *testing.T) {
	at := time.Date(2026, 10, 24, 6, 15, 0, 0, time.UTC)
	h := shared.Header{Version: 2, At: at}
	cases := []struct {
		name string
		ev   route.DeliveryAttempted
		data string
	}{
		{
			"failed with a detail",
			route.DeliveryAttempted{Header: h, RouteID: "route-81c2", TrackingNumber: "TRK-000123", Outcome: route.OutcomeFailed, Detail: route.DetailNotHome, FailureReason: "recipient absent", At: at},
			`{"routeId":"route-81c2","trackingNumber":"TRK-000123","outcome":"FAILED","outcomeDetail":"NOT_HOME","failureReason":"recipient absent","at":"2026-10-24T06:15:00Z"}`,
		},
		{
			"delivered in a safe place",
			route.DeliveryAttempted{Header: h, RouteID: "route-81c2", TrackingNumber: "TRK-000123", Outcome: route.OutcomeDelivered, Detail: route.DetailSafePlaceUsed, At: at},
			`{"routeId":"route-81c2","trackingNumber":"TRK-000123","outcome":"DELIVERED","outcomeDetail":"SAFE_PLACE_USED","at":"2026-10-24T06:15:00Z"}`,
		},
		{
			"failed without a detail",
			route.DeliveryAttempted{Header: h, RouteID: "route-81c2", TrackingNumber: "TRK-000123", Outcome: route.OutcomeFailed, FailureReason: "recipient absent", At: at},
			`{"routeId":"route-81c2","trackingNumber":"TRK-000123","outcome":"FAILED","failureReason":"recipient absent","at":"2026-10-24T06:15:00Z"}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msgs, err := fixedEncoder().Encode(c.ev)
			if err != nil || len(msgs) != 1 {
				t.Fatalf("Encode = %v, %v", msgs, err)
			}
			if got, want := string(msgs[0].Value), attemptedGolden(c.data); got != want {
				t.Fatalf("CloudEvent JSON\n got %s\nwant %s", got, want)
			}
		})
	}
}
