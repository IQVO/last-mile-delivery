package http_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type pageDoc struct {
	Items      []map[string]any `json:"items"`
	NextCursor string           `json:"nextCursor"`
}

// walk follows nextCursor from path (which carries its own query) with limit
// and returns every item in order plus the number of pages.
func (a *app) walk(t *testing.T, path string, limit int) ([]map[string]any, int) {
	t.Helper()
	var all []map[string]any
	cursor, pages := "", 0
	for {
		q := fmt.Sprintf("limit=%d", limit)
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		r := a.get(path + sep + q)
		if r.Code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, r.Code, r.Body)
		}
		var p pageDoc
		if err := json.Unmarshal([]byte(r.Body), &p); err != nil || p.Items == nil {
			t.Fatalf("not an {items,nextCursor} page: %s (%v)", r.Body, err)
		}
		if len(p.Items) > limit {
			t.Fatalf("page of %d items for limit %d", len(p.Items), limit)
		}
		all = append(all, p.Items...)
		pages++
		if p.NextCursor == "" {
			return all, pages
		}
		cursor = p.NextCursor
		if pages > 100 {
			t.Fatal("cursor never ends")
		}
	}
}

func assertWalk(t *testing.T, got []map[string]any, key string, want int) {
	t.Helper()
	seen := map[any]bool{}
	for _, it := range got {
		if seen[it[key]] {
			t.Fatalf("duplicate %v", it[key])
		}
		seen[it[key]] = true
	}
	if len(got) != want {
		t.Fatalf("walked %d rows, want %d", len(got), want)
	}
}

// Seven waves share one induct cutoff, so the waveId tie-break carries the order.
func TestListWavesWalksEveryPageWithTiedSortKeys(t *testing.T) {
	a := newApp(t)
	for i := 0; i < 7; i++ {
		if r := a.post("/waves", waveBodyJSON); r.Code != http.StatusCreated {
			t.Fatalf("schedule %d = %d %s", i, r.Code, r.Body)
		}
	}
	got, pages := a.walk(t, "/waves?stationNodeId=node-ds-01&serviceDate=2026-10-24", 3)
	assertWalk(t, got, "waveId", 7)
	if pages != 3 {
		t.Fatalf("pages = %d, want 3", pages)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1]["waveId"].(string) >= got[i]["waveId"].(string) {
			t.Fatalf("not ordered by waveId: %v then %v", got[i-1]["waveId"], got[i]["waveId"])
		}
	}
}

// Seven packages arrive at one instant: seven stops with one first-induction time.
func TestWaveStopsWalkEveryPageWithTiedSortKeys(t *testing.T) {
	a := newApp(t)
	a.post("/waves", strings.Replace(waveBodyJSON, `"stopCapacity":5`, `"stopCapacity":20`, 1))
	a.arrive("e1", "TRK-1", "TRK-2", "TRK-3", "TRK-4", "TRK-5", "TRK-6", "TRK-7")
	got, pages := a.walk(t, "/waves/wave-1/stops", 2)
	assertWalk(t, got, "stopKey", 7)
	if pages != 4 {
		t.Fatalf("pages = %d, want 4", pages)
	}
}

func TestCollectionsRejectBadPagingParameters(t *testing.T) {
	a := newApp(t)
	a.post("/waves", waveBodyJSON)
	base := "/waves?stationNodeId=node-ds-01&serviceDate=2026-10-24"
	for _, c := range []struct{ name, path, slug string }{
		{"limit zero", base + "&limit=0", "invalid-limit"},
		{"limit too large", base + "&limit=501", "invalid-limit"},
		{"limit not a number", base + "&limit=x", "invalid-limit"},
		{"empty limit", base + "&limit=", "invalid-limit"},
		{"repeated limit", base + "&limit=1&limit=2", "invalid-limit"},
		{"cursor garbage", base + "&cursor=%25%25", "invalid-cursor"},
		{"cursor not base64 json", base + "&cursor=bm90LWpzb24", "invalid-cursor"},
		{"repeated cursor", base + "&cursor=a&cursor=b", "invalid-cursor"},
		{"retired after", base + "&after=x", "unknown-query-parameter"},
		{"retired offset", base + "&offset=1", "unknown-query-parameter"},
		{"retired nextToken", base + "&nextToken=1", "unknown-query-parameter"},
		{"stops limit zero", "/waves/wave-1/stops?limit=0", "invalid-limit"},
		{"stops cursor garbage", "/waves/wave-1/stops?cursor=%25", "invalid-cursor"},
		{"stops retired after", "/waves/wave-1/stops?after=x", "unknown-query-parameter"},
		{"stops filter not declared", "/waves/wave-1/stops?stationNodeId=x", "unknown-query-parameter"},
	} {
		r := a.get(c.path)
		if r.Code != http.StatusBadRequest || r.slug(t) != c.slug {
			t.Errorf("%s: %s = %d %s, want 400 %s", c.name, c.path, r.Code, r.Body, c.slug)
		}
	}
}
