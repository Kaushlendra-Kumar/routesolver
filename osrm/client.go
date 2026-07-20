// Package osrm is a small client for the OSRM (Open Source Routing Machine)
// Table service, which returns the full pairwise road-distance and
// road-duration matrix for a set of coordinates in a single request.
//
// This is the drop-in replacement for straight-line haversine distances:
// the solver package consumes any [][]float64 matrix, so swapping
// haversine for real road distances is just swapping the matrix source.
//
// Server: point Client at a self-hosted OSRM instance in production. The
// public demo server (https://router.project-osrm.org) works for
// development but is rate-limited, driving-profile only, and must not be
// relied on for production traffic. Self-host with Docker:
//
//	wget http://download.geofabrik.de/asia/india/southern-zone-latest.osm.pbf
//	docker run -t -v "${PWD}:/data" osrm/osrm-backend osrm-extract -p /opt/car.lua /data/southern-zone-latest.osm.pbf
//	docker run -t -v "${PWD}:/data" osrm/osrm-backend osrm-partition /data/southern-zone-latest.osrm
//	docker run -t -v "${PWD}:/data" osrm/osrm-backend osrm-customize /data/southern-zone-latest.osrm
//	docker run -t -i -p 5000:5000 -v "${PWD}:/data" osrm/osrm-backend osrm-routed --algorithm mld /data/southern-zone-latest.osrm
package osrm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Kaushlendra-Kumar/routesolver/solver"
)

// DemoServer is OSRM's public test server. Development only — rate-limited
// and not for production use.
const DemoServer = "https://router.project-osrm.org"

// maxCoordinates guards against oversized Table requests. Public OSRM caps
// table requests at 100 coordinates; self-hosted servers can raise it via
// --max-table-size.
const maxCoordinates = 100

// Client talks to one OSRM server. The zero value is not usable; use New.
type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a Client for the given OSRM base URL (e.g. DemoServer or
// "http://localhost:5000"). A zero or negative timeout defaults to 15s.
func New(baseURL string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: timeout},
	}
}

// Matrices holds the two square matrices OSRM returns, already converted
// from OSRM's SI units into the units the rest of the app uses:
//
//	DistanceKm  — road distance in kilometres (OSRM gives metres)
//	DurationMin — travel time in minutes      (OSRM gives seconds)
//
// Both are indexed in the same order as the points passed to Table, so
// index 0 is whatever you passed first (the depot, by convention).
type Matrices struct {
	DistanceKm  [][]float64
	DurationMin [][]float64
}

// tableResponse mirrors the JSON shape of an OSRM /table reply. Distances
// and durations are pointers so a JSON null (an unroutable pair) is
// distinguishable from a genuine 0.
type tableResponse struct {
	Code      string       `json:"code"`
	Message   string       `json:"message"`
	Distances [][]*float64 `json:"distances"`
	Durations [][]*float64 `json:"durations"`
}

// Table fetches the road distance and duration matrices for points. The
// first point is treated as the depot only by downstream code; OSRM itself
// returns a full all-pairs matrix in input order.
func (c *Client) Table(ctx context.Context, points []solver.Point) (Matrices, error) {
	if len(points) < 2 {
		return Matrices{}, fmt.Errorf("osrm: need at least 2 points, got %d", len(points))
	}
	if len(points) > maxCoordinates {
		return Matrices{}, fmt.Errorf("osrm: %d points exceeds the %d-coordinate limit for a table request", len(points), maxCoordinates)
	}

	url := c.baseURL + "/table/v1/driving/" + coordString(points) +
		"?annotations=distance,duration"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Matrices{}, fmt.Errorf("osrm: building request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Matrices{}, fmt.Errorf("osrm: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8 MiB cap
	if err != nil {
		return Matrices{}, fmt.Errorf("osrm: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Matrices{}, fmt.Errorf("osrm: server returned %s: %s", resp.Status, snippet(body))
	}

	var tr tableResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return Matrices{}, fmt.Errorf("osrm: decoding response: %w", err)
	}
	if tr.Code != "Ok" {
		msg := tr.Message
		if msg == "" {
			msg = "no message"
		}
		return Matrices{}, fmt.Errorf("osrm: service error %q: %s", tr.Code, msg)
	}

	n := len(points)
	dist, err := convert(tr.Distances, n, 1.0/1000.0) // metres → km
	if err != nil {
		return Matrices{}, fmt.Errorf("osrm: distance matrix: %w", err)
	}
	dur, err := convert(tr.Durations, n, 1.0/60.0) // seconds → minutes
	if err != nil {
		return Matrices{}, fmt.Errorf("osrm: duration matrix: %w", err)
	}
	return Matrices{DistanceKm: dist, DurationMin: dur}, nil
}

// coordString renders points as OSRM's "lng,lat;lng,lat;..." path segment.
// Note the ordering: OSRM (like GeoJSON) puts LONGITUDE before latitude —
// the single most common mistake when calling it.
func coordString(points []solver.Point) string {
	var b strings.Builder
	for i, p := range points {
		if i > 0 {
			b.WriteByte(';')
		}
		fmt.Fprintf(&b, "%.6f,%.6f", p.Lng, p.Lat)
	}
	return b.String()
}

// convert turns OSRM's [][]*float64 (nullable, SI units) into a solid
// [][]float64 scaled by factor, rejecting the matrix if any pair is null
// (unroutable) or the shape is wrong.
func convert(raw [][]*float64, n int, factor float64) ([][]float64, error) {
	if len(raw) != n {
		return nil, fmt.Errorf("expected %d rows, got %d", n, len(raw))
	}
	out := make([][]float64, n)
	for i, row := range raw {
		if len(row) != n {
			return nil, fmt.Errorf("row %d: expected %d entries, got %d", i, n, len(row))
		}
		out[i] = make([]float64, n)
		for j, v := range row {
			if v == nil {
				return nil, fmt.Errorf("no route between points %d and %d", i, j)
			}
			out[i][j] = *v * factor
		}
	}
	return out, nil
}

func snippet(b []byte) string {
	const max = 200
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
