package geocode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestGeocodeParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]nominatimHit{
			{Lat: "12.9716", Lon: "77.5946", DisplayName: "Bengaluru, Karnataka, India"},
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "test/1.0", time.Second, time.Millisecond)
	got, err := c.Geocode(context.Background(), "Bengaluru")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Found {
		t.Fatal("expected Found=true")
	}
	if got.Lat != 12.9716 || got.Lng != 77.5946 {
		t.Errorf("coords = (%v, %v), want (12.9716, 77.5946)", got.Lat, got.Lng)
	}
}

func TestGeocodeNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`)) // Nominatim returns an empty array for no match
	}))
	defer srv.Close()

	c := New(srv.URL, "test/1.0", time.Second, time.Millisecond)
	got, err := c.Geocode(context.Background(), "asdfqwerty nowhere place")
	if err != nil {
		t.Fatalf("no-match should not be an error: %v", err)
	}
	if got.Found {
		t.Error("expected Found=false for no match")
	}
}

// TestGeocodeCaches verifies a repeated address hits the network only once.
func TestGeocodeCaches(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		json.NewEncoder(w).Encode([]nominatimHit{{Lat: "1.0", Lon: "2.0", DisplayName: "X"}})
	}))
	defer srv.Close()

	c := New(srv.URL, "test/1.0", time.Second, time.Millisecond)
	for i := 0; i < 3; i++ {
		if _, err := c.Geocode(context.Background(), "Same Place"); err != nil {
			t.Fatal(err)
		}
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("network calls = %d, want 1 (caching should dedupe)", n)
	}
}

func TestGeocodeSendsUserAgent(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := New(srv.URL, "routesolver-demo/1.0", time.Second, time.Millisecond)
	c.Geocode(context.Background(), "anywhere")
	if ua != "routesolver-demo/1.0" {
		t.Errorf("User-Agent = %q, want routesolver-demo/1.0 (Nominatim requires it)", ua)
	}
}

func TestGeocodeManyOrderAndDedupe(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		q := r.URL.Query().Get("q")
		json.NewEncoder(w).Encode([]nominatimHit{{Lat: "1.0", Lon: "2.0", DisplayName: q}})
	}))
	defer srv.Close()

	c := New(srv.URL, "test/1.0", time.Second, time.Millisecond)
	addrs := []string{"A", "B", "A", "C"} // "A" repeats
	got, err := c.GeocodeMany(context.Background(), addrs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("want 4 results in order, got %d", len(got))
	}
	if got[0].Address != "A" || got[3].Address != "C" {
		t.Errorf("order not preserved: %v", got)
	}
	if n := atomic.LoadInt32(&calls); n != 3 { // A, B, C — not 4
		t.Errorf("network calls = %d, want 3 (duplicate A cached)", n)
	}
}

// TestRateLimitSpacing checks two distinct lookups are spaced by at least
// the configured interval.
func TestRateLimitSpacing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	interval := 60 * time.Millisecond
	c := New(srv.URL, "test/1.0", time.Second, interval)

	start := time.Now()
	c.Geocode(context.Background(), "first")
	c.Geocode(context.Background(), "second")
	elapsed := time.Since(start)

	if elapsed < interval {
		t.Errorf("two lookups took %s, want >= %s (rate limiting)", elapsed, interval)
	}
}
