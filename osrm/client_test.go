package osrm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Kaushlendra-Kumar/routesolver/solver"
)

func samplePoints() []solver.Point {
	return []solver.Point{
		{Name: "Depot", Lat: 12.7105, Lng: 77.6960},
		{Name: "A", Lat: 12.9116, Lng: 77.6474},
		{Name: "B", Lat: 12.9698, Lng: 77.7500},
	}
}

// TestTableParsesAndConverts spins up a fake OSRM that returns metres and
// seconds, and checks the client converts them to km and minutes.
func TestTableParsesAndConverts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := func(v float64) *float64 { return &v }
		resp := map[string]any{
			"code": "Ok",
			"distances": [][]*float64{
				{m(0), m(2000), m(5000)},
				{m(2000), m(0), m(3000)},
				{m(5000), m(3000), m(0)},
			},
			"durations": [][]*float64{
				{m(0), m(120), m(300)},
				{m(120), m(0), m(180)},
				{m(300), m(180), m(0)},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New(srv.URL, 5*time.Second)
	got, err := c.Table(context.Background(), samplePoints())
	if err != nil {
		t.Fatal(err)
	}
	if got.DistanceKm[0][2] != 5.0 { // 5000 m → 5 km
		t.Errorf("distance[0][2] = %v km, want 5.0", got.DistanceKm[0][2])
	}
	if got.DurationMin[0][1] != 2.0 { // 120 s → 2 min
		t.Errorf("duration[0][1] = %v min, want 2.0", got.DurationMin[0][1])
	}
	if len(got.DistanceKm) != 3 || len(got.DistanceKm[0]) != 3 {
		t.Errorf("distance matrix is not 3x3: %v", got.DistanceKm)
	}
}

// TestCoordinateOrdering guards the classic OSRM gotcha: coordinates must
// be longitude,latitude — not lat,lng.
func TestCoordinateOrdering(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		m := func(v float64) *float64 { return &v }
		json.NewEncoder(w).Encode(map[string]any{
			"code":      "Ok",
			"distances": [][]*float64{{m(0), m(1)}, {m(1), m(0)}},
			"durations": [][]*float64{{m(0), m(1)}, {m(1), m(0)}},
		})
	}))
	defer srv.Close()

	pts := []solver.Point{
		{Name: "P0", Lat: 12.5, Lng: 77.5},
		{Name: "P1", Lat: 13.5, Lng: 78.5},
	}
	if _, err := New(srv.URL, 0).Table(context.Background(), pts); err != nil {
		t.Fatal(err)
	}
	// Longitude must come first for each pair.
	if !strings.Contains(gotPath, "77.500000,12.500000") {
		t.Errorf("coord ordering wrong; path = %q, want lng,lat (77.5,12.5 first)", gotPath)
	}
	if strings.Contains(gotPath, "12.500000,77.500000") {
		t.Errorf("coordinates are lat,lng but OSRM needs lng,lat; path = %q", gotPath)
	}
}

func TestTableServiceError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"code":    "NoTable",
			"message": "cannot compute table",
		})
	}))
	defer srv.Close()

	if _, err := New(srv.URL, 0).Table(context.Background(), samplePoints()); err == nil {
		t.Fatal("expected an error when OSRM code != Ok")
	} else if !strings.Contains(err.Error(), "NoTable") {
		t.Errorf("error should mention the OSRM code, got: %v", err)
	}
}

func TestTableUnroutablePair(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := func(v float64) *float64 { return &v }
		json.NewEncoder(w).Encode(map[string]any{
			"code":      "Ok",
			"distances": [][]*float64{{m(0), nil}, {m(1), m(0)}}, // null = no route
			"durations": [][]*float64{{m(0), m(1)}, {m(1), m(0)}},
		})
	}))
	defer srv.Close()

	pts := samplePoints()[:2]
	if _, err := New(srv.URL, 0).Table(context.Background(), pts); err == nil {
		t.Fatal("expected an error for an unroutable pair (null entry)")
	}
}

func TestTableTooManyPoints(t *testing.T) {
	pts := make([]solver.Point, maxCoordinates+1)
	if _, err := New(DemoServer, 0).Table(context.Background(), pts); err == nil {
		t.Fatal("expected an error above the coordinate cap")
	}
}

func TestTableContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := New(srv.URL, 5*time.Second).Table(ctx, samplePoints()); err == nil {
		t.Fatal("expected a context-deadline error")
	}
}
