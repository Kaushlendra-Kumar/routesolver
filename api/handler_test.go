package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Kaushlendra-Kumar/routesolver/osrm"
)

// quietServer builds a Server that discards request logs, with an optional
// OSRM client.
func quietServer(o *osrm.Client) *Server {
	return NewServer(Config{
		OSRM:   o,
		Logger: log.New(io.Discard, "", 0),
	})
}

func postJSON(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/optimize", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const fourStops = `{
  "stops": [
    {"name": "Depot", "lat": 12.7105, "lng": 77.6960},
    {"name": "Whitefield", "lat": 12.9698, "lng": 77.7500},
    {"name": "Jayanagar", "lat": 12.9308, "lng": 77.5838},
    {"name": "Hebbal", "lat": 13.0358, "lng": 77.5970}
  ]
}`

func TestOptimizeHaversineHappyPath(t *testing.T) {
	h := quietServer(nil).Handler()
	rec := postJSON(t, h, fourStops)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp optimizeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Metric != MetricHaversine || resp.Unit != "km" {
		t.Errorf("metric/unit = %q/%q, want haversine/km", resp.Metric, resp.Unit)
	}
	if resp.Method != "held-karp" {
		t.Errorf("method = %q, want held-karp (4 stops is exact)", resp.Method)
	}
	// Tour is depot..depot => 5 entries for 4 stops.
	if len(resp.TourIndices) != 5 || resp.TourIndices[0] != 0 || resp.TourIndices[4] != 0 {
		t.Errorf("bad tour shape: %v", resp.TourIndices)
	}
	if len(resp.Legs) != 4 {
		t.Errorf("want 4 legs, got %d", len(resp.Legs))
	}
	if resp.TotalCost > resp.BaselineCost+1e-9 {
		t.Errorf("optimised %.2f should not exceed baseline %.2f", resp.TotalCost, resp.BaselineCost)
	}
}

func TestOptimizeValidationErrors(t *testing.T) {
	h := quietServer(nil).Handler()

	cases := []struct {
		name string
		body string
		want int
	}{
		{"not enough stops", `{"stops":[{"name":"A","lat":1,"lng":1}]}`, http.StatusBadRequest},
		{"bad coordinates", `{"stops":[{"lat":0,"lng":0},{"lat":200,"lng":0}]}`, http.StatusBadRequest},
		{"malformed json", `{"stops": [`, http.StatusBadRequest},
		{"unknown field", `{"stops":[{"lat":1,"lng":1},{"lat":2,"lng":2}],"bogus":true}`, http.StatusBadRequest},
		{"unknown metric", `{"stops":[{"lat":1,"lng":1},{"lat":2,"lng":2}],"metric":"astral"}`, http.StatusBadRequest},
		{"unknown mode", `{"stops":[{"lat":1,"lng":1},{"lat":2,"lng":2}],"mode":"vibes"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postJSON(t, h, tc.body)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, tc.want, rec.Body.String())
			}
			var e errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e.Error == "" {
				t.Errorf("expected a JSON error body, got: %s", rec.Body.String())
			}
		})
	}
}

func TestRoadMetricWithoutOSRMis503(t *testing.T) {
	h := quietServer(nil).Handler() // no OSRM configured
	body := `{"stops":[{"lat":12.7,"lng":77.6},{"lat":12.9,"lng":77.7}],"metric":"road_distance"}`
	rec := postJSON(t, h, body)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when OSRM is not configured (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestOptimizeRoadDistance exercises the full road path against a mock OSRM
// server, so the OSRM provider is covered end-to-end without any network.
func TestOptimizeRoadDistance(t *testing.T) {
	osrmMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := func(v float64) *float64 { return &v }
		// 3x3 metres / seconds; asymmetric-ish but routable everywhere.
		json.NewEncoder(w).Encode(map[string]any{
			"code": "Ok",
			"distances": [][]*float64{
				{m(0), m(4000), m(9000)},
				{m(4000), m(0), m(6000)},
				{m(9000), m(6000), m(0)},
			},
			"durations": [][]*float64{
				{m(0), m(300), m(600)},
				{m(300), m(0), m(420)},
				{m(600), m(420), m(0)},
			},
		})
	}))
	defer osrmMock.Close()

	h := quietServer(osrm.New(osrmMock.URL, 5*time.Second)).Handler()

	body := `{
      "metric": "road_distance",
      "stops": [
        {"name":"D","lat":12.71,"lng":77.69},
        {"name":"X","lat":12.90,"lng":77.60},
        {"name":"Y","lat":12.97,"lng":77.75}
      ]
    }`
	rec := postJSON(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp optimizeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Metric != MetricRoadDistance || resp.Unit != "km" {
		t.Errorf("metric/unit = %q/%q, want road_distance/km", resp.Metric, resp.Unit)
	}
	// The only tour of 2 stops from the depot costs 4+6+9 = 19 km either way.
	if resp.TotalCost != 19.0 {
		t.Errorf("total = %v km, want 19.0 (4+6+9)", resp.TotalCost)
	}
}

func TestRoadDurationMetricUsesMinutes(t *testing.T) {
	osrmMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := func(v float64) *float64 { return &v }
		json.NewEncoder(w).Encode(map[string]any{
			"code": "Ok",
			"distances": [][]*float64{
				{m(0), m(4000), m(9000)},
				{m(4000), m(0), m(6000)},
				{m(9000), m(6000), m(0)},
			},
			"durations": [][]*float64{
				{m(0), m(300), m(600)}, // 5 and 10 min
				{m(300), m(0), m(420)}, // 7 min
				{m(600), m(420), m(0)},
			},
		})
	}))
	defer osrmMock.Close()

	h := quietServer(osrm.New(osrmMock.URL, 5*time.Second)).Handler()
	body := `{"metric":"road_duration","stops":[
        {"name":"D","lat":12.71,"lng":77.69},
        {"name":"X","lat":12.90,"lng":77.60},
        {"name":"Y","lat":12.97,"lng":77.75}]}`
	rec := postJSON(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp optimizeResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Unit != "min" {
		t.Errorf("unit = %q, want min", resp.Unit)
	}
	if resp.TotalCost != 22.0 { // 5+7+10
		t.Errorf("total = %v min, want 22.0 (5+7+10)", resp.TotalCost)
	}
}

func TestHealthz(t *testing.T) {
	h := quietServer(nil).Handler()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d", rec.Code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h := quietServer(nil).Handler()
	req := httptest.NewRequest(http.MethodGet, "/optimize", nil) // GET, not POST
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /optimize status = %d, want 405", rec.Code)
	}
}
