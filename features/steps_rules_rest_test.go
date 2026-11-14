package features_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/cucumber/godog"
)

var freshKeys atomic.Int64

// freshKey is an Idempotency-Key no earlier request used.
func freshKey() string { return fmt.Sprintf("fresh-key-%d", freshKeys.Add(1)) }

// nextPageOf asks the same collection for the page after the one received last.
func (h *httpWorld) nextPageOf(path string) error {
	var page struct {
		NextCursor string `json:"nextCursor"`
	}
	if err := json.Unmarshal([]byte(h.reply.body), &page); err != nil || page.NextCursor == "" {
		return fmt.Errorf("the previous response has no nextCursor (%s)", h.reply.body)
	}
	return h.send(http.MethodGet, path+"&cursor="+url.QueryEscape(page.NextCursor), "", "")
}

func (h *httpWorld) assertNoField(path string) error {
	m, err := h.json()
	if err != nil {
		return err
	}
	if got, ok := dig(m, path); ok {
		return fmt.Errorf("the response has the field %s = %v, want none (%s)", path, got, h.reply.body)
	}
	return nil
}

func registerRuleHTTPSteps(sc *godog.ScenarioContext, h *httpWorld) {
	sc.Step(`^the client posts to "([^"]*)" with a fresh key$`, func(path string) error {
		return h.send(http.MethodPost, path, freshKey(), "")
	})
	sc.Step(`^the client posts to "([^"]*)" with a fresh key and body:$`, func(path string, body *godog.DocString) error {
		return h.send(http.MethodPost, path, freshKey(), body.Content)
	})
	sc.Step(`^the client posts to "([^"]*)" with the key "([^"]*)"$`, func(path, key string) error {
		return h.send(http.MethodPost, path, key, "")
	})
	sc.Step(`^the client requests "([^"]*)" with the cursor of the previous page$`, h.nextPageOf)
	sc.Step(`^the response has no field "([^"]*)"$`, h.assertNoField)
	sc.Step(`^the client cancels wave "([^"]*)" with a reason of (\d+) characters$`, func(id string, n int) error {
		body := fmt.Sprintf(`{"reason":%q}`, strings.Repeat("r", n))
		return h.send(http.MethodPost, "/waves/"+id+"/cancellations", freshKey(), body)
	})
	sc.Step(`^the client sends the attempt of "([^"]*)" as FAILED with a reason of (\d+) characters to route "([^"]*)"$`, func(tn string, n int, id string) error {
		body := fmt.Sprintf(`{"trackingNumber":%q,"outcome":"FAILED","failureReason":%q,"at":"2026-10-24T14:00:00Z"}`, tn, strings.Repeat("r", n))
		return h.send(http.MethodPost, "/routes/"+id+"/attempts", freshKey(), body)
	})
}
